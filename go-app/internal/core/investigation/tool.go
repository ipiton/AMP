package investigation

import "context"

// ToolDefinition describes a tool for the LLM (OpenAI function spec).
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  JSONSchemaObject `json:"parameters"`
}

// JSONSchemaObject describes tool parameters in JSON Schema format.
type JSONSchemaObject struct {
	Type       string                     `json:"type"` // always "object"
	Properties map[string]JSONSchemaField `json:"properties"`
	Required   []string                   `json:"required,omitempty"`
}

// JSONSchemaField describes a single JSON Schema property.
type JSONSchemaField struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	Default     any    `json:"default,omitempty"`
}

// ToolResult holds the output of a tool execution.
type ToolResult struct {
	ToolName string
	CallID   string
	Content  string // JSON or plain text
	IsError  bool
	Error    string
}

// Tool is the interface every investigation tool must implement.
type Tool interface {
	Definition() ToolDefinition
	Execute(ctx context.Context, params map[string]any) (ToolResult, error)
}
