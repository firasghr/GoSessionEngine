// Package logger provides a thread-safe, levelled logger backed by the
// standard library's log package.
package logger

import (
	"fmt"
	"log"
	"os"
	"sync"
)

// Level represents a logging verbosity level.
type Level int

const (
	// LevelDebug emits all messages.
	LevelDebug Level = iota
	// LevelInfo emits INFO and ERROR messages.
	LevelInfo
	// LevelError emits only ERROR messages.
	LevelError
)

// Logger is a structured, levelled logger.
//
// Thread-safety: log.Logger (from the standard library) serialises writes to
// the underlying io.Writer with its own mutex.  The Logger wrapper adds a
// second mutex only for the level field so that SetLevel may be called
// concurrently with logging methods.
type Logger struct {
	infoLog  *log.Logger
	errorLog *log.Logger
	debugLog *log.Logger
	mu       sync.RWMutex
	level    Level
}

// New creates a Logger that writes to stderr at the given minimum level.
// log.Ldate|log.Ltime|log.Lmicroseconds gives millisecond-resolution
// timestamps which are sufficient for diagnosing latency problems in
// high-concurrency workloads.
func New(level Level) *Logger {
	flags := log.Ldate | log.Ltime | log.Lmicroseconds
	return &Logger{
		infoLog:  log.New(os.Stderr, "INFO  ", flags),
		errorLog: log.New(os.Stderr, "ERROR ", flags),
		debugLog: log.New(os.Stderr, "DEBUG ", flags),
		level:    level,
	}
}

// SetLevel changes the minimum log level at runtime.  Safe for concurrent use.
func (l *Logger) SetLevel(level Level) {
	l.mu.Lock()
	l.level = level
	l.mu.Unlock()
}

// Info logs a message at INFO level.
func (l *Logger) Info(msg string) {
	l.mu.RLock()
	lvl := l.level
	l.mu.RUnlock()
	if lvl <= LevelInfo {
		l.infoLog.Output(2, msg) //nolint:errcheck
	}
}

// Infof logs a formatted message at INFO level.
func (l *Logger) Infof(format string, args ...interface{}) {
	l.Info(fmt.Sprintf(format, args...))
}

// Error logs a message at ERROR level.
func (l *Logger) Error(msg string) {
	l.mu.RLock()
	lvl := l.level
	l.mu.RUnlock()
	if lvl <= LevelError {
		l.errorLog.Output(2, msg) //nolint:errcheck
	}
}

// Errorf logs a formatted message at ERROR level.
func (l *Logger) Errorf(format string, args ...interface{}) {
	l.Error(fmt.Sprintf(format, args...))
}

// Debug logs a message at DEBUG level.
func (l *Logger) Debug(msg string) {
	l.mu.RLock()
	lvl := l.level
	l.mu.RUnlock()
	if lvl <= LevelDebug {
		l.debugLog.Output(2, msg) //nolint:errcheck
	}
}

// Debugf logs a formatted message at DEBUG level.
func (l *Logger) Debugf(format string, args ...interface{}) {
	l.Debug(fmt.Sprintf(format, args...))
}
