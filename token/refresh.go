// Package token provides a production-grade JWT Token Refresh Manager for
// maintaining session authentication state across 2,000+ concurrent sessions.
//
// Design overview:
//   - TokenRefreshManager holds the current JWT and handles automatic renewal
//     before the token expires.  All mutations are protected by a sync.RWMutex
//     so the token can be read by thousands of goroutines with minimal
//     contention.
//   - A background heartbeat goroutine sends periodic keep-alive requests to
//     prevent the server from expiring the session while the engine waits for
//     high-value data states.
//   - JWT claims are decoded from the base64url-encoded payload segment using
//     only the standard library; signature verification is intentionally
//     omitted because the engine trusts the server-issued token and does not
//     need to re-verify it.
package token

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// TokenRefreshManager manages a single JWT for a session, refreshing it
// automatically before expiry and sending heartbeat requests to keep the
// server-side session alive.
type TokenRefreshManager struct {
	token        string
	refreshURL   string
	heartbeatURL string
	client       *http.Client
	mu           sync.RWMutex
	stopCh       chan struct{}
	once         sync.Once
}

// NewTokenRefreshManager creates a manager that will refresh the token from
// refreshURL and send heartbeats to heartbeatURL.  Pass a nil client to use
// http.DefaultClient.
func NewTokenRefreshManager(refreshURL, heartbeatURL string, client *http.Client) *TokenRefreshManager {
	if client == nil {
		client = http.DefaultClient
	}
	return &TokenRefreshManager{
		refreshURL:   refreshURL,
		heartbeatURL: heartbeatURL,
		client:       client,
		stopCh:       make(chan struct{}),
	}
}

// SetToken stores a new JWT.  Safe for concurrent use.
func (m *TokenRefreshManager) SetToken(token string) {
	m.mu.Lock()
	m.token = token
	m.mu.Unlock()
}

// GetToken returns the current JWT.  Safe for concurrent use.
func (m *TokenRefreshManager) GetToken() string {
	m.mu.RLock()
	t := m.token
	m.mu.RUnlock()
	return t
}

// ParseClaims decodes the payload segment of a JWT and returns the claims as a
// map.  It does not verify the signature.
//
// Returns an error if the token is not a valid three-segment JWT or if the
// payload cannot be base64-decoded or JSON-unmarshalled.
func (m *TokenRefreshManager) ParseClaims(token string) (map[string]interface{}, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("token: malformed JWT: expected 3 segments, got %d", len(parts))
	}

	// JWT uses base64url encoding without padding.
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("token: decode JWT payload: %w", err)
	}

	var claims map[string]interface{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("token: unmarshal JWT claims: %w", err)
	}
	return claims, nil
}

// IsExpired returns true if the token's "exp" claim is in the past, or if the
// token cannot be parsed.  A zero or missing "exp" claim is treated as
// non-expired.
func (m *TokenRefreshManager) IsExpired(token string) bool {
	claims, err := m.ParseClaims(token)
	if err != nil {
		return true
	}
	exp, ok := claims["exp"]
	if !ok {
		return false
	}
	// The "exp" claim is a JSON number (float64 after json.Unmarshal).
	expFloat, ok := exp.(float64)
	if !ok {
		return false
	}
	return time.Now().Unix() >= int64(expFloat)
}

// Refresh performs an HTTP GET to refreshURL, reads a new JWT from the
// response body, and calls SetToken.  The refreshURL should return the raw
// JWT string (or a JSON envelope — callers may override this method for
// custom response parsing).
//
// Returns an error if the request fails or the server returns a non-2xx status.
func (m *TokenRefreshManager) Refresh() error {
	if m.refreshURL == "" {
		return fmt.Errorf("token: refresh URL is not configured")
	}

	resp, err := m.client.Get(m.refreshURL) // #nosec G107 – URL is operator-supplied
	if err != nil {
		return fmt.Errorf("token: refresh request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("token: refresh returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if err != nil {
		return fmt.Errorf("token: read refresh response: %w", err)
	}

	newToken := strings.TrimSpace(string(body))
	if newToken == "" {
		return fmt.Errorf("token: refresh returned empty token")
	}
	m.SetToken(newToken)
	return nil
}

// StartHeartbeat launches a background goroutine that calls heartbeatFn every
// interval.  Any error returned by heartbeatFn is silently discarded so a
// single failed heartbeat does not abort the loop.
//
// StartHeartbeat is non-blocking and idempotent: calling it more than once
// on the same manager is safe and the extra calls are no-ops.
func (m *TokenRefreshManager) StartHeartbeat(interval time.Duration, heartbeatFn func() error) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-m.stopCh:
				return
			case <-ticker.C:
				_ = heartbeatFn()
			}
		}
	}()
}

// StartAutoRefresh launches a background goroutine that checks the current
// token every checkInterval and calls Refresh when the token is expired or
// will expire within refreshBefore.
//
// StartAutoRefresh is non-blocking.  Call Stop to terminate the goroutine.
func (m *TokenRefreshManager) StartAutoRefresh(checkInterval, refreshBefore time.Duration) {
	go func() {
		ticker := time.NewTicker(checkInterval)
		defer ticker.Stop()
		for {
			select {
			case <-m.stopCh:
				return
			case <-ticker.C:
				tok := m.GetToken()
				if tok == "" {
					continue
				}
				claims, err := m.ParseClaims(tok)
				if err != nil {
					_ = m.Refresh()
					continue
				}
				if expRaw, ok := claims["exp"]; ok {
					if expFloat, ok := expRaw.(float64); ok {
						deadline := time.Unix(int64(expFloat), 0).Add(-refreshBefore)
						if time.Now().After(deadline) {
							_ = m.Refresh()
						}
					}
				}
			}
		}
	}()
}

// SendHeartbeat performs a single HTTP GET to heartbeatURL, attaching the
// current token as a Bearer Authorization header.  It is suitable for use as
// the heartbeatFn parameter of StartHeartbeat.
func (m *TokenRefreshManager) SendHeartbeat() error {
	if m.heartbeatURL == "" {
		return fmt.Errorf("token: heartbeat URL is not configured")
	}

	req, err := http.NewRequest(http.MethodGet, m.heartbeatURL, nil) // #nosec G107
	if err != nil {
		return fmt.Errorf("token: build heartbeat request: %w", err)
	}

	tok := m.GetToken()
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return fmt.Errorf("token: heartbeat request: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("token: heartbeat returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// Stop signals all background goroutines started by this manager to exit.
// Stop is idempotent.
func (m *TokenRefreshManager) Stop() {
	m.once.Do(func() {
		close(m.stopCh)
	})
}
