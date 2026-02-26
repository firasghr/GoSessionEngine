package jschallenge_test

import (
	"testing"

	"github.com/firasghr/GoSessionEngine/jschallenge"
)

func newSolver(t *testing.T) *jschallenge.OttoSolver {
	t.Helper()
	s, err := jschallenge.NewOttoSolver("")
	if err != nil {
		t.Fatalf("NewOttoSolver: %v", err)
	}
	return s
}

func TestEval_Arithmetic(t *testing.T) {
	s := newSolver(t)
	result, err := s.Eval("2 + 2 * 3")
	if err != nil {
		t.Fatalf("Eval error: %v", err)
	}
	if result != "8" {
		t.Errorf("2+2*3: got %q, want 8", result)
	}
}

func TestEval_StringConcat(t *testing.T) {
	s := newSolver(t)
	result, err := s.Eval(`"hello" + " " + "world"`)
	if err != nil {
		t.Fatalf("Eval error: %v", err)
	}
	if result != "hello world" {
		t.Errorf("string concat: got %q, want 'hello world'", result)
	}
}

func TestEval_NavigatorUserAgent(t *testing.T) {
	ua := "TestAgent/1.0"
	s, err := jschallenge.NewOttoSolver(ua)
	if err != nil {
		t.Fatalf("NewOttoSolver: %v", err)
	}
	result, err := s.Eval("navigator.userAgent")
	if err != nil {
		t.Fatalf("Eval error: %v", err)
	}
	if result != ua {
		t.Errorf("navigator.userAgent: got %q, want %q", result, ua)
	}
}

func TestEval_WindowIsDefined(t *testing.T) {
	s := newSolver(t)
	result, err := s.Eval("typeof window")
	if err != nil {
		t.Fatalf("Eval error: %v", err)
	}
	if result != "object" {
		t.Errorf("window type: got %q, want object", result)
	}
}

func TestEval_DocumentDefined(t *testing.T) {
	s := newSolver(t)
	result, err := s.Eval("typeof document")
	if err != nil {
		t.Fatalf("Eval error: %v", err)
	}
	if result != "object" {
		t.Errorf("document type: got %q, want object", result)
	}
}

func TestEval_SyntaxError(t *testing.T) {
	s := newSolver(t)
	_, err := s.Eval("{{{{ invalid js")
	if err == nil {
		t.Error("expected error for invalid JavaScript")
	}
}

func TestEval_MultilineChallenge(t *testing.T) {
	s := newSolver(t)
	script := `
		var a = 7;
		var b = 6;
		a * b;
	`
	result, err := s.Eval(script)
	if err != nil {
		t.Fatalf("Eval error: %v", err)
	}
	if result != "42" {
		t.Errorf("multiline challenge: got %q, want 42", result)
	}
}

func TestGetSetCookie(t *testing.T) {
	s := newSolver(t)

	if err := s.SetCookie("session=abc123"); err != nil {
		t.Fatalf("SetCookie error: %v", err)
	}
	got, err := s.GetCookie()
	if err != nil {
		t.Fatalf("GetCookie error: %v", err)
	}
	if got != "session=abc123" {
		t.Errorf("GetCookie: got %q, want session=abc123", got)
	}
}

func TestCookieSeedingScript(t *testing.T) {
	s := newSolver(t)
	// Simulate a real cookie-seeding challenge script.
	script := `document.cookie = "cf_clearance=" + (1 + 2 + 3).toString();`
	if _, err := s.Eval(script); err != nil {
		t.Fatalf("Eval error: %v", err)
	}
	got, err := s.GetCookie()
	if err != nil {
		t.Fatalf("GetCookie error: %v", err)
	}
	if got != "cf_clearance=6" {
		t.Errorf("cookie seeding: got %q, want cf_clearance=6", got)
	}
}

func TestSolverImplementsInterface(t *testing.T) {
	s, err := jschallenge.NewOttoSolver("")
	if err != nil {
		t.Fatal(err)
	}
	// Compile-time check that *OttoSolver implements Solver.
	var _ jschallenge.Solver = s
}

// TestAkamaiStyleScript injects a mock Akamai-style challenge script that
//
//  1. Accesses window.navigator.userAgent (must not throw ReferenceError).
//  2. Reads document.cookie (must not throw ReferenceError).
//  3. Performs an integer math operation.
//  4. Seeds document.cookie with the computed result.
//
// The test asserts that GetCookie returns the expected cookie string,
// confirming that the Go solver handles all Akamai DOM globals and produces
// the correct side-effect without any JavaScript errors.
func TestAkamaiStyleScript(t *testing.T) {
	const ua = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

	s, err := jschallenge.NewOttoSolver(ua)
	if err != nil {
		t.Fatalf("NewOttoSolver: %v", err)
	}

	// Mock Akamai _abck seeding script.
	//  - window.navigator.userAgent is read and must equal the injected UA.
	//  - document.cookie is read (initially "") without error.
	//  - A math expression derives the token value.
	//  - document.cookie is set to the computed _abck value.
	script := `
		var ua      = window.navigator.userAgent;
		var initial = document.cookie;
		var token   = Math.floor(3.7) * 2 + 1;
		document.cookie = "_abck=" + token + "; path=/";
	`
	if _, err := s.Eval(script); err != nil {
		t.Fatalf("Eval Akamai-style script: %v", err)
	}

	// Verify navigator.userAgent was readable inside the script.
	gotUA, err := s.Eval("window.navigator.userAgent")
	if err != nil {
		t.Fatalf("Eval window.navigator.userAgent: %v", err)
	}
	if gotUA != ua {
		t.Errorf("window.navigator.userAgent: got %q, want %q", gotUA, ua)
	}

	// Math.floor(3.7) = 3, token = 3*2+1 = 7
	const wantCookie = "_abck=7; path=/"
	got, err := s.GetCookie()
	if err != nil {
		t.Fatalf("GetCookie: %v", err)
	}
	if got != wantCookie {
		t.Errorf("document.cookie: got %q, want %q", got, wantCookie)
	}
}
