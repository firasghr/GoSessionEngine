// GoSessionEngine is a high-concurrency HTTP session automation engine.
//
// Startup sequence:
//  1. Load configuration (JSON file or defaults).
//  2. Load proxy list (optional).
//  3. Initialise metrics and logger.
//  4. Create the session manager and instantiate all sessions concurrently.
//  5. Start the worker pool.
//  6. Start the scheduler, which fans work out to sessions continuously.
//  7. Monitor metrics in a background goroutine.
//  8. Block until OS signals SIGINT or SIGTERM, then perform a clean shutdown.
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/firasghr/GoSessionEngine/config"
	"github.com/firasghr/GoSessionEngine/dashboard"
	"github.com/firasghr/GoSessionEngine/logger"
	"github.com/firasghr/GoSessionEngine/metrics"
	"github.com/firasghr/GoSessionEngine/proxy"
	"github.com/firasghr/GoSessionEngine/scheduler"
	"github.com/firasghr/GoSessionEngine/session"
	"github.com/firasghr/GoSessionEngine/worker"
)

func main() {
	// ── Flags ──────────────────────────────────────────────────────────────
	configFile := flag.String("config", "", "Path to JSON config file (optional; uses defaults if omitted)")
	dashboardAddr := flag.String("dashboard", ":8080", "Address for the real-time dashboard HTTP server (e.g. :8080)")
	flag.Parse()

	// ── Logger ─────────────────────────────────────────────────────────────
	log := logger.New(logger.LevelInfo)
	log.Info("GoSessionEngine starting up")

	// ── Configuration ──────────────────────────────────────────────────────
	var cfg *config.Config
	if *configFile != "" {
		var err error
		cfg, err = config.LoadConfig(*configFile)
		if err != nil {
			log.Errorf("failed to load config from %q: %v", *configFile, err)
			os.Exit(1)
		}
		log.Infof("configuration loaded from %q", *configFile)
	} else {
		cfg = config.DefaultConfig()
		log.Info("using default configuration")
	}

	// ── Proxy manager ──────────────────────────────────────────────────────
	pm := &proxy.ProxyManager{}
	if cfg.ProxyFile != "" {
		if err := pm.LoadProxies(cfg.ProxyFile); err != nil {
			log.Errorf("failed to load proxies from %q: %v", cfg.ProxyFile, err)
			os.Exit(1)
		}
		log.Infof("loaded %d proxies from %q", pm.Count(), cfg.ProxyFile)
	} else {
		log.Info("no proxy file configured; sessions will connect directly")
	}

	// ── Metrics ────────────────────────────────────────────────────────────
	m := metrics.NewMetrics()

	// ── Dashboard server ───────────────────────────────────────────────────
	dash := dashboard.New(m, cfg)
	go func() {
		if err := dash.ListenAndServe(*dashboardAddr); err != nil {
			log.Errorf("dashboard server error: %v", err)
		}
	}()
	log.Infof("dashboard server starting on %s", *dashboardAddr)

	// ── Session manager ────────────────────────────────────────────────────
	sm := session.NewSessionManager(cfg)
	log.Infof("creating %d sessions…", cfg.NumberOfSessions)
	if err := sm.CreateSessions(cfg.NumberOfSessions, pm); err != nil {
		log.Errorf("session creation failed: %v", err)
		os.Exit(1)
	}
	log.Infof("%d sessions created", sm.Count())

	// ── Worker pool ────────────────────────────────────────────────────────
	// Use the same number of OS threads as sessions (capped at 2 000) to
	// maximise I/O parallelism.  In CPU-bound scenarios this should be tuned
	// down to runtime.NumCPU().
	workerCount := cfg.NumberOfSessions
	if workerCount < 1 {
		workerCount = 1
	}
	wp := worker.NewWorkerPool(workerCount)
	wp.Start()
	log.Infof("worker pool started with %d workers", workerCount)

	// ── Scheduler ──────────────────────────────────────────────────────────
	sc := scheduler.NewScheduler(sm, wp)

	// jobFn is the work each session performs on each iteration.
	// Replace this closure with your application-specific logic.
	jobFn := func(s *session.Session) {
		if cfg.TargetURL == "" {
			return
		}
		m.IncrementTotal()
		resp, err := s.ExecuteRequest(http.MethodGet, cfg.TargetURL, nil)
		if err != nil {
			m.IncrementFailed()
			log.Debugf("session %d request error: %v", s.ID, err)
			return
		}
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 400 {
			m.IncrementSuccess()
		} else {
			m.IncrementFailed()
		}
	}

	sm.StartAll()
	sc.Start(jobFn)
	log.Info("scheduler started; sessions are now active")

	// ── Metrics monitor ────────────────────────────────────────────────────
	// Print a summary line every 10 seconds and keep dashboard counters fresh.
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			total, success, failed := m.Snapshot()
			rps := m.RequestsPerSecond()
			count := sm.Count()
			log.Infof("metrics – total: %d | success: %d | failed: %d | rps: %.1f | sessions: %d",
				total, success, failed, rps, count)
			dash.SetActiveSessions(int64(count))
		}
	}()

	// ── Graceful shutdown ──────────────────────────────────────────────────
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	fmt.Println() // newline after ^C
	log.Infof("received signal %s; shutting down", sig)
	dash.AddLog("INFO", fmt.Sprintf("received signal %s; shutting down", sig))

	// Stop dispatching new jobs.
	sc.Stop()

	// Wait for in-flight jobs to finish, then shut down workers.
	wp.Stop()

	// Close all sessions and release transport resources.
	sm.StopAll()

	total, success, failed := m.Snapshot()
	log.Infof("final metrics – total: %d | success: %d | failed: %d | rps: %.1f",
		total, success, failed, m.RequestsPerSecond())
	log.Info("GoSessionEngine shut down cleanly")
}
