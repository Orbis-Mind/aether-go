package eval

import (
	"regexp"
	"strings"
)

// Score is the result of scoring one model output against one item.
type Score struct {
	// Value in 0..1. Only meaningful when Scorable is true.
	Value float64
	// Scorable is false when the item had no deterministic expectation, so no
	// honest quality number can be produced (we report "unknown", not a guess).
	Scorable bool
}

// ScoreItem scores a model's output against an item's expectations using
// deterministic, offline heuristics:
//   - expect_keywords: fraction of keywords present (case-insensitive substring).
//   - expect_regex: 1.0 if it matches, else 0.0.
//
// When both are present the two are averaged. This is a heuristic proxy for
// quality, clearly labelled as such — it is not a human judgment or an
// LLM-as-judge score.
func ScoreItem(it Item, output string) Score {
	if !it.Scorable() {
		return Score{Scorable: false}
	}
	lower := strings.ToLower(output)
	var parts []float64

	if len(it.ExpectKeywords) > 0 {
		hits := 0
		for _, kw := range it.ExpectKeywords {
			if strings.Contains(lower, strings.ToLower(kw)) {
				hits++
			}
		}
		parts = append(parts, float64(hits)/float64(len(it.ExpectKeywords)))
	}
	if it.ExpectRegex != "" {
		re, err := regexp.Compile(it.ExpectRegex)
		if err == nil && re.MatchString(output) {
			parts = append(parts, 1.0)
		} else {
			parts = append(parts, 0.0)
		}
	}

	sum := 0.0
	for _, p := range parts {
		sum += p
	}
	return Score{Value: sum / float64(len(parts)), Scorable: true}
}
