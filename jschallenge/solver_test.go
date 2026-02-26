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
