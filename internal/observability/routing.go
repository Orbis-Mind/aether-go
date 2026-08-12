package observability

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// RoutingRecord is one row of the routing dataset described in the architecture:
// the features a router saw and the outcome it got. Appended as JSON Lines so a
// future learned router can train on real local runs — no external service.
type RoutingRecord struct {
	CreatedAt    string  `json:"created_at"`
	TaskType     string  `json:"task_type"`
	Difficulty   float64 `json:"difficulty"`
	Risk         string  `json:"risk"`
	Strategy     string  `json:"strategy"`
	Model        string  `json:"model"`
	InputTokens  int     `json:"input_tokens"`
	OutTokens    int     `json:"output_tokens"`
	LatencyMS    float64 `json:"latency_ms"`
	Cost         float64 `json:"cost"`
	Success      bool    `json:"success"`
	UsedFallback bool    `json:"used_fallback"`
	Cached       bool    `json:"cached"`
}

// AppendRouting appends a routing record to <dir>/routing.jsonl. Errors are
// returned so callers can decide whether to surface them; capture is best-effort
// and never blocks a run.
func AppendRouting(dir string, rec RoutingRecord) error {
	if rec.CreatedAt == "" {
		rec.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "routing.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	return err
}
