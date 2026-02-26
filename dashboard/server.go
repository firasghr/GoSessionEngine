// Package dashboard provides a real-time HTTP dashboard server for GoSessionEngine.
//
// It exposes:
//   - GET  /api/metrics/stream  – SSE stream of live metrics (100 ms ticks)
//   - GET  /api/logs/stream     – SSE stream of log entries
//   - GET  /api/config          – current engine configuration (JSON)
//   - POST /api/config          – hot-reload selected config fields (JSON body)
//   - GET  /api/nodes           – cluster node status snapshot (JSON)
//   - POST /api/proxy           – upload a new proxy list (multipart file)
//
// All SSE endpoints set appropriate headers so browsers can use EventSource
// without any additional libraries.  CORS is wide-open so the Next.js dev
// server (typically :3000) can reach the Go backend (typically :8080).
package dashboard

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/firasghr/GoSessionEngine/config"
	"github.com/firasghr/GoSessionEngine/metrics"
)

// ─── Data Types ───────────────────────────────────────────────────────────────

// MetricsSnapshot is the JSON payload pushed to dashboard clients every tick.
type MetricsSnapshot struct {
	Timestamp     int64   `json:"timestamp"`
	Total         uint64  `json:"total"`
	Success       uint64  `json:"success"`
	Failed        uint64  `json:"failed"`
	RPS           float64 `json:"rps"`
	Sessions      int64   `json:"sessions"`
	CookieJarSize int64   `json:"cookie_jar_size"`
}

// NodeStatus represents one cluster node's health.
type NodeStatus struct {
	ID         string `json:"id"`
	Role       string `json:"role"`
	Status     string `json:"status"`
	MemoryMB   uint64 `json:"memory_mb"`
	Goroutines int    `json:"goroutines"`
	GRPCStatus string `json:"grpc_status"`
}

// LogEntry is a structured log line streamed to the dashboard.
type LogEntry struct {
	Timestamp int64  `json:"ts"`
	Level     string `json:"level"`
	Message   string `json:"message"`
}

// ConfigPayload is the subset of Config fields that can be hot-updated.
type ConfigPayload struct {
	TargetURL        string `json:"target_url"`
	NumberOfSessions int    `json:"number_of_sessions"`
	MaxRetries       int    `json:"max_retries"`
}

// ─── Server ───────────────────────────────────────────────────────────────────

// Server provides HTTP endpoints consumed by the Command Center frontend.
type Server struct {
	metrics *metrics.Metrics
	cfg     *config.Config
	cfgMu   sync.RWMutex

	// Live counters updated by the engine.
	activeSessions atomic.Int64
	cookieJarSize  atomic.Int64

	// Log ring buffer (capped at maxLogs).
	logMu    sync.Mutex
	logs     []LogEntry
	logSubs  map[chan LogEntry]struct{}
	logSubMu sync.Mutex

	// Metrics SSE subscribers.
	metricsSubs  map[chan MetricsSnapshot]struct{}
	metricsSubMu sync.Mutex

	mux *http.ServeMux
}

const maxLogs = 10_000

// New creates a dashboard Server backed by the given metrics and config.
// Call ListenAndServe to start accepting connections.
func New(m *metrics.Metrics, cfg *config.Config) *Server {
	s := &Server{
		metrics:     m,
		cfg:         cfg,
		logs:        make([]LogEntry, 0, 512),
		logSubs:     make(map[chan LogEntry]struct{}),
		metricsSubs: make(map[chan MetricsSnapshot]struct{}),
		mux:         http.NewServeMux(),
	}
	s.registerRoutes()
	return s
}

// SetActiveSessions updates the live session count displayed on the dashboard.
func (s *Server) SetActiveSessions(n int64) { s.activeSessions.Store(n) }

// SetCookieJarSize updates the live cookie-jar size displayed on the dashboard.
func (s *Server) SetCookieJarSize(n int64) { s.cookieJarSize.Store(n) }

