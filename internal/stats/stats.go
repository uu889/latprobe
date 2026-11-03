// Package stats keeps a rolling window of probe samples and summarises them.
package stats

import (
	"math"
	"slices"
	"sync"
	"time"
)

// Sample is one probe outcome. RTT is only meaningful when OK is true.
type Sample struct {
	RTT time.Duration
	OK  bool
}

// Summary describes the samples currently in a window.
type Summary struct {
	Sent    int     // samples in the window
	Lost    int     // failed samples in the window
	LossPct float64 // Lost / Sent * 100
	Min     time.Duration
	Avg     time.Duration
	P50     time.Duration
	P95     time.Duration
	P99     time.Duration
	Max     time.Duration
	// Jitter is the mean absolute difference between consecutive
	// successful RTTs (the same idea as RFC 3550 inter-arrival jitter,
	// without the exponential smoothing).
	Jitter time.Duration
}

// Window is a fixed-size ring buffer of samples, safe for concurrent use.
type Window struct {
	mu   sync.Mutex
	buf  []Sample
	next int
	full bool
}

// NewWindow returns a window holding the most recent size samples.
func NewWindow(size int) *Window {
	if size < 1 {
		size = 1
	}
	return &Window{buf: make([]Sample, size)}
}

// Add records a sample, evicting the oldest one when the window is full.
func (w *Window) Add(s Sample) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf[w.next] = s
	w.next = (w.next + 1) % len(w.buf)
	if w.next == 0 {
		w.full = true
	}
}

// Samples returns a copy of the window contents, oldest first.
func (w *Window) Samples() []Sample {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.full {
		return slices.Clone(w.buf[:w.next])
	}
	out := make([]Sample, 0, len(w.buf))
	out = append(out, w.buf[w.next:]...)
	return append(out, w.buf[:w.next]...)
}

// Summary summarises the current window contents.
func (w *Window) Summary() Summary { return Summarize(w.Samples()) }

// Summarize computes statistics over samples given in chronological order.
func Summarize(samples []Sample) Summary {
	s := Summary{Sent: len(samples)}
	rtts := make([]time.Duration, 0, len(samples))
	var (
		sum, jitterSum time.Duration
		prev           time.Duration
		havePrev       bool
		jitterN        int
	)
	for _, smp := range samples {
		if !smp.OK {
			s.Lost++
			continue
		}
		rtts = append(rtts, smp.RTT)
		sum += smp.RTT
		if havePrev {
			jitterSum += absDuration(smp.RTT - prev)
			jitterN++
		}
		prev, havePrev = smp.RTT, true
	}
	if s.Sent > 0 {
		s.LossPct = float64(s.Lost) / float64(s.Sent) * 100
	}
	if len(rtts) == 0 {
		return s
	}
	slices.Sort(rtts)
	s.Min, s.Max = rtts[0], rtts[len(rtts)-1]
	s.Avg = sum / time.Duration(len(rtts))
	s.P50 = Percentile(rtts, 50)
	s.P95 = Percentile(rtts, 95)
	s.P99 = Percentile(rtts, 99)
	if jitterN > 0 {
		s.Jitter = jitterSum / time.Duration(jitterN)
	}
	return s
}

// Percentile returns the p-th percentile (0..100) of an ascending slice
// using linear interpolation between the closest ranks (the same method
// as NumPy's default and Excel's PERCENTILE.INC).
func Percentile(sorted []time.Duration, p float64) time.Duration {
	n := len(sorted)
	switch {
	case n == 0:
		return 0
	case n == 1 || p <= 0:
		return sorted[0]
	case p >= 100:
		return sorted[n-1]
	}
	rank := p / 100 * float64(n-1)
	lo := int(math.Floor(rank))
	hi := int(math.Ceil(rank))
	frac := rank - float64(lo)
	return sorted[lo] + time.Duration(math.Round(frac*float64(sorted[hi]-sorted[lo])))
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
