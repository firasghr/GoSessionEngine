// Package config provides production-grade configuration management for GoSessionEngine.
// It supports JSON-based configuration loading with safe defaults optimized for high concurrency.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// Config holds all tunable parameters for the session engine.
// The struct is designed to be loaded once at startup and then shared across
// goroutines as a read-only value, making it inherently thread-safe after
// initialization. Fields cover HTTP transport tuning, session limits, and
// proxy configuration.
type Config struct {
	// NumberOfSessions controls how many independent sessions the engine
	// will maintain concurrently. Keep this <= 2000 for safe operation.
	NumberOfSessions int `json:"number_of_sessions"`

	// RequestTimeout is the end-to-end timeout for a single HTTP request,
	// including connection setup, TLS handshake, sending the request body,
	// and reading the full response. Use time.Duration JSON encoding
	// (e.g. "30s", "1m").
	RequestTimeout time.Duration `json:"request_timeout"`

	// MaxRetries is the number of times a failed request will be retried
	// before the session marks it as a permanent failure.
	MaxRetries int `json:"max_retries"`

	// TargetURL is the base URL the engine will interact with.
	TargetURL string `json:"target_url"`

	// ProxyFile is the path to a newline-delimited file containing proxy
	// addresses (host:port or scheme://host:port). Leave empty to run
	// without proxies.
	ProxyFile string `json:"proxy_file"`

	// MaxIdleConns is the total maximum number of idle (keep-alive)
	// connections across all hosts in the HTTP transport pool.
	// A higher value reduces connection setup overhead at the cost of
	// memory. Defaults to 500 for high-throughput scenarios.
	MaxIdleConns int `json:"max_idle_conns"`

	// MaxIdleConnsPerHost caps idle connections to a single host.
	// Setting this close to NumberOfSessions avoids connection churn
	// when all sessions target the same host.
	MaxIdleConnsPerHost int `json:"max_idle_conns_per_host"`

	// MaxConnsPerHost limits the total number of connections (idle +
	// active) to a single host. This prevents a runaway host from
	// exhausting all available file descriptors.
	MaxConnsPerHost int `json:"max_conns_per_host"`
}

// LoadConfig reads a JSON file at filename and deserialises it into a Config.
// It returns an error if the file cannot be opened or if the JSON is malformed.
// The returned *Config is ready to use; zero-value fields retain Go's zero
// values, so callers should validate required fields after loading.
func LoadConfig(filename string) (*Config, error) {
	f, err := os.Open(filename) // #nosec G304 – filename is caller-provided config path
	if err != nil {
		return nil, fmt.Errorf("config: open %q: %w", filename, err)
	}
	defer f.Close()

	var cfg Config
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields() // catch typos in config files early
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("config: decode %q: %w", filename, err)
	}
	return &cfg, nil
}

// DefaultConfig returns a *Config pre-filled with production-sensible defaults.
// The values are tuned for high-concurrency workloads (~500 sessions) while
// staying within typical OS file-descriptor limits.
// Callers are free to mutate the returned struct before passing it to other
// components; each call returns a fresh independent copy.
func DefaultConfig() *Config {
	return &Config{
		NumberOfSessions:    500,
		RequestTimeout:      30 * time.Second,
		MaxRetries:          3,
		TargetURL:           "",
		ProxyFile:           "",
		MaxIdleConns:        500,
		MaxIdleConnsPerHost: 100,
		MaxConnsPerHost:     200,
	}
}
