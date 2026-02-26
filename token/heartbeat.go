// Package token – HeartbeatManager.
//
// HeartbeatManager extends the existing TokenRefreshManager with three
// additional capabilities needed for a 2 000-session, 6-PC cluster:
//
//  1. A thread-safe sync.Map that stores per-session SessionState values so
//     that multiple workers can read a shared authenticated session without
//     creating a bottleneck on a single mutex.
//
//  2. Automatic extraction of JWT tokens and session cookies from HTTP
//     response Set-Cookie / Authorization headers, updating the stored state
//     in real time.
//
//  3. A background keep-alive goroutine that sends periodic HEAD or GET
//     requests to a configurable API endpoint, refreshes stale tokens, and
//     re-applies the latest cookies to the shared jar – all without disrupting
//     the main monitoring worker goroutines.
package token

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ─── SessionState ─────────────────────────────────────────────────────────────

// SessionState holds the current authentication credentials for one session.
// All fields are safe to read without a lock because the struct is replaced
// atomically in the sync.Map; callers should never mutate a retrieved pointer.
type SessionState struct {
	// SessionID is the session's numeric identifier within the engine.
	SessionID int

	// Token is the most recently obtained JWT (empty if not yet acquired).
	Token string

	// Cookies is the complete cookie set for this session.
	Cookies []*http.Cookie

	// LastRefreshed records when the token/cookies were last updated.
	LastRefreshed time.Time

	// Available is true when the session slot can be claimed by a new worker.
	// A worker that obtains valid cookies sets Available = true so other
	// workers can immediately reuse the session without re-solving a challenge.
	Available bool
}

// ─── HeartbeatManager ─────────────────────────────────────────────────────────

// HeartbeatManager manages background keep-alive requests and per-session
// authentication state.
//
// It wraps a sync.Map (keyed by session ID) so that:
//   - Thousands of goroutines can read session state concurrently with zero
//     lock contention.
//   - A single writer (the heartbeat goroutine or a challenge solver) updates
//     the entry atomically; all subsequent readers see the new value.
type HeartbeatManager struct {
	// sessions maps int (session ID) → *SessionState.
	sessions sync.Map

	keepAliveURL string
	client       *http.Client

	interval time.Duration
	stopCh   chan struct{}
	once     sync.Once

	// heartbeatCount is incremented on each successful keep-alive round-trip.
	heartbeatCount atomic.Int64
}

// NewHeartbeatManager creates a HeartbeatManager.
//
//   - keepAliveURL is the endpoint hit by the background goroutine (e.g.
//     "https://example.com/api/heartbeat").  Pass an empty string to disable
//     network keep-alives and use the manager only for state storage.
//   - interval is how often the keep-alive loop fires.  Typical value: 30 s.
//   - client is the HTTP client used for keep-alive requests.  Pass nil to use
//     http.DefaultClient.
func NewHeartbeatManager(keepAliveURL string, interval time.Duration, client *http.Client) *HeartbeatManager {
	if client == nil {
		client = http.DefaultClient
	}
	return &HeartbeatManager{
		keepAliveURL: keepAliveURL,
		client:       client,
		interval:     interval,
		stopCh:       make(chan struct{}),
	}
}

// ─── Session state API ────────────────────────────────────────────────────────

// SetState stores or replaces the SessionState for sessionID.
// Safe for concurrent use; the sync.Map provides lock-free reads after the
// initial store.
func (m *HeartbeatManager) SetState(sessionID int, state *SessionState) {
	m.sessions.Store(sessionID, state)
}

// GetState returns the SessionState for sessionID, or nil if not yet recorded.
func (m *HeartbeatManager) GetState(sessionID int) *SessionState {
	v, ok := m.sessions.Load(sessionID)
	if !ok {
		return nil
	}
	s, _ := v.(*SessionState)
	return s
}

// FindAvailable returns the first session that is marked Available, or nil if
// none exists.  Multiple goroutines may call this concurrently; the first one
// to call ClaimSession wins.
func (m *HeartbeatManager) FindAvailable() *SessionState {
	var found *SessionState
	m.sessions.Range(func(_, v any) bool {
		s, ok := v.(*SessionState)
		if ok && s.Available {
			found = s
			return false // stop iteration
		}
		return true
	})
	return found
}

// ClaimSession atomically marks the session as unavailable (preventing other
// workers from claiming it) and returns true.  Returns false if the session
// was already unavailable or does not exist.
func (m *HeartbeatManager) ClaimSession(sessionID int) bool {
	v, ok := m.sessions.Load(sessionID)
	if !ok {
		return false
	}
	old, ok := v.(*SessionState)
	if !ok || !old.Available {
		return false
	}
	// Replace with a copy that has Available = false.
	updated := *old
	updated.Available = false
	// CompareAndSwap ensures we win the race if two goroutines call
	// ClaimSession simultaneously.
	return m.sessions.CompareAndSwap(sessionID, old, &updated)
}

