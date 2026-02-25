// Package session provides the Session type – the fundamental unit of the
// automation engine. Each session owns its own HTTP client, cookie jar, and
// set of HTTP headers so it can operate fully independently of all other
// sessions.
package session

import (
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/firasghr/GoSessionEngine/client"
	"github.com/firasghr/GoSessionEngine/config"
)

// Session represents one independent automation session.
//
// Architecture notes:
//   - Each session holds its own *http.Client so that connection pools and
//     cookie jars are never shared between sessions.  This eliminates
//     cross-session interference and makes the engine behave predictably even
//     at 2 000 concurrent sessions.
//   - A sync.RWMutex protects the mutable fields (Headers, State,
//     LastActivity) so callers may safely read/write from multiple goroutines.
//   - CreatedAt is set once at construction and never mutated; no lock is
//     needed to read it.
type Session struct {
	// ID uniquely identifies the session within the engine.
	ID int

	// Client is the underlying HTTP client.  It must not be replaced after
	// construction; replace the whole Session instead.
	Client *http.Client

	// CookieJar stores cookies for this session.  It is also embedded inside
	// Client, so cookies are applied automatically on every request.
	CookieJar http.CookieJar

	// Proxy is the proxy URL string used by this session, or empty for direct
	// connections.  Stored for introspection/logging purposes only; the actual
	// proxy is baked into the HTTP transport at construction time.
	Proxy string

	// Headers contains custom HTTP headers injected into every request made by
	// this session (e.g. User-Agent, Authorization).
	Headers map[string]string

	// State represents the current lifecycle state of the session.
	// Conventional values: "idle", "active", "closed".
	State string

	// CreatedAt records the wall-clock time the session was constructed.
	CreatedAt time.Time

	// LastActivity records the wall-clock time of the most-recent request.
	// Updated automatically by ExecuteRequest; may also be called manually
	// via UpdateLastActivity.
	LastActivity time.Time

	mu sync.RWMutex // guards Headers, State, LastActivity
}

// NewSession constructs a Session with a dedicated HTTP client configured
// according to cfg.  proxy may be an empty string for direct connections.
//
// Returns an error if the HTTP client cannot be constructed (e.g. invalid
// proxy URL).
func NewSession(id int, proxy string, cfg *config.Config) (*Session, error) {
	if cfg == nil {
		return nil, fmt.Errorf("session %d: config must not be nil", id)
	}

	c, err := client.NewHTTPClient(proxy, cfg.RequestTimeout)
	if err != nil {
		return nil, fmt.Errorf("session %d: create HTTP client: %w", id, err)
	}

	now := time.Now()
	return &Session{
		ID:           id,
		Client:       c,
		CookieJar:    c.Jar,
		Proxy:        proxy,
		Headers:      make(map[string]string),
		State:        "idle",
		CreatedAt:    now,
		LastActivity: now,
	}, nil
}

// ExecuteRequest sends an HTTP request and returns the response.
//
// The method is safe for concurrent use: it acquires a read-lock to snapshot
// the current headers before building the request, and calls
// UpdateLastActivity (which acquires a write-lock) after the request
// completes.
//
// Callers are responsible for closing the returned *http.Response body.
func (s *Session) ExecuteRequest(method, targetURL string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, targetURL, body)
	if err != nil {
		return nil, fmt.Errorf("session %d: build request: %w", s.ID, err)
	}

	// Snapshot headers under a read-lock so we don't race with concurrent
	// header updates.
	s.mu.RLock()
	for k, v := range s.Headers {
		req.Header.Set(k, v)
	}
	s.mu.RUnlock()

	resp, err := s.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("session %d: execute %s %s: %w", s.ID, method, targetURL, err)
	}

	s.UpdateLastActivity()
	return resp, nil
}

// UpdateLastActivity records the current time as the session's last activity
// timestamp.  Call this whenever work is performed on the session outside of
// ExecuteRequest (e.g. after processing a response body).
func (s *Session) UpdateLastActivity() {
	s.mu.Lock()
	s.LastActivity = time.Now()
	s.mu.Unlock()
}

// Close transitions the session to the "closed" state and releases transport
// resources by closing all idle connections.  After Close returns the session
// must not be used.
func (s *Session) Close() {
	s.mu.Lock()
	s.State = "closed"
	s.mu.Unlock()

	// Drain the idle-connection pool so the OS can reclaim sockets promptly.
	if t, ok := s.Client.Transport.(*http.Transport); ok {
		t.CloseIdleConnections()
	}
}
