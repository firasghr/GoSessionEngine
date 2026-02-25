package config_test

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/firasghr/GoSessionEngine/config"
)

func TestDefaultConfig(t *testing.T) {
	cfg := config.DefaultConfig()
	if cfg == nil {
		t.Fatal("DefaultConfig returned nil")
	}
	if cfg.NumberOfSessions <= 0 {
		t.Errorf("NumberOfSessions should be > 0, got %d", cfg.NumberOfSessions)
	}
	if cfg.RequestTimeout <= 0 {
		t.Errorf("RequestTimeout should be > 0, got %v", cfg.RequestTimeout)
	}
	if cfg.MaxRetries <= 0 {
		t.Errorf("MaxRetries should be > 0, got %d", cfg.MaxRetries)
	}
	if cfg.MaxIdleConns <= 0 {
		t.Errorf("MaxIdleConns should be > 0, got %d", cfg.MaxIdleConns)
	}
}

func TestLoadConfig_ValidFile(t *testing.T) {
	raw := map[string]interface{}{
		"number_of_sessions":    10,
		"request_timeout":       int64(30 * time.Second),
		"max_retries":           3,
		"target_url":            "http://example.com",
		"proxy_file":            "",
		"max_idle_conns":        100,
		"max_idle_conns_per_host": 20,
		"max_conns_per_host":    50,
	}
	f, err := os.CreateTemp(t.TempDir(), "config*.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(f).Encode(raw); err != nil {
		t.Fatal(err)
	}
	f.Close()

	cfg, err := config.LoadConfig(f.Name())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.NumberOfSessions != 10 {
		t.Errorf("got NumberOfSessions=%d, want 10", cfg.NumberOfSessions)
	}
	if cfg.TargetURL != "http://example.com" {
		t.Errorf("got TargetURL=%q, want http://example.com", cfg.TargetURL)
	}
}

func TestLoadConfig_MissingFile(t *testing.T) {
	_, err := config.LoadConfig("/nonexistent/path/config.json")
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
}

func TestLoadConfig_InvalidJSON(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "bad*.json")
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("{not valid json}")
	f.Close()

	_, err = config.LoadConfig(f.Name())
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}
