package fingerprint_test

import (
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"testing"

	"github.com/firasghr/GoSessionEngine/fingerprint"
)

func TestGenerateSensorPayload_NotNil(t *testing.T) {
	p := fingerprint.GenerateSensorPayload(nil, 1)
	if p == nil {
		t.Fatal("GenerateSensorPayload returned nil")
	}
}

func TestGenerateSensorPayload_ScreenResolution(t *testing.T) {
	p := fingerprint.GenerateSensorPayload(nil, 1)
	if p.Screen.Width <= 0 || p.Screen.Height <= 0 {
		t.Errorf("invalid screen resolution: %dx%d", p.Screen.Width, p.Screen.Height)
	}
	// All known resolutions have width >= 1280.
	if p.Screen.Width < 1280 {
		t.Errorf("screen width %d is unrealistically small for a Windows Chrome client", p.Screen.Width)
	}
}

func TestGenerateSensorPayload_PluginsLength(t *testing.T) {
	p := fingerprint.GenerateSensorPayload(nil, 1)
	// Chrome on Windows reports 3–5 plugins.
	if p.Navigator.PluginsLength < 3 || p.Navigator.PluginsLength > 5 {
		t.Errorf("pluginsLength %d outside expected range [3,5]", p.Navigator.PluginsLength)
	}
}

func TestGenerateSensorPayload_TimezoneOffset(t *testing.T) {
	p := fingerprint.GenerateSensorPayload(nil, 1)
	// Offset must be one of our known values (bounded range).
	if p.TimezoneOffset < -720 || p.TimezoneOffset > 720 {
		t.Errorf("timezoneOffset %d outside plausible range", p.TimezoneOffset)
	}
}

func TestGenerateSensorPayload_MouseMovements(t *testing.T) {
	p := fingerprint.GenerateSensorPayload(nil, 1)
	if len(p.MouseMovements) < 20 {
		t.Errorf("expected at least 20 mouse points, got %d", len(p.MouseMovements))
	}

	// Timestamps must be monotonically increasing.
	for i := 1; i < len(p.MouseMovements); i++ {
		if p.MouseMovements[i].T < p.MouseMovements[i-1].T {
			t.Errorf("mouse timestamps not monotonically increasing at index %d", i)
		}
	}

	// Last two events should be mousedown (1) and mouseup (2).
	n := len(p.MouseMovements)
	if p.MouseMovements[n-2].EventType != 1 {
		t.Errorf("second-to-last event should be mousedown (1), got %d", p.MouseMovements[n-2].EventType)
	}
	if p.MouseMovements[n-1].EventType != 2 {
		t.Errorf("last event should be mouseup (2), got %d", p.MouseMovements[n-1].EventType)
	}
}

func TestGenerateSensorPayload_NonLinearPath(t *testing.T) {
	p := fingerprint.GenerateSensorPayload(nil, 1)
	moves := p.MouseMovements

	// Check that the path is not simply a straight line by measuring deviation.
	// A non-linear Bézier path will have at least some points off the
	// start→end line.
	if len(moves) < 3 {
		t.Skip("not enough points to check non-linearity")
	}
	x0, y0 := moves[0].X, moves[0].Y
	xN, yN := moves[len(moves)-1].X, moves[len(moves)-1].Y
	maxDev := 0.0
	for _, m := range moves[1 : len(moves)-1] {
		// Perpendicular distance from point to line (x0,y0)→(xN,yN).
		dx, dy := xN-x0, yN-y0
		length := math.Sqrt(dx*dx + dy*dy)
		if length < 1 {
			continue
		}
		dev := math.Abs((m.X-x0)*dy-(m.Y-y0)*dx) / length
		if dev > maxDev {
			maxDev = dev
		}
	}
	if maxDev < 1.0 {
		t.Errorf("mouse path appears to be a straight line (max deviation = %.3f px)", maxDev)
	}
}

func TestGenerateSensorPayload_WebDriverFalse(t *testing.T) {
	p := fingerprint.GenerateSensorPayload(nil, 1)
	if p.Navigator.WebDriver {
		t.Error("webDriver must always be false (critical bot signal)")
	}
}

func TestGenerateSensorPayload_Serialisable(t *testing.T) {
	p := fingerprint.GenerateSensorPayload(nil, 42)
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if len(b) == 0 {
		t.Error("serialised payload is empty")
	}

	// Round-trip: unmarshal back and check a few fields.
	var got fingerprint.SensorPayload
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if got.Seq != 42 {
		t.Errorf("Seq: got %d, want 42", got.Seq)
	}
	if got.Navigator.Platform != "Win32" {
		t.Errorf("platform: got %q, want Win32", got.Navigator.Platform)
	}
}

func TestNewSensorRequest_PostMethod(t *testing.T) {
	p := fingerprint.GenerateSensorPayload(nil, 1)
	req, err := fingerprint.NewSensorRequest("https://example.com/akam/11/pixel", p)
	if err != nil {
		t.Fatalf("NewSensorRequest: %v", err)
	}
	if req.Method != http.MethodPost {
		t.Errorf("expected POST, got %s", req.Method)
	}
}

func TestNewSensorRequest_ContentType(t *testing.T) {
	p := fingerprint.GenerateSensorPayload(nil, 1)
	req, err := fingerprint.NewSensorRequest("https://example.com/akam/11/pixel", p)
	if err != nil {
		t.Fatalf("NewSensorRequest: %v", err)
	}
	ct := req.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type: got %q, want text/plain;charset=UTF-8", ct)
	}
}

func TestNewSensorRequest_OriginHeader(t *testing.T) {
	p := fingerprint.GenerateSensorPayload(nil, 1)
	req, err := fingerprint.NewSensorRequest("https://example.com/akam/11/pixel", p)
	if err != nil {
		t.Fatalf("NewSensorRequest: %v", err)
	}
	if got := req.Header.Get("Origin"); got != "https://example.com" {
		t.Errorf("Origin: got %q, want https://example.com", got)
	}
}

func TestNewSensorRequest_BodyNotEmpty(t *testing.T) {
	p := fingerprint.GenerateSensorPayload(nil, 1)
	req, err := fingerprint.NewSensorRequest("https://example.com/akam/11/pixel", p)
	if err != nil {
		t.Fatalf("NewSensorRequest: %v", err)
	}
	if req.Body == nil {
		t.Fatal("expected non-nil request body")
	}
}

func TestGenerateSensorPayload_UniqueSequences(t *testing.T) {
	p1 := fingerprint.GenerateSensorPayload(nil, 1)
	p2 := fingerprint.GenerateSensorPayload(nil, 2)
	if p1.Seq == p2.Seq {
		t.Error("different seq values should produce different Seq fields")
	}
	// Mouse paths should differ across calls (different RNG seed from time).
	if len(p1.MouseMovements) > 0 && len(p2.MouseMovements) > 0 {
		if p1.MouseMovements[0].X == p2.MouseMovements[0].X &&
			p1.MouseMovements[0].Y == p2.MouseMovements[0].Y {
			t.Log("note: first mouse point identical (possible with same seed in fast tests)")
		}
	}
}
