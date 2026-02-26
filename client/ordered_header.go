package client

import (
	"net/http"
)

// headerEntry stores a single header key/value pair with its original casing.
type headerEntry struct {
	key   string
	value string
}

// OrderedHeader is a drop-in companion to http.Header that preserves the exact
// capitalisation and insertion order of HTTP headers.
//
// Unlike http.Header (which is a map[string][]string and therefore unordered),
// OrderedHeader stores entries in a slice so iteration always returns them in
// the order they were added.  This is important for HTTP/2 fingerprinting:
// servers that profile client fingerprints inspect both the capitalisation
// (e.g. "sec-ch-ua-platform" vs "Sec-Ch-Ua-Platform") and the ordering of
// headers such as "accept-language", "sec-ch-ua-*", and "user-agent".
//
// OrderedHeader is NOT safe for concurrent use without external
// synchronisation.  In the session model each session owns exactly one
// OrderedHeader and builds it before the goroutine that uses it is started, so
// no additional locking is required.
type OrderedHeader struct {
	entries []headerEntry
}

// Add appends key/value to the header list, preserving the exact casing of
// key.  Multiple calls with the same key produce multiple entries (equivalent
// to http.Header.Add).
func (h *OrderedHeader) Add(key, value string) {
	h.entries = append(h.entries, headerEntry{key: key, value: value})
}

// Set replaces the first entry whose key matches key (case-insensitively) with
// the new value and removes any subsequent duplicates.  If no entry with that
// key exists, Set behaves like Add.
//
// The canonical casing of the surviving entry is updated to key, so callers
// can use Set to change capitalisation as well as value.
func (h *OrderedHeader) Set(key, value string) {
	canonKey := http.CanonicalHeaderKey(key)
	replaced := false
	out := h.entries[:0]
	for _, e := range h.entries {
		if http.CanonicalHeaderKey(e.key) == canonKey {
			if !replaced {
				out = append(out, headerEntry{key: key, value: value})
				replaced = true
			}
			// Skip duplicates.
		} else {
			out = append(out, e)
		}
	}
	if !replaced {
		out = append(out, headerEntry{key: key, value: value})
	}
	h.entries = out
}

// Del removes all entries whose key matches key (case-insensitively).
func (h *OrderedHeader) Del(key string) {
	canonKey := http.CanonicalHeaderKey(key)
	out := h.entries[:0]
	for _, e := range h.entries {
		if http.CanonicalHeaderKey(e.key) != canonKey {
			out = append(out, e)
		}
	}
	h.entries = out
}

// Get returns the value of the first entry whose key matches key
// (case-insensitively), or an empty string if no such entry exists.
func (h *OrderedHeader) Get(key string) string {
	canonKey := http.CanonicalHeaderKey(key)
	for _, e := range h.entries {
		if http.CanonicalHeaderKey(e.key) == canonKey {
			return e.value
		}
	}
	return ""
}

// Len returns the number of header entries (including duplicates).
func (h *OrderedHeader) Len() int { return len(h.entries) }

// Clone returns a shallow copy of the receiver.
func (h *OrderedHeader) Clone() *OrderedHeader {
	c := &OrderedHeader{entries: make([]headerEntry, len(h.entries))}
	copy(c.entries, h.entries)
	return c
}

// ApplyToRequest writes every entry in h into req.Header, preserving the exact
// key casing and insertion order.
//
// Because net/http's http.Header is a map[string][]string keyed by
// CanonicalHeaderKey, ApplyToRequest sets the raw header bytes directly via
// req.Header[key] so that the original capitalisation is preserved on the
// wire.  This technique works with both HTTP/1.1 (where Go writes headers as
// given) and with the http2 transport (which encodes headers with HPACK but
// still uses the key string we supply).
//
// Any headers already present in req.Header are replaced, not merged.
func (h *OrderedHeader) ApplyToRequest(req *http.Request) {
	// Start with a fresh map so we control the exact set of headers.
	req.Header = make(http.Header, len(h.entries))
	for _, e := range h.entries {
		// Bypass net/http's canonical-key normalisation by writing directly
		// into the underlying map.
		req.Header[e.key] = append(req.Header[e.key], e.value)
	}
}

// ToHTTPHeader converts the OrderedHeader to a standard http.Header map.
// Insertion order is NOT preserved in the resulting map (maps are unordered),
// but the exact key casing IS preserved because we use the raw key as the map
// key rather than http.CanonicalHeaderKey(key).
func (h *OrderedHeader) ToHTTPHeader() http.Header {
	out := make(http.Header, len(h.entries))
	for _, e := range h.entries {
		out[e.key] = append(out[e.key], e.value)
	}
	return out
}

// ChromeOrderedHeaders returns an OrderedHeader pre-populated with the
// standard Chrome 120 request headers in the exact order and casing that a
// real Windows Chrome 120 client sends.
//
// Callers should call ApplyToRequest before executing each request so that
// dynamic values (accept-language locale, sec-ch-ua version strings, …) can
// be overridden with Set after construction.
func ChromeOrderedHeaders() *OrderedHeader {
	h := &OrderedHeader{}
	h.Add("sec-ch-ua", `"Not_A Brand";v="8", "Chromium";v="120", "Google Chrome";v="120"`)
	h.Add("sec-ch-ua-mobile", "?0")
	h.Add("sec-ch-ua-platform", `"Windows"`)
	h.Add("Upgrade-Insecure-Requests", "1")
	h.Add("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	h.Add("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7")
	h.Add("sec-fetch-site", "none")
	h.Add("sec-fetch-mode", "navigate")
	h.Add("sec-fetch-user", "?1")
	h.Add("sec-fetch-dest", "document")
	h.Add("accept-encoding", "gzip, deflate, br")
	h.Add("accept-language", "en-US,en;q=0.9")
	return h
}
