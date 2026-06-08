package observability

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Orbis-Mind/aether/internal/core"
)

// ─────────────────────────────────────────────
// Run Record
// ─────────────────────────────────────────────

type RunRecord struct {
	RunID        string  `json:"run_id"`
	PlanID       string  `json:"plan_id"`
	ModelUsed    string  `json:"model_used"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	Cost         float64 `json:"cost"`
	LatencyMS    float64 `json:"latency_ms"`
	Success      bool    `json:"success"`
	Cached       bool    `json:"cached"`
	Retries      int     `json:"retries"`
	Error        string  `json:"error,omitempty"`
	CreatedAt    string  `json:"created_at"`
}

// ─────────────────────────────────────────────
// Store
// ─────────────────────────────────────────────

type Store struct {
	records []RunRecord
	path    string
}

func NewStore(dataDir string) *Store {
	os.MkdirAll(dataDir, 0755)
	s := &Store{path: filepath.Join(dataDir, "runs.json")}
	s.load()
	return s
}

func (s *Store) Record(result *core.ExecutionResult) {
	s.records = append(s.records, RunRecord{
		RunID:        result.RunID,
		PlanID:       result.PlanID,
		ModelUsed:    result.ModelUsed,
		InputTokens:  result.InputTokens,
		OutputTokens: result.OutputTokens,
		Cost:         result.Cost,
		LatencyMS:    result.LatencyMS,
		Success:      result.Success,
		Cached:       result.Cached,
		Retries:      result.Retries,
		Error:        result.Error,
		CreatedAt:    result.CreatedAt,
	})
	s.save()
}

// ─────────────────────────────────────────────
// Analytics
// ─────────────────────────────────────────────

func (s *Store) Summary() map[string]interface{} {
	if len(s.records) == 0 {
		return map[string]interface{}{"message": "No runs recorded yet."}
	}

	total := len(s.records)
	successful := 0
	cached := 0
	totalCost := 0.0
	totalTokens := 0
	totalLatency := 0.0
	modelCounts := make(map[string]int)
	modelCosts := make(map[string]float64)
	errorCount := 0

	for _, r := range s.records {
		if r.Success {
			successful++
		}
		if r.Cached {
			cached++
		}
		if r.Error != "" {
			errorCount++
		}
		totalCost += r.Cost
		totalTokens += r.InputTokens + r.OutputTokens
		totalLatency += r.LatencyMS
		modelCounts[r.ModelUsed]++
		modelCosts[r.ModelUsed] += r.Cost
	}

	return map[string]interface{}{
		"total_runs":        total,
		"successful_runs":   successful,
		"success_rate":      fmt.Sprintf("%.1f%%", 100*float64(successful)/float64(total)),
		"cached_runs":       cached,
		"cache_hit_rate":    fmt.Sprintf("%.1f%%", 100*float64(cached)/float64(total)),
		"total_cost_usd":    totalCost,
		"avg_cost_usd":      totalCost / float64(total),
		"total_tokens":      totalTokens,
		"avg_latency_ms":    totalLatency / float64(total),
		"total_errors":      errorCount,
		"model_distribution": modelCounts,
		"cost_by_model":     modelCosts,
	}
}

func (s *Store) RecentRuns(limit int) []RunRecord {
	if limit > len(s.records) {
		limit = len(s.records)
	}
	recent := s.records[len(s.records)-limit:]
	// reverse
	result := make([]RunRecord, len(recent))
	for i, r := range recent {
		result[len(recent)-1-i] = r
	}
	return result
}

func (s *Store) LatencyPercentiles() map[string]float64 {
	if len(s.records) == 0 {
		return nil
	}
	latencies := make([]float64, len(s.records))
	for i, r := range s.records {
		latencies[i] = r.LatencyMS
	}
	sort.Float64s(latencies)
	n := len(latencies)
	return map[string]float64{
		"p50": latencies[int(float64(n)*0.50)],
		"p90": latencies[int(float64(n)*0.90)],
		"p99": latencies[minInt(int(float64(n)*0.99), n-1)],
		"max": latencies[n-1],
	}
}

// ─────────────────────────────────────────────
// Report Renderer
// ─────────────────────────────────────────────

func RenderReport(s *Store) string {
	summary := s.Summary()
	if msg, ok := summary["message"]; ok {
		return fmt.Sprintf("%v", msg)
	}

	var sb strings.Builder
	sb.WriteString("╔══════════════════════════════════════════════╗\n")
	sb.WriteString("║        Aether Observability Report           ║\n")
	sb.WriteString("╚══════════════════════════════════════════════╝\n\n")

	sb.WriteString(fmt.Sprintf("  Total Runs       : %v\n", summary["total_runs"]))
	sb.WriteString(fmt.Sprintf("  Success Rate     : %v\n", summary["success_rate"]))
	sb.WriteString(fmt.Sprintf("  Cache Hit Rate   : %v\n", summary["cache_hit_rate"]))
	sb.WriteString(fmt.Sprintf("  Total Cost       : $%.6f\n", summary["total_cost_usd"]))
	sb.WriteString(fmt.Sprintf("  Avg Cost / Run   : $%.6f\n", summary["avg_cost_usd"]))
	sb.WriteString(fmt.Sprintf("  Total Tokens     : %v\n", summary["total_tokens"]))
	sb.WriteString(fmt.Sprintf("  Avg Latency      : %.0fms\n", summary["avg_latency_ms"]))
	sb.WriteString(fmt.Sprintf("  Total Errors     : %v\n", summary["total_errors"]))
	sb.WriteString("\n  Model Distribution:\n")

	if dist, ok := summary["model_distribution"].(map[string]int); ok {
		costs := summary["cost_by_model"].(map[string]float64)
		for model, count := range dist {
			cost := costs[model]
			sb.WriteString(fmt.Sprintf("    %-30s runs=%4d  cost=$%.5f\n", model, count, cost))
		}
	}

	perc := s.LatencyPercentiles()
	if perc != nil {
		sb.WriteString("\n  Latency Percentiles:\n")
		sb.WriteString(fmt.Sprintf("    p50=%.0fms  p90=%.0fms  p99=%.0fms  max=%.0fms\n",
			perc["p50"], perc["p90"], perc["p99"], perc["max"]))
	}

	return sb.String()
}

func (s *Store) save() {
	data, _ := json.MarshalIndent(s.records, "", "  ")
	os.WriteFile(s.path, data, 0644)
}

func (s *Store) load() {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	json.Unmarshal(data, &s.records)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
