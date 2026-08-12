package router

import (
	"testing"

	"github.com/Orbis-Mind/aether/internal/classifier"
	"github.com/Orbis-Mind/aether/internal/core"
	"github.com/Orbis-Mind/aether/internal/models"
)

func mkAnalysis() classifier.TaskAnalysis {
	return classifier.TaskAnalysis{Type: models.TaskGeneral, Difficulty: 0.4, ContextSize: 100}
}

func TestPipelineCheapestPicksLowestCost(t *testing.T) {
	pol := core.DefaultPolicy()
	pol.MaxCostPerRun = 1.0
	dec, err := NewPipeline().Route(mkAnalysis(), pol, core.StrategyCheapest, 100, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	// No eligible model may be cheaper than the chosen one.
	chosenCost := dec.ChosenCand.EstCost
	for _, c := range dec.Candidates {
		if c.Eligible && c.EstCost < chosenCost {
			t.Errorf("cheapest chose %s ($%.5f) but %s is cheaper ($%.5f)", dec.Chosen, chosenCost, c.Key, c.EstCost)
		}
	}
}

func TestPipelineBestPicksHighestQuality(t *testing.T) {
	pol := core.DefaultPolicy()
	pol.MaxCostPerRun = 1.0
	dec, err := NewPipeline().Route(mkAnalysis(), pol, core.StrategyBest, 100, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range dec.Candidates {
		if c.Eligible && c.Quality.Score > dec.ChosenCand.Quality.Score {
			t.Errorf("best chose quality %.2f but %s has %.2f", dec.ChosenCand.Quality.Score, c.Key, c.Quality.Score)
		}
	}
}

func TestPipelineLocalStrategyIsLocalOnly(t *testing.T) {
	pol := core.DefaultPolicy()
	dec, err := NewPipeline().Route(mkAnalysis(), pol, core.StrategyLocal, 100, 100, nil)
	if err != nil {
		t.Fatalf("local strategy should find a local model: %v", err)
	}
	prof, _ := models.Get(dec.Chosen)
	if !prof.Local() {
		t.Errorf("local strategy chose non-local model %s", dec.Chosen)
	}
}

func TestPipelineAvailabilityFiltersUninstalledLocal(t *testing.T) {
	pol := core.DefaultPolicy()
	// Only qwen2.5 is "installed"; local strategy must pick it.
	avail := map[string]bool{"qwen2.5": true}
	dec, err := NewPipeline().Route(mkAnalysis(), pol, core.StrategyLocal, 100, 100, avail)
	if err != nil {
		t.Fatal(err)
	}
	if dec.Chosen != "qwen2.5" {
		t.Errorf("expected only-installed local model qwen2.5, got %s", dec.Chosen)
	}
}

func TestPipelineNoEligibleReturnsError(t *testing.T) {
	pol := core.DefaultPolicy()
	pol.MaxCostPerRun = 0.0000001
	pol.QualityMinimum = 0.99
	// local disabled by default, cloud too expensive/low quality → none.
	_, err := NewPipeline().Route(mkAnalysis(), pol, core.StrategyBest, 5000, 5000, nil)
	if err == nil {
		t.Error("expected error when no model satisfies policy")
	}
}
