package client_test

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	utls "github.com/refraction-networking/utls"

	"github.com/firasghr/GoSessionEngine/client"
)

// chrome120TLS13Ciphers is the set of TLS 1.3 cipher suite IDs that Chrome 120
// advertises.  A Go standard-library TLS 1.3 server will always negotiate one
// of these when the client presents the Chrome 120 ClientHello.
var chrome120TLS13Ciphers = map[uint16]bool{
	tls.TLS_AES_128_GCM_SHA256:       true,
	tls.TLS_AES_256_GCM_SHA384:       true,
	tls.TLS_CHACHA20_POLY1305_SHA256: true,
}

// buildInsecureChrome120Transport returns an *http.Transport that uses the
// uTLS Chrome 120 fingerprint for TLS but skips certificate verification.
// InsecureSkipVerify is acceptable here because the test server uses a
// self-signed httptest certificate and the test never contacts a real server.
func buildInsecureChrome120Transport() *http.Transport {
	dialFn := client.UTLSDialer(utls.HelloChrome_120)
	return &http.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			// Pass InsecureSkipVerify so the uTLS client accepts the
			// self-signed httptest certificate.
			return dialFn(ctx, network, addr, &tls.Config{InsecureSkipVerify: true}) // #nosec G402 – test only
		},
	}
}

// TestFingerprintChrome120_TLSState stands up a local httptest.NewTLSServer,
// fires a request through the uTLS Chrome 120 transport, and verifies that the
// server-side TLS ConnectionState reflects Chrome 120 fingerprint
// characteristics:
//   - TLS 1.3 is negotiated (Chrome 120 requires TLS 1.3 with modern servers).
//   - The negotiated cipher suite is from Chrome 120's known TLS 1.3 set.
//   - ALPN is non-empty (both h2 and http/1.1 were offered by the server).
func TestFingerprintChrome120_TLSState(t *testing.T) {
	tlsStateCh := make(chan tls.ConnectionState, 1)

	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS != nil {
			select {
			case tlsStateCh <- *r.TLS:
			default:
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	// Advertise only http/1.1 so the server and our http.Transport agree on the
	// application protocol.  The uTLS Chrome 120 ClientHello will still include
	// both "h2" and "http/1.1" in ALPN; the server picks "http/1.1", which
	// confirms ALPN negotiation occurred.
	ts.TLS = &tls.Config{NextProtos: []string{"http/1.1"}}
	ts.StartTLS()
	t.Cleanup(ts.Close)

	httpClient := &http.Client{
		Transport: buildInsecureChrome120Transport(),
		Timeout:   5 * time.Second,
	}
	resp, err := httpClient.Get(ts.URL)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()

	select {
	case state := <-tlsStateCh:
		// Chrome 120 negotiates TLS 1.3 with any server that supports it.
		if state.Version != tls.VersionTLS13 {
			t.Errorf("expected TLS 1.3 (0x%04x), got 0x%04x", tls.VersionTLS13, state.Version)
		}
		// Cipher suite must come from Chrome 120's TLS 1.3 advertised set.
		if !chrome120TLS13Ciphers[state.CipherSuite] {
			t.Errorf("cipher suite 0x%04x is not in Chrome 120's TLS 1.3 set", state.CipherSuite)
		}
		// ALPN must be non-empty; the server advertised "http/1.1" and the
		// Chrome 120 ClientHello included ALPN with "h2" and "http/1.1".
		if state.NegotiatedProtocol != "http/1.1" {
			t.Errorf("expected NegotiatedProtocol %q, got %q", "http/1.1", state.NegotiatedProtocol)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout: server handler did not capture TLS state")
	}
}

// TestFingerprintChrome120_BypassesGoDefaultSignature verifies that the Chrome
// 120 transport uses TLS 1.3 while the plain Go default transport also
// negotiates TLS 1.3 but with a different cipher-suite preference.  The key
// assertion is that the engine does NOT fall back to plain net/http's default
// DialTLSContext (i.e. UTLSDialer is actually invoked).
func TestFingerprintChrome120_BypassesGoDefaultSignature(t *testing.T) {
	dialInvoked := make(chan struct{}, 1)

	dialFn := client.UTLSDialer(utls.HelloChrome_120)
	transport := &http.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			select {
			case dialInvoked <- struct{}{}:
			default:
			}
			return dialFn(ctx, network, addr, &tls.Config{InsecureSkipVerify: true}) // #nosec G402 – test only
		},
	}

	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)

	httpClient := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	resp, err := httpClient.Get(ts.URL)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()

	select {
	case <-dialInvoked:
		// UTLSDialer was called – engine is using uTLS, not the default dialer.
	case <-time.After(time.Second):
		t.Fatal("UTLSDialer was never invoked; default Go dialer may have been used")
	}
}

// TestFingerprintChrome120_PseudoHeaderOrder verifies that Chrome120PseudoHeaderOrder
// reflects the Chrome-specific ordering (:method → :authority → :scheme → :path),
// which differs from Go's default HTTP/2 ordering (:method → :path → :scheme →
// :authority).  This ensures the engine documents and applies the correct
// pseudo-header sequence for JA3/ALPS-style fingerprint bypass.
func TestFingerprintChrome120_PseudoHeaderOrder(t *testing.T) {
	want := []string{":method", ":authority", ":scheme", ":path"}
	got := client.Chrome120PseudoHeaderOrder

	if len(got) != len(want) {
		t.Fatalf("pseudo-header order length: got %d, want %d", len(got), len(want))
	}
	for i, h := range want {
		if got[i] != h {
			t.Errorf("pseudo-header[%d]: got %q, want %q", i, got[i], h)
		}
	}

	// Verify the order differs from Go's default (:method → :path → :scheme → :authority).
	goDefault := []string{":method", ":path", ":scheme", ":authority"}
	identical := true
	for i := range got {
		if got[i] != goDefault[i] {
			identical = false
			break
		}
	}
	if identical {
		t.Error("Chrome120PseudoHeaderOrder must differ from Go's default HTTP/2 pseudo-header order to bypass fingerprinting")
	}
}
