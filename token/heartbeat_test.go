package token_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/firasghr/GoSessionEngine/token"
)

// ─── SessionState / store API ─────────────────────────────────────────────────

func TestHeartbeatManager_SetAndGetState(t *testing.T) {
	m := token.NewHeartbeatManager("", 0, nil)
	defer m.Stop()

	if m.GetState(1) != nil {
		t.Error("expected nil for unknown session")
	}

	m.SetState(1, &token.SessionState{SessionID: 1, Token: "tok", Available: true})
	s := m.GetState(1)
	if s == nil {
		t.Fatal("expected non-nil after SetState")
	}
	if s.Token != "tok" {
		t.Errorf("Token: got %q, want tok", s.Token)
	}
}

func TestHeartbeatManager_ClaimSession(t *testing.T) {
	m := token.NewHeartbeatManager("", 0, nil)
	defer m.Stop()

	m.SetState(5, &token.SessionState{SessionID: 5, Available: true})

	// First claim succeeds.
	if !m.ClaimSession(5) {
		t.Error("expected ClaimSession to succeed")
	}
	// Second claim must fail (already unavailable).
	if m.ClaimSession(5) {
		t.Error("expected ClaimSession to fail after first claim")
	}
}

func TestHeartbeatManager_FindAvailable(t *testing.T) {
	m := token.NewHeartbeatManager("", 0, nil)
	defer m.Stop()

	m.SetState(10, &token.SessionState{SessionID: 10, Available: false})
	m.SetState(11, &token.SessionState{SessionID: 11, Available: true})

	s := m.FindAvailable()
	if s == nil {
		t.Fatal("expected to find an available session")
	}
	if s.SessionID != 11 {
		t.Errorf("expected session 11, got %d", s.SessionID)
	}
}

func TestHeartbeatManager_FindAvailable_NoneAvailable(t *testing.T) {
	m := token.NewHeartbeatManager("", 0, nil)
	defer m.Stop()

	m.SetState(20, &token.SessionState{SessionID: 20, Available: false})
	if m.FindAvailable() != nil {
		t.Error("expected nil when no session is available")
	}
}

// ─── ExtractFromResponse ──────────────────────────────────────────────────────

func TestHeartbeatManager_ExtractFromResponse_Cookies(t *testing.T) {
	m := token.NewHeartbeatManager("", 0, nil)
	defer m.Stop()

	resp := &http.Response{
		Header: make(http.Header),
	}
	resp.Header.Add("Set-Cookie", "sess=abc; Path=/; Domain=example.com")

	m.ExtractFromResponse(7, resp)

	s := m.GetState(7)
	if s == nil {
		t.Fatal("expected state to be set after ExtractFromResponse")
	}
	if len(s.Cookies) == 0 {
		t.Error("expected at least one cookie")
	}
	if s.Cookies[0].Value != "abc" {
		t.Errorf("cookie value: got %q, want abc", s.Cookies[0].Value)
	}
	if !s.Available {
		t.Error("session should be marked Available after cookie extraction")
	}
}

func TestHeartbeatManager_ExtractFromResponse_JWTCookie(t *testing.T) {
	m := token.NewHeartbeatManager("", 0, nil)
	defer m.Stop()

	resp := &http.Response{
		Header: make(http.Header),
	}
	resp.Header.Add("Set-Cookie", "jwt_token=eyJhbGci.payload.sig; Path=/")

	m.ExtractFromResponse(8, resp)
	s := m.GetState(8)
	if s == nil || s.Token == "" {
		t.Error("expected JWT to be extracted from jwt_token cookie")
	}
}

func TestHeartbeatManager_ExtractFromResponse_NilResp(t *testing.T) {
	m := token.NewHeartbeatManager("", 0, nil)
	defer m.Stop()
	// Must not panic.
	m.ExtractFromResponse(9, nil)
}

