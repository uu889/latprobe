package probe

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func ctxTimeout(t *testing.T, d time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}

func phaseNames(ps []Phase) string {
	names := make([]string, len(ps))
	for i, p := range ps {
		names[i] = p.Name
	}
	return strings.Join(names, ",")
}

func TestTCPProbe(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	m, err := NewTCP(ln.Addr().String()).Probe(ctxTimeout(t, 2*time.Second))
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if m.RTT <= 0 || phaseNames(m.Phases) != "connect" {
		t.Errorf("unexpected measurement: %+v", m)
	}

	// A hostname adds a separately timed DNS phase.
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	m, err = NewTCP("localhost:" + port).Probe(ctxTimeout(t, 2*time.Second))
	if err != nil {
		t.Fatalf("probe localhost: %v", err)
	}
	if phaseNames(m.Phases) != "dns,connect" {
		t.Errorf("want dns,connect phases, got %s", phaseNames(m.Phases))
	}
}

func TestTCPProbeRefused(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close() // nothing listens here any more

	if _, err := NewTCP(addr).Probe(ctxTimeout(t, 2*time.Second)); err == nil {
		t.Fatal("expected connection refused")
	}
}

// fakeDNS answers every A query with 192.0.2.1 and every other query with
// an empty answer. It is just enough DNS to exercise the prober.
func fakeDNS(t *testing.T) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 512)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			if resp := dnsAnswer(buf[:n]); resp != nil {
				pc.WriteTo(resp, addr)
			}
		}
	}()
	return pc.LocalAddr().String()
}

func dnsAnswer(q []byte) []byte {
	if len(q) < 12 {
		return nil
	}
	// Walk the QNAME labels to find the end of the question.
	i := 12
	for i < len(q) && q[i] != 0 {
		i += int(q[i]) + 1
	}
	if i+5 > len(q) {
		return nil
	}
	qEnd := i + 5 // zero byte + QTYPE + QCLASS
	qtype := binary.BigEndian.Uint16(q[i+1:])

	resp := make([]byte, 0, qEnd+16)
	resp = append(resp, q[0], q[1]) // same ID
	resp = append(resp, 0x81, 0x80) // response, recursion desired+available
	ancount := byte(0)
	if qtype == 1 {
		ancount = 1
	}
	resp = append(resp, 0, 1, 0, ancount, 0, 0, 0, 0)
	resp = append(resp, q[12:qEnd]...)
	if ancount == 1 {
		resp = append(resp,
			0xc0, 0x0c, // pointer to QNAME
			0, 1, 0, 1, // type A, class IN
			0, 0, 0, 60, // TTL
			0, 4, 192, 0, 2, 1)
	}
	return resp
}

func TestDNSProbe(t *testing.T) {
	server := fakeDNS(t)
	m, err := NewDNS("probe.test", server, "A").Probe(ctxTimeout(t, 2*time.Second))
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if m.Detail != "192.0.2.1" || m.RTT <= 0 {
		t.Errorf("unexpected measurement: %+v", m)
	}
}

func TestDNSProbeNoAnswer(t *testing.T) {
	server := fakeDNS(t)
	if _, err := NewDNS("probe.test", server, "AAAA").Probe(ctxTimeout(t, 2*time.Second)); err == nil {
		t.Fatal("expected error for empty AAAA answer")
	}
}

func TestHTTPProbe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	m, err := NewHTTP(srv.URL, nil).Probe(ctxTimeout(t, 2*time.Second))
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if got := phaseNames(m.Phases); got != "connect,ttfb,transfer" {
		t.Errorf("phases = %s", got)
	}
	if !strings.Contains(m.Detail, "200") {
		t.Errorf("detail = %q", m.Detail)
	}

	if _, err := NewHTTP(srv.URL+"/missing", nil).Probe(ctxTimeout(t, 2*time.Second)); err == nil {
		t.Error("expected 404 to fail with default expectations")
	}
	if _, err := NewHTTP(srv.URL+"/missing", []int{404}).Probe(ctxTimeout(t, 2*time.Second)); err != nil {
		t.Errorf("404 should pass when expected: %v", err)
	}
}

func TestHTTPSProbeHasTLSPhase(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()

	p := NewHTTP(srv.URL, nil)
	// Trust the test server's self-signed certificate.
	p.tlsConfig = srv.Client().Transport.(*http.Transport).TLSClientConfig

	// Every probe must use a cold connection, so the second one still
	// reports connect and TLS.
	for i := 0; i < 2; i++ {
		m, err := p.Probe(ctxTimeout(t, 2*time.Second))
		if err != nil {
			t.Fatalf("probe %d: %v", i, err)
		}
		if got := phaseNames(m.Phases); got != "connect,tls,ttfb,transfer" {
			t.Errorf("probe %d: phases = %s", i, got)
		}
	}
}

func TestHTTPProbeHonoursDeadline(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-release
	}))
	defer srv.Close()
	defer close(release)

	start := time.Now()
	_, err := NewHTTP(srv.URL, nil).Probe(ctxTimeout(t, 100*time.Millisecond))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline exceeded, got %v", err)
	}
	if time.Since(start) > time.Second {
		t.Error("probe did not stop at the deadline")
	}
}
