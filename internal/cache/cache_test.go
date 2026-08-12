package cache

import "testing"

func TestCacheableGuards(t *testing.T) {
	cases := []struct {
		prompt string
		want   bool
	}{
		{"Explain how binary search works", true},
		{"What is the weather today?", false},  // time-sensitive
		{"What is my account balance?", false}, // personalized
		{"Store my password securely", false},  // sensitive
		{"Summarize this paragraph about physics", true},
		{"What is the latest news?", false}, // time-sensitive
	}
	for _, tc := range cases {
		if got := Cacheable(tc.prompt); got != tc.want {
			t.Errorf("Cacheable(%q) = %v, want %v", tc.prompt, got, tc.want)
		}
	}
}

func TestSemanticCacheRejectsUnrelatedPrompts(t *testing.T) {
	c := NewSemanticCache(semanticThreshold, "")
	c.Set("what is the capital of france", "Paris", 3600)

	// A totally unrelated prompt must NOT return the cached answer, even if the
	// toy embedding scores it similar — the lexical-overlap guard blocks it.
	if _, ok := c.Get("explain quantum entanglement in physics"); ok {
		t.Error("semantic cache returned a false-positive hit for an unrelated prompt")
	}
}

func TestSemanticCacheAllowsLegitimateHit(t *testing.T) {
	c := NewSemanticCache(semanticThreshold, "")
	c.Set("what is the capital of france", "Paris", 3600)
	// The same prompt must still hit — the safety guards block false positives,
	// not legitimate reuse. (cosine = 1.0, lexical overlap = 1.0)
	if v, ok := c.Get("what is the capital of france"); !ok || v != "Paris" {
		t.Errorf("semantic cache should hit for identical prompt, got ok=%v v=%q", ok, v)
	}
}

func TestJaccardWords(t *testing.T) {
	if j := jaccardWords("a b c", "a b c"); j != 1.0 {
		t.Errorf("identical sets should be 1.0, got %f", j)
	}
	if j := jaccardWords("a b c", "x y z"); j != 0 {
		t.Errorf("disjoint sets should be 0, got %f", j)
	}
	if j := jaccardWords("anything", ""); j != 0 {
		t.Errorf("empty stored prompt should be 0, got %f", j)
	}
}
