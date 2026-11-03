// Package config loads and validates latprobe's YAML configuration.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ProbeType identifies how a target is measured.
type ProbeType string

// Supported probe types.
const (
	TCP  ProbeType = "tcp"
	DNS  ProbeType = "dns"
	HTTP ProbeType = "http"
)

// Defaults applied when a field is omitted.
const (
	DefaultInterval = 10 * time.Second
	DefaultTimeout  = 3 * time.Second
	DefaultWindow   = 100
)

// Config is the top-level configuration file.
type Config struct {
	// Interval between two probes of the same target.
	Interval time.Duration `yaml:"interval"`
	// Timeout for a single probe; can be overridden per target.
	Timeout time.Duration `yaml:"timeout"`
	// Window is the number of recent samples kept per target for stats.
	Window  int      `yaml:"window"`
	Targets []Target `yaml:"targets"`
}

// Target is a single endpoint to probe.
type Target struct {
	Name string    `yaml:"name"`
	Type ProbeType `yaml:"type"`
	// Address meaning depends on Type:
	//   tcp:  host:port
	//   dns:  hostname to resolve
	//   http: full URL (http:// or https://)
	Address string `yaml:"address"`
	// Region is a free-form label (e.g. "ap-east", "eu-central") exported
	// as a Prometheus label so results can be grouped by location.
	Region string `yaml:"region"`
	// Resolver (dns only) is the DNS server to query, as ip[:port].
	// Empty means the system resolver.
	Resolver string `yaml:"resolver"`
	// Record (dns only) is "A" (default) or "AAAA".
	Record string `yaml:"record"`
	// ExpectStatus (http only) lists acceptable status codes.
	// Empty means any status below 400.
	ExpectStatus []int `yaml:"expect_status"`
	// Timeout overrides the global timeout for this target.
	Timeout time.Duration `yaml:"timeout"`
}

// Load reads and validates a config file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// Parse decodes YAML, applies defaults and validates the result.
// Unknown fields are rejected so typos do not silently change behaviour.
func Parse(data []byte) (*Config, error) {
	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// ApplyDefaults fills in zero values with sensible defaults.
func (c *Config) ApplyDefaults() {
	if c.Interval == 0 {
		c.Interval = DefaultInterval
	}
	if c.Timeout == 0 {
		c.Timeout = DefaultTimeout
	}
	if c.Window == 0 {
		c.Window = DefaultWindow
	}
	for i := range c.Targets {
		t := &c.Targets[i]
		t.Type = ProbeType(strings.ToLower(string(t.Type)))
		if t.Name == "" {
			t.Name = string(t.Type) + "://" + t.Address
		}
		if t.Timeout == 0 {
			t.Timeout = c.Timeout
		}
		if t.Type == DNS {
			if t.Record == "" {
				t.Record = "A"
			}
			t.Record = strings.ToUpper(t.Record)
			if t.Resolver != "" {
				if _, _, err := net.SplitHostPort(t.Resolver); err != nil {
					t.Resolver = net.JoinHostPort(t.Resolver, "53")
				}
			}
		}
	}
}

// Validate reports every problem found, not just the first one.
func (c *Config) Validate() error {
	var errs []error
	if c.Interval <= 0 {
		errs = append(errs, errors.New("interval must be positive"))
	}
	if c.Window < 1 {
		errs = append(errs, errors.New("window must be >= 1"))
	}
	if len(c.Targets) == 0 {
		errs = append(errs, errors.New("at least one target is required"))
	}
	seen := make(map[string]bool, len(c.Targets))
	for i, t := range c.Targets {
		if seen[t.Name] {
			errs = append(errs, fmt.Errorf("targets[%d]: duplicate name %q", i, t.Name))
		}
		seen[t.Name] = true
		if err := t.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("targets[%d] (%s): %w", i, t.Name, err))
		}
		if t.Timeout > c.Interval {
			errs = append(errs, fmt.Errorf("targets[%d] (%s): timeout %s exceeds interval %s", i, t.Name, t.Timeout, c.Interval))
		}
	}
	return errors.Join(errs...)
}

// Validate checks a single target in isolation.
func (t Target) Validate() error {
	if t.Address == "" {
		return errors.New("address is required")
	}
	if t.Timeout <= 0 {
		return errors.New("timeout must be positive")
	}
	switch t.Type {
	case TCP:
		if _, port, err := net.SplitHostPort(t.Address); err != nil || port == "" {
			return fmt.Errorf("tcp address must be host:port, got %q", t.Address)
		}
	case DNS:
		if t.Record != "A" && t.Record != "AAAA" {
			return fmt.Errorf("record must be A or AAAA, got %q", t.Record)
		}
	case HTTP:
		u, err := url.Parse(t.Address)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("http address must be an http(s) URL, got %q", t.Address)
		}
	case "":
		return errors.New("type is required (tcp, dns or http)")
	default:
		return fmt.Errorf("unknown type %q (want tcp, dns or http)", t.Type)
	}
	return nil
}
