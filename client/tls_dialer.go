// Package client provides a high-performance HTTP client factory optimised for
// concurrent use across thousands of sessions.
package client

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"

	utls "github.com/refraction-networking/utls"
)

// UTLSDialer returns a DialTLSContext-compatible function that performs the TLS
// handshake using the uTLS library, impersonating the browser fingerprint
// described by helloID.
//
// The returned dialer is safe for concurrent use and is designed to be wired
// directly into an http.Transport.DialTLSContext or an
// http2.Transport.DialTLSContext field.
//
// Supported Chrome HelloIDs (use the utls package constants):
//
//	utls.HelloChrome_120      – parrots Google Chrome 120
//	utls.HelloChrome_131      – parrots Google Chrome 131
//	utls.HelloChrome_Auto     – parrots the latest supported Chrome version
//
// The dialer applies the full ClientHelloSpec associated with helloID,
// including GREASE values, cipher-suite ordering
// (TLS_AES_128_GCM_SHA256, TLS_AES_256_GCM_SHA384, …), and extension
// ordering, to produce a TLS fingerprint that matches a real Chrome browser.
//
// tlsCfg may be nil; if provided, its ServerName is used as the SNI hostname
// (the dialer also derives SNI from the addr argument when tlsCfg.ServerName
// is empty).
func UTLSDialer(helloID utls.ClientHelloID) func(ctx context.Context, network, addr string, tlsCfg *tls.Config) (net.Conn, error) {
	return func(ctx context.Context, network, addr string, tlsCfg *tls.Config) (net.Conn, error) {
		// Resolve the SNI hostname from the address or from the caller-supplied
		// TLS config (the http2 layer passes its TLSClientConfig here).
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("utls dialer: parse addr %q: %w", addr, err)
		}
		sni := host
		if tlsCfg != nil && tlsCfg.ServerName != "" {
			sni = tlsCfg.ServerName
		}

		// Establish the raw TCP connection, honouring the context deadline /
		// cancellation.
		var d net.Dialer
		rawConn, err := d.DialContext(ctx, network, addr)
		if err != nil {
			return nil, fmt.Errorf("utls dialer: dial %s: %w", addr, err)
		}

		// Build the uTLS config.  We deliberately do not copy the caller's
		// *tls.Config verbatim because many of its fields (CipherSuites,
		// CurvePreferences, …) are overridden by the ClientHelloSpec anyway.
		// We only forward the fields that uTLS still respects.
		uCfg := &utls.Config{
			ServerName:         sni,
			InsecureSkipVerify: tlsCfg != nil && tlsCfg.InsecureSkipVerify, // #nosec G402 – caller-controlled
		}

		// Wrap the TCP connection with a uTLS client.
		uConn := utls.UClient(rawConn, uCfg, helloID)

		// Apply the ClientHelloSpec for the chosen helloID.  This is where
		// GREASE values are randomised, cipher-suite order is set, and all
		// extensions (SNI, supported-groups, key-share, ALPN, …) are
		// configured to match the real browser.
		spec := buildClientHelloSpec(helloID)
		if err := uConn.ApplyPreset(&spec); err != nil {
			_ = rawConn.Close()
			return nil, fmt.Errorf("utls dialer: apply preset for %s: %w", helloID.Str(), err)
		}

		// Perform the TLS handshake.
		if err := uConn.HandshakeContext(ctx); err != nil {
			_ = uConn.Close()
			return nil, fmt.Errorf("utls dialer: TLS handshake with %s: %w", addr, err)
		}

		return uConn, nil
	}
}

// UTLSDialerHTTP1 is identical to UTLSDialer but returns a function whose
// signature matches http.Transport.DialTLSContext, which does not receive a
// *tls.Config argument (the SNI is derived solely from the addr parameter).
// Use this when wiring uTLS into an http.Transport; use UTLSDialer for
// golang.org/x/net/http2.Transport.
func UTLSDialerHTTP1(helloID utls.ClientHelloID) func(ctx context.Context, network, addr string) (net.Conn, error) {
	inner := UTLSDialer(helloID)
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		return inner(ctx, network, addr, nil)
	}
}

// buildClientHelloSpec returns the ClientHelloSpec for the given helloID.
//
// For recognised Chrome 120 / 131 IDs the spec is returned verbatim from the
// utls parrot table (which already encodes GREASE placeholders, the correct
// cipher-suite list, and shuffled/ordered extensions).  For any other ID the
// function falls back to the utls default spec so that callers can still pass
// custom or non-Chrome IDs without error.
func buildClientHelloSpec(helloID utls.ClientHelloID) utls.ClientHelloSpec {
	switch helloID {
	case utls.HelloChrome_120,
		utls.HelloChrome_120_PQ,
		utls.HelloChrome_131,
		utls.HelloChrome_Auto:
		// utls.UTLSIdToSpec returns the full parrot spec – including GREASE
		// extensions, the exact cipher suite list
		// (TLS_AES_128_GCM_SHA256, TLS_AES_256_GCM_SHA384,
		//  TLS_CHACHA20_POLY1305_SHA256, …), and Chrome's shuffled extension
		// ordering – so we don't need to build it by hand.
		spec, err := utls.UTLSIdToSpec(helloID)
		if err == nil {
			return spec
		}
		// Fall through to the default on unexpected error.
	}

	// Default: let uTLS fill in the spec itself during the handshake.
	return utls.ClientHelloSpec{}
}
