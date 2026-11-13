package observability

import (
	"sync"
	"testing"

	"github.com/Orbis-Mind/aether/internal/core"
)

// TestStoreConcurrentRecord exercises concurrent Record/Summary/RecentRuns under
// -race (the store previously mutated its slice with no lock).
func TestStoreConcurrentRecord(t *testing.T) {
	s := NewStore(t.TempDir())
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			res := core.NewExecutionResult("plan")
			res.Success = true
			res.LatencyMS = float64(n)
			res.ModelUsed = "mock"
			s.Record(&res)
			s.Summary()
			s.RecentRuns(5)
			s.LatencyPercentiles()
		}(i)
	}
	wg.Wait()
	if got := s.RecentRuns(100); len(got) != 50 {
		t.Errorf("recorded %d runs, want 50", len(got))
	}
}
