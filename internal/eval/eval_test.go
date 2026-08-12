package eval

import (
	"context"
	"testing"

	"github.com/Orbis-Mind/aether/internal/provider"
)

func TestDefaultDatasetLoads(t *testing.T) {
	ds, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if len(ds.Items) < 20 {
		t.Errorf("expected a substantial dataset, got %d items", len(ds.Items))
	}
	seen := map[string]bool{}
	for _, it := range ds.Items {
		if it.ID == "" || it.Prompt == "" {
			t.Errorf("item missing id/prompt: %+v", it)
		}
		if seen[it.ID] {
			t.Errorf("duplicate item id %s", it.ID)
		}
		seen[it.ID] = true
	}
}

func TestScorerKeywords(t *testing.T) {
	it := Item{ExpectKeywords: []string{"paris", "france"}}
	if s := ScoreItem(it, "The capital is Paris in France."); !s.Scorable || s.Value != 1.0 {
		t.Errorf("expected full score, got %+v", s)
	}
	if s := ScoreItem(it, "The capital is Paris."); s.Value != 0.5 {
		t.Errorf("expected 0.5 partial score, got %f", s.Value)
	}
}

func TestScorerUnscorable(t *testing.T) {
	if s := ScoreItem(Item{}, "anything"); s.Scorable {
		t.Error("item with no expectations must be unscorable, not fabricated")
	}
}

func TestScorerRegex(t *testing.T) {
	it := Item{ExpectRegex: "\\d{4}-\\d{2}-\\d{2}"}
	if s := ScoreItem(it, "The date is 2026-03-14."); s.Value != 1.0 {
		t.Errorf("expected regex match score 1.0, got %f", s.Value)
	}
}

func TestRunnerAgainstMockProducesReport(t *testing.T) {
	ds, _ := Default()
	reg := provider.NewRegistry()
	rep := Run(context.Background(), ds, []string{"mock"}, reg, Options{MaxTokens: 32})
	if len(rep.Results) != 1 {
		t.Fatalf("expected 1 model result, got %d", len(rep.Results))
	}
	r := rep.Results[0]
	if r.Total != len(ds.Items) {
		t.Errorf("expected %d total, got %d", len(ds.Items), r.Total)
	}
	if r.SuccessRate() != 1.0 {
		t.Errorf("mock should succeed on all items, got rate %.2f", r.SuccessRate())
	}
}
