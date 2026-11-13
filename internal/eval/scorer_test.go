package eval

import "testing"

func TestScoreExpectAnswerNumeric(t *testing.T) {
	it := Item{ExpectAnswer: "18"}
	cases := map[string]float64{
		"After working it out, the answer is 18.":     1,
		"... so we get 18 eggs in total.":             1, // last number
		"The answer is 1,800":                         0, // wrong (comma-stripped 1800)
		"Step 1: 16-3=13. Step 2: 13-4=9. Answer: 42": 0,
		"no numbers here":                             0,
	}
	for out, want := range cases {
		if got := ScoreItem(it, out); got.Value != want {
			t.Errorf("ScoreItem(%q).Value = %.1f, want %.1f", out, got.Value, want)
		}
	}
	if !ScoreItem(it, "x").Scorable {
		t.Error("expect_answer item must be scorable")
	}
}

func TestFinalNumberExtraction(t *testing.T) {
	cases := map[string]float64{
		"The answer is 42":                    42,
		"total: $1,234.50":                    1234.50,
		"first 10 then 20, the answer is: 30": 30,
	}
	for in, want := range cases {
		got, ok := finalNumber(in)
		if !ok || got != want {
			t.Errorf("finalNumber(%q) = %v,%v want %v", in, got, ok, want)
		}
	}
}
