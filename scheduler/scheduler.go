// Package scheduler assigns work to sessions and drives the worker pool.
package scheduler

import (
	"sync"

	"github.com/firasghr/GoSessionEngine/session"
	"github.com/firasghr/GoSessionEngine/worker"
)

// Scheduler bridges the SessionManager and the WorkerPool.
//
// Architecture:
//   - Scheduler.Start spawns a control goroutine that iterates over all active
//     sessions and submits a job for each one to the WorkerPool.  The job
//     calls the session's JobFunc (a user-supplied closure stored at Start
//     time).
//   - A stop channel allows clean shutdown: calling Stop closes the channel,
//     which causes the control goroutine to exit after the current iteration
//     completes.
//   - The design is intentionally decoupled: Scheduler does not know what the
//     job does; it only knows how to fan work out to sessions efficiently.
type Scheduler struct {
	sessionManager *session.SessionManager
	workerPool     *worker.WorkerPool
	stopCh         chan struct{}
	once           sync.Once
}

// NewScheduler creates a Scheduler that uses sm to enumerate sessions and wp
// to execute jobs.
func NewScheduler(sm *session.SessionManager, wp *worker.WorkerPool) *Scheduler {
	return &Scheduler{
		sessionManager: sm,
		workerPool:     wp,
		stopCh:         make(chan struct{}),
	}
}

// Start begins continuous job assignment.  For every active session the
// Scheduler submits a job to the WorkerPool via jobFn(session).  The loop
// runs until Stop is called.
//
// Start is non-blocking: the control goroutine runs in the background.
// jobFn must be safe for concurrent use by multiple goroutines.
func (sc *Scheduler) Start(jobFn func(s *session.Session)) {
	go func() {
		for {
			select {
			case <-sc.stopCh:
				return
			default:
				sc.dispatchJobs(jobFn)
			}
		}
	}()
}

// dispatchJobs iterates over all registered sessions and submits a job for
// each one.  Internally it queries the session manager for the current session
// count and submits by session ID so it does not need to hold any locks while
// waiting for the worker pool to accept the job.
func (sc *Scheduler) dispatchJobs(jobFn func(s *session.Session)) {
	count := sc.sessionManager.Count()
	for id := 0; id < count; id++ {
		s, ok := sc.sessionManager.GetSession(id)
		if !ok {
			continue
		}
		// Capture s in the closure to avoid the classic loop-variable trap.
		captured := s
		sc.workerPool.Submit(func() {
			jobFn(captured)
		})
	}
}

// Stop signals the Scheduler to stop dispatching new jobs.  It does not wait
// for in-flight jobs to complete; call WorkerPool.Stop for that.
// Stop is idempotent.
func (sc *Scheduler) Stop() {
	sc.once.Do(func() {
		close(sc.stopCh)
	})
}
