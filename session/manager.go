// Package session – SessionManager manages the lifecycle of all sessions.
package session

import (
	"fmt"
	"sync"

	"github.com/firasghr/GoSessionEngine/config"
	"github.com/firasghr/GoSessionEngine/proxy"
)

// SessionManager manages up to 2 000 concurrent sessions.
//
// Concurrency model:
//   - A sync.RWMutex protects the sessions map.  Reads (GetSession, Count)
//     use RLock so they never block each other.  Writes (CreateSessions,
//     StopAll) use a full Lock.
//   - Session creation is parallelised with goroutines so that initialising
//     2 000 sessions (each requiring a TLS dial) does not take seconds on a
//     fast machine.  A sync.WaitGroup ensures CreateSessions blocks until
//     every goroutine has finished.
//   - Error collection uses a dedicated mutex so multiple goroutines can
//     append failures safely.
type SessionManager struct {
	sessions map[int]*Session
	mutex    sync.RWMutex
	config   *config.Config
}

// NewSessionManager creates an empty SessionManager backed by cfg.
func NewSessionManager(cfg *config.Config) *SessionManager {
	return &SessionManager{
		sessions: make(map[int]*Session),
		config:   cfg,
	}
}

// CreateSessions creates count sessions concurrently, assigning each one the
// next available proxy from pm (or an empty proxy if pm is nil or exhausted).
//
// Sessions are created in parallel goroutines – one per session – so the wall-
// clock time is bounded by the slowest individual session creation rather than
// O(count) serial time.  All goroutines must finish before the function
// returns.  If any session fails to initialise, an aggregated error is
// returned and the successfully-created sessions remain registered.
func (sm *SessionManager) CreateSessions(count int, pm *proxy.ProxyManager) error {
	type result struct {
		s   *Session
		err error
		id  int
	}

	results := make(chan result, count)
	var wg sync.WaitGroup

	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			p := ""
			if pm != nil {
				p = pm.GetNextProxy()
			}
			s, err := NewSession(id, p, sm.config)
			results <- result{s: s, err: err, id: id}
		}(i)
	}

	// Close the channel once all goroutines have written their result.
	go func() {
		wg.Wait()
		close(results)
	}()

	var errs []error
	sm.mutex.Lock()
	for r := range results {
		if r.err != nil {
			errs = append(errs, r.err)
			continue
		}
		sm.sessions[r.s.ID] = r.s
	}
	sm.mutex.Unlock()

	if len(errs) > 0 {
		return fmt.Errorf("session manager: %d session(s) failed to create; first error: %w", len(errs), errs[0])
	}
	return nil
}

// GetSession returns the session with the given id and true, or nil and false
// if no such session exists.  Safe for concurrent use.
func (sm *SessionManager) GetSession(id int) (*Session, bool) {
	sm.mutex.RLock()
	s, ok := sm.sessions[id]
	sm.mutex.RUnlock()
	return s, ok
}

// StartAll transitions every session from "idle" to "active".  It is
// intentionally lightweight: actual work is dispatched by the Scheduler.
func (sm *SessionManager) StartAll() {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()
	for _, s := range sm.sessions {
		s.mu.Lock()
		if s.State == "idle" {
			s.State = "active"
		}
		s.mu.Unlock()
	}
}

// StopAll closes every session, releasing their HTTP transport resources.
func (sm *SessionManager) StopAll() {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()
	for id, s := range sm.sessions {
		s.Close()
		delete(sm.sessions, id)
	}
}

// Count returns the number of currently registered sessions.
func (sm *SessionManager) Count() int {
	sm.mutex.RLock()
	n := len(sm.sessions)
	sm.mutex.RUnlock()
	return n
}
