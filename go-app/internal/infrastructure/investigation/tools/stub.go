// Package tools provides stub and real tool implementations for the investigation agent.
package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ipiton/AMP/internal/core/investigation"
)

// EchoTool is a stub tool for testing. It returns its input params as JSON.
type EchoTool struct{}

func (EchoTool) Definition() investigation.ToolDefinition {
	return investigation.ToolDefinition{
		Name:        "echo",
		Description: "Returns the provided parameters as JSON. Used in tests.",
		Parameters: investigation.JSONSchemaObject{
			Type: "object",
			Properties: map[string]investigation.JSONSchemaField{
				"message": {
					Type:        "string",
					Description: "Any string to echo back",
				},
			},
		},
	}
}

func (EchoTool) Execute(_ context.Context, params map[string]any) (investigation.ToolResult, error) {
	b, err := json.Marshal(params)
	if err != nil {
		return investigation.ToolResult{IsError: true, Error: fmt.Sprintf("marshal params: %v", err)}, nil
	}
	return investigation.ToolResult{Content: string(b)}, nil
}
