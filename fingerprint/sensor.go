// Package sensor generates synthetic Akamai _abck-style telemetry payloads.
//
// Akamai Bot Manager (formerly known as Bot Detector) collects a rich set of
// client-side signals through its injected _abck JavaScript sensor script.
// The payload is a base64-encoded JSON object that includes:
//
//   - Device / screen metrics (resolution, colour depth, pixel ratio).
//   - Navigator properties (plugins.length, platform, language, cookiesEnabled).
//   - Timezone offset.
//   - A time-series array of mouse-movement events with sub-pixel coordinates,
//     timestamps, and derived velocity/acceleration signals.
//   - A canvas fingerprint hash (approximated here).
//   - A monotonic event sequence number.
//
// This package provides the SensorPayload type and the Generate function which
// produces randomised but statistically plausible values.  The output is
// designed to be sent as the body of a POST request to the Akamai sensor
// endpoint (typically /akam/11/… or a path matching /_bm/).
package fingerprint

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"strings"
	"time"
)

// ─── Data types ──────────────────────────────────────────────────────────────

// ScreenInfo represents the device screen/viewport geometry.
type ScreenInfo struct {
	Width       int `json:"width"`
	Height      int `json:"height"`
	AvailWidth  int `json:"availWidth"`
	AvailHeight int `json:"availHeight"`
	ColorDepth  int `json:"colorDepth"`
	PixelDepth  int `json:"pixelDepth"`
}

// NavigatorInfo mimics the subset of navigator properties collected by _abck.
type NavigatorInfo struct {
	// PluginsLength is navigator.plugins.length.  Chrome on Windows typically
	// reports 3–5 plugins (PDF Viewer, Chrome PDF Viewer, Native Client, …).
	PluginsLength    int    `json:"pluginsLength"`
	Platform         string `json:"platform"`
	Language         string `json:"language"`
	Languages        string `json:"languages"` // comma-separated
	CookiesEnabled   bool   `json:"cookiesEnabled"`
	DoNotTrack       string `json:"doNotTrack"`     // "1", "0", or "unspecified"
	HardwareConcurrency int `json:"hardwareConcurrency"`
	MaxTouchPoints   int    `json:"maxTouchPoints"`
	// WebDriver is true when navigator.webdriver is set – a critical bot signal.
	// This must always be false in a real browser payload.
	WebDriver        bool   `json:"webDriver"`
}

// MousePoint is one sample in the mouse-movement time series.
type MousePoint struct {
	// X and Y are viewport coordinates (pixels, sub-pixel precision).
	X float64 `json:"x"`
	Y float64 `json:"y"`
	// T is the milliseconds elapsed since the start of mouse recording.
	T int64 `json:"t"`
	// EventType: 0 = mousemove, 1 = mousedown, 2 = mouseup.
	EventType int `json:"e"`
}

// SensorPayload is the top-level object serialised and sent to the Akamai
// sensor endpoint.  Field names intentionally use the short, obfuscated-style
// keys that the _abck script uses internally.
type SensorPayload struct {
	// Sensor format version tag used by Akamai's server-side decoder.
	Version string `json:"sensor_data_version"`

	// ab is the _abck cookie value currently in the jar (empty on first hit).
	Ab string `json:"ab"`

	Screen    ScreenInfo    `json:"screen"`
	Navigator NavigatorInfo `json:"navigator"`

	// TimezoneOffset is minutes behind UTC (positive = west of UTC, matching
	// the JS Date.prototype.getTimezoneOffset() convention).
	// US Eastern Standard = 300, US Pacific Standard = 480, UTC = 0.
	TimezoneOffset int `json:"timezoneOffset"`

	// MouseMovements contains the recorded pointer path.  Akamai uses this
	// to run behavioural analytics – the array must be non-empty and must
	// exhibit a plausibly human non-linear path.
	MouseMovements []MousePoint `json:"mouseMovements"`

	// CanvasHash is a 32-bit canvas fingerprint (hex string).
	CanvasHash string `json:"canvasHash"`

	// Seq is a monotonically increasing counter (incremented per page request).
	Seq int `json:"seq"`

	// Timestamp is the Unix millisecond time of payload generation.
	Timestamp int64 `json:"timestamp"`
}

// ─── Generators ──────────────────────────────────────────────────────────────

