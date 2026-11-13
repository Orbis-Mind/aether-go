package cache

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Orbis-Mind/aether/internal/logging"
)

const defaultTTL = 3600

// maxCacheEntries bounds each cache so it cannot grow without limit. On overflow
// the cache evicts expired entries first, then the oldest, keeping memory and
// on-disk size bounded.
const maxCacheEntries = 1000

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
	mu   sync.Mutex
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
	c.mu.Lock()
	defer c.mu.Unlock()
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
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[h] = &entry{
		Key:       h,
		Value:     response,
		Preview:   preview,
		CreatedAt: float64(time.Now().Unix()),
		TTL:       ttl,
	}
	c.evictLocked()
	c.save()
}

// evictLocked keeps the cache within maxCacheEntries. Caller holds c.mu.
func (c *ExactCache) evictLocked() {
	if len(c.data) <= maxCacheEntries {
		return
	}
	for k, e := range c.data { // expired first
		if !e.isValid() {
			delete(c.data, k)
		}
	}
	for len(c.data) > maxCacheEntries { // then oldest
		oldestKey := ""
		oldest := math.MaxFloat64
		for k, e := range c.data {
			if e.CreatedAt < oldest {
				oldest, oldestKey = e.CreatedAt, k
			}
		}
		delete(c.data, oldestKey)
	}
}

func (c *ExactCache) Stats() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
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

func (c *ExactCache) save() { persistJSON(c.path, c.data) }

func (c *ExactCache) load() { loadJSON(c.path, &c.data) }

// ─────────────────────────────────────────────
// Semantic Cache
// ─────────────────────────────────────────────

type SemanticCache struct {
	mu        sync.Mutex
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
	c.mu.Lock()
	defer c.mu.Unlock()
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
	c.mu.Lock()
	defer c.mu.Unlock()
	e := &entry{
		Value:     response,
		Preview:   preview,
		CreatedAt: float64(time.Now().Unix()),
		TTL:       ttl,
		Embedding: embedText(prompt),
		Prompt:    prompt,
	}
	c.entries = append(c.entries, e)
	c.evictLocked()
	c.save()
}

// evictLocked keeps the semantic cache within maxCacheEntries. Entries are
// appended in time order, so the front is oldest. Caller holds c.mu.
func (c *SemanticCache) evictLocked() {
	if len(c.entries) <= maxCacheEntries {
		return
	}
	kept := c.entries[:0]
	for _, e := range c.entries {
		if e.isValid() {
			kept = append(kept, e)
		}
	}
	c.entries = kept
	if len(c.entries) > maxCacheEntries {
		c.entries = append([]*entry(nil), c.entries[len(c.entries)-maxCacheEntries:]...)
	}
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
	c.mu.Lock()
	defer c.mu.Unlock()
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

func (c *SemanticCache) save() { persistJSON(c.path, c.entries) }

func (c *SemanticCache) load() { loadJSON(c.path, &c.entries) }

// persistJSON writes v as indented JSON to path, logging (never panicking) on
// failure. An empty path disables persistence.
func persistJSON(path string, v interface{}) {
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		logging.L().Warn("cache: mkdir failed", "path", path, "err", err)
		return
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		logging.L().Warn("cache: marshal failed", "path", path, "err", err)
		return
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		logging.L().Warn("cache: persist failed", "path", path, "err", err)
	}
}

// loadJSON reads JSON from path into dst. A missing file is normal (cold start)
// and silent; other read/parse errors are logged and the store stays empty.
func loadJSON(path string, dst interface{}) {
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			logging.L().Warn("cache: read failed", "path", path, "err", err)
		}
		return
	}
	if err := json.Unmarshal(data, dst); err != nil {
		logging.L().Warn("cache: ignoring corrupt cache file", "path", path, "err", err)
	}
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
