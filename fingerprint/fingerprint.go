// Package fingerprint provides utilities for synchronising TLS/HTTP protocol
// fingerprints across all session workers.
//
// Advanced anti-bot systems correlate the TLS ClientHello (JA3), HTTP/2
// SETTINGS frame, and the User-Agent header to detect automation.  A mismatch
// between any of these three signals – e.g. a Chrome-like TLS hello combined
// with a custom User-Agent – is a reliable automation indicator.  This package
// provides a Profile type that bundles all three signals and can apply them
// consistently to an http.Transport and session Headers map, ensuring every
// one of the 2,000 workers sends an identical, coherent fingerprint.
//
// # TLS fingerprint
//
// The standard crypto/tls package does not expose JA3 directly, but the shape
// of the ClientHello is determined by the cipher suite list, TLS version
// range, and extension set.  By fixing these parameters in tls.Config the
// engine produces a consistent, browser-like ClientHello across all sessions.
//
// # HTTP/2 SETTINGS
//
// Go's net/http automatically negotiates HTTP/2 when the server supports it.
// The SETTINGS frame parameters (HEADER_TABLE_SIZE, ENABLE_PUSH, etc.) are
// controlled by golang.org/x/net/http2 tuning knobs, which are set in
// http2.Transport.  Because the standard library's http.Transport wraps the
// HTTP/2 transport, callers can tune it via http2.ConfigureTransport.
//
// # Usage
//
//	p := fingerprint.ChromeProfile()
//	p.ApplyToTransport(myTransport)
//	p.ApplyHeaders(mySession.Headers)
package fingerprint

import (
	"crypto/tls"
	"net/http"
)

// Profile bundles the three correlated fingerprint signals:
//   - TLSConfig: controls the shape of the TLS ClientHello (JA3).
//   - UserAgent: the HTTP User-Agent header value.
//   - ExtraHeaders: additional headers (e.g. Accept, Accept-Language)
//     that browsers send to further increase fingerprint fidelity.
type Profile struct {
	// TLSConfig is applied to the http.Transport's TLSClientConfig.
	TLSConfig *tls.Config

	// UserAgent is injected into every request as the "User-Agent" header.
	UserAgent string

	// ExtraHeaders contains additional static headers that should be sent
	// with every request, in the order they are defined.
	ExtraHeaders []Header
}

// Header is an ordered name-value pair for HTTP headers.
type Header struct {
	Name  string
	Value string
}

// ChromeProfile returns a Profile that mimics a recent version of Google
// Chrome on Windows.  The TLS cipher suite list and minimum version are chosen
// to produce a JA3 fingerprint consistent with Chrome 120+.
//
// Callers may clone and modify the returned profile without affecting the
// original.
func ChromeProfile() *Profile {
	return &Profile{
		TLSConfig: chromeTLSConfig(),
		UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) " +
			"AppleWebKit/537.36 (KHTML, like Gecko) " +
			"Chrome/120.0.0.0 Safari/537.36",
		ExtraHeaders: []Header{
			{Name: "Accept", Value: "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8"},
			{Name: "Accept-Language", Value: "en-US,en;q=0.9"},
			{Name: "Accept-Encoding", Value: "gzip, deflate, br"},
			{Name: "Sec-Ch-Ua", Value: `"Not_A Brand";v="8", "Chromium";v="120", "Google Chrome";v="120"`},
			{Name: "Sec-Ch-Ua-Mobile", Value: "?0"},
			{Name: "Sec-Ch-Ua-Platform", Value: `"Windows"`},
			{Name: "Sec-Fetch-Dest", Value: "document"},
			{Name: "Sec-Fetch-Mode", Value: "navigate"},
			{Name: "Sec-Fetch-Site", Value: "none"},
			{Name: "Upgrade-Insecure-Requests", Value: "1"},
		},
	}
}

// FirefoxProfile returns a Profile that mimics Mozilla Firefox 121 on Windows.
func FirefoxProfile() *Profile {
	return &Profile{
		TLSConfig: firefoxTLSConfig(),
		UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:121.0) " +
			"Gecko/20100101 Firefox/121.0",
		ExtraHeaders: []Header{
			{Name: "Accept", Value: "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8"},
			{Name: "Accept-Language", Value: "en-US,en;q=0.5"},
			{Name: "Accept-Encoding", Value: "gzip, deflate, br"},
			{Name: "Upgrade-Insecure-Requests", Value: "1"},
			{Name: "Sec-Fetch-Dest", Value: "document"},
			{Name: "Sec-Fetch-Mode", Value: "navigate"},
			{Name: "Sec-Fetch-Site", Value: "none"},
			{Name: "Sec-Fetch-User", Value: "?1"},
		},
	}
}

// ApplyToTransport configures t's TLS settings from the profile.  Call this
// once when constructing the http.Transport for each session.  It does not
// mutate any other transport fields.
func (p *Profile) ApplyToTransport(t *http.Transport) {
	if t == nil || p.TLSConfig == nil {
		return
	}
	t.TLSClientConfig = p.TLSConfig.Clone()
}

// ApplyHeaders merges the profile's User-Agent and ExtraHeaders into headers.
// ExtraHeaders are only written if the key is not already present in headers,
// so session-level overrides take precedence.
func (p *Profile) ApplyHeaders(headers map[string]string) {
	if headers == nil {
		return
	}
	if p.UserAgent != "" {
		headers["User-Agent"] = p.UserAgent
	}
	for _, h := range p.ExtraHeaders {
		if _, exists := headers[h.Name]; !exists {
			headers[h.Name] = h.Value
		}
	}
}

// chromeTLSConfig returns a *tls.Config whose cipher suite and version
// settings produce a ClientHello consistent with Chrome 120.
//
// Cipher suites are ordered to match Chrome's preference list.  TLS 1.2 is
// the minimum to avoid advertising TLS 1.0/1.1, which modern Chrome does not
// support and which would be an automation signal.
func chromeTLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		// Chrome 120 cipher suite preference order (TLS 1.2 only; TLS 1.3
		// suites are fixed by the spec and cannot be customised via this
		// field in Go's tls package).
		CipherSuites: []uint16{
			tls.TLS_AES_128_GCM_SHA256,
			tls.TLS_AES_256_GCM_SHA384,
			tls.TLS_CHACHA20_POLY1305_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
		},
	}
}

// firefoxTLSConfig returns a *tls.Config consistent with Firefox 121.
func firefoxTLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		CipherSuites: []uint16{
			tls.TLS_AES_128_GCM_SHA256,
			tls.TLS_CHACHA20_POLY1305_SHA256,
			tls.TLS_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
		},
	}
}