// commonScreenResolutions lists the most common screen sizes reported by real
// Chrome 120 clients on Windows.
var commonScreenResolutions = []ScreenInfo{
	{1920, 1080, 1920, 1040, 24, 24},
	{1366, 768, 1366, 728, 24, 24},
	{1536, 864, 1536, 824, 24, 24},
	{1440, 900, 1440, 860, 24, 24},
	{1280, 720, 1280, 680, 24, 24},
	{2560, 1440, 2560, 1400, 24, 24},
	{1600, 900, 1600, 860, 24, 24},
}

// commonTimezoneOffsets lists common Windows client timezone offsets in
// minutes (matching JS Date.getTimezoneOffset()).
var commonTimezoneOffsets = []int{
	0,   // UTC
	-60, // CET (Europe/Paris)
	300, // EST (US/Eastern)
	360, // CST (US/Central)
	420, // MST (US/Mountain)
	480, // PST (US/Pacific)
	-330, // IST (India) – negative because IST is ahead of UTC
	-540, // JST (Japan)
}

// GenerateSensorPayload creates a SensorPayload with randomised but
// statistically realistic values.  rng may be nil, in which case a
// new source seeded from the current time is created.
//
// seq should be incremented by the caller on each page load so that the master
// server can detect replay attacks via the monotonic counter.
func GenerateSensorPayload(rng *rand.Rand, seq int) *SensorPayload {
	if rng == nil {
		rng = rand.New(rand.NewSource(time.Now().UnixNano())) // #nosec G404
	}

	screen := commonScreenResolutions[rng.Intn(len(commonScreenResolutions))]
	tzOffset := commonTimezoneOffsets[rng.Intn(len(commonTimezoneOffsets))]

	// plugins.length: Chrome on Windows typically has 3–5 navigator.plugins
	// (PDF Viewer, Chrome PDF Viewer, Widevine Content Decryption Module, …).
	pluginCount := 3 + rng.Intn(3) // [3, 5]

	nav := NavigatorInfo{
		PluginsLength:       pluginCount,
		Platform:            "Win32",
		Language:            "en-US",
		Languages:           "en-US,en",
		CookiesEnabled:      true,
		DoNotTrack:          "unspecified",
		HardwareConcurrency: hwConcurrency(rng),
		MaxTouchPoints:      0,
		WebDriver:           false, // must always be false
	}

	movements := generateMousePath(rng, screen.Width, screen.Height)

	return &SensorPayload{
		Version:        "2.0",
		Ab:             "",
		Screen:         screen,
		Navigator:      nav,
		TimezoneOffset: tzOffset,
		MouseMovements: movements,
		CanvasHash:     randomCanvasHash(rng),
		Seq:            seq,
		Timestamp:      time.Now().UnixMilli(),
	}
}

// hwConcurrency returns a plausible navigator.hardwareConcurrency value.
// Modern Windows laptops typically report 4, 8, or 12 logical cores.
func hwConcurrency(rng *rand.Rand) int {
	choices := []int{4, 4, 4, 8, 8, 8, 12, 16}
	return choices[rng.Intn(len(choices))]
}

// randomCanvasHash returns a fake 8-hex-digit canvas fingerprint.
func randomCanvasHash(rng *rand.Rand) string {
	return fmt.Sprintf("%08x", rng.Uint32())
}

