package proxy_test

import (
	"os"
	"testing"

	"github.com/firasghr/GoSessionEngine/proxy"
)

func writeProxyFile(t *testing.T, lines string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "proxies*.txt")
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(lines)
	f.Close()
	return f.Name()
}

func TestLoadProxies_Count(t *testing.T) {
	path := writeProxyFile(t, "http://proxy1:8080\nhttp://proxy2:8080\n# comment\n\nhttp://proxy3:8080\n")
	pm := &proxy.ProxyManager{}
	if err := pm.LoadProxies(path); err != nil {
		t.Fatalf("LoadProxies error: %v", err)
	}
	if pm.Count() != 3 {
		t.Errorf("expected 3 proxies, got %d", pm.Count())
	}
}

func TestGetNextProxy_Rotation(t *testing.T) {
	path := writeProxyFile(t, "a\nb\nc\n")
	pm := &proxy.ProxyManager{}
	if err := pm.LoadProxies(path); err != nil {
		t.Fatal(err)
	}

	got := []string{pm.GetNextProxy(), pm.GetNextProxy(), pm.GetNextProxy(), pm.GetNextProxy()}
	want := []string{"a", "b", "c", "a"}
	for i, v := range got {
		if v != want[i] {
			t.Errorf("index %d: got %q, want %q", i, v, want[i])
		}
	}
}

func TestGetNextProxy_EmptyReturnsEmptyString(t *testing.T) {
	pm := &proxy.ProxyManager{}
	if got := pm.GetNextProxy(); got != "" {
		t.Errorf("expected empty string for empty proxy list, got %q", got)
	}
}

func TestLoadProxies_MissingFile(t *testing.T) {
	pm := &proxy.ProxyManager{}
	if err := pm.LoadProxies("/nonexistent.txt"); err == nil {
		t.Error("expected error for missing file")
	}
}
