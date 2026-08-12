package memory

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Orbis-Mind/aether/internal/core"
)

const embeddingDim = 256

// ─────────────────────────────────────────────
// Embedding (n-gram hashing, no external deps)
// ─────────────────────────────────────────────

func embed(text string) []float64 {
	vec := make([]float64, embeddingDim)
	words := strings.Fields(strings.ToLower(text))
	for _, w := range words {
		for n := 1; n <= min(len(w), 4); n++ {
			ngram := w[:n]
			h := md5.Sum([]byte(ngram))
			idx := int(h[0]) % embeddingDim
			vec[idx] += 1.0
		}
	}
	norm := 0.0
	for _, v := range vec {
		norm += v * v
	}
	norm = math.Sqrt(norm)
	if norm > 0 {
		for i := range vec {
			vec[i] /= norm
		}
	}
	return vec
}

func cosineSim(a, b []float64) float64 {
	dot := 0.0
	for i := range a {
		dot += a[i] * b[i]
	}
	return dot
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ─────────────────────────────────────────────
// Memory Entry
// ─────────────────────────────────────────────

type Entry struct {
	Key        string            `json:"key"`
	Content    string            `json:"content"`
	Metadata   map[string]string `json:"metadata"`
	CreatedAt  float64           `json:"created_at"`
	TTLSeconds int               `json:"ttl_seconds"`
	Embedding  []float64         `json:"embedding,omitempty"`
}

func (e *Entry) IsExpired() bool {
	return time.Now().Unix()-int64(e.CreatedAt) > int64(e.TTLSeconds)
}

// ─────────────────────────────────────────────
// Vector Store
// ─────────────────────────────────────────────

type VectorStore struct {
	entries     []*Entry
	persistPath string
}

func NewVectorStore(persistPath string) *VectorStore {
	vs := &VectorStore{persistPath: persistPath}
	if persistPath != "" {
		vs.load()
	}
	return vs
}

func (vs *VectorStore) Add(key, content string, metadata map[string]string, ttl int) *Entry {
	e := &Entry{
		Key:        key,
		Content:    content,
		Metadata:   metadata,
		CreatedAt:  float64(time.Now().Unix()),
		TTLSeconds: ttl,
		Embedding:  embed(content),
	}
	vs.entries = append(vs.entries, e)
	vs.save()
	return e
}

type SearchResult struct {
	Entry      *Entry
	Similarity float64
}

func (vs *VectorStore) Search(query string, topK int, minSim float64) []SearchResult {
	qEmb := embed(query)
	var results []SearchResult
	for _, e := range vs.entries {
		if e.IsExpired() {
			continue
		}
		if e.Embedding == nil {
			e.Embedding = embed(e.Content)
		}
		sim := cosineSim(qEmb, e.Embedding)
		if sim >= minSim {
			results = append(results, SearchResult{Entry: e, Similarity: sim})
		}
	}
	// Sort by similarity desc
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].Similarity > results[i].Similarity {
				results[i], results[j] = results[j], results[i]
			}
		}
	}
	if len(results) > topK {
		return results[:topK]
	}
	return results
}

func (vs *VectorStore) Count() int {
	count := 0
	for _, e := range vs.entries {
		if !e.IsExpired() {
			count++
		}
	}
	return count
}

func (vs *VectorStore) save() {
	if vs.persistPath == "" {
		return
	}
	os.MkdirAll(filepath.Dir(vs.persistPath), 0755)
	data, _ := json.MarshalIndent(vs.entries, "", "  ")
	os.WriteFile(vs.persistPath, data, 0644)
}

func (vs *VectorStore) load() {
	data, err := os.ReadFile(vs.persistPath)
	if err != nil {
		return
	}
	json.Unmarshal(data, &vs.entries)
	for _, e := range vs.entries {
		if e.Embedding == nil {
			e.Embedding = embed(e.Content)
		}
	}
}

// ─────────────────────────────────────────────
// Entity Memory
// ─────────────────────────────────────────────

type EntityMemory struct {
	entities map[string]map[string]string
}

func NewEntityMemory() *EntityMemory {
	return &EntityMemory{entities: make(map[string]map[string]string)}
}

func (em *EntityMemory) Upsert(name string, attrs map[string]string) {
	key := strings.ToLower(name)
	if em.entities[key] == nil {
		em.entities[key] = make(map[string]string)
		em.entities[key]["name"] = name
	}
	for k, v := range attrs {
		em.entities[key][k] = v
	}
}

func (em *EntityMemory) Get(name string) map[string]string {
	return em.entities[strings.ToLower(name)]
}

