package core

import "time"

// InvestigationStatus represents the lifecycle status of an investigation.
type InvestigationStatus string

const (
	InvestigationQueued     InvestigationStatus = "queued"
	InvestigationProcessing InvestigationStatus = "processing"
	InvestigationCompleted  InvestigationStatus = "completed"
	InvestigationFailed     InvestigationStatus = "failed"
	InvestigationDLQ        InvestigationStatus = "dlq"
)

// InvestigationErrorType classifies errors for retry decisions.
type InvestigationErrorType string

const (
	InvestigationErrorTransient InvestigationErrorType = "transient"
	InvestigationErrorPermanent InvestigationErrorType = "permanent"
	InvestigationErrorUnknown   InvestigationErrorType = "unknown"
)

// InvestigationJob is the unit of work passed to the investigation worker.
type InvestigationJob struct {
	ID             string
	Alert          *Alert
	Classification *ClassificationResult // may be nil if classification was unavailable
	RetryCount     int
	SubmittedAt    time.Time
}

// InvestigationResult holds the structured output of an LLM investigation.
type InvestigationResult struct {
	Summary         string         `json:"summary"`
	Findings        map[string]any `json:"findings"`
	Recommendations []string       `json:"recommendations"`
	Confidence      float64        `json:"confidence"`

	// LLM telemetry
	LLMModel         string  `json:"llm_model,omitempty"`
	PromptTokens     int     `json:"prompt_tokens,omitempty"`
	CompletionTokens int     `json:"completion_tokens,omitempty"`
	ProcessingTime   float64 `json:"processing_time,omitempty"`
}

// Investigation is the persisted record in alert_investigations.
type Investigation struct {
	ID               string
	Fingerprint      string
	ClassificationID *int64
	Status           InvestigationStatus
	Result           *InvestigationResult
	RetryCount       int
	ErrorMessage     *string
	ErrorType        *InvestigationErrorType
	QueuedAt         time.Time
	StartedAt        *time.Time
	CompletedAt      *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}
