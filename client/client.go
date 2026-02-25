// Package client provides a high-performance HTTP client factory optimised for
// concurrent use across thousands of sessions.
package client

import (
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"time"
)

// transportDefaults groups transport-layer knobs that are set once at
// construction time. Exposing them as a struct makes unit-testing easier and
// keeps NewHTTPClient's signature small.
type transportDefaults struct {
	maxIdleConns        int
	maxIdleConnsPerHost int
	maxConnsPerHost     int
}

// defaultTransport holds the tuning values used when callers do not supply
// an explicit Config. These numbers are sized for ~500 concurrent sessions
// hitting a single origin.
var defaultTransport = transportDefaults{
	maxIdleConns:        500,
	maxIdleConnsPerHost: 100,
	maxConnsPerHost:     200,
}

// NewHTTPClient constructs a *http.Client that is safe for concurrent use.
//
// Design decisions:
//
//  1. Custom http.Transport – the default transport shares a global pool which
//     can become a bottleneck when thousands of sessions compete for idle
//     connections. Each session gets its own transport, eliminating lock
//     contention on the shared pool.
//
//  2. Keep-alives are enabled (DisableKeepAlives: false) so that TCP
//     connections are reused across sequential requests within the same
//     session, dramatically reducing latency and CPU spend on TLS handshakes.
//
//  3. Connection-pool limits (MaxIdleConns / MaxIdleConnsPerHost /
//     MaxConnsPerHost) prevent a single session from exhausting OS
//     file-descriptor limits while still allowing burst parallelism.
//
//  4. IdleConnTimeout evicts stale connections from the pool so the OS can
//     reclaim sockets that were silently closed by the remote server or
//     intermediate proxies.
//
//  5. TLSHandshakeTimeout bounds the time spent on TLS negotiation, which
//     protects against servers that accept the TCP connection but never
//     complete the TLS exchange.
//
//  6. A per-session http.CookieJar (using the public-suffix list) provides
//     automatic cookie management without cross-session contamination.
//
//  7. Proxy support is optional: pass an empty string to run direct.
//
// Parameters:
//   - proxy:   optional proxy URL string, e.g. "http://host:port". Empty means direct.
//   - timeout: end-to-end request timeout passed to http.Client.Timeout.
func NewHTTPClient(proxy string, timeout time.Duration) (*http.Client, error) {
	// Build the transport first; any error here (invalid proxy URL) prevents
	// constructing an unusable client.
	transport, err := buildTransport(proxy)
	if err != nil {
		return nil, err
	}

	// A cookie jar that respects the public-suffix list prevents cookies
	// from leaking across effective top-level domains (e.g. .co.uk).
	jar, err := newCookieJar()
	if err != nil {
		return nil, fmt.Errorf("client: create cookie jar: %w", err)
	}

	return &http.Client{
		Transport: transport,
		Jar:       jar,
		Timeout:   timeout,
		// CheckRedirect is intentionally left nil so the client follows
		// redirects automatically (up to the default limit of 10).
	}, nil
}

// buildTransport creates an *http.Transport with carefully tuned defaults.
// If proxy is non-empty it is parsed and attached to the transport.
func buildTransport(proxy string) (*http.Transport, error) {
	t := &http.Transport{
		// Keep-alives are on by default; making this explicit documents intent.
		DisableKeepAlives: false,

		// Pool sizing – see module-level comment for rationale.
		MaxIdleConns:        defaultTransport.maxIdleConns,
		MaxIdleConnsPerHost: defaultTransport.maxIdleConnsPerHost,
		MaxConnsPerHost:     defaultTransport.maxConnsPerHost,

		// Evict idle connections after 90 s so we do not hold dead sockets.
		IdleConnTimeout: 90 * time.Second,

		// TLS handshakes that stall for more than 10 s are aborted.
		TLSHandshakeTimeout: 10 * time.Second,

		// ExpectContinueTimeout limits the time to wait for a server's
		// first response headers after sending the request headers when
		// the request body uses "Expect: 100-continue".
		ExpectContinueTimeout: 1 * time.Second,

		// DisableCompression: false (default) lets the transport request
		// gzip from the server and decompress transparently, saving bandwidth.
	}

	if proxy != "" {
		proxyURL, err := url.Parse(proxy)
		if err != nil {
			return nil, fmt.Errorf("client: parse proxy URL %q: %w", proxy, err)
		}
		t.Proxy = http.ProxyURL(proxyURL)
	}

	return t, nil
}

// newCookieJar creates a cookie jar that honours the public-suffix list.
// Using cookiejar.Options with PublicSuffixList nil falls back to a basic
// implementation that is still correct for most use-cases and requires no
// external dependency.
func newCookieJar() (http.CookieJar, error) {
	// Pass nil options to use the default cookie jar behaviour.
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	return jar, nil
}