// ExtractFromResponse inspects resp and updates the SessionState for
// sessionID:
//
//   - Any Set-Cookie headers are stored in the session's Cookies slice.
//   - If a "Authorization: Bearer <token>" header is present in the response
//     (some APIs echo the new token in the response), the token is updated.
//   - If Set-Cookie contains a cookie whose name contains "jwt" or "token"
//     (case-insensitive), its value is also treated as the new JWT.
//
// The session is marked Available = true whenever at least one new cookie is
// extracted, so waiting workers can immediately claim the slot.
func (m *HeartbeatManager) ExtractFromResponse(sessionID int, resp *http.Response) {
	if resp == nil {
		return
	}

	newCookies := resp.Cookies()
	if len(newCookies) == 0 {
		return
	}

	// Retrieve existing state or start fresh.
	existing := m.GetState(sessionID)
	var base SessionState
	if existing != nil {
		base = *existing // copy
	} else {
		base.SessionID = sessionID
	}

	// Merge: replace cookies with the same name, append new ones.
	merged := mergeCookies(base.Cookies, newCookies)
	base.Cookies = merged
	base.LastRefreshed = time.Now()
	base.Available = true

	// Extract JWT from cookies if present.
	for _, c := range newCookies {
		lname := strings.ToLower(c.Name)
		if strings.Contains(lname, "jwt") || strings.Contains(lname, "token") {
			base.Token = c.Value
			break
		}
	}

	// Also check for Authorization header in the response.
	if auth := resp.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		base.Token = strings.TrimPrefix(auth, "Bearer ")
	}

	m.SetState(sessionID, &base)
}

// mergeCookies returns a new slice that contains all cookies from existing,
// updated with any cookies from updates that share the same name, plus any
// cookies in updates that are not yet in existing.
func mergeCookies(existing, updates []*http.Cookie) []*http.Cookie {
	out := make([]*http.Cookie, len(existing))
	copy(out, existing)

	for _, u := range updates {
		found := false
		for i, e := range out {
			if e.Name == u.Name {
				out[i] = u
				found = true
				break
			}
		}
		if !found {
			out = append(out, u)
		}
	}
	return out
}

// ─── Background keep-alive ────────────────────────────────────────────────────

// Start launches the background keep-alive goroutine.  It is idempotent:
// calling Start more than once is a no-op.
//
// The goroutine fires every m.interval, sends a keep-alive request to
// m.keepAliveURL (if non-empty), and calls ExtractFromResponse on the reply
// so that any Set-Cookie headers in the keep-alive response are propagated to
// the session states automatically.
//
// sessionIDs is the list of session IDs whose tokens should be attached to the
// keep-alive request.  Pass nil to send the request without any token.
func (m *HeartbeatManager) Start(ctx context.Context, sessionIDs []int) {
	m.once.Do(func() {
		go m.loop(ctx, sessionIDs)
	})
}

// Stop signals the background goroutine to exit.  Idempotent.
func (m *HeartbeatManager) Stop() {
	m.once.Do(func() {}) // ensure once is consumed even if Start was never called
	select {
	case <-m.stopCh:
	default:
		close(m.stopCh)
	}
}

// HeartbeatCount returns how many successful keep-alive round-trips have
// completed since the manager started.
func (m *HeartbeatManager) HeartbeatCount() int64 { return m.heartbeatCount.Load() }

func (m *HeartbeatManager) loop(ctx context.Context, sessionIDs []int) {
	if m.interval <= 0 {
		m.interval = 30 * time.Second
	}
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.sendKeepAlive(sessionIDs)
		}
	}
}

// sendKeepAlive fires a single keep-alive request.  Errors are silently
// discarded so one failed heartbeat does not abort the loop.
func (m *HeartbeatManager) sendKeepAlive(sessionIDs []int) {
	if m.keepAliveURL == "" {
		return
	}

	req, err := http.NewRequest(http.MethodGet, m.keepAliveURL, nil) // #nosec G107
	if err != nil {
		return
	}

	// Attach the token from the first available session that has one.
	for _, id := range sessionIDs {
		if s := m.GetState(id); s != nil && s.Token != "" {
			req.Header.Set("Authorization", "Bearer "+s.Token)
			break
		}
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return
	}

	m.heartbeatCount.Add(1)

	// Propagate any Set-Cookie headers to all tracked sessions.
	for _, id := range sessionIDs {
		m.ExtractFromResponse(id, resp)
	}
}

// AllStates returns a snapshot of every stored SessionState.  The result is a
// newly allocated map; mutations do not affect the manager's state.
func (m *HeartbeatManager) AllStates() map[int]*SessionState {
	out := make(map[int]*SessionState)
	m.sessions.Range(func(k, v any) bool {
		id, ok1 := k.(int)
		s, ok2 := v.(*SessionState)
		if ok1 && ok2 {
			out[id] = s
		}
		return true
	})
	return out
}

// ApplyCookiesToRequest sets the cookies from sessionID's SessionState on req.
// If the session has no state or no cookies, the request is not modified.
func (m *HeartbeatManager) ApplyCookiesToRequest(sessionID int, req *http.Request) error {
	s := m.GetState(sessionID)
	if s == nil || len(s.Cookies) == 0 {
		return nil
	}
	if req == nil {
		return fmt.Errorf("heartbeat: apply cookies: request must not be nil")
	}
	for _, c := range s.Cookies {
		req.AddCookie(c)
	}
	return nil
}
