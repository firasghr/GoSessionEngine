// Package jschallenge provides a zero-browser JavaScript challenge solver for
// GoSessionEngine.
//
// Many target services defend their endpoints with lightweight JavaScript
// challenges – dynamic math expressions, cookie-seeding scripts, or obfuscated
// one-liners – that must be evaluated before the real request can be sent.
// This package solves those challenges in-process using the otto pure-Go
// JavaScript interpreter, requiring no headless browser or external process.
//
// Architecture:
//   - Solver is the public interface; callers supply a raw JavaScript snippet
//     and receive the evaluated result as a string.
//   - OttoSolver wraps an otto.Otto VM.  Each solver instance is protected by
//     a sync.Mutex so a single VM may be shared across goroutines.  For
//     maximum throughput at 2,000 sessions, create one OttoSolver per session.
//   - The VM is seeded with a minimal browser-like global (navigator.userAgent,
//     window, document) so common fingerprinting scripts run without errors.
package jschallenge

import (
	"fmt"
	"sync"

	"github.com/robertkrimen/otto"
)

// Solver is the interface implemented by all challenge solvers.
type Solver interface {
	// Eval executes script and returns the string representation of the
	// final expression value.  Returns an error on syntax or runtime errors.
	Eval(script string) (string, error)
}

// OttoSolver implements Solver using the otto pure-Go JavaScript interpreter.
// It is safe for concurrent use: a mutex serialises access to the shared VM.
type OttoSolver struct {
	vm *otto.Otto
	mu sync.Mutex
}

// NewOttoSolver creates a new OttoSolver with a browser-stub environment
// pre-loaded.  The stub defines window, document, and navigator.userAgent so
// that typical challenge scripts that reference these globals run without
// ReferenceError.
//
// Pass userAgent as the User-Agent string to expose to the JS environment.
// If empty, a generic string is used.
func NewOttoSolver(userAgent string) (*OttoSolver, error) {
	if userAgent == "" {
		userAgent = "Mozilla/5.0 (compatible; GoSessionEngine/1.0)"
	}
	vm := otto.New()

	// Seed minimal browser globals so challenge scripts do not throw on
	// missing references.
	bootstrap := fmt.Sprintf(`
var window = this;
var document = { cookie: "" };
var navigator = { userAgent: %q };
`, userAgent)

	if _, err := vm.Run(bootstrap); err != nil {
		return nil, fmt.Errorf("jschallenge: bootstrap JS globals: %w", err)
	}
	return &OttoSolver{vm: vm}, nil
}

// Eval executes the given JavaScript snippet and returns the string
// representation of the value produced by the last expression.
//
// The method acquires the VM mutex for the duration of the call, so concurrent
// Eval invocations are serialised on the same OttoSolver.  To parallelise
// challenge solving across many sessions, give each session its own
// OttoSolver.
func (s *OttoSolver) Eval(script string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	val, err := s.vm.Run(script)
	if err != nil {
		return "", fmt.Errorf("jschallenge: eval: %w", err)
	}
	result, err := val.ToString()
	if err != nil {
		return "", fmt.Errorf("jschallenge: convert result to string: %w", err)
	}
	return result, nil
}

// GetCookie retrieves the value of document.cookie from the JS environment.
// Challenge scripts that seed cookies via document.cookie = "..." store them
// here; callers should copy the value into their HTTP cookie jar after running
// the challenge.
func (s *OttoSolver) GetCookie() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	val, err := s.vm.Get("document")
	if err != nil {
		return "", fmt.Errorf("jschallenge: get document: %w", err)
	}
	cookieVal, err := val.Object().Get("cookie")
	if err != nil {
		return "", fmt.Errorf("jschallenge: get document.cookie: %w", err)
	}
	return cookieVal.String(), nil
}

// SetCookie injects a cookie string into document.cookie in the JS environment
// before running a challenge that expects existing cookies to be present.
func (s *OttoSolver) SetCookie(cookie string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	script := fmt.Sprintf("document.cookie = %q;", cookie)
	if _, err := s.vm.Run(script); err != nil {
		return fmt.Errorf("jschallenge: set document.cookie: %w", err)
	}
	return nil
}
