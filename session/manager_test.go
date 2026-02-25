package session_test

import (
	"testing"

	"github.com/firasghr/GoSessionEngine/config"
	"github.com/firasghr/GoSessionEngine/proxy"
	"github.com/firasghr/GoSessionEngine/session"
)

func TestNewSessionManager_Empty(t *testing.T) {
	sm := session.NewSessionManager(config.DefaultConfig())
	if sm.Count() != 0 {
		t.Errorf("expected 0 sessions, got %d", sm.Count())
	}
}

func TestCreateSessions(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.NumberOfSessions = 5
	sm := session.NewSessionManager(cfg)
	if err := sm.CreateSessions(5, nil); err != nil {
		t.Fatalf("CreateSessions error: %v", err)
	}
	if sm.Count() != 5 {
		t.Errorf("expected 5 sessions, got %d", sm.Count())
	}
}

func TestGetSession(t *testing.T) {
	cfg := config.DefaultConfig()
	sm := session.NewSessionManager(cfg)
	sm.CreateSessions(3, nil)

	for i := 0; i < 3; i++ {
		s, ok := sm.GetSession(i)
		if !ok {
			t.Errorf("session %d not found", i)
		}
		if s == nil {
			t.Errorf("session %d is nil", i)
		}
	}

	_, ok := sm.GetSession(999)
	if ok {
		t.Error("expected not-found for session 999")
	}
}

func TestStopAll(t *testing.T) {
	cfg := config.DefaultConfig()
	sm := session.NewSessionManager(cfg)
	sm.CreateSessions(3, nil)
	sm.StopAll()
	if sm.Count() != 0 {
		t.Errorf("expected 0 sessions after StopAll, got %d", sm.Count())
	}
}

func TestCreateSessions_WithProxies(t *testing.T) {
	cfg := config.DefaultConfig()
	sm := session.NewSessionManager(cfg)

	pm := &proxy.ProxyManager{}
	// LoadProxies requires a file; skip proxy rotation here.
	if err := sm.CreateSessions(2, pm); err != nil {
		t.Fatalf("CreateSessions with nil-loaded proxy manager: %v", err)
	}
	if sm.Count() != 2 {
		t.Errorf("expected 2 sessions, got %d", sm.Count())
	}
}
