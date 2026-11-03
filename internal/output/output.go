// Package output renders runner snapshots as a text table or JSON.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/uu889/latprobe/internal/runner"
)

// Report is the JSON document produced by `-o json` and /api/v1/summary.
type Report struct {
	GeneratedAt time.Time      `json:"generated_at"`
	Targets     []TargetReport `json:"targets"`
}

// TargetReport holds the window statistics of one target. Durations are
// in milliseconds, which is easier to read and plot than nanoseconds.
type TargetReport struct {
	Name     string      `json:"name"`
	Probe    string      `json:"probe"`
	Address  string      `json:"address"`
	Region   string      `json:"region,omitempty"`
	Sent     int         `json:"sent"`
	Lost     int         `json:"lost"`
	LossPct  float64     `json:"loss_pct"`
	MinMs    float64     `json:"min_ms"`
	AvgMs    float64     `json:"avg_ms"`
	P50Ms    float64     `json:"p50_ms"`
	P95Ms    float64     `json:"p95_ms"`
	P99Ms    float64     `json:"p99_ms"`
	MaxMs    float64     `json:"max_ms"`
	JitterMs float64     `json:"jitter_ms"`
	Last     *LastResult `json:"last,omitempty"`
}

// LastResult describes the most recent probe of a target.
type LastResult struct {
	Time   time.Time          `json:"time"`
	OK     bool               `json:"ok"`
	RTTMs  float64            `json:"rtt_ms"`
	Phases map[string]float64 `json:"phases_ms,omitempty"`
	Detail string             `json:"detail,omitempty"`
	Error  string             `json:"error,omitempty"`
}

// NewReport converts runner snapshots into a Report.
func NewReport(snap []runner.TargetStats, now time.Time) Report {
	r := Report{GeneratedAt: now.UTC(), Targets: make([]TargetReport, 0, len(snap))}
	for _, ts := range snap {
		s := ts.Summary
		tr := TargetReport{
			Name: ts.Target.Name, Probe: string(ts.Target.Type),
			Address: ts.Target.Address, Region: ts.Target.Region,
			Sent: s.Sent, Lost: s.Lost, LossPct: round(s.LossPct),
			MinMs: millis(s.Min), AvgMs: millis(s.Avg), P50Ms: millis(s.P50),
			P95Ms: millis(s.P95), P99Ms: millis(s.P99), MaxMs: millis(s.Max),
			JitterMs: millis(s.Jitter),
		}
		if l := ts.Last; l != nil {
			lr := &LastResult{Time: l.Time.UTC(), OK: l.Err == nil, RTTMs: millis(l.RTT), Detail: l.Detail}
			if l.Err != nil {
				lr.Error = l.Err.Error()
			}
			if len(l.Phases) > 0 {
				lr.Phases = make(map[string]float64, len(l.Phases))
				for _, p := range l.Phases {
					lr.Phases[p.Name] = millis(p.Duration)
				}
			}
			tr.Last = lr
		}
		r.Targets = append(r.Targets, tr)
	}
	return r
}

// WriteJSON writes the snapshot as indented JSON.
func WriteJSON(w io.Writer, snap []runner.TargetStats) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(NewReport(snap, time.Now()))
}

// WriteTable writes a human-friendly summary table, followed by the
// phase breakdown of the last probe for targets that have one.
func WriteTable(w io.Writer, snap []runner.TargetStats) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TARGET\tPROBE\tREGION\tSENT\tLOSS\tMIN\tAVG\tP50\tP95\tP99\tMAX\tJITTER\tLAST")
	for _, ts := range snap {
		s := ts.Summary
		lat := []string{"-", "-", "-", "-", "-", "-", "-"}
		if s.Sent > s.Lost {
			lat = []string{dur(s.Min), dur(s.Avg), dur(s.P50), dur(s.P95), dur(s.P99), dur(s.Max), dur(s.Jitter)}
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%.1f%%\t%s\t%s\n",
			ts.Target.Name, ts.Target.Type, orDash(ts.Target.Region),
			s.Sent, s.LossPct, strings.Join(lat, "\t"), lastNote(ts.Last))
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	first := true
	for _, ts := range snap {
		if ts.Last == nil || len(ts.Last.Phases) < 2 {
			continue
		}
		if first {
			fmt.Fprintln(tw, "\nPhase breakdown (last probe):")
			first = false
		}
		parts := make([]string, len(ts.Last.Phases))
		for i, p := range ts.Last.Phases {
			parts[i] = p.Name + "=" + dur(p.Duration)
		}
		fmt.Fprintf(tw, "  %s\t%s\n", ts.Target.Name, strings.Join(parts, "  "))
	}
	return tw.Flush()
}

func lastNote(l *runner.Result) string {
	switch {
	case l == nil:
		return "-"
	case l.Err != nil:
		return "error: " + truncate(l.Err.Error(), 60)
	default:
		return l.Detail
	}
}

func dur(d time.Duration) string {
	if d >= time.Second {
		return fmt.Sprintf("%.2fs", d.Seconds())
	}
	return fmt.Sprintf("%.2fms", float64(d)/float64(time.Millisecond))
}

func millis(d time.Duration) float64 { return round(float64(d) / float64(time.Millisecond)) }

func round(v float64) float64 { return math.Round(v*1000) / 1000 }

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