// AddLog appends a structured log entry to the ring buffer and fans it out to
// every active SSE /api/logs/stream subscriber.
func (s *Server) AddLog(level, message string) {
	entry := LogEntry{
		Timestamp: time.Now().UnixMilli(),
		Level:     level,
		Message:   message,
	}

	s.logMu.Lock()
	s.logs = append(s.logs, entry)
	if len(s.logs) > maxLogs {
		s.logs = s.logs[len(s.logs)-maxLogs:]
	}
	s.logMu.Unlock()

	s.logSubMu.Lock()
	for ch := range s.logSubs {
		select {
		case ch <- entry:
		default:
			// Slow subscriber – drop rather than block.
		}
	}
	s.logSubMu.Unlock()
}

// ListenAndServe starts the HTTP server on addr (e.g. ":8080") and blocks
// until the process exits.  It also starts the background goroutine that ticks
// metrics to SSE subscribers every 100 ms.
//
// Timeouts are intentionally generous for a local dashboard: SSE and log
// streams are long-lived connections that must not be cut off by short write
// deadlines.  Operators exposing the dashboard on a public interface should
// wrap this in a reverse proxy with appropriate rate limiting.
func (s *Server) ListenAndServe(addr string) error {
	go s.metricsTicker()
	log.Printf("dashboard: listening on %s", addr)
	srv := &http.Server{
		Addr:         addr,
		Handler:      s.mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // disabled – SSE/log streams are unbounded
		IdleTimeout:  120 * time.Second,
	}
	return srv.ListenAndServe() // #nosec G114 – replaced with explicit http.Server
}

// ─── Route registration ───────────────────────────────────────────────────────

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("/api/metrics/stream", s.withCORS(s.handleMetricsStream))
	s.mux.HandleFunc("/api/logs/stream", s.withCORS(s.handleLogsStream))
	s.mux.HandleFunc("/api/config", s.withCORS(s.handleConfig))
	s.mux.HandleFunc("/api/nodes", s.withCORS(s.handleNodes))
	s.mux.HandleFunc("/api/proxy", s.withCORS(s.handleProxy))
}

// ─── CORS middleware ──────────────────────────────────────────────────────────

func (s *Server) withCORS(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h(w, r)
	}
}

// ─── /api/metrics/stream ─────────────────────────────────────────────────────

func (s *Server) metricsTicker() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		snap := s.snapshot()
		s.metricsSubMu.Lock()
		for ch := range s.metricsSubs {
			select {
			case ch <- snap:
			default:
			}
		}
		s.metricsSubMu.Unlock()
	}
}

func (s *Server) snapshot() MetricsSnapshot {
	total, success, failed := s.metrics.Snapshot()
	return MetricsSnapshot{
		Timestamp:     time.Now().UnixMilli(),
		Total:         total,
		Success:       success,
		Failed:        failed,
		RPS:           s.metrics.RequestsPerSecond(),
		Sessions:      s.activeSessions.Load(),
		CookieJarSize: s.cookieJarSize.Load(),
	}
}

func (s *Server) handleMetricsStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := make(chan MetricsSnapshot, 16)
	s.metricsSubMu.Lock()
	s.metricsSubs[ch] = struct{}{}
	s.metricsSubMu.Unlock()

	defer func() {
		s.metricsSubMu.Lock()
		delete(s.metricsSubs, ch)
		s.metricsSubMu.Unlock()
	}()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case snap := <-ch:
			data, err := json.Marshal(snap)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// ─── /api/logs/stream ────────────────────────────────────────────────────────

