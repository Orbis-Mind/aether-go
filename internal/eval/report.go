package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Orbis-Mind/aether/internal/models"
)

// Render produces a human-readable evaluation report. It is careful never to
// claim a global "best model" — only the best measured model for THIS dataset.
func Render(rep *Report) string {
	var sb strings.Builder
	sb.WriteString("AETHER MODEL EVALUATION\n")
	sb.WriteString(strings.Repeat("=", 64) + "\n")
	sb.WriteString(fmt.Sprintf("Dataset: %s\n", rep.Dataset))
	if len(rep.Results) > 0 {
		sb.WriteString(fmt.Sprintf("Tasks:   %d\n\n", rep.Results[0].Total))
	}

	sb.WriteString(fmt.Sprintf("%-16s %-9s %8s %10s %8s\n", "Model", "Quality", "P95", "Success", "Tokens"))
	sb.WriteString(strings.Repeat("-", 64) + "\n")

	for _, r := range rep.Results {
		q, ok := r.Quality()
		qStr := "unknown"
		if ok {
			qStr = fmt.Sprintf("%.1f%%", q*100)
		}
		tokProv := "est"
		if r.AllTokensMeasured && r.Succeeded > 0 {
			tokProv = "meas"
		}
		sb.WriteString(fmt.Sprintf("%-16s %-9s %6.0fms %8.0f%% %5d/%s\n",
			r.Model, qStr, r.P95LatencyMS(), r.SuccessRate()*100,
			r.InTokens+r.OutTokens, tokProv))
		if r.Succeeded == 0 && r.FirstError != "" {
			sb.WriteString(fmt.Sprintf("    ! all runs failed: %s\n", truncate(r.FirstError, 70)))
		}
	}

	best := recommend(rep)
	sb.WriteString(strings.Repeat("-", 64) + "\n")
	if best == "" {
		sb.WriteString("Recommendation: none — no model produced scorable, successful runs.\n")
		sb.WriteString("(Is Ollama running with a model pulled? Try `aether doctor`.)\n")
	} else {
		sb.WriteString("Recommendation:\n  " + best + "\n\n")
		sb.WriteString("Reason:\n  Best MEASURED quality on this dataset. Not a claim about any\n")
		sb.WriteString("  other workload — measure your own prompts to route on your data.\n")
	}
	return sb.String()
}

// recommend returns the model with the highest measured quality that also had a
// perfect-ish success rate, or "" when none qualify.
func recommend(rep *Report) string {
	best := ""
	bestQ := -1.0
	for _, r := range rep.Results {
		q, ok := r.Quality()
		if !ok || r.SuccessRate() < 0.5 {
			continue
		}
		if q > bestQ {
			bestQ = q
			best = r.Model
		}
	}
	return best
}

// WriteMeasured persists measured per-(model,task) quality to
// <dir>/benchmarks.json for the model registry to overlay. It MERGES with any
// existing records (newer wins per model+task).
func WriteMeasured(dir string, rep *Report) (int, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return 0, err
	}
	path := filepath.Join(dir, "benchmarks.json")

	existing := map[string]models.MeasuredRecord{}
	if data, err := os.ReadFile(path); err == nil {
		var recs []models.MeasuredRecord
		if json.Unmarshal(data, &recs) == nil {
			for _, r := range recs {
				existing[r.Model+"|"+string(r.Task)] = r
			}
		}
	}
	for _, r := range rep.MeasuredRecords() {
		existing[r.Model+"|"+string(r.Task)] = r
	}

	merged := make([]models.MeasuredRecord, 0, len(existing))
	for _, r := range existing {
		merged = append(merged, r)
	}
	sort.Slice(merged, func(i, j int) bool {
		if merged[i].Model != merged[j].Model {
			return merged[i].Model < merged[j].Model
		}
		return merged[i].Task < merged[j].Task
	})
	data, _ := json.MarshalIndent(merged, "", "  ")
	if err := os.WriteFile(path, data, 0644); err != nil {
		return 0, err
	}
	return len(rep.MeasuredRecords()), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
