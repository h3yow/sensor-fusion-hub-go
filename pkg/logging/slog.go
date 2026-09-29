// Package logging provides a small slog-backed implementation of
// sensor.Logger with deterministic output.
package logging

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"time"

	"io"
)

// Logger wraps an slog.Handler and serializes writes.
type Logger struct {
	h  slog.Handler
	mu *sync.Mutex
}

// New returns a text logger writing to stderr (or the given writer).
func New(w io.Writer) *Logger {
	if w == nil {
		w = os.Stderr
	}
	return &Logger{
		h:  slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}),
		mu: &sync.Mutex{},
	}
}

func (l *Logger) log(level slog.Level, msg string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	r := slog.NewRecord(time.Time{}, level, msg, 0)
	r.Add(args...)
	_ = l.h.Handle(context.Background(), r)
}

// Info logs at info level.
func (l *Logger) Info(msg string, args ...any) { l.log(slog.LevelInfo, msg, args...) }

// Warn logs at warn level.
func (l *Logger) Warn(msg string, args ...any) { l.log(slog.LevelWarn, msg, args...) }

// Error logs at error level.
func (l *Logger) Error(msg string, args ...any) { l.log(slog.LevelError, msg, args...) }

// InputTimeline logs an ingest decision with a stable marker
// ("event=input_timeline") so test logs show the full input timeline
// (id/seq/epoch/timestamps) and the deterministic verdict basis.
func (l *Logger) InputTimeline(msg string, args ...any) {
	full := make([]any, 0, len(args)+2)
	full = append(full, "event", "input_timeline")
	full = append(full, args...)
	l.log(slog.LevelInfo, msg, full...)
}
