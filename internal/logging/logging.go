// Package logging provides Aether's structured logger — a thin, dependency-free
// wrapper over the standard library's log/slog. Logs are written to stderr (so
// they never pollute a command's stdout output, including --json results) and
// default to WARN, keeping the CLI quiet unless the operator opts in.
//
// Configuration (environment):
//
//	AETHER_LOG_LEVEL   debug | info | warn (default) | error
//	AETHER_LOG_FORMAT  json (default) | text
package logging

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
)

var loggerPtr atomic.Pointer[slog.Logger]

// L returns the process logger, building it from the environment on first use.
func L() *slog.Logger {
	if l := loggerPtr.Load(); l != nil {
		return l
	}
	built := build()
	loggerPtr.CompareAndSwap(nil, built)
	return loggerPtr.Load()
}

// Use overrides the process logger (primarily for tests and embedders).
func Use(l *slog.Logger) { loggerPtr.Store(l) }

func build() *slog.Logger {
	opts := &slog.HandlerOptions{Level: parseLevel(os.Getenv("AETHER_LOG_LEVEL"))}
	var h slog.Handler
	if strings.EqualFold(os.Getenv("AETHER_LOG_FORMAT"), "text") {
		h = slog.NewTextHandler(os.Stderr, opts)
	} else {
		h = slog.NewJSONHandler(os.Stderr, opts)
	}
	return slog.New(h)
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "error":
		return slog.LevelError
	default: // "warn", "warning", "", and anything unrecognised
		return slog.LevelWarn
	}
}

// ─────────────────────────────────────────────
// Execution / trace ID propagation
// ─────────────────────────────────────────────

type ctxKey struct{}

// WithExecutionID attaches a trace/execution ID to ctx so it can be logged
// consistently across a request's lifecycle.
func WithExecutionID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// ExecutionID returns the execution ID attached to ctx, or "" if none.
func ExecutionID(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKey{}).(string); ok {
		return v
	}
	return ""
}
