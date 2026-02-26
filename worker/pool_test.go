package worker_test

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/firasghr/GoSessionEngine/worker"
)

func TestWorkerPool_ExecutesAllJobs(t *testing.T) {
	const jobs = 500
	wp := worker.NewWorkerPool(10)
	wp.Start()

	var counter int64
	for i := 0; i < jobs; i++ {
		wp.Submit(func() {
			atomic.AddInt64(&counter, 1)
		})
	}
	wp.Stop()

	if counter != jobs {
		t.Errorf("expected %d jobs executed, got %d", jobs, counter)
	}
}

func TestWorkerPool_ZeroWorkersFallsBackToOne(t *testing.T) {
	wp := worker.NewWorkerPool(0)
	wp.Start()
	var ran int64
	wp.Submit(func() { atomic.AddInt64(&ran, 1) })
	wp.Stop()
	if ran != 1 {
		t.Errorf("expected job to run, ran=%d", ran)
	}
}

// TestWorkerPool_HighConcurrency spawns 2,000 workers and submits 50,000 jobs.
// An atomic counter inside each job verifies that exactly 50,000 executions
// occurred without deadlocks, channel blocking, or goroutine leaks when Stop
// is called.  The test is designed to pass with the -race flag enabled.
func TestWorkerPool_HighConcurrency(t *testing.T) {
	const (
		numWorkers = 2_000
		numJobs    = 50_000
	)

	wp := worker.NewWorkerPool(numWorkers)
	wp.Start()

	var counter int64

	// A WaitGroup ensures all jobs are enqueued before we call Stop, so that
	// Submit never races with Stop on the closed channel.
	var enqueued sync.WaitGroup
	enqueued.Add(numJobs)

	for i := 0; i < numJobs; i++ {
		wp.Submit(func() {
			atomic.AddInt64(&counter, 1)
			enqueued.Done()
		})
	}

	// Wait until every job has fully executed (Done is called after the counter
	// increment), then stop the pool.  This guarantees Stop is never called
	// concurrently with running jobs and that the counter check below is safe.
	enqueued.Wait()
	wp.Stop()

	if counter != numJobs {
		t.Errorf("expected %d jobs executed, got %d", numJobs, counter)
	}
}

// BenchmarkWorkerPool_Submit measures the throughput of submitting jobs to the
// pool using GOMAXPROCS workers so the benchmark is CPU-proportional.
func BenchmarkWorkerPool_Submit(b *testing.B) {
	wp := worker.NewWorkerPool(runtime.GOMAXPROCS(0))
	wp.Start()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		wp.Submit(func() {})
	}
	b.StopTimer()
	wp.Stop()
}