func (s *Server) handleLogsStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Send buffered history first.
	s.logMu.Lock()
	history := make([]LogEntry, len(s.logs))
	copy(history, s.logs)
	s.logMu.Unlock()

	for _, entry := range history {
		if err := sseWrite(w, entry); err != nil {
			return
		}
	}
	flusher.Flush()

	ch := make(chan LogEntry, 256)
	s.logSubMu.Lock()
	s.logSubs[ch] = struct{}{}
	s.logSubMu.Unlock()

	defer func() {
		s.logSubMu.Lock()
		delete(s.logSubs, ch)
		s.logSubMu.Unlock()
	}()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case entry := <-ch:
			if err := sseWrite(w, entry); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func sseWrite(w io.Writer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", data)
	return err
}

// ─── /api/config ─────────────────────────────────────────────────────────────

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.cfgMu.RLock()
		cfg := *s.cfg
		s.cfgMu.RUnlock()

		payload := ConfigPayload{
			TargetURL:        cfg.TargetURL,
			NumberOfSessions: cfg.NumberOfSessions,
			MaxRetries:       cfg.MaxRetries,
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			log.Printf("dashboard: encode config: %v", err)
		}

	case http.MethodPost:
		var payload ConfigPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		s.cfgMu.Lock()
		if payload.TargetURL != "" {
			s.cfg.TargetURL = payload.TargetURL
		}
		if payload.NumberOfSessions > 0 && payload.NumberOfSessions <= 2000 {
			s.cfg.NumberOfSessions = payload.NumberOfSessions
		}
		if payload.MaxRetries > 0 && payload.MaxRetries <= 100 {
			s.cfg.MaxRetries = payload.MaxRetries
		}
		s.cfgMu.Unlock()
		s.AddLog("INFO", fmt.Sprintf("config updated via dashboard: target_url=%q sessions=%d retries=%d",
			payload.TargetURL, payload.NumberOfSessions, payload.MaxRetries))
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// ─── /api/nodes ──────────────────────────────────────────────────────────────

// handleNodes returns a synthetic cluster health snapshot.
// In a real deployment this would query the gRPC workers; here we return the
// master node's actual runtime stats plus placeholder worker stubs so the
// frontend Cluster Health Matrix renders correctly out-of-the-box.
func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	nodes := make([]NodeStatus, 0, 7)

	// Master node – real runtime data.
	nodes = append(nodes, NodeStatus{
		ID:         "master-1",
		Role:       "master",
		Status:     "online",
		MemoryMB:   memStats.Alloc / 1024 / 1024,
		Goroutines: runtime.NumGoroutine(),
		GRPCStatus: "online",
	})

	// Worker stubs – represent the 6 worker PCs.
	workerStatuses := []string{"online", "online", "online", "online", "syncing", "online"}
	for i, st := range workerStatuses {
		grpc := "online"
		if st == "syncing" {
			grpc = "syncing"
		}
		nodes = append(nodes, NodeStatus{
			ID:         fmt.Sprintf("worker-%d", i+1),
			Role:       "worker",
			Status:     st,
			MemoryMB:   0,
			Goroutines: 0,
			GRPCStatus: grpc,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(nodes); err != nil {
		log.Printf("dashboard: encode nodes: %v", err)
	}
}

// ─── /api/proxy ──────────────────────────────────────────────────────────────

const maxProxyUploadSize = 10 << 20 // 10 MiB

func (s *Server) handleProxy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxProxyUploadSize)
	if err := r.ParseMultipartForm(maxProxyUploadSize); err != nil {
		http.Error(w, "request too large or not multipart", http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("proxies")
	if err != nil {
		http.Error(w, "missing 'proxies' field", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Write to a temp file; callers can watch the configured ProxyFile path.
	dest, err := os.CreateTemp("", "proxies-*.txt")
	if err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	defer dest.Close()

	n, err := io.Copy(dest, file)
	if err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}

	s.cfgMu.Lock()
	s.cfg.ProxyFile = dest.Name()
	s.cfgMu.Unlock()

	s.AddLog("INFO", fmt.Sprintf("proxy list uploaded: file=%q size=%d bytes original=%q",
		dest.Name(), n, header.Filename))

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"ok":true,"path":%q,"bytes":%d}`, dest.Name(), n)
}
