package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseAppliesDefaults(t *testing.T) {
	cfg, err := Parse([]byte(`
targets:
  - type: TCP
    address: example.com:443
  - type: dns
    address: example.com
    resolver: 1.1.1.1
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Interval != DefaultInterval || cfg.Timeout != DefaultTimeout || cfg.Window != DefaultWindow {
		t.Errorf("defaults not applied: %+v", cfg)
	}
	tcp := cfg.Targets[0]
	if tcp.Type != TCP || tcp.Name != "tcp://example.com:443" || tcp.Timeout != DefaultTimeout {
		t.Errorf("unexpected tcp target: %+v", tcp)
	}
	dns := cfg.Targets[1]
	if dns.Resolver != "1.1.1.1:53" || dns.Record != "A" {
		t.Errorf("unexpected dns target: %+v", dns)
	}
}

func TestParseFullConfig(t *testing.T) {
	cfg, err := Parse([]byte(`
interval: 5s
timeout: 2s
window: 50
targets:
  - name: web
    type: http
    address: https://example.com/
    region: eu-west
    expect_status: [200, 301]
    timeout: 4s
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := cfg.Targets[0]
	if got.Region != "eu-west" || got.Timeout != 4*time.Second || len(got.ExpectStatus) != 2 {
		t.Errorf("unexpected target: %+v", got)
	}
	if cfg.Interval != 5*time.Second || cfg.Window != 50 {
		t.Errorf("unexpected globals: %+v", cfg)
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{"no targets", "interval: 1s\n", "at least one target"},
		{"unknown field", "targets:\n  - type: tcp\n    adress: x:1\n", "field adress not found"},
		{"bad type", "targets:\n  - type: icmp\n    address: x\n", "unknown type"},
		{"tcp without port", "targets:\n  - type: tcp\n    address: example.com\n", "host:port"},
		{"bad url", "targets:\n  - type: http\n    address: ftp://x\n", "http(s) URL"},
		{"bad record", "targets:\n  - type: dns\n    address: x\n    record: MX\n", "A or AAAA"},
		{"duplicate", "targets:\n  - {name: a, type: tcp, address: 'x:1'}\n  - {name: a, type: tcp, address: 'y:1'}\n", "duplicate name"},
		{"timeout > interval", "interval: 1s\ntargets:\n  - {type: tcp, address: 'x:1', timeout: 2s}\n", "exceeds interval"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("want error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestLoadExampleConfig(t *testing.T) {
	path := filepath.Join("..", "..", "examples", "config.yaml")
	if _, err := os.Stat(path); err != nil {
		t.Skip("example config not present")
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("example config must stay valid: %v", err)
	}
}
