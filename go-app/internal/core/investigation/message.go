package investigation

// MessageRole identifies the author of an agent message.
type MessageRole string

const (
	RoleSystem    MessageRole = "system"
	RoleUser      MessageRole = "user"
	RoleAssistant MessageRole = "assistant"
	RoleTool      MessageRole = "tool"
)

// ToolCallRequest is a single tool invocation requested by the LLM.
type ToolCallRequest struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Params   map[string]any `json:"params"`
}

// AgentMessage is one entry in the conversation history sent to/from the LLM.
type AgentMessage struct {
	Role       MessageRole       `json:"role"`
	Content    string            `json:"content,omitempty"`
	ToolCalls  []ToolCallRequest `json:"tool_calls,omitempty"`
	ToolCallID string            `json:"tool_call_id,omitempty"` // set when Role == RoleTool
}