func (em *EntityMemory) ToContext() string {
	if len(em.entities) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("Known entities:\n")
	for _, attrs := range em.entities {
		var parts []string
		for k, v := range attrs {
			parts = append(parts, fmt.Sprintf("%s=%s", k, v))
		}
		sb.WriteString("  " + strings.Join(parts, ", ") + "\n")
	}
	return sb.String()
}

func (em *EntityMemory) Count() int {
	return len(em.entities)
}

// ─────────────────────────────────────────────
// Relationship Memory
// ─────────────────────────────────────────────

type Relationship struct {
	Source     string  `json:"source"`
	Relation   string  `json:"relation"`
	Target     string  `json:"target"`
	Confidence float64 `json:"confidence"`
}

type RelationshipMemory struct {
	edges []Relationship
}

func NewRelationshipMemory() *RelationshipMemory {
	return &RelationshipMemory{}
}

func (rm *RelationshipMemory) Add(source, relation, target string, confidence float64) {
	rm.edges = append(rm.edges, Relationship{
		Source:     strings.ToLower(source),
		Relation:   relation,
		Target:     strings.ToLower(target),
		Confidence: confidence,
	})
}

func (rm *RelationshipMemory) ToContext() string {
	if len(rm.edges) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("Relationships:\n")
	for _, e := range rm.edges {
		sb.WriteString(fmt.Sprintf("  %s --[%s]--> %s\n", e.Source, e.Relation, e.Target))
	}
	return sb.String()
}

func (rm *RelationshipMemory) Count() int {
	return len(rm.edges)
}

// ─────────────────────────────────────────────
// Memory Orchestrator
// ─────────────────────────────────────────────

type Orchestrator struct {
	config       core.MemoryConfig
	Semantic     *VectorStore
	LongTerm     *VectorStore
	Entity       *EntityMemory
	Relationship *RelationshipMemory
}

func NewOrchestrator(config core.MemoryConfig, dataDir string) *Orchestrator {
	os.MkdirAll(dataDir, 0755)

	o := &Orchestrator{config: config}

	if config.Semantic {
		o.Semantic = NewVectorStore(filepath.Join(dataDir, "semantic.json"))
	}
	if config.LongTerm {
		o.LongTerm = NewVectorStore(filepath.Join(dataDir, "long_term.json"))
	}
	if config.Entity {
		o.Entity = NewEntityMemory()
	}
	if config.Relationship {
		o.Relationship = NewRelationshipMemory()
	}

	return o
}

func (o *Orchestrator) Store(content, memType string) {
	key := fmt.Sprintf("%x", md5.Sum([]byte(content+fmt.Sprint(time.Now().Unix()))))[:16]
	switch memType {
	case "semantic":
		if o.Semantic != nil {
			o.Semantic.Add(key, content, nil, o.config.TTLSeconds)
		}
	case "long_term":
		if o.LongTerm != nil {
			o.LongTerm.Add(key, content, nil, 365*86400)
		}
	}
}

func (o *Orchestrator) Retrieve(query string, topK int) string {
	var parts []string

	if o.Semantic != nil {
		results := o.Semantic.Search(query, topK, 0.1)
		if len(results) > 0 {
			parts = append(parts, "Relevant memories:")
			for _, r := range results {
				parts = append(parts, fmt.Sprintf("  [%.2f] %s", r.Similarity, r.Entry.Content))
			}
		}
	}

	if o.LongTerm != nil {
		results := o.LongTerm.Search(query, 2, 0.1)
		if len(results) > 0 {
			parts = append(parts, "Long-term knowledge:")
			for _, r := range results {
				parts = append(parts, "  "+r.Entry.Content)
			}
		}
	}

	if o.Entity != nil {
		ctx := o.Entity.ToContext()
		if ctx != "" {
			parts = append(parts, ctx)
		}
	}

	if o.Relationship != nil {
		ctx := o.Relationship.ToContext()
		if ctx != "" {
			parts = append(parts, ctx)
		}
	}

	return strings.Join(parts, "\n")
}

func (o *Orchestrator) Stats() map[string]int {
	stats := map[string]int{
		"semantic_entries":   0,
		"long_term_entries":  0,
		"entity_count":       0,
		"relationship_count": 0,
	}
	if o.Semantic != nil {
		stats["semantic_entries"] = o.Semantic.Count()
	}
	if o.LongTerm != nil {
		stats["long_term_entries"] = o.LongTerm.Count()
	}
	if o.Entity != nil {
		stats["entity_count"] = o.Entity.Count()
	}
	if o.Relationship != nil {
		stats["relationship_count"] = o.Relationship.Count()
	}
	return stats
}
