// Package cluster provides distributed synchronisation primitives for
// GoSessionEngine's multi-node deployment.
//
// When the engine runs across a 6-node cluster, multiple nodes may attempt to
// access a shared resource (e.g. the "Applicant Page") simultaneously.
// Without coordination, this causes race conditions such as duplicate
// submissions, wasted requests, or corrupted session state.
//
// This package defines a DistributedLock interface and ships two
// implementations:
//
//  1. InMemoryLock – a single-process lock backed by sync primitives.  Useful
//     for unit tests, single-node deployments, and as a reference
//     implementation.
//
//  2. The interface itself is designed so that production deployments can plug
//     in a Redis-backed or etcd-backed lock by implementing the four methods.
//
// # Recommended cluster architecture
//
// Use a gRPC Master-Worker model:
//   - One master node holds the InMemoryLock (or a Redis SETNX-based lock).
//   - Worker nodes request a lease from the master via a gRPC Acquire RPC.
//   - The master serialises access to the shared resource and returns the
//     lease token to the winner.
//   - Workers include the lease token in every request to the protected
//     resource and release it via the Release RPC when done.
package cluster

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// DistributedLock is the interface that all lock implementations must satisfy.
// Keys are arbitrary strings; callers should use a descriptive name such as
// "applicant-page" or "session-slot-42".
type DistributedLock interface {
	// Lock acquires the lock for key, blocking until it is available or ctx
	// is cancelled.  Returns an error if the context expires before the lock
	// is acquired.
	Lock(ctx context.Context, key string) error

	// TryLock attempts to acquire the lock for key without blocking.  Returns
	// true if the lock was acquired, false if it is already held.
	TryLock(key string) bool

	// Unlock releases the lock for key.  It is a no-op if the key is not
	// currently locked.
	Unlock(key string)

	// IsLocked reports whether key is currently locked.
	IsLocked(key string) bool
}

// keyMutex pairs a sync.Mutex with a reference count so entries can be pruned
// from the map when no goroutine holds or is waiting on the lock.
type keyMutex struct {
	mu      sync.Mutex
	waiters int
}

// InMemoryLock is a DistributedLock implementation backed by per-key
// sync.Mutex values.  It is safe for concurrent use by any number of
// goroutines within a single process.
//
// Design:
//   - A top-level sync.RWMutex guards the locks map.
//   - Each key gets its own *keyMutex so contention on one key never blocks
//     goroutines contending on a different key.
//   - The reference counter (waiters) on each keyMutex allows the map entry
//     to be removed once no goroutine is waiting, keeping memory bounded even
//     when thousands of distinct keys are used transiently.
type InMemoryLock struct {
	mu    sync.Mutex
	locks map[string]*keyMutex
}

// NewInMemoryLock creates an empty InMemoryLock.
func NewInMemoryLock() *InMemoryLock {
	return &InMemoryLock{
		locks: make(map[string]*keyMutex),
	}
}

// getOrCreate returns the keyMutex for key, creating it if necessary, and
// increments its waiter count.  Must be called with il.mu held.
func (il *InMemoryLock) getOrCreate(key string) *keyMutex {
	km, ok := il.locks[key]
	if !ok {
		km = &keyMutex{}
		il.locks[key] = km
	}
	km.waiters++
	return km
}

// Lock acquires the per-key mutex, blocking until available or ctx is done.
func (il *InMemoryLock) Lock(ctx context.Context, key string) error {
	il.mu.Lock()
	km := il.getOrCreate(key)
	il.mu.Unlock()

	// Try to acquire the per-key mutex in a goroutine so we can respect ctx.
	acquired := make(chan struct{}, 1)
	go func() {
		km.mu.Lock()
		acquired <- struct{}{}
	}()

	select {
	case <-acquired:
		return nil
	case <-ctx.Done():
		// We lost the race: the goroutine may still be blocked on km.mu.Lock.
		// Decrement the waiter count but do NOT unlock; the goroutine will
		// eventually acquire and immediately unlock it.
		il.mu.Lock()
		km.waiters--
		if km.waiters == 0 {
			delete(il.locks, key)
		}
		il.mu.Unlock()

		// Drain the channel and unlock if the goroutine already acquired.
		select {
		case <-acquired:
			km.mu.Unlock()
		default:
			// Goroutine has not acquired yet; it will unlock when it does.
			go func() {
				<-acquired
				km.mu.Unlock()
			}()
		}
		return fmt.Errorf("cluster: lock %q: %w", key, ctx.Err())
	}
}

// TryLock attempts to acquire the per-key mutex without blocking.  Returns
// true on success.
func (il *InMemoryLock) TryLock(key string) bool {
	il.mu.Lock()
	km := il.getOrCreate(key)
	il.mu.Unlock()

	acquired := km.mu.TryLock()
	if !acquired {
		il.mu.Lock()
		km.waiters--
		if km.waiters == 0 {
			delete(il.locks, key)
		}
		il.mu.Unlock()
	}
	return acquired
}

// Unlock releases the per-key mutex.  If no goroutine is waiting on the key
// its map entry is removed to bound memory usage.
func (il *InMemoryLock) Unlock(key string) {
	il.mu.Lock()
	km, ok := il.locks[key]
	if !ok {
		il.mu.Unlock()
		return
	}
	km.waiters--
	if km.waiters == 0 {
		delete(il.locks, key)
	}
	il.mu.Unlock()
	km.mu.Unlock()
}

// IsLocked reports whether key is currently held by a goroutine.  The result
// is advisory: the state may change before the caller acts on it.
func (il *InMemoryLock) IsLocked(key string) bool {
	il.mu.Lock()
	km, ok := il.locks[key]
	il.mu.Unlock()
	if !ok {
		return false
	}
	// TryLock returns true when the mutex is free.
	if km.mu.TryLock() {
		km.mu.Unlock()
		return false
	}
	return true
}

// WithLock is a convenience helper that acquires the lock for key, calls fn,
// and releases the lock.  A timeout of 0 means no deadline (use a
// context.Background with a cancel if you want manual control).
func WithLock(ctx context.Context, dl DistributedLock, key string, timeout time.Duration, fn func()) error {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	if err := dl.Lock(ctx, key); err != nil {
		return err
	}
	defer dl.Unlock(key)
	fn()
	return nil
}
