package investigation_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/core/investigation"
)

// leakyTool reports the alert namespace it sees and returns a secret, or an
// error that carries one.
type leakyTool struct {
	sawNamespace *string
	fail         bool
}

func (leakyTool) Definition() investigation.ToolDefinition {
	return investigation.ToolDefinition{Name: "leaky", Parameters: investigation.JSONSchemaObject{Type: "object"}}
}

func (l leakyTool) Execute(ctx context.Context, _ map[string]any) (investigation.ToolResult, error) {
	*l.sawNamespace = investigation.AlertNamespaceFromCtx(ctx)
	if l.fail {
		return investigation.ToolResult{IsError: true, Error: "dial postgres://amp:hunter2@db/amp: refused"}, nil
	}
	return investigation.ToolResult{Content: `{"logs":"login ok password=hunter2"}`}, nil
}

func TestAgentLoop_ToolGetsAlertNamespaceAndOutputIsRedacted(t *testing.T) {
	for _, fail := range []bool{false, true} {
		var ns string
		reg := investigation.NewToolRegistry()
		reg.Register(leakyTool{sawNamespace: &ns, fail: fail})
		llm := &mockAgentLLM{responses: []investigation.AgentResponse{
			{Kind: investigation.AgentResponseToolCalls, ToolCalls: []investigation.ToolCallRequest{{ID: "c1", Name: "leaky"}}},
			{Kind: investigation.AgentResponseFinalAnswer, Content: `{"summary":"done"}`},
		}}
		alert := &core.Alert{Fingerprint: "fp", AlertName: "A", Labels: map[string]string{"namespace": "payments"}}

		run, err := investigation.NewAgentLoop(llm, reg, investigation.DefaultAgentLoopConfig()).Run(context.Background(), alert, nil)
		if err != nil {
			t.Fatalf("fail=%v: Run: %v", fail, err)
		}
		if ns != "payments" {
			t.Errorf("fail=%v: tool saw namespace %q, want payments", fail, ns)
		}
		for _, s := range run.Steps {
			if s.Type != investigation.StepTypeObservation {
				continue
			}
			if strings.Contains(s.Output, "hunter2") || !strings.Contains(s.Output, "[REDACTED]") {
				t.Errorf("fail=%v: observation not redacted: %q", fail, s.Output)
			}
		}
	}
}
