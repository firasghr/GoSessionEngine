package fingerprint_test

import (
	"net/http"
	"testing"

	"github.com/firasghr/GoSessionEngine/fingerprint"
)

func TestChromeProfile_NotNil(t *testing.T) {
	p := fingerprint.ChromeProfile()
	if p == nil {
		t.Fatal("ChromeProfile returned nil")
	}
	if p.TLSConfig == nil {
		t.Error("TLSConfig should not be nil")
	}
	if p.UserAgent == "" {
		t.Error("UserAgent should not be empty")
	}
	if len(p.ExtraHeaders) == 0 {
		t.Error("ExtraHeaders should not be empty")
	}
}

func TestFirefoxProfile_NotNil(t *testing.T) {
	p := fingerprint.FirefoxProfile()
	if p == nil {
		t.Fatal("FirefoxProfile returned nil")
	}
	if p.TLSConfig == nil {
		t.Error("TLSConfig should not be nil")
	}
	if p.UserAgent == "" {
		t.Error("UserAgent should not be empty")
	}
}

func TestApplyToTransport_SetsTLSConfig(t *testing.T) {
	p := fingerprint.ChromeProfile()
	tr := &http.Transport{}
	p.ApplyToTransport(tr)

	if tr.TLSClientConfig == nil {
		t.Fatal("TLSClientConfig not set on transport")
	}
	if len(tr.TLSClientConfig.CipherSuites) == 0 {
		t.Error("expected non-empty cipher suite list")
	}
}

func TestApplyToTransport_NilTransport(t *testing.T) {
	p := fingerprint.ChromeProfile()
	// Must not panic.
	p.ApplyToTransport(nil)
}

func TestApplyToTransport_Isolation(t *testing.T) {
	p := fingerprint.ChromeProfile()
	tr1 := &http.Transport{}
	tr2 := &http.Transport{}
	p.ApplyToTransport(tr1)
	p.ApplyToTransport(tr2)

	// Modifying one transport's TLS config must not affect the other.
	tr1.TLSClientConfig.MinVersion = 0
	if tr2.TLSClientConfig.MinVersion == 0 {
		t.Error("TLS configs of tr1 and tr2 should be independent clones")
	}
}

func TestApplyHeaders_SetsUserAgent(t *testing.T) {
	p := fingerprint.ChromeProfile()
	headers := make(map[string]string)
	p.ApplyHeaders(headers)

	if headers["User-Agent"] != p.UserAgent {
		t.Errorf("User-Agent: got %q, want %q", headers["User-Agent"], p.UserAgent)
	}
}

func TestApplyHeaders_ExtraHeadersPresent(t *testing.T) {
	p := fingerprint.ChromeProfile()
	headers := make(map[string]string)
	p.ApplyHeaders(headers)

	if headers["Accept"] == "" {
		t.Error("expected Accept header to be set")
	}
	if headers["Accept-Language"] == "" {
		t.Error("expected Accept-Language header to be set")
	}
}

func TestApplyHeaders_DoesNotOverrideExisting(t *testing.T) {
	p := fingerprint.ChromeProfile()
	headers := map[string]string{
		"Accept": "application/json",
	}
	p.ApplyHeaders(headers)

	if headers["Accept"] != "application/json" {
		t.Errorf("existing Accept header should not be overridden, got %q", headers["Accept"])
	}
}

func TestApplyHeaders_NilMap(t *testing.T) {
	p := fingerprint.ChromeProfile()
	// Must not panic.
	p.ApplyHeaders(nil)
}

func TestChromeCipherSuites_MinLength(t *testing.T) {
	p := fingerprint.ChromeProfile()
	if len(p.TLSConfig.CipherSuites) < 4 {
		t.Errorf("expected at least 4 cipher suites, got %d", len(p.TLSConfig.CipherSuites))
	}
}
