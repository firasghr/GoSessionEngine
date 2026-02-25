package token_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/firasghr/GoSessionEngine/token"
)

// sampleJWT is a minimal signed-looking JWT whose payload encodes:
//
//	{"sub":"1234567890","name":"Test","exp":9999999999}
//
// (exp is far in the future so IsExpired returns false for a valid token)
const sampleJWT = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9." +
	"eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IlRlc3QiLCJleHAiOjk5OTk5OTk5OTl9." +
	"SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"

// expiredJWT has exp=1 (1 Jan 1970) so IsExpired always returns true.
const expiredJWT = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9." +
	"eyJzdWIiOiIxMjM0NTY3ODkwIiwiZXhwIjoxfQ." +
	"SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"

func TestSetGetToken(t *testing.T) {
	m := token.NewTokenRefreshManager("", "", nil)
	defer m.Stop()

	if m.GetToken() != "" {
		t.Error("expected empty token on construction")
	}
	m.SetToken("abc.def.ghi")
	if got := m.GetToken(); got != "abc.def.ghi" {
		t.Errorf("GetToken: got %q, want abc.def.ghi", got)
	}
}

func TestParseClaims_Valid(t *testing.T) {
	m := token.NewTokenRefreshManager("", "", nil)
	defer m.Stop()

	claims, err := m.ParseClaims(sampleJWT)
	if err != nil {
		t.Fatalf("ParseClaims error: %v", err)
	}
	if _, ok := claims["sub"]; !ok {
		t.Error("expected 'sub' claim")
	}
	if _, ok := claims["exp"]; !ok {
		t.Error("expected 'exp' claim")
	}
}

func TestParseClaims_Malformed(t *testing.T) {
	m := token.NewTokenRefreshManager("", "", nil)
	defer m.Stop()

	_, err := m.ParseClaims("not.a.valid.jwt.here")
	if err == nil {
		t.Error("expected error for too many segments")
	}

	_, err = m.ParseClaims("onlyone")
	if err == nil {
		t.Error("expected error for single segment")
	}
}

func TestIsExpired(t *testing.T) {
	m := token.NewTokenRefreshManager("", "", nil)
	defer m.Stop()

	if m.IsExpired(sampleJWT) {
		t.Error("sampleJWT should not be expired")
	}
	if !m.IsExpired(expiredJWT) {
		t.Error("expiredJWT should be expired")
	}
	if !m.IsExpired("bad token") {
		t.Error("malformed token should be treated as expired")
	}
}

func TestRefresh_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sampleJWT))
	}))
	defer srv.Close()

	m := token.NewTokenRefreshManager(srv.URL, "", srv.Client())
	defer m.Stop()

	if err := m.Refresh(); err != nil {
		t.Fatalf("Refresh error: %v", err)
	}
	if got := m.GetToken(); got != sampleJWT {
		t.Errorf("after Refresh, token = %q, want %q", got, sampleJWT)
	}
}

func TestRefresh_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	m := token.NewTokenRefreshManager(srv.URL, "", srv.Client())
	defer m.Stop()

	if err := m.Refresh(); err == nil {
		t.Error("expected error on HTTP 401")
	}
}

func TestRefresh_NoURL(t *testing.T) {
	m := token.NewTokenRefreshManager("", "", nil)
	defer m.Stop()

	if err := m.Refresh(); err == nil {
		t.Error("expected error when refreshURL is empty")
	}
}

func TestSendHeartbeat_Success(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := token.NewTokenRefreshManager("", srv.URL, srv.Client())
	defer m.Stop()
	m.SetToken(sampleJWT)

	if err := m.SendHeartbeat(); err != nil {
		t.Fatalf("SendHeartbeat error: %v", err)
	}
	want := "Bearer " + sampleJWT
	if gotAuth != want {
		t.Errorf("Authorization header: got %q, want %q", gotAuth, want)
	}
}

func TestSendHeartbeat_NoURL(t *testing.T) {
	m := token.NewTokenRefreshManager("", "", nil)
	defer m.Stop()

	if err := m.SendHeartbeat(); err == nil {
		t.Error("expected error when heartbeatURL is empty")
	}
}

func TestStartHeartbeat_Fires(t *testing.T) {
	called := make(chan struct{}, 5)
	m := token.NewTokenRefreshManager("", "", nil)

	m.StartHeartbeat(20*time.Millisecond, func() error {
		called <- struct{}{}
		return nil
	})

	select {
	case <-called:
	case <-time.After(500 * time.Millisecond):
		t.Error("heartbeat did not fire within 500 ms")
	}
	m.Stop()
}

func TestStop_Idempotent(t *testing.T) {
	m := token.NewTokenRefreshManager("", "", nil)
	m.Stop()
	m.Stop() // must not panic
}
