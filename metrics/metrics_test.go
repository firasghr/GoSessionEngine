package metrics_test

import (
	"sync"
	"testing"

	"github.com/firasghr/GoSessionEngine/metrics"
)

func TestIncrements(t *testing.T) {
	m := metrics.NewMetrics()
	m.IncrementTotal()
	m.IncrementTotal()
	m.IncrementSuccess()
	m.IncrementFailed()

	total, success, failed := m.Snapshot()
	if total != 2 {
		t.Errorf("TotalRequests: got %d, want 2", total)
	}
	if success != 1 {
		t.Errorf("Success: got %d, want 1", success)
	}
	if failed != 1 {
		t.Errorf("Failed: got %d, want 1", failed)
	}
}

func TestConcurrentIncrements(t *testing.T) {
	m := metrics.NewMetrics()
	const goroutines = 1000
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			m.IncrementTotal()
			m.IncrementSuccess()
		}()
	}
	wg.Wait()

	total, success, _ := m.Snapshot()
	if total != goroutines {
		t.Errorf("TotalRequests: got %d, want %d", total, goroutines)
	}
	if success != goroutines {
		t.Errorf("Success: got %d, want %d", success, goroutines)
	}
}
