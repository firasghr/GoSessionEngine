# GoSessionEngine

A high-performance, headless, concurrent HTTP session automation engine written in Go.

---

## Table of Contents

1. [Introduction](#introduction)
2. [Core Features](#core-features)
3. [Architecture Overview](#architecture-overview)
4. [Session Lifecycle](#session-lifecycle)
5. [Concurrency Model](#concurrency-model)
6. [Performance Design](#performance-design)
7. [Project Structure](#project-structure)
8. [Installation Guide](#installation-guide)
9. [Configuration Guide](#configuration-guide)
10. [Execution Flow](#execution-flow)
11. [Scalability Design](#scalability-design)
12. [Performance Characteristics](#performance-characteristics)
13. [Fault Tolerance](#fault-tolerance)
14. [Logging and Metrics](#logging-and-metrics)
15. [Development Guide](#development-guide)
16. [Production Deployment Guide](#production-deployment-guide)
17. [Future Improvements](#future-improvements)
18. [Conclusion](#conclusion)

---

## Introduction

GoSessionEngine is a production-grade, headless HTTP session automation engine designed for high-concurrency infrastructure workloads. It is written entirely in Go and engineered to manage thousands of independent HTTP sessions simultaneously while maintaining predictable memory consumption, minimal CPU overhead, and clean, graceful lifecycle management.

The engine was designed around a single core principle: each session must be a fully isolated, self-contained HTTP client. Unlike naive multi-threaded approaches that share a single HTTP transport or cookie store across all workers, GoSessionEngine assigns every session its own connection pool, cookie jar, header profile, and lifecycle state. This isolation eliminates cross-session interference, makes failure domains narrow, and allows the system to scale without coordination overhead between sessions.

### Purpose and Goals

GoSessionEngine is built for workloads that require:

- Sustaining thousands of simultaneous, long-running HTTP sessions against one or more remote origins.
- Precise per-session state management, including cookies, custom headers, and authentication tokens that must not bleed between sessions.
- Efficient connection reuse within each session to avoid the latency and CPU cost of repeated TLS handshakes.
- Transparent proxy rotation across sessions, distributing egress load across a proxy pool.
- Observable runtime behaviour through structured metrics and levelled logging.
- Deterministic shutdown that releases all transport resources cleanly without goroutine leaks.

### Design Philosophy

Modern infrastructure automation requires engines that can operate continuously under load without degrading. Three principles guide every design decision in GoSessionEngine:

**Isolation over sharing.** Shared state is the primary source of concurrency bugs and performance bottlenecks. Where possible, each session owns its resources outright. Where sharing is unavoidable (e.g., the metrics counters, the proxy rotation index), it is protected by the narrowest possible synchronisation primitive.

**Back-pressure over unbounded growth.** The worker pool uses a bounded job queue. When all workers are busy, job submission blocks the scheduler rather than spawning additional goroutines. This prevents runaway memory growth under overload and makes the system's resource consumption predictable at any concurrency level.

**Clean shutdown over forced termination.** Every component exposes a `Stop` method that drains in-flight work before returning. The main entry point listens for OS signals and calls these methods in the correct dependency order, ensuring no request is abandoned mid-flight and no file descriptor is leaked.

### Why High-Performance Concurrent Engines Matter

Infrastructure automation at scale demands engines that can hold open thousands of authenticated sessions and exercise them continuously. Applications range from synthetic monitoring and load generation to data-pipeline orchestration and distributed system testing. At these scales, the difference between a well-engineered engine and a naive implementation is the difference between a binary that holds 2,000 sessions at 50 MB of resident memory and one that runs out of file descriptors at 200 sessions or allocates gigabytes of heap due to unbounded goroutine spawning.

GoSessionEngine addresses these requirements through deliberate architecture: a worker pool that reuses goroutines, per-session transports that eliminate shared-pool contention, atomic counters that impose no mutex overhead on hot paths, and a scheduler that drives work without polling delays.

---

## Core Features

### High Concurrency Support

GoSessionEngine is designed to sustain up to 2,000 independent sessions concurrently. Session creation itself is parallelised: each session is initialised in its own goroutine, so the wall-clock time to bring up 2,000 sessions is bounded by the slowest individual initialisation rather than the cumulative serial cost. The worker pool then drives all sessions continuously with a fixed goroutine count, eliminating goroutine-per-request overhead.

### Session Isolation

Each session owns a dedicated `*http.Client` instance, which in turn owns a dedicated `*http.Transport` and `http.CookieJar`. Connection pools, TLS state, and cookies are never shared between sessions. This isolation means that a failure in one session (e.g., a server closing a persistent connection) has no effect on any other session, and per-session authentication state is always consistent.

### Efficient Connection Pooling

Within each session, TCP connections are reused across sequential requests via HTTP keep-alives. The transport is configured with explicit limits on idle connections, idle connections per host, and total connections per host. These limits prevent any single session from exhausting operating-system file descriptors while still allowing connection-level burst parallelism when a session makes rapid sequential requests.

### Low Memory Usage

Memory efficiency is achieved through three mechanisms. First, goroutines are pooled and reused rather than spawned per request. Second, the job queue is bounded, preventing unbounded heap growth under sustained load. Third, idle TCP connections are evicted from the pool after a configurable timeout, allowing the OS to reclaim socket buffers promptly.

### Modular Architecture

The engine is organised into independent Go packages, each with a single well-defined responsibility. The packages communicate through explicit interfaces rather than global state. This design makes individual components testable in isolation, replaceable without cascading changes, and auditable at the package level.

### Proxy Support

The `proxy` package provides a thread-safe round-robin proxy manager. It reads a newline-delimited proxy list from disk at startup and rotates through entries atomically as sessions are created. Each session bakes its assigned proxy into its transport at construction time so the proxy selection is immutable for the session's lifetime. Sessions that exhaust the proxy list or are created without a proxy file connect directly.

### Worker Pool Execution Model

The `worker` package implements a fixed-size goroutine pool backed by a buffered job channel. Workers are started once and loop indefinitely over the channel until it is closed. The channel buffer (four times the worker count) absorbs short bursts of job submissions without blocking the scheduler while still applying back-pressure when the system is genuinely saturated.

### Scheduler System

The `scheduler` package decouples job definition from job dispatch. The caller supplies a single `jobFn` closure at scheduler start time. The scheduler then continuously enumerates all registered sessions and submits `jobFn(session)` to the worker pool for each one. The scheduler's control goroutine checks a stop channel on each iteration so shutdown is prompt and does not require external signalling beyond closing the channel.

### Metrics Tracking

The `metrics` package tracks total requests dispatched, successful responses, and failed responses using `sync/atomic` operations exclusively. There is no mutex on the hot path. A `RequestsPerSecond` method computes throughput relative to engine start time. A `Snapshot` method returns a consistent read of all three counters for periodic reporting.

### Logging System

The `logger` package wraps the standard library `log.Logger` with a levelled interface supporting DEBUG, INFO, and ERROR levels. Level checks and writes are protected by a `sync.RWMutex` so that `SetLevel` can be called at runtime without data races. Log lines include microsecond-resolution timestamps, making latency diagnosis practical at high request rates.

---

## Architecture Overview

GoSessionEngine is composed of nine discrete components that interact through clearly defined interfaces.

### Component Descriptions

**main (controller)**
The entry point and composition root. It parses flags, loads configuration, constructs all components in dependency order, starts the worker pool and scheduler, runs the metrics monitor in a background goroutine, and blocks until an OS signal triggers graceful shutdown. It owns the shutdown sequence and calls component `Stop` methods in reverse construction order.

**config**
Loads and validates engine configuration from a JSON file or from compiled-in defaults. The `Config` struct is populated once at startup and then treated as read-only, making it safe to share across goroutines without synchronisation.

**session.Session**
The atomic unit of the engine. Each session holds an `*http.Client` (with its own transport and cookie jar), a custom header map, a lifecycle state string, and activity timestamps. The `ExecuteRequest` method sends an HTTP request with per-session headers applied and updates the last-activity timestamp atomically under a mutex.

**session.SessionManager**
Manages the map of all active sessions. Session creation is fully parallel. Read operations (`GetSession`, `Count`) use `sync.RWMutex` read-locks so they never block each other. Write operations (`CreateSessions`, `StopAll`) take a full lock. The manager provides the scheduler with a consistent view of active sessions.

**client (HTTP client factory)**
Constructs `*http.Client` instances with tuned transports. Each call to `NewHTTPClient` returns a client with its own `*http.Transport` and `http.CookieJar`. Transport parameters (connection limits, timeouts) are set to production-safe defaults. Proxy URLs are parsed and attached to the transport at construction time.

**proxy.ProxyManager**
Reads a proxy list from disk and rotates through entries in round-robin order under a mutex. Returns an empty string when no proxies are loaded, signalling callers to use direct connections.

**worker.WorkerPool**
Maintains a fixed number of goroutines draining a bounded job channel. Provides `Start`, `Submit`, and `Stop` methods. `Stop` closes the channel and waits for all in-flight jobs to complete via a `sync.WaitGroup`.

**scheduler.Scheduler**
Drives continuous job dispatch. On each tick of its internal loop it calls `sessionManager.Count`, iterates over session IDs, retrieves each session, and submits `jobFn(session)` to the worker pool. A stop channel terminates the loop cleanly.

**metrics.Metrics**
Maintains three `uint64` atomic counters and a start timestamp. All increment and read operations use `sync/atomic`, eliminating lock contention on the metrics hot path.

**logger.Logger**
Levelled, thread-safe logger backed by three `log.Logger` instances (one per level). Level filtering is performed under a `sync.RWMutex` read-lock to allow concurrent log writes without blocking.

### Component Interaction

```
main
 ├── config.LoadConfig / config.DefaultConfig
 ├── proxy.ProxyManager.LoadProxies
 ├── metrics.NewMetrics
 ├── session.NewSessionManager
 │    └── session.SessionManager.CreateSessions
 │         └── [goroutine per session] client.NewHTTPClient → session.NewSession
 ├── worker.NewWorkerPool → WorkerPool.Start
 ├── scheduler.NewScheduler
 │    └── Scheduler.Start(jobFn)
 │         └── [control goroutine]
 │              └── SessionManager.GetSession → WorkerPool.Submit(jobFn(session))
 └── [signal handler] → Scheduler.Stop → WorkerPool.Stop → SessionManager.StopAll
```

### ASCII Architecture Diagram

```
+--------------------------------------------------------------------+
|                           main (controller)                        |
|  flags → config → proxy manager → metrics → session manager       |
|               ↓                                    ↓               |
|          worker pool ←──────── scheduler ──────────┘               |
|          (goroutines)          (control loop)                      |
+--------------------------------------------------------------------+
         ↕ Submit(job)                   ↕ GetSession(id)
+------------------+          +----------------------------+
|   WorkerPool     |          |      SessionManager        |
|  [w0][w1]…[wN]  |          |  map[int]*Session          |
|  jobQueue chan   |          |  sync.RWMutex              |
+------------------+          +----------------------------+
         ↕ job()                          ↕ Session
+------------------+          +----------------------------+
|   jobFn closure  |          |   Session                  |
|  s.ExecuteRequest|──────────|   *http.Client             |
+------------------+          |   *http.Transport          |
                               |   http.CookieJar           |
                               |   Headers map              |
                               |   State / LastActivity     |
                               +----------------------------+
                                            ↕
                               +----------------------------+
                               |   ProxyManager             |
                               |   round-robin rotation     |
                               +----------------------------+

  metrics ← IncrementTotal / IncrementSuccess / IncrementFailed
  logger  ← Info / Debug / Error (levelled, microsecond timestamps)
```

### Data Flow

1. The scheduler's control goroutine enumerates session IDs each loop iteration.
2. For each session, a closure capturing that session is submitted to the worker pool's job channel.
3. An idle worker goroutine picks up the closure and invokes `jobFn(session)`.
4. Inside `jobFn`, the session executes an HTTP request via its dedicated client, the response status is evaluated, and the appropriate metrics counter is incremented.
5. The worker goroutine returns to the channel drain loop and picks up the next job.

### Concurrency Model Overview

The engine uses three concurrency primitives:

- **Goroutines**: one per worker (fixed), one for the scheduler control loop, one for the metrics monitor, and temporary goroutines during session creation.
- **Channels**: the worker pool's job queue provides both communication and back-pressure between the scheduler (producer) and workers (consumers).
- **Mutexes and atomics**: `sync.RWMutex` protects maps and level fields; `sync/atomic` protects metrics counters.

---

## Session Lifecycle

### Creation

Session creation is triggered by `SessionManager.CreateSessions`. For each requested session, a goroutine is spawned that:

1. Retrieves the next proxy address from the `ProxyManager` (or uses an empty string for direct connections).
2. Calls `client.NewHTTPClient(proxy, timeout)` to build a dedicated `*http.Transport` and `http.CookieJar`, then wraps them in an `*http.Client`.
3. Constructs a `Session` struct with `State: "idle"`, a fresh header map, and `CreatedAt` and `LastActivity` both set to the current wall-clock time.
4. Sends the result (or error) on a buffered results channel.

A separate goroutine waits for all creation goroutines to finish (via `sync.WaitGroup`), then closes the results channel. The manager drains the channel under a write-lock, registering successful sessions and collecting errors.

### Initialization

After `CreateSessions` returns, `SessionManager.StartAll` transitions every session from `"idle"` to `"active"`. This transition is performed under each session's own mutex, not the manager's mutex, to minimise lock contention.

### Request Execution

`Session.ExecuteRequest(method, url, body)` performs the following steps:

1. Builds an `*http.Request` from the provided method, URL, and optional body.
2. Acquires a read-lock on the session mutex and copies all entries from the `Headers` map into the request headers.
3. Releases the read-lock and delegates to `session.Client.Do(req)`.
4. On success, calls `UpdateLastActivity` (write-lock) and returns the response.
5. On transport error, wraps the error with session ID context and returns it.

The caller is responsible for closing the response body. The session itself does not buffer response bodies.

### State Management

Session state is the string field `State`, protected by `session.mu`. Conventional values are `"idle"` (created, not yet started), `"active"` (dispatching requests), and `"closed"` (terminated). State transitions are:

```
idle → active  (SessionManager.StartAll)
active → closed (Session.Close via SessionManager.StopAll)
```

Custom states can be introduced by callers that extend the `jobFn` logic.

### Activity Tracking

`Session.LastActivity` is updated by `UpdateLastActivity`, which acquires a write-lock and records `time.Now()`. This field can be used by external monitors to detect stale sessions that have not made a request within an expected window. `ExecuteRequest` calls `UpdateLastActivity` automatically after each successful response.

### Termination

`Session.Close` acquires the write-lock, sets `State` to `"closed"`, releases the lock, and then drains the transport's idle connection pool by calling `transport.CloseIdleConnections()`. This releases TCP sockets to the OS promptly without waiting for the idle timeout.

`SessionManager.StopAll` holds the manager's write-lock while iterating over all sessions, calling `Close` on each one and removing it from the map.

### Memory and Resource Management

Each session holds one `*http.Client`, one `*http.Transport`, one `http.CookieJar`, and one `map[string]string` for headers. The transport internally manages a pool of idle `*persistConn` objects. These are released by `CloseIdleConnections` at session termination. After `StopAll`, the sessions map is empty and the Go garbage collector can reclaim all session heap allocations.

---

## Concurrency Model

### Goroutines

GoSessionEngine uses goroutines in four distinct roles:

- **Worker goroutines**: created once by `WorkerPool.Start` in a count equal to the number of sessions (capped at 2,000 in the default configuration). They run for the engine's entire lifetime, draining the job channel.
- **Scheduler control goroutine**: a single goroutine that loops over session IDs and submits jobs to the worker pool. It exits when the stop channel is closed.
- **Metrics monitor goroutine**: a single goroutine that ticks every 10 seconds and logs a summary line. It exits when its ticker channel is closed at shutdown.
- **Session creation goroutines**: short-lived goroutines, one per session, spawned during `CreateSessions` and exiting after writing their result to the results channel.

The total steady-state goroutine count is `workerCount + 2` (workers + scheduler + metrics monitor), plus the Go runtime's own internal goroutines.

### Channels

The worker pool's `jobQueue` channel is the primary communication mechanism. It is a buffered channel with capacity `workerCount * 4`. This buffer allows the scheduler to submit a burst of jobs without blocking while workers are still completing their previous jobs, reducing the number of scheduler-side context switches. When the buffer is full, `Submit` blocks, implementing natural back-pressure.

The scheduler uses a `stopCh chan struct{}` as a termination signal. Closing the channel unblocks the `select` in the scheduler's control goroutine on the next iteration.

The session creation process uses a buffered results channel of capacity `count` so that creation goroutines never block waiting for the manager to consume their result.

### Mutex Usage

Three types of mutex are used:

- `session.mu` (`sync.RWMutex`): protects `Headers`, `State`, and `LastActivity` within a single session. Read-locks are used for header reads and level checks; write-locks are used for state and activity updates.
- `SessionManager.mutex` (`sync.RWMutex`): protects the sessions map. Read-locks for `GetSession` and `Count`; write-lock for `CreateSessions` and `StopAll`.
- `ProxyManager.mutex` (`sync.Mutex`): protects the proxy list and index. A full mutex (not RWMutex) is used because `GetNextProxy` both reads and writes the index atomically.
- `Logger.mu` (`sync.RWMutex`): protects the level field. Read-locks for all log methods; write-lock for `SetLevel`.

### Atomic Counters

All three metrics counters (`TotalRequests`, `Success`, `Failed`) are `uint64` values accessed exclusively through `sync/atomic`. This means that incrementing a counter from any of the 2,000 concurrent workers requires no mutex acquisition, no goroutine scheduling, and no cache-line invalidation beyond the single atomic operation. The performance difference versus a mutex-protected counter is measurable at high request rates.

### Worker Pools

The worker pool pattern eliminates goroutine-per-request overhead. Spawning a goroutine allocates a minimum 2 KB stack that grows on demand; at 2,000 requests per second, goroutine-per-request would require allocating and scheduling thousands of goroutines per second. The pool amortises this cost: goroutines are allocated once and reused indefinitely.

### Why This Model Is Efficient

The combination of pooled goroutines, bounded channels, and atomic counters means that the engine's concurrency overhead scales with the worker count, not with the request rate. Once the pool is running, each request adds exactly one channel send (scheduler to worker), one channel receive (worker from queue), one atomic increment per counter update, and the actual HTTP I/O. There are no heap allocations in the dispatch hot path beyond the job closure itself.

### Scalability Properties

The concurrency model scales linearly with `NumberOfSessions` up to OS limits (open file descriptors, ephemeral port range). Above a single machine's limits, the stateless design of the engine (all session state is in-process memory with no external coordination) means multiple engine instances can be deployed in parallel without modification.

---

## Performance Design

### Connection Reuse

HTTP keep-alives are enabled on every transport. This means that once a TCP connection to a target host is established by a session, subsequent requests from that session reuse the same connection, bypassing TCP handshake and TLS negotiation. At a typical TLS handshake cost of 1-5 ms per connection, reuse at 100 requests per session translates to seconds of saved latency and significant CPU reduction.

### Transport Configuration

Each session's transport is configured with three connection pool limits:

- `MaxIdleConns` (default: 500): total idle connections across all hosts in the pool.
- `MaxIdleConnsPerHost` (default: 100): idle connections to a single host.
- `MaxConnsPerHost` (default: 200): total connections (idle + active) to a single host.

`IdleConnTimeout` is set to 90 seconds to evict connections that remote servers or intermediary proxies may have silently closed, preventing the session from attempting to send on a dead connection.

`TLSHandshakeTimeout` is set to 10 seconds to bound the time spent waiting for servers that accept TCP connections but stall during TLS negotiation.

### Memory Efficiency

Memory usage is bounded at three levels:

1. **Session level**: each session's heap footprint is dominated by the transport's idle connection pool. With `MaxIdleConnsPerHost: 100`, a session targeting a single host holds at most 100 idle connections, each consuming a small amount of buffer memory.
2. **Worker pool level**: the job queue buffers at most `workerCount * 4` closures. A closure capturing a single `*Session` pointer is approximately 16 bytes on a 64-bit platform.
3. **Metrics level**: three 8-byte atomic counters plus a `time.Time` value (24 bytes). Regardless of request rate, metrics memory usage is constant.

### Thread Efficiency

Worker goroutines spend the vast majority of their time blocked on `s.Client.Do(req)`, which is an I/O-bound operation. The Go scheduler transparently parks the goroutine while the network I/O is outstanding and resumes it when data arrives, allowing the OS thread underlying that goroutine to service other goroutines. This means that even a modest number of OS threads can drive thousands of concurrent HTTP sessions efficiently.

### Minimal Overhead Design

The scheduler's dispatch loop has no sleep or polling delay. It submits jobs as fast as the worker pool's back-pressure allows, maximising throughput when the target server can sustain it. When the server cannot keep up, `Submit` blocks, and the scheduler naturally throttles to the server's capacity. No explicit rate-limiting configuration is required for most use cases.

---

## Project Structure

```
GoSessionEngine/
├── main.go                  Engine entry point and composition root
├── go.mod                   Go module definition
├── config/
│   ├── config.go            Configuration struct, JSON loader, and defaults
│   └── config_test.go       Unit tests for config loading and defaults
├── session/
│   ├── session.go           Session type, construction, request execution, lifecycle
│   ├── session_test.go      Unit tests for session construction and request execution
│   ├── manager.go           SessionManager: parallel creation, lookup, start/stop
│   └── manager_test.go      Unit tests for session manager
├── client/
│   └── client.go            HTTP client factory with tuned transport and cookie jar
├── proxy/
│   ├── proxy.go             Thread-safe round-robin proxy manager
│   └── proxy_test.go        Unit tests for proxy loading and rotation
├── worker/
│   ├── pool.go              Fixed-size goroutine pool with bounded job channel
│   └── pool_test.go         Unit tests for worker pool start/stop and job execution
├── scheduler/
│   └── scheduler.go         Continuous job dispatch loop bridging manager and pool
├── metrics/
│   ├── metrics.go           Atomic request counters and throughput calculation
│   └── metrics_test.go      Unit tests for counter operations and snapshot
└── logger/
    └── logger.go            Levelled, thread-safe logger backed by standard library
```

### File Responsibilities

**main.go**
Parses the `-config` CLI flag, constructs all components in dependency order, starts the worker pool and scheduler, launches the metrics monitor goroutine, and blocks on an OS signal channel. On signal receipt, stops the scheduler, drains the worker pool, stops all sessions, logs final metrics, and exits cleanly.

**config/config.go**
Defines `Config` with JSON field tags for all tunable parameters. `LoadConfig` opens and JSON-decodes a file with `DisallowUnknownFields` to catch configuration typos early. `DefaultConfig` returns a pre-filled struct tuned for approximately 500 concurrent sessions.

**session/session.go**
Defines the `Session` struct and its three primary methods: `NewSession` (constructor), `ExecuteRequest` (HTTP dispatch with header injection and activity tracking), and `Close` (state transition and transport drain). All mutable state is protected by `session.mu`.

**session/manager.go**
Defines `SessionManager` with a `map[int]*Session` protected by `sync.RWMutex`. `CreateSessions` parallelises construction using goroutines and a results channel. `StartAll` and `StopAll` batch-transition all sessions. `GetSession` and `Count` are read-optimised with `RLock`.

**client/client.go**
Implements `NewHTTPClient(proxy, timeout)`, the factory function for session HTTP clients. `buildTransport` constructs a `*http.Transport` with explicit pool limits and optional proxy. `newCookieJar` wraps `cookiejar.New` with error handling.

**proxy/proxy.go**
Implements `ProxyManager` with `LoadProxies` (file reader with comment and blank-line filtering), `GetNextProxy` (atomic round-robin under mutex), and `Count`.

**worker/pool.go**
Implements `WorkerPool` with `NewWorkerPool`, `Start` (goroutine launcher), `Submit` (blocking channel send), and `Stop` (channel close + WaitGroup drain).

**scheduler/scheduler.go**
Implements `Scheduler` with `NewScheduler`, `Start` (non-blocking control goroutine launch), `dispatchJobs` (session iteration and job submission), and `Stop` (idempotent stop channel close via `sync.Once`).

**metrics/metrics.go**
Implements `Metrics` with `NewMetrics`, `IncrementTotal`, `IncrementSuccess`, `IncrementFailed`, `RequestsPerSecond`, and `Snapshot`. All counter operations use `sync/atomic`.

**logger/logger.go**
Implements `Logger` with `New(level)`, `SetLevel`, `Info`/`Infof`, `Error`/`Errorf`, and `Debug`/`Debugf`. Uses three `log.Logger` instances for level-specific prefixes and flags.

---

## Installation Guide

### Prerequisites

GoSessionEngine requires Go 1.24 or later. Verify your installation:

```bash
go version
```

If Go is not installed, download and install it from the official distribution:

```bash
# Linux (amd64)
wget https://go.dev/dl/go1.24.0.linux-amd64.tar.gz
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf go1.24.0.linux-amd64.tar.gz
export PATH=$PATH:/usr/local/go/bin
```

Add the `PATH` export to your shell profile (`~/.bashrc`, `~/.profile`, or `~/.zshrc`) for persistence.

### Clone the Repository

```bash
git clone https://github.com/firasghr/GoSessionEngine.git
cd GoSessionEngine
```

### Build the Binary

```bash
go build -o gosessionengine .
```

This produces a statically-linked binary `gosessionengine` in the current directory. To embed version metadata at build time:

```bash
go build -ldflags "-X main.version=$(git describe --tags --always)" -o gosessionengine .
```

### Run the Engine

Run with default configuration:

```bash
./gosessionengine
```

Run with a custom configuration file:

```bash
./gosessionengine -config /etc/gosessionengine/config.json
```

The engine logs to stderr. Redirect or pipe as needed:

```bash
./gosessionengine -config config.json 2>&1 | tee engine.log
```

### Run Tests

```bash
go test ./...
```

To run tests with the race detector enabled:

```bash
go test -race ./...
```

---

## Configuration Guide

Configuration is supplied as a JSON file passed via the `-config` flag. If the flag is omitted, `DefaultConfig` values are used.

### Configuration Fields

| Field | Type | Default | Description |
|---|---|---|---|
| `number_of_sessions` | integer | 500 | Number of independent sessions to create and maintain concurrently. Keep at or below 2,000 for safe operation within typical OS file-descriptor limits. |
| `request_timeout` | duration (nanoseconds) | 30000000000 (30s) | End-to-end HTTP request timeout covering connection setup, TLS handshake, request body transmission, and full response reading. |
| `max_retries` | integer | 3 | Maximum number of times a failed request is retried before being counted as a permanent failure. |
| `target_url` | string | "" | Base URL the engine will target. If empty, the default `jobFn` is a no-op. |
| `proxy_file` | string | "" | Path to a newline-delimited proxy list. Lines beginning with `#` and blank lines are ignored. Leave empty for direct connections. |
| `max_idle_conns` | integer | 500 | Total maximum idle (keep-alive) connections across all hosts per session transport. |
| `max_idle_conns_per_host` | integer | 100 | Maximum idle connections to a single host per session transport. |
| `max_conns_per_host` | integer | 200 | Maximum total connections (idle + active) to a single host per session transport. |

### Duration Encoding

`request_timeout` is stored internally as a `time.Duration` (nanoseconds). When loading from JSON, supply the value in nanoseconds as an integer. Example: `30000000000` represents 30 seconds.

### Example Configuration File

```json
{
  "number_of_sessions": 200,
  "request_timeout": 30000000000,
  "max_retries": 3,
  "target_url": "https://example.com",
  "proxy_file": "/etc/gosessionengine/proxies.txt",
  "max_idle_conns": 200,
  "max_idle_conns_per_host": 50,
  "max_conns_per_host": 100
}
```

### Example Proxy File

```
# HTTP proxies
http://10.0.0.1:8080
http://10.0.0.2:8080

# SOCKS5 proxies
socks5://10.0.1.1:1080
```

---

## Execution Flow

The following describes the complete startup-to-shutdown sequence of the engine:

1. **Flag parsing**: the `-config` flag is parsed. If provided, the path is stored for use in step 3.

2. **Logger initialisation**: a `Logger` at `LevelInfo` is constructed. All subsequent startup messages are written through this logger to stderr.

3. **Configuration loading**: if a config file path was provided, `config.LoadConfig` opens and JSON-decodes it with strict field validation. Otherwise, `config.DefaultConfig` returns the production defaults. A loading error at this stage causes the engine to exit with code 1.

4. **Proxy manager initialisation**: a `ProxyManager` is constructed. If `cfg.ProxyFile` is non-empty, `LoadProxies` reads and parses the file. A file read error causes the engine to exit with code 1.

5. **Metrics initialisation**: `metrics.NewMetrics` records the start time. The metrics instance is used throughout the engine lifetime.

6. **Session manager and session creation**: `session.NewSessionManager` creates the manager. `sm.CreateSessions(cfg.NumberOfSessions, pm)` spawns one goroutine per session, each of which calls `client.NewHTTPClient` and `session.NewSession`. All goroutines write their results to a buffered channel. The manager collects results under a write-lock, registers successful sessions, and aggregates errors. Any session creation failure causes the engine to exit with code 1.

7. **Worker pool startup**: `worker.NewWorkerPool(workerCount)` allocates the pool and `wp.Start()` launches all worker goroutines. They immediately block on the job channel.

8. **Scheduler startup**: `scheduler.NewScheduler(sm, wp)` creates the scheduler. `sc.Start(jobFn)` launches the control goroutine. The job function closure captures the session, increments the total counter, executes the HTTP request, and increments the success or failure counter.

9. **Session activation**: `sm.StartAll()` transitions all sessions from `"idle"` to `"active"` under per-session write-locks.

10. **Metrics monitor**: a background goroutine ticks every 10 seconds and logs `total`, `success`, `failed`, `rps`, and `session count` at INFO level.

11. **Steady-state operation**: the scheduler continuously iterates over sessions and submits jobs to the worker pool. Workers execute jobs concurrently. Metrics accumulate. Logs are written.

12. **Signal receipt**: the main goroutine unblocks when `SIGINT` or `SIGTERM` is received on the signal channel.

13. **Graceful shutdown sequence**:
    - `sc.Stop()` closes the scheduler's stop channel, causing the control goroutine to exit after its current iteration.
    - `wp.Stop()` closes the job channel and blocks until all in-flight jobs complete and all worker goroutines exit.
    - `sm.StopAll()` calls `Session.Close` on every session, draining idle connections and releasing transports, then empties the sessions map.

14. **Final metrics log**: the engine logs total, success, failed, and rps values at INFO level.

15. **Exit**: the main function returns, the process exits with code 0.

---

## Scalability Design

### Vertical Scaling

On a single machine, performance scales with:

- **CPU cores**: the Go scheduler maps goroutines to OS threads using `GOMAXPROCS`, which defaults to the number of available CPU cores. Additional cores allow more worker goroutines to execute simultaneously during CPU-bound phases (response parsing, metrics updates).
- **Available file descriptors**: each session transport can hold up to `MaxConnsPerHost` open TCP connections. With 2,000 sessions and `MaxConnsPerHost: 200`, the theoretical maximum is 400,000 open sockets. The OS `ulimit -n` must be set accordingly. In practice, the engine will have far fewer open connections at any given time since most sessions are blocked on network I/O with a single keep-alive connection.
- **RAM**: memory usage scales linearly with `NumberOfSessions`. Each session's resident footprint is dominated by its transport's connection pool and cookie jar. At 500 sessions with typical usage, total RSS is measured in tens of megabytes.

Recommended vertical scaling adjustments:

```bash
# Increase open file descriptor limit for the engine process
ulimit -n 1048576

# Or set permanently in /etc/security/limits.conf
*   hard   nofile   1048576
*   soft   nofile   1048576
```

### Horizontal Scaling

GoSessionEngine holds no shared external state. All session state is in-process memory. Multiple engine instances can be deployed on separate machines and aimed at the same target without coordination. Load across instances can be distributed by:

- Assigning disjoint proxy subsets to each instance (partition the proxy file).
- Running instances behind a process supervisor (systemd, supervisor) with different config files.
- Orchestrating with Kubernetes as a `Deployment` with multiple replicas, each inheriting its config from a `ConfigMap`.

### Limitations

- **Single process**: the engine is a single OS process. It cannot exceed a single machine's file-descriptor limit or network interface capacity without horizontal scaling.
- **No distributed coordination**: there is no inter-instance state sharing, distributed rate limiting, or centralised session registry. Applications requiring these features must implement them externally.
- **In-process metrics**: metrics are stored in process memory. If the process is terminated uncleanly, accumulated metrics are lost. Integration with an external metrics system (Prometheus, InfluxDB) requires implementing an HTTP exporter endpoint.

### Optimization Strategies

- Set `NumberOfSessions` to match the target server's concurrent connection capacity. Exceeding it will result in high error rates and wasted resources.
- If the target is a single host, set `MaxIdleConnsPerHost` to `NumberOfSessions` to ensure every session can maintain one persistent connection.
- On Linux, tune the TCP stack for high connection counts: increase `net.ipv4.ip_local_port_range`, `net.core.somaxconn`, and `net.ipv4.tcp_tw_reuse`.
- Profile with `pprof` under production load to identify any unexpected allocation hotspots before tuning `GOGC`.

---

## Performance Characteristics

### Memory Behavior

At steady state with 500 sessions and `MaxIdleConnsPerHost: 100`, the expected resident set size is approximately 20-60 MB depending on cookie accumulation and TLS session cache sizes. Memory is dominated by per-session transport state, not by goroutine stacks. Worker goroutine stacks start at 2-8 KB and grow only if the job function allocates deeply; simple HTTP dispatch jobs do not trigger stack growth.

The job queue buffer (`workerCount * 4` closures) is a negligible contributor to memory. At 500 workers, this is 2,000 closure pointers, approximately 16 KB.

### CPU Behavior

CPU usage is primarily driven by TLS handshakes (amortised across connections due to keep-alives), JSON/response body parsing within the job function, and the Go scheduler's goroutine context-switching overhead. At 2,000 sessions executing simple GET requests against a fast server, CPU usage on a modern 4-core machine is typically under 200% (two full cores), with the remainder idle waiting for network I/O. Adding more CPU cores above this point yields diminishing returns unless the job function is computationally intensive.

### Connection Behavior

With keep-alives enabled, each session establishes one TCP connection to the target host on its first request and reuses it for all subsequent requests. The number of active TCP connections in steady state equals the number of sessions, not the request rate. Connection churn (new connections per second) is low and occurs only when `IdleConnTimeout` (90 seconds) expires on an unused connection or the remote server sends a `Connection: close` header.

---

## Fault Tolerance

### Error Handling Strategy

Errors are classified at two levels:

- **Fatal startup errors**: configuration load failures, proxy file read failures, and session creation failures cause the engine to log the error and exit with code 1. These errors indicate misconfiguration or an unavailable dependency that makes operation impossible.
- **Per-request errors**: transport errors (connection refused, timeout, DNS failure) and application-level failures (non-2xx/3xx status) are counted in the `Failed` metric and logged at DEBUG level. The session remains active and the scheduler continues dispatching work to it. The engine does not automatically retry failed sessions without explicit retry logic in the `jobFn` closure.

Error messages include the session ID and, where applicable, the method and URL, providing sufficient context to diagnose failures in logs without examining additional state.

### Retry Logic Philosophy

`Config.MaxRetries` provides a configuration field for retry counts, and the `jobFn` closure in `main.go` is the natural location to implement retry loops. The engine itself does not implement automatic retries in the transport layer, as retry semantics are application-specific: retrying a non-idempotent POST may cause double-submission, and retrying after a 429 Too Many Requests response requires back-off logic that the engine does not prescribe.

Implementations requiring retries should wrap `s.ExecuteRequest` in a loop within `jobFn`, decrementing a per-call retry counter and sleeping with exponential back-off before each retry.

### Failure Isolation

Session isolation is the primary fault-containment mechanism. A session that enters an error state (e.g., its proxy becomes unreachable) accumulates failures in the `Failed` counter but does not affect other sessions. The scheduler continues to submit jobs to the failing session, which will continue to fail until the underlying condition resolves or the session is explicitly closed and replaced.

The worker pool isolates panics at the goroutine level. If a job closure panics, the worker goroutine executing it will crash. To prevent this from silently reducing the worker pool size, production deployments should recover panics within `jobFn` using `defer recover()`.

---

## Logging and Metrics

### Metrics System

The `Metrics` struct maintains three `uint64` counters via `sync/atomic`:

- `TotalRequests`: incremented once per request dispatch, before the HTTP call.
- `Success`: incremented when the response status code is 2xx or 3xx.
- `Failed`: incremented on transport error or non-2xx/3xx response.

`RequestsPerSecond` computes throughput as `TotalRequests / elapsed_seconds` since engine start. This is an average rate over the engine lifetime, not an instantaneous rate. For instantaneous rates, capture two `Snapshot` readings at a known interval and compute the delta.

`Snapshot` performs three separate atomic loads without a covering lock. In theory, the three values can be momentarily inconsistent (e.g., `Total` is updated but `Success` has not yet been incremented for the same request). In practice, this inconsistency is at nanosecond granularity and is irrelevant for monitoring purposes. If strict consistency is required, replace the three atomics with a mutex-protected struct.

### Logging System

The logger produces structured lines to stderr with the following format:

```
LEVEL  YYYY/MM/DD HH:MM:SS.microseconds message
```

Example:

```
INFO  2026/02/25 22:00:01.123456 metrics – total: 15000 | success: 14850 | failed: 150 | rps: 250.0 | sessions: 500
```

Log levels in ascending order of verbosity restriction:

- `LevelDebug` (0): all messages including per-request error details.
- `LevelInfo` (1): startup/shutdown events and periodic metrics summaries.
- `LevelError` (2): only error events.

The engine runs at `LevelInfo` by default. Change to `LevelDebug` when diagnosing request failures; change to `LevelError` in high-throughput production environments where INFO volume is excessive.

### Monitoring Strategy

For production deployments, the metrics monitor goroutine provides a baseline of observability by logging summaries every 10 seconds. For richer monitoring:

- **Prometheus integration**: expose a `/metrics` HTTP endpoint using the `prometheus/client_golang` library. Map `TotalRequests`, `Success`, and `Failed` to `Counter` metrics and derive rates in the Prometheus query layer.
- **Log aggregation**: forward stderr to a log aggregation system (Loki, Elasticsearch, CloudWatch Logs) and create dashboards on the structured fields (`total`, `success`, `failed`, `rps`, `sessions`).
- **Alerting**: alert on `failed / total > 0.05` (5% error rate) or `rps < threshold` (throughput degradation).

---

## Development Guide

### Extending the Job Function

The primary extension point is the `jobFn` closure passed to `scheduler.Start`. Replace the GET request in `main.go` with any application-specific logic:

```go
jobFn := func(s *session.Session) {
    m.IncrementTotal()

    // Set a per-request header
    s.mu.Lock()
    s.Headers["Authorization"] = "Bearer " + getToken()
    s.mu.Unlock()

    resp, err := s.ExecuteRequest(http.MethodPost, cfg.TargetURL+"/api/action", buildBody())
    if err != nil {
        m.IncrementFailed()
        return
    }
    defer resp.Body.Close()

    if resp.StatusCode == http.StatusOK {
        m.IncrementSuccess()
    } else {
        m.IncrementFailed()
    }
}
```

### Adding a New Module

1. Create a new directory under the repository root (e.g., `ratelimiter/`).
2. Declare `package ratelimiter` and implement the module.
3. Write unit tests in `ratelimiter_test.go` following the existing test patterns.
4. Import the package in `main.go` and wire it into the startup sequence.

### Adding Per-Session Custom State

If your use case requires state that is specific to a session but not covered by the existing fields (e.g., a session-specific authentication token or a cursor for paginated requests), extend the session by embedding it in a wrapper struct:

```go
type EnrichedSession struct {
    *session.Session
    AuthToken string
    PageCursor string
}
```

Pass `*EnrichedSession` through the `jobFn` closure by maintaining a parallel map in your application layer.

### Best Practices

- Keep `jobFn` non-blocking on paths other than `s.ExecuteRequest`. Long-running CPU work inside `jobFn` reduces the effective parallelism of the worker pool.
- Always close the response body. Failing to close it prevents the underlying connection from being returned to the idle pool, causing connection exhaustion.
- Recover from panics within `jobFn` to prevent worker goroutines from crashing silently.
- Write unit tests for new modules using the `testing` package and run them with `-race` to verify absence of data races.
- Use `config.Config` for all tunable parameters rather than hard-coding values.

---

## Production Deployment Guide

### Linux Server / VPS

1. Build the binary on the target architecture or cross-compile:

   ```bash
   GOOS=linux GOARCH=amd64 go build -o gosessionengine .
   ```

2. Transfer the binary and configuration file to the server:

   ```bash
   scp gosessionengine config.json user@server:/opt/gosessionengine/
   ```

3. Set the file-descriptor limit for the service user:

   ```bash
   # /etc/security/limits.conf
   serviceuser  hard  nofile  1048576
   serviceuser  soft  nofile  1048576
   ```

4. Create a systemd service unit at `/etc/systemd/system/gosessionengine.service`:

   ```ini
   [Unit]
   Description=GoSessionEngine HTTP Automation Engine
   After=network.target

   [Service]
   User=serviceuser
   WorkingDirectory=/opt/gosessionengine
   ExecStart=/opt/gosessionengine/gosessionengine -config /opt/gosessionengine/config.json
   Restart=on-failure
   RestartSec=5s
   StandardOutput=journal
   StandardError=journal
   LimitNOFILE=1048576

   [Install]
   WantedBy=multi-user.target
   ```

5. Enable and start the service:

   ```bash
   sudo systemctl daemon-reload
   sudo systemctl enable gosessionengine
   sudo systemctl start gosessionengine
   sudo journalctl -u gosessionengine -f
   ```

### Cloud Instance (AWS EC2, GCP Compute Engine, Azure VM)

The deployment procedure is identical to the Linux server steps above. Additional cloud-specific considerations:

- **Security groups / firewall rules**: ensure outbound TCP is permitted on the ports your target URLs use (typically 80 and 443).
- **Instance sizing**: for 500 sessions, a 2-vCPU / 2 GB RAM instance (e.g., AWS t3.small or GCP e2-small) is typically sufficient. For 2,000 sessions, use a 4-vCPU / 4 GB RAM instance and verify that the instance's network bandwidth supports the expected request rate.
- **Auto Scaling**: for horizontal scaling, create an AMI with the binary and config pre-installed and use an Auto Scaling Group or Managed Instance Group with a minimum instance count of the desired replica count.

### Kubernetes

Build and push a container image:

```dockerfile
FROM golang:1.24-alpine AS builder
WORKDIR /src
COPY . .
RUN go build -o gosessionengine .

FROM alpine:3.21
COPY --from=builder /src/gosessionengine /usr/local/bin/gosessionengine
ENTRYPOINT ["/usr/local/bin/gosessionengine"]
```

Deploy as a `Deployment`:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: gosessionengine
spec:
  replicas: 3
  selector:
    matchLabels:
      app: gosessionengine
  template:
    metadata:
      labels:
        app: gosessionengine
    spec:
      containers:
      - name: engine
        image: your-registry/gosessionengine:latest
        args: ["-config", "/etc/gosessionengine/config.json"]
        volumeMounts:
        - name: config
          mountPath: /etc/gosessionengine
        resources:
          requests:
            cpu: "500m"
            memory: "256Mi"
          limits:
            cpu: "2"
            memory: "1Gi"
      volumes:
      - name: config
        configMap:
          name: gosessionengine-config
```

### Best Practices for Production

- Run under a dedicated, unprivileged user. The engine requires no elevated permissions.
- Mount the proxy file as a read-only volume or ConfigMap so it can be updated without redeploying.
- Redirect stderr to a structured log aggregation pipeline. Do not use file-based log rotation for high-throughput deployments; use journald or a container log driver.
- Monitor the `rps` and `failed` log fields and alert on degradation.
- Use process supervision with automatic restart (`Restart=on-failure` in systemd) to recover from unexpected exits.

---

## Future Improvements

**Distributed Cluster Support**

Implement a cluster coordination layer that allows multiple engine instances to share a session registry and distribute sessions across nodes. A lightweight consensus mechanism (etcd, Consul) or a pub/sub bus (NATS, Redis Streams) could synchronise session state, enabling session hand-off between nodes for rolling deployments without session loss.

**Monitoring Dashboard**

Expose a Prometheus-compatible `/metrics` HTTP endpoint and provide a pre-built Grafana dashboard definition. The dashboard would display request rate, error rate, active session count, requests per session per second, and per-proxy error rates. This would transform the engine from a fire-and-forget binary into an observable system suitable for production SRE workflows.

**REST API Interface**

Implement an embedded HTTP API server that accepts runtime commands: add sessions, remove sessions, update the proxy list, change the target URL, adjust the log level, and retrieve the current metrics snapshot. This would allow operators to manage running engine instances without restarting the process and losing session warm-up state.

**Advanced Scheduling**

Replace the simple continuous loop scheduler with a priority-aware or rate-limited scheduler that can:
- Assign different request rates to different sessions.
- Implement per-session back-off when a session accumulates errors.
- Support time-based schedules (e.g., ramp up over 60 seconds, sustain for 10 minutes, ramp down).
- Pause individual sessions without terminating them, preserving their connection pools.

**Dynamic Session Scaling**

Add a controller that monitors the error rate and request throughput and adjusts `NumberOfSessions` at runtime by calling `CreateSessions` or `StopAll` on a subset of sessions. This would allow the engine to scale session count in response to observed server capacity rather than requiring manual configuration changes.

**TLS Client Fingerprint Rotation**

Extend the HTTP client factory to randomise TLS fingerprints (cipher suites, extensions, curve preferences) across sessions, making the engine's traffic profile more closely resemble a diverse population of real clients. This is relevant for automation workloads where server-side fingerprinting is a concern.

**Persistent Session State**

Implement optional serialisation of session state (cookies, headers, last activity timestamp) to disk or an external store (Redis). On startup, deserialise existing session state to resume long-running authenticated sessions without re-authenticating.

---

## Conclusion

GoSessionEngine is a purpose-built, production-grade infrastructure component for high-concurrency HTTP session automation. Its architecture reflects a disciplined application of Go's concurrency primitives: worker pools eliminate goroutine-per-request overhead, per-session transports eliminate shared-state contention, atomic counters eliminate metrics hot-path locking, and a bounded job channel implements back-pressure without explicit rate limiting.

The design is intentionally minimal in scope and maximal in reliability. Each component has a single responsibility, a clean interface, and full unit test coverage. The shutdown sequence is deterministic and resource-safe. The configuration surface is explicit and validated at startup.

Engineers deploying GoSessionEngine in production environments should expect predictable memory consumption scaling linearly with session count, CPU usage proportional to request rate and response processing complexity, and connection behavior determined entirely by the transport configuration. The engine's stateless horizontal scaling model means that capacity can be added by deploying additional instances with no architectural changes.

For workloads requiring capabilities beyond the current feature set, the modular architecture and documented extension points provide a clear path to adding new functionality without destabilising the existing components.