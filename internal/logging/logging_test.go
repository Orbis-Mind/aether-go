package logging

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"debug": slog.LevelDebug,
		"INFO":  slog.LevelInfo,
		"error": slog.LevelError,
		"warn":  slog.LevelWarn,
		"":      slog.LevelWarn,
		"bogus": slog.LevelWarn,
	}
	for in, want := range cases {
		if got := parseLevel(in); got != want {
			t.Errorf("parseLevel(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestLNeverNil(t *testing.T) {
	if L() == nil {
		t.Fatal("L() returned nil")
	}
}

func TestUseOverridesLogger(t *testing.T) {
	var buf bytes.Buffer
	Use(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	defer Use(build())

	L().Info("hello", "k", "v")
	if !strings.Contains(buf.String(), "hello") {
		t.Errorf("override logger did not capture message: %q", buf.String())
	}
}

func TestExecutionIDRoundTrip(t *testing.T) {
	ctx := WithExecutionID(context.Background(), "trace-xyz")
	if got := ExecutionID(ctx); got != "trace-xyz" {
		t.Errorf("ExecutionID = %q, want trace-xyz", got)
	}
	if got := ExecutionID(context.Background()); got != "" {
		t.Errorf("ExecutionID on bare ctx = %q, want empty", got)
	}
}
