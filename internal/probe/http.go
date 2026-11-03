package probe

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"slices"
	"sync"
	"time"
)

// maxBody caps how much of the response body is read per probe.
const maxBody = 1 << 20

// UserAgent is sent with every HTTP probe.
var UserAgent = "latprobe"

// HTTPProber issues a GET request on a fresh connection and reports the
// time spent in each phase.
type HTTPProber struct {
	url    string
	expect []int
	// tlsConfig is nil (system roots) except in tests.
	tlsConfig *tls.Config
}

// NewHTTP returns a prober for url. expect lists acceptable status codes;
// empty means any status below 400.
func NewHTTP(url string, expect []int) *HTTPProber {
	return &HTTPProber{url: url, expect: expect}
}

// client builds a throwaway client for a single probe.
//
// A new Transport per probe guarantees a cold connection, so every sample
// includes DNS, connect and TLS. DisableKeepAlives alone is not enough: a
// dial that completes after a timed-out probe can still land in a shared
// pool and be reused, silently hiding those phases from the next sample.
func (p *HTTPProber) client() (*http.Client, *http.Transport) {
	tr := &http.Transport{
		DisableKeepAlives: true,
		ForceAttemptHTTP2: true,
		TLSClientConfig:   p.tlsConfig,
		// Proxies would measure the path to the proxy, not to the target.
		Proxy: nil,
	}
	return &http.Client{
		Transport: tr,
		// Measure a single hop; following redirects would mix targets.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, tr
}

// timings collects httptrace callbacks. Some callbacks (e.g. ConnectStart
// and ConnectDone during Happy Eyeballs dialing) can fire on other
// goroutines, so access is serialised with a mutex.
type timings struct {
	mu                        sync.Mutex
	dnsStart, dnsDone         time.Time
	connectStart, connectDone time.Time
	tlsStart, tlsDone         time.Time
	wroteRequest, firstByte   time.Time
}

// mark records now into *field the first time it is called for that field.
func (t *timings) mark(field *time.Time) {
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	if field.IsZero() {
		*field = now
	}
}

func (t *timings) trace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		DNSStart:     func(httptrace.DNSStartInfo) { t.mark(&t.dnsStart) },
		DNSDone:      func(httptrace.DNSDoneInfo) { t.mark(&t.dnsDone) },
		ConnectStart: func(string, string) { t.mark(&t.connectStart) },
		ConnectDone: func(_, _ string, err error) {
			if err == nil { // only the attempt that actually won
				t.mark(&t.connectDone)
			}
		},
		TLSHandshakeStart:    func() { t.mark(&t.tlsStart) },
		TLSHandshakeDone:     func(tls.ConnectionState, error) { t.mark(&t.tlsDone) },
		WroteRequest:         func(httptrace.WroteRequestInfo) { t.mark(&t.wroteRequest) },
		GotFirstResponseByte: func() { t.mark(&t.firstByte) },
	}
}

// phases converts the raw timestamps into an ordered breakdown. Each
// phase is incremental, so they add up to (roughly) the total:
//
//	dns -> connect -> tls -> ttfb (request written to first byte) -> transfer
func (t *timings) phases(end time.Time) []Phase {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []Phase
	add := func(name string, from, to time.Time) {
		if !from.IsZero() && !to.IsZero() {
			out = append(out, Phase{name, to.Sub(from)})
		}
	}
	add("dns", t.dnsStart, t.dnsDone)
	add("connect", t.connectStart, t.connectDone)
	add("tls", t.tlsStart, t.tlsDone)
	add("ttfb", t.wroteRequest, t.firstByte)
	add("transfer", t.firstByte, end)
	return out
}

// Probe implements Prober.
func (p *HTTPProber) Probe(ctx context.Context) (Measurement, error) {
	var tm timings
	ctx = httptrace.WithClientTrace(ctx, tm.trace())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.url, nil)
	if err != nil {
		return Measurement{}, err
	}
	req.Header.Set("User-Agent", UserAgent)

	client, tr := p.client()
	defer tr.CloseIdleConnections()

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		end := time.Now()
		return Measurement{RTT: end.Sub(start), Phases: tm.phases(end)}, err
	}
	_, copyErr := io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))
	_ = resp.Body.Close()
	end := time.Now()

	m := Measurement{
		RTT:    end.Sub(start),
		Phases: tm.phases(end),
		Detail: fmt.Sprintf("%s %s", resp.Proto, resp.Status),
	}
	if copyErr != nil {
		return m, fmt.Errorf("read body: %w", copyErr)
	}
	if !p.statusOK(resp.StatusCode) {
		return m, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return m, nil
}

func (p *HTTPProber) statusOK(code int) bool {
	if len(p.expect) == 0 {
		return code < 400
	}
	return slices.Contains(p.expect, code)
}