func TestHeartbeatManager_ExtractFromResponse_MergesCookies(t *testing.T) {
	m := token.NewHeartbeatManager("", 0, nil)
	defer m.Stop()

	// First response: set cookie A.
	r1 := &http.Response{Header: make(http.Header)}
	r1.Header.Add("Set-Cookie", "a=1; Path=/")
	m.ExtractFromResponse(10, r1)

	// Second response: update A and add B.
	r2 := &http.Response{Header: make(http.Header)}
	r2.Header.Add("Set-Cookie", "a=updated; Path=/")
	r2.Header.Add("Set-Cookie", "b=new; Path=/")
	m.ExtractFromResponse(10, r2)

	s := m.GetState(10)
	if len(s.Cookies) != 2 {
		t.Errorf("expected 2 cookies after merge, got %d", len(s.Cookies))
	}
	for _, c := range s.Cookies {
		if c.Name == "a" && c.Value != "updated" {
			t.Errorf("cookie a: got %q, want updated", c.Value)
		}
	}
}

// ─── ApplyCookiesToRequest ────────────────────────────────────────────────────

func TestHeartbeatManager_ApplyCookiesToRequest(t *testing.T) {
	m := token.NewHeartbeatManager("", 0, nil)
	defer m.Stop()

	m.SetState(15, &token.SessionState{
		SessionID: 15,
		Cookies:   []*http.Cookie{{Name: "sess", Value: "xyz"}},
	})

	req, _ := http.NewRequest("GET", "http://example.com", nil)
	if err := m.ApplyCookiesToRequest(15, req); err != nil {
		t.Fatalf("ApplyCookiesToRequest: %v", err)
	}
	cookies := req.Cookies()
	if len(cookies) != 1 || cookies[0].Value != "xyz" {
		t.Errorf("unexpected cookies on request: %v", cookies)
	}
}

func TestHeartbeatManager_ApplyCookiesToRequest_NoState(t *testing.T) {
	m := token.NewHeartbeatManager("", 0, nil)
	defer m.Stop()

	req, _ := http.NewRequest("GET", "http://example.com", nil)
	// Must not error when session has no state.
	if err := m.ApplyCookiesToRequest(99, req); err != nil {
		t.Errorf("expected nil error for unknown session, got %v", err)
	}
}

// ─── Background keep-alive ────────────────────────────────────────────────────

func TestHeartbeatManager_KeepAlive_Fires(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := token.NewHeartbeatManager(srv.URL, 20*time.Millisecond, srv.Client())
	ctx, cancel := context.WithCancel(context.Background())
	m.Start(ctx, nil)

	time.Sleep(120 * time.Millisecond)
	cancel()
	m.Stop()

	if m.HeartbeatCount() == 0 {
		t.Error("expected at least one heartbeat to fire")
	}
}

func TestHeartbeatManager_KeepAlive_AttachesToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := token.NewHeartbeatManager(srv.URL, 20*time.Millisecond, srv.Client())
	m.SetState(0, &token.SessionState{SessionID: 0, Token: "secret-jwt"})

	ctx, cancel := context.WithCancel(context.Background())
	m.Start(ctx, []int{0})

	time.Sleep(80 * time.Millisecond)
	cancel()
	m.Stop()

	if gotAuth != "Bearer secret-jwt" {
		t.Errorf("Authorization header: got %q, want Bearer secret-jwt", gotAuth)
	}
}

func TestHeartbeatManager_KeepAlive_ExtractsCookiesFromResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "heartbeat_cookie", Value: "fresh"})
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := token.NewHeartbeatManager(srv.URL, 20*time.Millisecond, srv.Client())
	ctx, cancel := context.WithCancel(context.Background())
	m.Start(ctx, []int{0})

	time.Sleep(80 * time.Millisecond)
	cancel()
	m.Stop()

	s := m.GetState(0)
	if s == nil {
		t.Fatal("expected state to be set after keep-alive response")
	}
	found := false
	for _, c := range s.Cookies {
		if c.Name == "heartbeat_cookie" {
			found = true
		}
	}
	if !found {
		t.Error("heartbeat cookie not extracted from keep-alive response")
	}
}

func TestHeartbeatManager_Stop_Idempotent(t *testing.T) {
	m := token.NewHeartbeatManager("", 0, nil)
	m.Stop()
	m.Stop() // must not panic
}

func TestHeartbeatManager_AllStates(t *testing.T) {
	m := token.NewHeartbeatManager("", 0, nil)
	defer m.Stop()

	for i := 0; i < 5; i++ {
		m.SetState(i, &token.SessionState{SessionID: i})
	}
	all := m.AllStates()
	if len(all) != 5 {
		t.Errorf("AllStates: expected 5, got %d", len(all))
	}
}
