package classifier

import (
	"testing"

	"github.com/Orbis-Mind/aether/internal/models"
)

func TestClassifyTaskTypes(t *testing.T) {
	c := New()
	cases := []struct {
		name string
		user string
		want models.TaskType
	}{
		{"coding", "Write a Go function that reverses a string", models.TaskCoding},
		{"math", "What is 47 * 23?", models.TaskMath},
		{"translation", "Translate to French: good morning", models.TaskTranslation},
		{"classification", "Classify the sentiment as positive or negative", models.TaskClassification},
		{"summarization", "Summarize this article in one sentence", models.TaskSummarization},
		{"extraction", "Extract the email address from this text", models.TaskExtraction},
		{"rag", "Based on the following context, answer the question", models.TaskRAG},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := c.Classify("", tc.user, false).Type
			if got != tc.want {
				t.Errorf("Classify(%q) type = %s, want %s", tc.user, got, tc.want)
			}
		})
	}
}

func TestClassifyDifficultyOrdering(t *testing.T) {
	c := New()
	easy := c.Classify("", "Translate to French: hello", false)
	hard := c.Classify("", "Research and critically evaluate the trade-offs of three architectures in a comprehensive, step by step analysis", false)
	if hard.Difficulty <= easy.Difficulty {
		t.Errorf("expected research prompt harder than translation: hard=%.2f easy=%.2f", hard.Difficulty, easy.Difficulty)
	}
}

func TestClassifyRiskHigh(t *testing.T) {
	c := New()
	a := c.Classify("", "What medication dosage should I take for my diagnosis?", false)
	if a.Risk != RiskHigh {
		t.Errorf("expected high risk for medical prompt, got %s", a.Risk)
	}
}

func TestClassifyToolsAndReasoning(t *testing.T) {
	c := New()
	a := c.Classify("", "Explain why the sky is blue and reason about the physics", false)
	if !a.RequiresReasoning {
		t.Error("expected RequiresReasoning true")
	}
	b := c.Classify("", "Say hi", true)
	if !b.RequiresTools {
		t.Error("expected RequiresTools true when hasTools=true")
	}
}

func TestComplexityMapping(t *testing.T) {
	c := New()
	a := c.Classify("", "count the words", false)
	if a.Complexity() == "" {
		t.Error("complexity should map to a non-empty bucket")
	}
}
