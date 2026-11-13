package runtime

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/Orbis-Mind/aether/internal/logging"
)

// TestRunEmitsTracedLifecycleLog verifies the runtime emits a structured
// completion log carrying the caller-supplied execution/trace ID, and that the
// ID is recorded on the result for request tracing.
func TestRunEmitsTracedLifecycleLog(t *testing.T) {
	var buf bytes.Buffer
	logging.Use(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer logging.Use(slog.New(slog.NewJSONHandler(io.Discard, nil)))

	rt := New(mockSpec(), mockPlan(), t.TempDir())
	ctx := logging.WithExecutionID(context.Background(), "trace-123")
	res := rt.RunContext(ctx, "hello", nil)

	if !res.Success {
		t.Fatalf("run failed: %s", res.Error)
	}
	logs := buf.String()
	if !strings.Contains(logs, "run.complete") {
		t.Errorf("expected a run.complete log, got: %s", logs)
	}
	if !strings.Contains(logs, "trace-123") {
		t.Errorf("expected execution_id trace-123 in logs, got: %s", logs)
	}
	if res.Metadata["execution_id"] != "trace-123" {
		t.Errorf("result execution_id = %q, want trace-123", res.Metadata["execution_id"])
	}
}
