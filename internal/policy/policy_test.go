package policy

import (
	"testing"

	"github.com/Orbis-Mind/aether/internal/classifier"
	"github.com/Orbis-Mind/aether/internal/core"
	"github.com/Orbis-Mind/aether/internal/models"
)

func analysis(diff float64, tools bool) classifier.TaskAnalysis {
	return classifier.TaskAnalysis{Type: models.TaskGeneral, Difficulty: diff, RequiresTools: tools, ContextSize: 100}
}

func TestLocalOnlyExcludesCloud(t *testing.T) {
	e := New()
	pol := core.DefaultPolicy()
	pol.Privacy = core.PrivacyLocal
	req := Resolve(pol, analysis(0.3, false), 100, 100)
	cands := e.Evaluate(pol, models.TaskGeneral, req)

	for _, c := range Eligible(cands) {
		if !c.Profile.Local() {
			t.Errorf("local-only policy allowed cloud model %s", c.Key)
		}
	}
	// And at least one local model should be eligible (they're enabled here).
	if len(Eligible(cands)) == 0 {
		t.Error("expected at least one eligible local model under local-only policy")
	}
}

func TestBudgetZeroForcesFree(t *testing.T) {
	e := New()
	pol := core.DefaultPolicy()
	pol.MaxCostPerRun = 0
	pol.LocalFirst = true // enable local models
	req := Resolve(pol, analysis(0.3, false), 100, 100)
	for _, c := range Eligible(e.Evaluate(pol, models.TaskGeneral, req)) {
		if c.EstCost > 0 {
			t.Errorf("zero budget allowed paid model %s at cost %f", c.Key, c.EstCost)
		}
	}
}

func TestLocalOptInByDefault(t *testing.T) {
	e := New()
	pol := core.DefaultPolicy() // no local signal
	req := Resolve(pol, analysis(0.3, false), 100, 100)
	for _, c := range Eligible(e.Evaluate(pol, models.TaskGeneral, req)) {
		if c.Profile.Local() {
			t.Errorf("local model %s should be opt-in but was eligible by default", c.Key)
		}
	}
}

func TestQualityMinimumRaisesFloor(t *testing.T) {
	pol := core.DefaultPolicy()
	pol.QualityMinimum = 0.99
	req := Resolve(pol, analysis(0.1, false), 100, 100)
	if req.MinQuality < 0.99 {
		t.Errorf("expected min quality >= 0.99, got %.2f", req.MinQuality)
	}
}

func TestToolRequirementFiltersToollessModels(t *testing.T) {
	e := New()
	pol := core.DefaultPolicy()
	req := Resolve(pol, analysis(0.3, true), 100, 100)
	req.NeedTools = true
	for _, c := range Eligible(e.Evaluate(pol, models.TaskGeneral, req)) {
		if !c.Profile.Capabilities.SupportsTools {
			t.Errorf("tool-requiring task kept toolless model %s", c.Key)
		}
	}
}