// generateMousePath produces a slice of MousePoint values that trace a
// smooth, non-linear Bézier-like path across the viewport.
//
// The algorithm:
//  1. Pick a random start point near the top-left quadrant.
//  2. Pick a random end point near the centre of the page.
//  3. Generate two off-axis control points to create a curved, human-like arc.
//  4. Sample the cubic Bézier at monotonically increasing t values with slight
//     jitter on both position and timing to simulate natural hand tremor.
//  5. Append a final "click" sequence (mousedown + mouseup) at the endpoint.
func generateMousePath(rng *rand.Rand, screenW, screenH int) []MousePoint {
	const (
		minPoints = 18
		maxPoints = 45
	)
	n := minPoints + rng.Intn(maxPoints-minPoints+1)

	// Start: upper-left area of the viewport.
	x0 := float64(50 + rng.Intn(screenW/4))
	y0 := float64(50 + rng.Intn(screenH/4))

	// End: somewhere near the centre (target element area).
	x3 := float64(screenW/4 + rng.Intn(screenW/2))
	y3 := float64(screenH/4 + rng.Intn(screenH/2))

	// Control points: offset to produce a curved arc.
	x1 := x0 + float64(rng.Intn(screenW/3)+screenW/6)
	y1 := y0 - float64(rng.Intn(screenH/4)+30)
	x2 := x3 - float64(rng.Intn(screenW/3)+screenW/6)
	y2 := y3 + float64(rng.Intn(screenH/4)+30)

	points := make([]MousePoint, 0, n+3)

	// Base time: random start in the last 2 seconds.
	baseT := int64(800 + rng.Intn(1200)) // ms since recording started
	elapsed := int64(0)

	for i := 0; i < n; i++ {
		// Cubic Bézier parameter, slightly non-uniform to simulate acceleration
		// / deceleration (ease-in-out via a sin-based distribution).
		rawT := float64(i) / float64(n-1)
		bt := easeInOut(rawT)

		x, y := cubicBezier(bt, x0, y0, x1, y1, x2, y2, x3, y3)

		// Sub-pixel jitter to simulate optical mouse noise.
		x += (rng.Float64() - 0.5) * 1.2
		y += (rng.Float64() - 0.5) * 1.2

		// Inter-sample delay: faster in the middle of the gesture, slower at
		// start and end (matches real human deceleration near a target).
		speed := 0.5 + math.Sin(math.Pi*rawT)          // peaks at t=0.5
		delay := int64(math.Round(12 / (speed + 0.1))) // 6–22 ms
		delay += int64(rng.Intn(6)) - 2                 // ± 2 ms jitter
		if delay < 4 {
			delay = 4
		}
		elapsed += delay

		points = append(points, MousePoint{
			X: math.Round(x*100) / 100,
			Y: math.Round(y*100) / 100,
			T: baseT + elapsed,
			EventType: 0, // mousemove
		})
	}

	// Simulate a click at the endpoint: mousedown followed by mouseup.
	lastT := points[len(points)-1].T
	points = append(points,
		MousePoint{X: x3, Y: y3, T: lastT + int64(20+rng.Intn(40)), EventType: 1},
		MousePoint{X: x3, Y: y3, T: lastT + int64(80+rng.Intn(120)), EventType: 2},
	)

	return points
}

// cubicBezier evaluates the cubic Bézier curve at parameter t ∈ [0,1].
func cubicBezier(t, x0, y0, x1, y1, x2, y2, x3, y3 float64) (float64, float64) {
	u := 1 - t
	x := u*u*u*x0 + 3*u*u*t*x1 + 3*u*t*t*x2 + t*t*t*x3
	y := u*u*u*y0 + 3*u*u*t*y1 + 3*u*t*t*y2 + t*t*t*y3
	return x, y
}

// easeInOut maps t ∈ [0,1] through a smooth cubic ease-in-out curve.
func easeInOut(t float64) float64 {
	return t * t * (3 - 2*t)
}

// ─── HTTP helpers ─────────────────────────────────────────────────────────────

// MarshalJSON serialises p to compact JSON.
func (p *SensorPayload) MarshalJSON() ([]byte, error) {
	// Use a type alias to avoid infinite recursion.
	type alias SensorPayload
	return json.Marshal((*alias)(p))
}

// NewSensorRequest builds a POST *http.Request aimed at endpoint with the
// sensor payload as the body.  endpoint is typically the Akamai sensor path,
// e.g. "https://example.com/akam/11/pixel_c22cfd2d".
//
// The request carries the standard Akamai sensor content-type and an Origin
// header matching the target origin.
func NewSensorRequest(endpoint string, payload *SensorPayload) (*http.Request, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("sensor: marshal payload: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("sensor: build request: %w", err)
	}

	// Derive the Origin from the endpoint URL.
	origin := extractOrigin(endpoint)

	req.Header.Set("Content-Type", "text/plain;charset=UTF-8")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Origin", origin)
	req.Header.Set("Referer", origin+"/")
	req.Header.Set("sec-fetch-site", "same-origin")
	req.Header.Set("sec-fetch-mode", "cors")
	req.Header.Set("sec-fetch-dest", "empty")

	return req, nil
}

// extractOrigin returns the scheme+host portion of rawURL.
func extractOrigin(rawURL string) string {
	// Fast path: find third slash.
	if i := strings.Index(rawURL, "://"); i >= 0 {
		rest := rawURL[i+3:]
		if j := strings.IndexByte(rest, '/'); j >= 0 {
			return rawURL[:i+3+j]
		}
		return rawURL
	}
	return rawURL
}
