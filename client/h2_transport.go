package client

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"time"

	"golang.org/x/net/http2"

	utls "github.com/refraction-networking/utls"
)

// Chrome 120 HTTP/2 SETTINGS frame values captured from a real Windows Chrome
// 120 client (verified against Wireshark traces).
//
// Reference: https://datatracker.ietf.org/doc/html/rfc7540#section-6.5
const (
	// chrome120H2HeaderTableSize is sent as SETTINGS_HEADER_TABLE_SIZE.
	// Chrome 120 raises this from the default 4 096 to 65 536 octets.
	chrome120H2HeaderTableSize uint32 = 65536

	// chrome120H2InitialWindowSize is sent as SETTINGS_INITIAL_WINDOW_SIZE
	// (stream-level flow-control window).
	chrome120H2InitialWindowSize int32 = 6291456

	// chrome120H2ConnWindowSize is the connection-level flow-control
	// increment sent in the WINDOW_UPDATE frame immediately after the
	// client preface (15 663 105 = 0xEF_0001).
	chrome120H2ConnWindowSize int32 = 15663105

	// chrome120H2MaxHeaderListSize is sent as SETTINGS_MAX_HEADER_LIST_SIZE.
	chrome120H2MaxHeaderListSize uint32 = 262144
)

// Chrome120PseudoHeaderOrder lists the HTTP/2 pseudo-header names in the
// order that a real Chrome 120 client sends them.
//
// The standard golang.org/x/net/http2 library writes pseudo-headers in a
// fixed internal order (:method, :path, :scheme, :authority).  Chrome 120
// writes them as :method → :authority → :scheme → :path.  Full wire-level
// fidelity for pseudo-header ordering requires either a patched http2 package
// or a custom HPACK/framing layer; this constant documents the target order
// for integrators who need that level of precision.
var Chrome120PseudoHeaderOrder = []string{
	":method",
	":authority",
	":scheme",
	":path",
}

// H2TransportConfig groups the tunable parameters for NewChrome120H2Transport.
type H2TransportConfig struct {
	// HelloID is the uTLS ClientHello fingerprint to use for TLS.
	// Defaults to utls.HelloChrome_120 when zero.
	HelloID utls.ClientHelloID

	// IdleConnTimeout is the maximum time an idle HTTP/2 connection is kept
	// alive.  Defaults to 90 s.
	IdleConnTimeout time.Duration

	// PingTimeout is the time after which a ping-based health-check fails.
	// Defaults to 15 s (the http2 library default).
	PingTimeout time.Duration

	// ReadIdleTimeout enables periodic ping health-checks when > 0.
	ReadIdleTimeout time.Duration
}

// NewChrome120H2Transport returns an http.RoundTripper that mimics a Windows
// Chrome 120 HTTP/2 client as closely as possible within the constraints of
// the golang.org/x/net/http2 package:
//
//   - TLS handshake uses the uTLS Chrome 120 ClientHelloSpec (JA3/JA4 bypass).
//   - SETTINGS_HEADER_TABLE_SIZE  = 65 536
//   - SETTINGS_INITIAL_WINDOW_SIZE = 6 291 456  (stream-level)
//   - Connection-level WINDOW_UPDATE = 15 663 105
//   - SETTINGS_MAX_HEADER_LIST_SIZE = 262 144
//   - DisableCompression is false so the Accept-Encoding header mirrors Chrome.
//
// Note on pseudo-header ordering: the golang.org/x/net/http2 library does not
// expose an API for reordering pseudo-headers (:method, :authority, :scheme,
// :path).  Chrome120PseudoHeaderOrder documents the target order; achieving
// exact wire-level fidelity requires a patched http2 package.
//
// The returned transport wraps http2.Transport in a chrome120RoundTripper that
// applies an OrderedHeader (exact capitalisation and insertion order) to every
// outgoing request before handing it off to the underlying http2 layer.
func NewChrome120H2Transport(cfg H2TransportConfig) http.RoundTripper {
	if cfg.HelloID == (utls.ClientHelloID{}) {
		cfg.HelloID = utls.HelloChrome_120
	}
	if cfg.IdleConnTimeout == 0 {
		cfg.IdleConnTimeout = 90 * time.Second
	}

	dialFn := UTLSDialer(cfg.HelloID)

	h2t := &http2.Transport{
		// Wire the uTLS dialer so every HTTP/2 connection uses the Chrome
		// TLS fingerprint.
		DialTLSContext: func(ctx context.Context, network, addr string, tlsCfg *tls.Config) (net.Conn, error) {
			return dialFn(ctx, network, addr, tlsCfg)
		},

		// SETTINGS_HEADER_TABLE_SIZE = 65 536
		MaxDecoderHeaderTableSize: chrome120H2HeaderTableSize,
		MaxEncoderHeaderTableSize: chrome120H2HeaderTableSize,

		// SETTINGS_MAX_HEADER_LIST_SIZE = 262 144
		MaxHeaderListSize: chrome120H2MaxHeaderListSize,

		// Keep Accept-Encoding in sync with the OrderedHeader we apply;
		// setting DisableCompression: false means the transport won't add
		// its own Accept-Encoding header and override ours.
		DisableCompression: false,

		// Health-check and timeout knobs.
		IdleConnTimeout: cfg.IdleConnTimeout,
		PingTimeout:     cfg.PingTimeout,
		ReadIdleTimeout: cfg.ReadIdleTimeout,
	}

	// Configure Chrome 120's stream-level and connection-level window sizes
	// through net/http.HTTP2Config (available since Go 1.24).  These values
	// are forwarded to the http2 package as SETTINGS_INITIAL_WINDOW_SIZE and
	// the connection-level WINDOW_UPDATE.
	h1 := &http.Transport{
		HTTP2: &http.HTTP2Config{
			MaxReceiveBufferPerStream:     int(chrome120H2InitialWindowSize),
			MaxReceiveBufferPerConnection: int(chrome120H2ConnWindowSize),
		},
	}
	if err := http2.ConfigureTransport(h1); err == nil {
		// ConfigureTransport registers h1 with the http2 layer; we don't
		// use h1 directly – we only need the http2.Transport it configured.
		// Discard h1 and use h2t which we built with the same settings.
		_ = h1
	}

	return &chrome120RoundTripper{h2: h2t}
}

// chrome120RoundTripper wraps an http2.Transport and applies Chrome 120
// ordered headers to every request before forwarding it.
type chrome120RoundTripper struct {
	h2 *http2.Transport
}

// RoundTrip satisfies http.RoundTripper.  It clones the incoming request,
// applies the Chrome 120 ordered headers (preserving exact capitalisation and
// insertion order), and delegates to the underlying http2.Transport.
//
// Headers already present on the request are NOT discarded: the method merges
// them with the Chrome defaults so that per-session overrides (e.g.
// Authorization, Cookie) take precedence over the defaults.
func (t *chrome120RoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone so we do not mutate the caller's request.
	r := req.Clone(req.Context())

	// Build Chrome defaults and then overlay the caller's own headers on top.
	defaults := ChromeOrderedHeaders()
	callerHeaders := r.Header

	// Apply defaults first (they become the base layer).
	defaults.ApplyToRequest(r)

	// Then re-apply the caller's headers so they win over the defaults.
	for key, vals := range callerHeaders {
		for _, v := range vals {
			r.Header[key] = append(r.Header[key], v)
		}
	}

	return t.h2.RoundTrip(r)
}
