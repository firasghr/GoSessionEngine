package client_test

import (
	"net/http"
	"testing"

	"github.com/firasghr/GoSessionEngine/client"
)

func TestOrderedHeader_AddAndGet(t *testing.T) {
	var h client.OrderedHeader
	h.Add("accept-language", "en-US,en;q=0.9")
	h.Add("sec-ch-ua-platform", `"Windows"`)

	if got := h.Get("accept-language"); got != "en-US,en;q=0.9" {
		t.Errorf("Get: got %q, want en-US,en;q=0.9", got)
	}
	// Case-insensitive lookup.
	if got := h.Get("Accept-Language"); got != "en-US,en;q=0.9" {
		t.Errorf("Get (canonical case): got %q, want en-US,en;q=0.9", got)
	}
}

func TestOrderedHeader_SetReplaces(t *testing.T) {
	var h client.OrderedHeader
	h.Add("User-Agent", "old-value")
	h.Add("Accept", "*/*")
	h.Set("User-Agent", "new-value")

	if got := h.Get("User-Agent"); got != "new-value" {
		t.Errorf("after Set: got %q, want new-value", got)
	}
	// No duplicates after Set.
	req, _ := http.NewRequest("GET", "http://example.com", nil)
	h.ApplyToRequest(req)
	if vals := req.Header["User-Agent"]; len(vals) != 1 {
		t.Errorf("expected 1 User-Agent after Set, got %d", len(vals))
	}
}

func TestOrderedHeader_Del(t *testing.T) {
	var h client.OrderedHeader
	h.Add("X-Foo", "bar")
	h.Add("X-Baz", "qux")
	h.Del("X-Foo")

	if got := h.Get("X-Foo"); got != "" {
		t.Errorf("after Del: expected empty, got %q", got)
	}
	if h.Len() != 1 {
		t.Errorf("expected 1 entry after Del, got %d", h.Len())
	}
}

func TestOrderedHeader_ApplyToRequest_PreservesCasing(t *testing.T) {
	var h client.OrderedHeader
	h.Add("sec-ch-ua-platform", `"Windows"`)
	h.Add("accept-language", "en-US")

	req, _ := http.NewRequest("GET", "http://example.com", nil)
	h.ApplyToRequest(req)

	// Raw map access must show the exact lowercase key, not the canonical form.
	if _, ok := req.Header["sec-ch-ua-platform"]; !ok {
		t.Error("expected raw key sec-ch-ua-platform to be present in header map")
	}
}

func TestOrderedHeader_Clone(t *testing.T) {
	var h client.OrderedHeader
	h.Add("A", "1")
	c := h.Clone()
	c.Add("B", "2")

	if h.Len() != 1 {
		t.Error("Clone should not affect original length")
	}
	if c.Len() != 2 {
		t.Error("cloned header should have 2 entries")
	}
}

func TestChromeOrderedHeaders_HasRequiredFields(t *testing.T) {
	h := client.ChromeOrderedHeaders()
	required := []string{
		"User-Agent",
		"Accept",
		"accept-language",
		"sec-ch-ua",
		"sec-ch-ua-platform",
	}
	for _, k := range required {
		if h.Get(k) == "" {
			t.Errorf("ChromeOrderedHeaders missing %q", k)
		}
	}
}
