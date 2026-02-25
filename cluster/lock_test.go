package cluster_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/firasghr/GoSessionEngine/cluster"
)

func TestTryLock_Basic(t *testing.T) {
	l := cluster.NewInMemoryLock()

	if !l.TryLock("key1") {
		t.Fatal("expected TryLock to succeed on uncontended key")
	}
	// Second TryLock on same key should fail while still held.
	if l.TryLock("key1") {
		t.Error("expected TryLock to fail on already-locked key")
	}
	l.Unlock("key1")
	// After unlock, TryLock should succeed again.
	if !l.TryLock("key1") {
		t.Error("expected TryLock to succeed after unlock")
	}
	l.Unlock("key1")
}

func TestLock_BlocksUntilUnlock(t *testing.T) {
	l := cluster.NewInMemoryLock()
	if !l.TryLock("page") {
		t.Fatal("expected initial TryLock to succeed")
	}

	var reached atomic.Bool
	go func() {
		ctx := context.Background()
		_ = l.Lock(ctx, "page")
		reached.Store(true)
		l.Unlock("page")
	}()

	time.Sleep(50 * time.Millisecond)
	if reached.Load() {
		t.Error("second Lock should be blocked while first is held")
	}
	l.Unlock("page")
	time.Sleep(50 * time.Millisecond)
	if !reached.Load() {
		t.Error("second Lock should have proceeded after first unlock")
	}
}

func TestLock_ContextCancellation(t *testing.T) {
	l := cluster.NewInMemoryLock()
	if !l.TryLock("resource") {
		t.Fatal("expected initial TryLock to succeed")
	}
	defer l.Unlock("resource")

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := l.Lock(ctx, "resource")
	if err == nil {
		t.Error("expected error when context times out")
	}
}

func TestIsLocked(t *testing.T) {
	l := cluster.NewInMemoryLock()

	if l.IsLocked("x") {
		t.Error("key should not be locked before any call")
	}
	if !l.TryLock("x") {
		t.Fatal("TryLock failed")
	}
	if !l.IsLocked("x") {
		t.Error("key should be locked after TryLock")
	}
	l.Unlock("x")
	if l.IsLocked("x") {
		t.Error("key should not be locked after Unlock")
	}
}

func TestUnlock_Noop_OnUnknownKey(t *testing.T) {
	l := cluster.NewInMemoryLock()
	// Must not panic.
	l.Unlock("nonexistent")
}

func TestNoRaceCondition_MultipleGoroutines(t *testing.T) {
	l := cluster.NewInMemoryLock()
	const goroutines = 20
	var counter int
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := context.Background()
			if err := l.Lock(ctx, "applicant-page"); err != nil {
				t.Errorf("Lock error: %v", err)
				return
			}
			// Critical section – only one goroutine at a time.
			counter++
			l.Unlock("applicant-page")
		}()
	}
	wg.Wait()

	if counter != goroutines {
		t.Errorf("counter = %d, want %d (race condition detected)", counter, goroutines)
	}
}

func TestWithLock_Success(t *testing.T) {
	l := cluster.NewInMemoryLock()
	var called bool
	err := cluster.WithLock(context.Background(), l, "k", 0, func() {
		called = true
	})
	if err != nil {
		t.Fatalf("WithLock error: %v", err)
	}
	if !called {
		t.Error("fn was not called")
	}
}

func TestWithLock_Timeout(t *testing.T) {
	l := cluster.NewInMemoryLock()
	if !l.TryLock("k") {
		t.Fatal("initial TryLock failed")
	}
	defer l.Unlock("k")

	err := cluster.WithLock(context.Background(), l, "k", 30*time.Millisecond, func() {})
	if err == nil {
		t.Error("expected timeout error")
	}
}

func TestImplementsInterface(t *testing.T) {
	var _ cluster.DistributedLock = cluster.NewInMemoryLock()
}
