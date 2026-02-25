package worker_test

import (
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
