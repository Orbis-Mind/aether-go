package runtime

import (
	"context"
	"strings"
	"testing"
)

// TestRunContextCancelledFailsFast verifies a cancelled context aborts the run
// without a successful model call (cancellation propagation).
func TestRunContextCancelledFailsFast(t *testing.T) {
	rt := New(mockSpec(), mockPlan(), t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before running

	res := rt.RunContext(ctx, "hello", nil)
	if res.Success {
		t.Fatal("expected cancelled run to fail")
	}
	if !strings.Contains(res.Error, "context canceled") {
		t.Errorf("expected context cancellation error, got %q", res.Error)
	}
}

// TestRunContextHonoursDeadlineDuringStream checks a cancelled stream surfaces an
// error rather than a bogus success.
func TestRunStreamContextCancelled(t *testing.T) {
	rt := New(mockSpec(), mockPlan(), t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var sb strings.Builder
	res := rt.RunStreamContext(ctx, "hello world this is a longer prompt", nil, &sb)
	if res.Success {
		t.Fatal("expected cancelled stream to fail")
	}
}
