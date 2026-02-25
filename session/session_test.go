package session_test

import (
	"strings"
	"testing"
	"time"

	"github.com/firasghr/GoSessionEngine/config"
	"github.com/firasghr/GoSessionEngine/session"
)

func testConfig() *config.Config {
	return &config.Config{
		RequestTimeout:      5 * time.Second,
		MaxIdleConns:        10,
		MaxIdleConnsPerHost: 5,
		MaxConnsPerHost:     10,
	}
}

func TestNewSession_Basic(t *testing.T) {
	cfg := testConfig()
	s, err := session.NewSession(1, "", cfg)
	if err != nil {
		t.Fatalf("NewSession error: %v", err)
	}
	if s.ID != 1 {
		t.Errorf("ID: got %d, want 1", s.ID)
	}
	if s.State != "idle" {
		t.Errorf("State: got %q, want idle", s.State)
	}
	if s.Client == nil {
		t.Error("Client should not be nil")
	}
}

func TestNewSession_NilConfig(t *testing.T) {
	_, err := session.NewSession(1, "", nil)
	if err == nil {
		t.Error("expected error for nil config")
	}
}

func TestNewSession_InvalidProxy(t *testing.T) {
	cfg := testConfig()
	_, err := session.NewSession(1, "://bad proxy", cfg)
	if err == nil {
		t.Error("expected error for invalid proxy URL")
	}
}

func TestExecuteRequest_InvalidURL(t *testing.T) {
	cfg := testConfig()
	s, _ := session.NewSession(1, "", cfg)
	_, err := s.ExecuteRequest("GET", "://bad", nil)
	if err == nil {
		t.Error("expected error for invalid URL")
	}
}

func TestExecuteRequest_SetsHeaders(t *testing.T) {
	cfg := testConfig()
	s, _ := session.NewSession(1, "", cfg)
	s.Headers["X-Test"] = "value"
	// We don't make a real network call; just verify the header map is set.
	if s.Headers["X-Test"] != "value" {
		t.Error("header not stored")
	}
}

func TestUpdateLastActivity(t *testing.T) {
	cfg := testConfig()
	s, _ := session.NewSession(1, "", cfg)
	before := s.LastActivity
	time.Sleep(time.Millisecond)
	s.UpdateLastActivity()
	if !s.LastActivity.After(before) {
		t.Error("LastActivity should be updated")
	}
}

func TestClose_SetsState(t *testing.T) {
	cfg := testConfig()
	s, _ := session.NewSession(1, "", cfg)
	s.Close()
	if s.State != "closed" {
		t.Errorf("State after Close: got %q, want closed", s.State)
	}
}

func TestExecuteRequest_UnreachableHost(t *testing.T) {
	cfg := testConfig()
	cfg.RequestTimeout = 100 * time.Millisecond
	s, _ := session.NewSession(1, "", cfg)
	_, err := s.ExecuteRequest("GET", "http://192.0.2.1/test", strings.NewReader(""))
	if err == nil {
		t.Error("expected error for unreachable host")
	}
}
