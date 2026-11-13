package eval

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

var (
	answerIsRe = regexp.MustCompile(`(?i)answer is\s*:?\s*(-?\d+(?:\.\d+)?)`)
	numberRe   = regexp.MustCompile(`-?\d+(?:\.\d+)?`)
)

// finalNumber extracts a model's final numeric answer using the standard method:
// the number after the last "answer is ...", else the last number in the text.
// Commas and currency/percent symbols are stripped first.
func finalNumber(text string) (float64, bool) {
	t := strings.NewReplacer(",", "", "$", "", "%", "").Replace(text)
	if m := answerIsRe.FindAllStringSubmatch(t, -1); len(m) > 0 {
		if v, err := strconv.ParseFloat(m[len(m)-1][1], 64); err == nil {
			return v, true
		}
	}
	nums := numberRe.FindAllString(t, -1)
	if len(nums) == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(nums[len(nums)-1], 64)
	return v, err == nil
}

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
	if it.ExpectAnswer != "" {
		want, werr := strconv.ParseFloat(strings.NewReplacer(",", "", "$", "", "%", "").Replace(it.ExpectAnswer), 64)
		got, ok := finalNumber(output)
		if werr == nil && ok && math.Abs(want-got) < 1e-4 {
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
