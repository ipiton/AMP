package investigation

import (
	"context"
	"fmt"
	"sort"
)

// ToolRegistry registers tools by name and executes them on behalf of the agent loop.
type ToolRegistry struct {
	tools map[string]Tool
}

// NewToolRegistry creates an empty ToolRegistry.
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{tools: make(map[string]Tool)}
}

// Register adds a tool to the registry. Panics on duplicate name.
func (r *ToolRegistry) Register(t Tool) {
	name := t.Definition().Name
	if _, exists := r.tools[name]; exists {
		panic(fmt.Sprintf("investigation: tool %q already registered", name))
	}
	r.tools[name] = t
}

// Execute runs a registered tool and returns its result.
// Returns ToolResult{IsError:true} when the tool is not found.
func (r *ToolRegistry) Execute(ctx context.Context, name string, callID string, params map[string]any) ToolResult {
	t, ok := r.tools[name]
	if !ok {
		return ToolResult{
			ToolName: name,
			CallID:   callID,
			IsError:  true,
			Error:    fmt.Sprintf("unknown tool: %q", name),
		}
	}
	res, err := t.Execute(ctx, params)
	res.ToolName = name
	res.CallID = callID
	if err != nil {
		res.IsError = true
		res.Error = err.Error()
	}
	return res
}

// Definitions returns the ToolDefinition for every registered tool, sorted by name
// for deterministic ordering across calls.
func (r *ToolRegistry) Definitions() []ToolDefinition {
	defs := make([]ToolDefinition, 0, len(r.tools))
	for _, t := range r.tools {
		defs = append(defs, t.Definition())
	}
	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
	return defs
}
