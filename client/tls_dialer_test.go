package client_test

import (
	"testing"

	utls "github.com/refraction-networking/utls"

	"github.com/firasghr/GoSessionEngine/client"
)

func TestUTLSDialer_NotNil(t *testing.T) {
	d := client.UTLSDialer(utls.HelloChrome_120)
	if d == nil {
		t.Fatal("UTLSDialer returned nil for HelloChrome_120")
	}
}

func TestUTLSDialerHTTP1_NotNil(t *testing.T) {
	for _, id := range []utls.ClientHelloID{
		utls.HelloChrome_120,
		utls.HelloChrome_131,
		utls.HelloChrome_Auto,
	} {
		d := client.UTLSDialerHTTP1(id)
		if d == nil {
			t.Errorf("UTLSDialerHTTP1 returned nil for %s", id.Str())
		}
	}
}

func TestNewHTTPClientWithTLS_Chrome120(t *testing.T) {
	c, err := client.NewHTTPClientWithTLS("", 10e9, utls.HelloChrome_120)
	if err != nil {
		t.Fatalf("NewHTTPClientWithTLS: %v", err)
	}
	if c == nil {
		t.Fatal("NewHTTPClientWithTLS returned nil client")
	}
	if c.Jar == nil {
		t.Error("expected non-nil cookie jar")
	}
}

func TestNewHTTPClientWithTLS_Chrome131(t *testing.T) {
	c, err := client.NewHTTPClientWithTLS("", 10e9, utls.HelloChrome_131)
	if err != nil {
		t.Fatalf("NewHTTPClientWithTLS: %v", err)
	}
	if c == nil {
		t.Fatal("NewHTTPClientWithTLS returned nil client")
	}
}

func TestNewHTTPClientWithTLS_InvalidProxy(t *testing.T) {
	_, err := client.NewHTTPClientWithTLS("://bad-proxy", 10e9, utls.HelloChrome_120)
	if err == nil {
		t.Error("expected error for invalid proxy URL")
	}
}
