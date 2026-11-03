package probe

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"time"
)

// TCPProber measures the TCP three-way handshake to host:port.
//
// Name resolution is timed separately and excluded from RTT, so a slow
// resolver does not show up as network latency to the target.
type TCPProber struct {
	address  string
	resolver *net.Resolver
	dialer   net.Dialer
}

// NewTCP returns a prober for a host:port address.
func NewTCP(address string) *TCPProber {
	return &TCPProber{address: address, resolver: net.DefaultResolver}
}

// Probe implements Prober.
func (p *TCPProber) Probe(ctx context.Context) (Measurement, error) {
	host, port, err := net.SplitHostPort(p.address)
	if err != nil {
		return Measurement{}, err
	}

	var phases []Phase
	ip, err := netip.ParseAddr(host)
	if err != nil { // not an IP literal: resolve first
		start := time.Now()
		addrs, err := p.resolver.LookupNetIP(ctx, "ip", host)
		phases = append(phases, Phase{"dns", time.Since(start)})
		if err != nil {
			return Measurement{Phases: phases}, fmt.Errorf("resolve %s: %w", host, err)
		}
		ip = preferIPv4(addrs)
	}

	target := net.JoinHostPort(ip.Unmap().String(), port)
	start := time.Now()
	conn, err := p.dialer.DialContext(ctx, "tcp", target)
	rtt := time.Since(start)
	phases = append(phases, Phase{"connect", rtt})
	if err != nil {
		return Measurement{RTT: rtt, Phases: phases}, err
	}
	_ = conn.Close()
	return Measurement{RTT: rtt, Phases: phases, Detail: target}, nil
}

// preferIPv4 picks the first IPv4 address, falling back to the first one.
// Probing a stable address family keeps successive samples comparable.
func preferIPv4(addrs []netip.Addr) netip.Addr {
	for _, a := range addrs {
		if a.Unmap().Is4() {
			return a
		}
	}
	return addrs[0]
}
