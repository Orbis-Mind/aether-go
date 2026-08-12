package cache

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const defaultTTL = 3600

// semanticThreshold is intentionally high: the built-in n-gram embedding is a
// cheap approximation and unrelated prompts can score deceptively high, so a
// cosine hit alone is NOT trusted.
const semanticThreshold = 0.95

// semanticJaccardFloor is a second, independent safeguard (see Section 19 of the
// design): a semantic hit must also share this fraction of word tokens with the
// stored prompt. This prevents "similar-looking" but unrelated prompts from
// returning each other's cached answers.
const semanticJaccardFloor = 0.6

// ─────────────────────────────────────────────
// Cache Entry
// ─────────────────────────────────────────────

type entry struct {
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	Preview   string    `json:"preview"`
	CreatedAt float64   `json:"created_at"`
	TTL       int       `json:"ttl"`
	Hits      int       `json:"hits"`
	Embedding []float64 `json:"embedding,omitempty"`
	// Prompt is the full prompt for semantic entries, kept so a candidate hit can
	// be verified by lexical overlap (not just embedding similarity).
	Prompt string `json:"prompt,omitempty"`
}

func (e *entry) isValid() bool {
	return time.Now().Unix()-int64(e.CreatedAt) < int64(e.TTL)
}

// ─────────────────────────────────────────────
// Exact Cache
// ─────────────────────────────────────────────

type ExactCache struct {
	data map[string]*entry
	path string
}

func NewExactCache(path string) *ExactCache {
	c := &ExactCache{data: make(map[string]*entry), path: path}
	c.load()
	return c
}

func (c *ExactCache) hash(prompt string) string {
	sum := sha256.Sum256([]byte(prompt))
	return fmt.Sprintf("%x", sum)
}

func (c *ExactCache) Get(prompt string) (string, bool) {
	h := c.hash(prompt)
	e, ok := c.data[h]
	if !ok || !e.isValid() {
		delete(c.data, h)
		return "", false
	}
	e.Hits++
	return e.Value, true
}

func (c *ExactCache) Set(prompt, response string, ttl int) {
	h := c.hash(prompt)
	preview := prompt
	if len(preview) > 80 {
		preview = preview[:80]
	}
	c.data[h] = &entry{
		Key:       h,
		Value:     response,
		Preview:   preview,
		CreatedAt: float64(time.Now().Unix()),
		TTL:       ttl,
	}
	c.save()
}

func (c *ExactCache) Stats() map[string]int {
	valid := 0
	hits := 0
	for _, e := range c.data {
		if e.isValid() {
			valid++
			hits += e.Hits
		}
	}
	return map[string]int{"entries": valid, "total_hits": hits}
}

func (c *ExactCache) save() {
	if c.path == "" {
		return
	}
	os.MkdirAll(filepath.Dir(c.path), 0755)
	data, _ := json.MarshalIndent(c.data, "", "  ")
	os.WriteFile(c.path, data, 0644)
}

func (c *ExactCache) load() {
	if c.path == "" {
		return
	}
	data, err := os.ReadFile(c.path)
	if err != nil {
		return
	}
	json.Unmarshal(data, &c.data)
}

// ─────────────────────────────────────────────
// Semantic Cache
// ─────────────────────────────────────────────

type SemanticCache struct {
	entries   []*entry
	threshold float64
	path      string
}

func NewSemanticCache(threshold float64, path string) *SemanticCache {
	c := &SemanticCache{threshold: threshold, path: path}
	c.load()
	return c
}

func (c *SemanticCache) Get(prompt string) (string, bool) {
	// reuse embed from memory package via inline call
	qEmb := embedText(prompt)
	bestSim := 0.0
	var bestEntry *entry
	for _, e := range c.entries {
		if !e.isValid() {
			continue
		}
		if e.Embedding == nil {
			continue
		}
		sim := cosineSim(qEmb, e.Embedding)
		if sim > bestSim {
			bestSim = sim
			bestEntry = e
		}
	}
	// Two independent conditions must BOTH hold to return a semantic hit:
	//   1. embedding cosine >= threshold, and
	//   2. lexical (word-set) overlap >= floor.
	// The toy embedding alone produces false positives on unrelated prompts, so
	// the lexical check is what makes semantic caching safe.
	if bestEntry != nil && bestSim >= c.threshold {
		if jaccardWords(prompt, bestEntry.Prompt) >= semanticJaccardFloor {
			bestEntry.Hits++
			return bestEntry.Value, true
		}
	}
	return "", false
}

func (c *SemanticCache) Set(prompt, response string, ttl int) {
	preview := prompt
	if len(preview) > 80 {
		preview = preview[:80]
	}
	e := &entry{
		Value:     response,
		Preview:   preview,
		CreatedAt: float64(time.Now().Unix()),
		TTL:       ttl,
		Embedding: embedText(prompt),
		Prompt:    prompt,
	}
	c.entries = append(c.entries, e)
	c.save()
}

