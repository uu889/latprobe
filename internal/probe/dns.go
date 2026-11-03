package probe

import (
	"context"
	"fmt"
	"net"
	"time"
)

// DNSProber measures how long a resolver takes to answer a query.
type DNSProber struct {
	host     string
	network  string // "ip4" for A, "ip6" for AAAA
	resolver *net.Resolver
}

// NewDNS returns a prober that resolves host via server (ip:port).
// An empty server uses the system resolver configuration.
func NewDNS(host, server, record string) *DNSProber {
	network := "ip4"
	if record == "AAAA" {
		network = "ip6"
	}
	r := &net.Resolver{PreferGo: true}
	if server != "" {
		// Ignore the system-configured address and always talk to server.
		r.Dial = func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, server)
		}
	}
	return &DNSProber{host: host, network: network, resolver: r}
}

// Probe implements Prober.
func (p *DNSProber) Probe(ctx context.Context) (Measurement, error) {
	start := time.Now()
	addrs, err := p.resolver.LookupNetIP(ctx, p.network, p.host)
	rtt := time.Since(start)
	if err != nil {
		return Measurement{RTT: rtt}, err
	}
	detail := addrs[0].String()
	if len(addrs) > 1 {
		detail = fmt.Sprintf("%s (+%d)", detail, len(addrs)-1)
	}
	return Measurement{RTT: rtt, Detail: detail}, nil
}
