package models

// Pricing captures per-token cost. Values are per 1,000 tokens to match the
// existing core catalogue and CostEstimate. Local models are $0.
type Pricing struct {
	InputPer1K  float64 `json:"input_per_1k"`
	OutputPer1K float64 `json:"output_per_1k"`
}

// CostEstimate returns the estimated USD cost of a run at the given token counts.
func (p Pricing) CostEstimate(inputTokens, outputTokens int) float64 {
	return (float64(inputTokens)/1000)*p.InputPer1K +
		(float64(outputTokens)/1000)*p.OutputPer1K
}

// Free reports whether the model costs nothing to run.
func (p Pricing) Free() bool { return p.InputPer1K == 0 && p.OutputPer1K == 0 }
