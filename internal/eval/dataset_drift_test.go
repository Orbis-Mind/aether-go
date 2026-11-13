package eval

import (
	"os"
	"testing"
)

// TestEmbeddedDatasetMatchesPublicCopy guards against the two copies of
// aether-basic-v1.json drifting apart. The canonical dataset is embedded in the
// binary (internal/eval/aether-basic-v1.json); the repo also ships a
// user-facing copy under evaluation/datasets/ that the README points at. They
// must stay byte-identical so there is effectively one source of truth.
func TestEmbeddedDatasetMatchesPublicCopy(t *testing.T) {
	embedded, err := os.ReadFile("aether-basic-v1.json")
	if err != nil {
		t.Fatalf("read embedded dataset: %v", err)
	}
	public, err := os.ReadFile("../../evaluation/datasets/aether-basic-v1.json")
	if err != nil {
		t.Fatalf("read public dataset: %v", err)
	}
	if string(embedded) != string(public) {
		t.Error("internal/eval/aether-basic-v1.json and evaluation/datasets/aether-basic-v1.json have drifted; keep them identical")
	}
}
