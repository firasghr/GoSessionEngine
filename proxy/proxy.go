// Package proxy provides thread-safe proxy rotation for the session engine.
package proxy

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"sync"
)

// ProxyManager holds a list of proxy addresses and rotates through them in a
// round-robin fashion.
//
// Thread-safety: a sync.Mutex serialises all mutations of index, so
// GetNextProxy may be called from any number of goroutines simultaneously
// without data races.
type ProxyManager struct {
	proxies []string
	index   int
	mutex   sync.Mutex
}

// LoadProxies reads a newline-delimited list of proxy addresses from filename
// and stores them in pm.  Lines that are blank or begin with '#' are ignored.
// Addresses may be in any format understood by net/url (e.g. "host:port" or
// "http://user:pass@host:port").
//
// LoadProxies replaces any previously loaded proxies.  It is the caller's
// responsibility not to call LoadProxies concurrently with GetNextProxy.
func (pm *ProxyManager) LoadProxies(filename string) error {
	f, err := os.Open(filename) // #nosec G304 – filename is an operator-supplied config path
	if err != nil {
		return fmt.Errorf("proxy: open %q: %w", filename, err)
	}
	defer f.Close()

	var loaded []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		loaded = append(loaded, line)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("proxy: read %q: %w", filename, err)
	}

	pm.mutex.Lock()
	pm.proxies = loaded
	pm.index = 0
	pm.mutex.Unlock()
	return nil
}

// GetNextProxy returns the next proxy in the rotation and advances the internal
// index.  If no proxies are loaded it returns an empty string, signalling the
// caller to make a direct connection.
//
// The rotation is performed under the mutex so concurrent callers each receive
// a distinct proxy and the index never wraps incorrectly.
func (pm *ProxyManager) GetNextProxy() string {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	if len(pm.proxies) == 0 {
		return ""
	}
	p := pm.proxies[pm.index]
	pm.index = (pm.index + 1) % len(pm.proxies)
	return p
}

// Count returns the number of loaded proxies.
func (pm *ProxyManager) Count() int {
	pm.mutex.Lock()
	n := len(pm.proxies)
	pm.mutex.Unlock()
	return n
}
