package client_test

import (
	"net/http"
	"testing"
	"time"

	utls "github.com/refraction-networking/utls"

	"github.com/firasghr/GoSessionEngine/client"
)

func TestNewChrome120H2Transport_NotNil(t *testing.T) {
	rt := client.NewChrome120H2Transport(client.H2TransportConfig{})
	if rt == nil {
		t.Fatal("NewChrome120H2Transport returned nil")
	}
}

func TestNewChrome120H2Transport_Chrome131(t *testing.T) {
	rt := client.NewChrome120H2Transport(client.H2TransportConfig{
		HelloID:         utls.HelloChrome_131,
		IdleConnTimeout: 30 * time.Second,
	})
	if rt == nil {
		t.Fatal("NewChrome120H2Transport with Chrome131 returned nil")
	}
}

func TestNewChrome120H2Transport_ImplementsRoundTripper(t *testing.T) {
	rt := client.NewChrome120H2Transport(client.H2TransportConfig{})
	var _ http.RoundTripper = rt // compile-time interface check
}

func TestChrome120PseudoHeaderOrder_Length(t *testing.T) {
	if len(client.Chrome120PseudoHeaderOrder) != 4 {
		t.Errorf("expected 4 pseudo-headers, got %d", len(client.Chrome120PseudoHeaderOrder))
	}
}

func TestChrome120PseudoHeaderOrder_Contents(t *testing.T) {
	want := map[string]bool{
		":method":    true,
		":authority": true,
		":scheme":    true,
		":path":      true,
	}
	for _, h := range client.Chrome120PseudoHeaderOrder {
		if !want[h] {
			t.Errorf("unexpected pseudo-header %q", h)
		}
	}
}
