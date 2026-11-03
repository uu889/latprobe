package stats

import (
	"sync"
	"testing"
	"time"
)

func ms(v ...int) []time.Duration {
	out := make([]time.Duration, len(v))
	for i, x := range v {
		out[i] = time.Duration(x) * time.Millisecond
	}
	return out
}

func TestPercentile(t *testing.T) {
	data := ms(10, 20, 30, 40, 50, 60, 70, 80, 90, 100)
	tests := []struct {
		p    float64
		want time.Duration
	}{
		{0, 10 * time.Millisecond},
		{50, 55 * time.Millisecond},    // rank 4.5 -> between 50 and 60
		{95, 95500 * time.Microsecond}, // rank 8.55
		{99, 99100 * time.Microsecond}, // rank 8.91
		{100, 100 * time.Millisecond},
	}
	for _, tt := range tests {
		if got := Percentile(data, tt.p); got != tt.want {
			t.Errorf("Percentile(p%v) = %v, want %v", tt.p, got, tt.want)
		}
	}
	if got := Percentile(nil, 50); got != 0 {
		t.Errorf("empty slice: got %v", got)
	}
	if got := Percentile(ms(7), 99); got != 7*time.Millisecond {
		t.Errorf("single element: got %v", got)
	}
}

func TestSummarizeJitterAndLoss(t *testing.T) {
	samples := []Sample{
		{RTT: 10 * time.Millisecond, OK: true},
		{RTT: 14 * time.Millisecond, OK: true}, // |14-10| = 4
		{OK: false},                            // lost, skipped for jitter
		{RTT: 12 * time.Millisecond, OK: true}, // |12-14| = 2
		{RTT: 20 * time.Millisecond, OK: true}, // |20-12| = 8
	}
	s := Summarize(samples)
	if s.Sent != 5 || s.Lost != 1 || s.LossPct != 20 {
		t.Errorf("loss: got sent=%d lost=%d pct=%v", s.Sent, s.Lost, s.LossPct)
	}
	if s.Min != 10*time.Millisecond || s.Max != 20*time.Millisecond {
		t.Errorf("min/max: got %v/%v", s.Min, s.Max)
	}
	if s.Avg != 14*time.Millisecond {
		t.Errorf("avg: got %v", s.Avg)
	}
	if want := (4 + 2 + 8) * time.Millisecond / 3; s.Jitter != want {
		t.Errorf("jitter: got %v want %v", s.Jitter, want)
	}
}

func TestSummarizeAllLost(t *testing.T) {
	s := Summarize([]Sample{{OK: false}, {OK: false}})
	if s.LossPct != 100 || s.Avg != 0 || s.Jitter != 0 {
		t.Errorf("unexpected summary: %+v", s)
	}
	if empty := Summarize(nil); empty.Sent != 0 || empty.LossPct != 0 {
		t.Errorf("empty: %+v", empty)
	}
}

func TestWindowEvictsOldest(t *testing.T) {
	w := NewWindow(3)
	for i := 1; i <= 5; i++ {
		w.Add(Sample{RTT: time.Duration(i), OK: true})
	}
	got := w.Samples()
	if len(got) != 3 || got[0].RTT != 3 || got[2].RTT != 5 {
		t.Fatalf("want [3 4 5], got %v", got)
	}
}

func TestWindowConcurrentUse(t *testing.T) {
	w := NewWindow(64)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				w.Add(Sample{RTT: time.Millisecond, OK: true})
				_ = w.Summary()
			}
		}()
	}
	wg.Wait()
	if s := w.Summary(); s.Sent != 64 {
		t.Errorf("want full window of 64, got %d", s.Sent)
	}
}
