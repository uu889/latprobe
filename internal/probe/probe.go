// Package probe implements the individual latency measurements.
//
// Each Prober performs exactly one measurement per call and honours the
// context deadline, so the caller fully controls timeouts and cancellation.
package probe

import (
	"context"
	"fmt"
	"time"

	"github.com/uu889/latprobe/internal/config"
)

// Phase is one named step of a measurement, e.g. "dns" or "tls".
type Phase struct {
	Name     string        `json:"name"`
	Duration time.Duration `json:"duration"`
}

// Measurement is the result of a single successful (or partially
// successful) probe.
type Measurement struct {
	// RTT is the headline latency for this probe type:
	//   tcp:  TCP handshake time (DNS excluded, reported as a phase)
	//   dns:  time to get an answer from the resolver
	//   http: total request time, first byte of DNS to last byte of body
	RTT time.Duration
	// Phases is an ordered breakdown of RTT, when the probe has one.
	Phases []Phase
	// Detail is a short human-readable note: peer address, status, answer.
	Detail string
}

// Prober measures one target.
type Prober interface {
	Probe(ctx context.Context) (Measurement, error)
}

// New builds the Prober matching the target's type.
func New(t config.Target) (Prober, error) {
	switch t.Type {
	case config.TCP:
		return NewTCP(t.Address), nil
	case config.DNS:
		return NewDNS(t.Address, t.Resolver, t.Record), nil
	case config.HTTP:
		return NewHTTP(t.Address, t.ExpectStatus), nil
	default:
		return nil, fmt.Errorf("unsupported probe type %q", t.Type)
	}
}
