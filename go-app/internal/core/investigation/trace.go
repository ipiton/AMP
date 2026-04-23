package investigation

import "time"

// StepType identifies the kind of work performed in one agent loop iteration.
type StepType string

const (
	StepTypeThought     StepType = "thought"
	StepTypeToolCall    StepType = "tool_call"
	StepTypeObservation StepType = "observation"
	StepTypeConclusion  StepType = "conclusion"
)

// InvestigationStep records one unit of work during the agentic loop.
type InvestigationStep struct {
	StepNumber int       `json:"step_number"`
	Type       StepType  `json:"type"`
	ToolName   string    `json:"tool_name,omitempty"`
	ToolCallID string    `json:"tool_call_id,omitempty"`
	Input      any       `json:"input,omitempty"`
	Output     string    `json:"output,omitempty"`
	IsError    bool      `json:"is_error,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
}