// jaccardWords returns the Jaccard similarity of the two prompts' lowercase word
// sets (|A∩B| / |A∪B|). Empty stored prompt (legacy entries) yields 0, which
// safely fails the overlap check rather than trusting an unverifiable hit.
func jaccardWords(a, b string) float64 {
	if b == "" {
		return 0
	}
	sa := wordSet(a)
	sb := wordSet(b)
	if len(sa) == 0 || len(sb) == 0 {
		return 0
	}
	inter := 0
	for w := range sa {
		if sb[w] {
			inter++
		}
	}
	union := len(sa) + len(sb) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

func wordSet(s string) map[string]bool {
	set := map[string]bool{}
	for _, w := range splitWords(s) {
		lw := ""
		for _, r := range w {
			if r >= 'A' && r <= 'Z' {
				r = r + ('a' - 'A')
			}
			lw += string(r)
		}
		if lw != "" {
			set[lw] = true
		}
	}
	return set
}

func (c *SemanticCache) Stats() map[string]int {
	valid := 0
	hits := 0
	for _, e := range c.entries {
		if e.isValid() {
			valid++
			hits += e.Hits
		}
	}
	return map[string]int{"entries": valid, "total_hits": hits}
}

func (c *SemanticCache) save() {
	if c.path == "" {
		return
	}
	os.MkdirAll(filepath.Dir(c.path), 0755)
	data, _ := json.MarshalIndent(c.entries, "", "  ")
	os.WriteFile(c.path, data, 0644)
}

func (c *SemanticCache) load() {
	if c.path == "" {
		return
	}
	data, err := os.ReadFile(c.path)
	if err != nil {
		return
	}
	json.Unmarshal(data, &c.entries)
}

// ─────────────────────────────────────────────
// Orchestrator
// ─────────────────────────────────────────────

type Orchestrator struct {
	strategy string
	Exact    *ExactCache
	Semantic *SemanticCache
}

func NewOrchestrator(strategy, dataDir string) *Orchestrator {
	os.MkdirAll(dataDir, 0755)
	return &Orchestrator{
		strategy: strategy,
		Exact:    NewExactCache(filepath.Join(dataDir, "exact.json")),
		Semantic: NewSemanticCache(semanticThreshold, filepath.Join(dataDir, "semantic.json")),
	}
}

func (o *Orchestrator) Lookup(prompt string) (string, bool) {
	if val, ok := o.Exact.Get(prompt); ok {
		return val, true
	}
	if o.strategy == "semantic" {
		if val, ok := o.Semantic.Get(prompt); ok {
			return val, true
		}
	}
	return "", false
}

func (o *Orchestrator) Store(prompt, response string) {
	o.Exact.Set(prompt, response, defaultTTL)
	if o.strategy == "semantic" {
		o.Semantic.Set(prompt, response, defaultTTL)
	}
}

func (o *Orchestrator) Stats() map[string]map[string]int {
	return map[string]map[string]int{
		"exact":    o.Exact.Stats(),
		"semantic": o.Semantic.Stats(),
	}
}

// ─────────────────────────────────────────────
// Internal helpers
// ─────────────────────────────────────────────

func embedText(text string) []float64 {
	// same n-gram hashing as memory package
	const dim = 256
	vec := make([]float64, dim)
	words := splitWords(text)
	for _, w := range words {
		for n := 1; n <= minInt(len(w), 4); n++ {
			ngram := w[:n]
			h := simpleHash(ngram)
			idx := h % dim
			vec[idx] += 1.0
		}
	}
	norm := 0.0
	for _, v := range vec {
		norm += v * v
	}
	if norm > 0 {
		sqrtNorm := sqrtF(norm)
		for i := range vec {
			vec[i] /= sqrtNorm
		}
	}
	return vec
}

func cosineSim(a, b []float64) float64 {
	dot := 0.0
	for i := range a {
		if i < len(b) {
			dot += a[i] * b[i]
		}
	}
	return dot
}

func splitWords(s string) []string {
	var words []string
	word := ""
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' {
			if word != "" {
				words = append(words, word)
				word = ""
			}
		} else {
			word += string(r)
		}
	}
	if word != "" {
		words = append(words, word)
	}
	return words
}

func simpleHash(s string) int {
	h := 0
	for _, c := range s {
		h = h*31 + int(c)
	}
	if h < 0 {
		h = -h
	}
	return h
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func sqrtF(x float64) float64 {
	z := x / 2
	for i := 0; i < 20; i++ {
		z -= (z*z - x) / (2 * z)
	}
	return z
}
