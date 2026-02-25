// Package metrics provides lightweight, lock-free request counters using
// atomic operations so they impose minimal overhead on hot paths.
package metrics

import (
	"sync/atomic"
	"time"
)

// Metrics tracks aggregate statistics for the session engine.
//
// All counters are accessed exclusively through atomic operations, which means:
//   - There is no mutex contention even at 2 000 concurrent sessions.
//   - The struct may be embedded or passed as a pointer without additional
//     synchronisation.
//   - Reads and writes are linearisable: a value read after a write always
//     reflects at least that write.
//
// Fields are uint64 and aligned to 64-bit boundaries to satisfy the
// requirements of sync/atomic on 32-bit platforms.
type Metrics struct {
	// TotalRequests is the number of HTTP requests dispatched since startup.
	TotalRequests uint64

	// Success is the number of requests that received a non-error response.
	Success uint64

	// Failed is the number of requests that resulted in a transport error or
	// a non-2xx/3xx response (application-level definition of failure).
	Failed uint64

	// startTime records when the metrics instance was created so that
	// RequestsPerSecond can compute a meaningful rate.
	startTime time.Time
}

// NewMetrics creates a Metrics instance with the start time set to now.
func NewMetrics() *Metrics {
	return &Metrics{startTime: time.Now()}
}

// IncrementTotal atomically increments the total-requests counter.
func (m *Metrics) IncrementTotal() {
	atomic.AddUint64(&m.TotalRequests, 1)
}

// IncrementSuccess atomically increments the successful-requests counter.
func (m *Metrics) IncrementSuccess() {
	atomic.AddUint64(&m.Success, 1)
}

// IncrementFailed atomically increments the failed-requests counter.
func (m *Metrics) IncrementFailed() {
	atomic.AddUint64(&m.Failed, 1)
}

// RequestsPerSecond returns the average request rate since the Metrics
// instance was created.  Returns 0 if called in the same wall-clock second as
// creation to avoid division by zero.
func (m *Metrics) RequestsPerSecond() float64 {
	elapsed := time.Since(m.startTime).Seconds()
	if elapsed == 0 {
		return 0
	}
	return float64(atomic.LoadUint64(&m.TotalRequests)) / elapsed
}

// Snapshot returns a point-in-time copy of the counters.  Because three
// separate atomic loads are not performed under a single lock, the snapshot
// may be very slightly inconsistent at nanosecond granularity, which is
// acceptable for monitoring purposes.
func (m *Metrics) Snapshot() (total, success, failed uint64) {
	return atomic.LoadUint64(&m.TotalRequests),
		atomic.LoadUint64(&m.Success),
		atomic.LoadUint64(&m.Failed)
}
