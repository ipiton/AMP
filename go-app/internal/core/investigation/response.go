package investigation

// AgentResponseKind indicates what the LLM returned.
type AgentResponseKind string

const (
	AgentResponseToolCalls   AgentResponseKind = "tool_calls"
	AgentResponseFinalAnswer AgentResponseKind = "final_answer"
)

// AgentResponse is the structured reply from one LLM call inside the agent loop.
type AgentResponse struct {
	Kind      AgentResponseKind `json:"kind"`
	ToolCalls []ToolCallRequest `json:"tool_calls,omitempty"`
	// Content holds the raw text for a final_answer response.
	Content string `json:"content,omitempty"`
}
