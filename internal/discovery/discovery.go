// Package discovery detects which local models are actually installed, so the
// router never routes to a local model the user hasn't pulled. It queries the
// live Ollama daemon — no fabricated availability.
package discovery

import (
	"context"
	"strings"

	"github.com/Orbis-Mind/aether/internal/core"
	"github.com/Orbis-Mind/aether/internal/provider"
)

// Local pairs an installed Ollama model with the Aether catalogue key it maps
// to (empty CatalogueKey when the installed model isn't in the catalogue).
type Local struct {
	Installed    provider.LocalModel
	CatalogueKey string
}

// baseName strips an Ollama tag suffix ("llama3.2:latest" -> "llama3.2").
func baseName(name string) string {
	if i := strings.IndexByte(name, ':'); i >= 0 {
		return name[:i]
	}
	return name
}

// matchKey finds the catalogue key for an installed model name, matching on the
// tag-stripped base so "llama3.2:latest" matches catalogue ID "llama3.2".
func matchKey(installedName string) string {
	inBase := baseName(installedName)
	for key, spec := range core.ModelCatalogue {
		if spec.Provider != core.ProviderOllama {
			continue
		}
		if spec.ID == installedName || baseName(spec.ID) == inBase {
			return key
		}
	}
	return ""
}

// LocalModels lists installed Ollama models, each annotated with its catalogue
// key when known. A daemon-unreachable error is returned verbatim so callers
// can guide the user (never silently pretend there are none).
func LocalModels(ctx context.Context) ([]Local, error) {
	op := provider.NewOllamaProvider("")
	installed, err := op.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Local, 0, len(installed))
	for _, m := range installed {
		out = append(out, Local{Installed: m, CatalogueKey: matchKey(m.Name)})
	}
	return out, nil
}

// AvailableCatalogueKeys returns the set of catalogue keys that are installed
// locally right now. When Ollama is unreachable it returns (nil, err); callers
// treat nil as "availability unknown" and skip availability filtering.
func AvailableCatalogueKeys(ctx context.Context) (map[string]bool, error) {
	locals, err := LocalModels(ctx)
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, l := range locals {
		if l.CatalogueKey != "" {
			set[l.CatalogueKey] = true
		}
	}
	return set, nil
}
