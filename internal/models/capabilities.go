// Package models is the model capability registry. It enriches the base
// catalogue in internal/core with honest quality provenance (measured /
// estimated / unknown), per-task quality, capabilities and pricing, and can
// overlay measured benchmark results produced by the evaluation framework.
package models

// TaskType is the canonical set of workload categories the classifier and
// router reason about.
type TaskType string

const (
	TaskCoding         TaskType = "coding"
	TaskReasoning      TaskType = "reasoning"
	TaskMath           TaskType = "math"
	TaskSummarization  TaskType = "summarization"
	TaskTranslation    TaskType = "translation"
	TaskClassification TaskType = "classification"
	TaskExtraction     TaskType = "extraction"
	TaskCreative       TaskType = "creative"
	TaskRAG            TaskType = "rag"
	TaskResearch       TaskType = "research"
	TaskAgentic        TaskType = "agentic"
	TaskGeneral        TaskType = "general"
)

// AllTaskTypes lists every category, in a stable order for reports.
func AllTaskTypes() []TaskType {
	return []TaskType{
		TaskCoding, TaskReasoning, TaskMath, TaskSummarization, TaskTranslation,
		TaskClassification, TaskExtraction, TaskCreative, TaskRAG, TaskResearch,
		TaskAgentic, TaskGeneral,
	}
}

// Capabilities describes what a model can do, mirroring the provider-level
// capabilities but attached to a specific catalogue model.
type Capabilities struct {
	ContextWindow  int
	SupportsTools  bool
	SupportsVision bool
	SupportsAudio  bool
	Local          bool
}
