// Package worker provides a bounded goroutine pool for executing arbitrary
// jobs with controlled concurrency.
package worker

import (
	"sync"
)

// WorkerPool manages a fixed number of goroutines that drain a shared job
// queue.
//
// Design choices:
//   - workerCount goroutines are started once and reused, avoiding the cost of
//     spawning a goroutine per job.
//   - jobQueue is a buffered channel (capacity workerCount*4): workers can pick
//     up the next job immediately after finishing the current one, reducing
//     context switches at high throughput.  Submit blocks only when the buffer
//     is full, applying natural back-pressure to producers.
//   - Stop closes the channel and waits (via wg) for every in-flight job to
//     finish before returning, preventing goroutine leaks.
type WorkerPool struct {
	workerCount int
	jobQueue    chan func()
	wg          sync.WaitGroup
}

// NewWorkerPool creates a WorkerPool with workerCount goroutines ready to
// receive jobs.  The queue can buffer up to workerCount*4 pending jobs before
// Submit starts blocking, providing a small burst buffer without unbounded
// growth.
func NewWorkerPool(workerCount int) *WorkerPool {
	if workerCount <= 0 {
		workerCount = 1
	}
	return &WorkerPool{
		workerCount: workerCount,
		// Buffer the channel to allow workers to pick up the next job
		// immediately after finishing the current one, reducing context
		// switches at high throughput.
		jobQueue: make(chan func(), workerCount*4),
	}
}

// Start launches the worker goroutines.  It must be called exactly once before
// any jobs are submitted.
func (wp *WorkerPool) Start() {
	for i := 0; i < wp.workerCount; i++ {
		wp.wg.Add(1)
		go func() {
			defer wp.wg.Done()
			// Each worker drains the channel until it is closed.
			for job := range wp.jobQueue {
				job()
			}
		}()
	}
}

// Submit enqueues job for execution by one of the pool's goroutines.  It
// blocks if the internal buffer is full, applying back-pressure to the caller.
// Submit must not be called after Stop.
func (wp *WorkerPool) Submit(job func()) {
	wp.jobQueue <- job
}

// Stop signals the pool to finish all queued jobs and then waits for all
// worker goroutines to exit.  No new jobs may be submitted after Stop is
// called.
func (wp *WorkerPool) Stop() {
	close(wp.jobQueue)
	wp.wg.Wait()
}
