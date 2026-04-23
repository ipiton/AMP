package investigation_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/core/investigation"
	"github.com/ipiton/AMP/internal/infrastructure/investigation/tools"
)

// mockAgentLLM replays a fixed sequence of AgentResponse values.
type mockAgentLLM struct {
	responses []investigation.AgentResponse
	callCount int
}

func (m *mockAgentLLM) InvestigateWithTools(
	_ context.Context,
	_ *core.Alert,
	_ *core.ClassificationResult,
	_ []investigation.ToolDefinition,
	_ []investigation.AgentMessage,
) (*investigation.AgentResponse, error) {
	if m.callCount >= len(m.responses) {
		return nil, errors.New("mockAgentLLM: no more responses")
	}
	r := m.responses[m.callCount]
	m.callCount++
	return &r, nil
}

func newTestAlert() *core.Alert {
	return &core.Alert{Fingerprint: "fp-test", AlertName: "TestAlert"}
}

func TestAgentLoop_HappyPath_ToolCallThenFinalAnswer(t *testing.T) {
	reg := investigation.NewToolRegistry()
	reg.Register(tools.EchoTool{})

	llm := &mockAgentLLM{
		responses: []investigation.AgentResponse{
			{
				Kind: investigation.AgentResponseToolCalls,
				ToolCalls: []investigation.ToolCallRequest{
					{ID: "c1", Name: "echo", Params: map[string]any{"message": "hello"}},
				},
			},
			{
				Kind:    investigation.AgentResponseFinalAnswer,
				Content: `{"summary":"all good","confidence":0.9}`,
			},
		},
	}

	cfg := investigation.DefaultAgentLoopConfig()
	loop := investigation.NewAgentLoop(llm, reg, cfg)

	res, err := loop.Run(context.Background(), newTestAlert(), nil)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if res.TerminationKind != "final_answer" {
		t.Errorf("TerminationKind = %q, want final_answer", res.TerminationKind)
	}
	if res.ToolCallsCount != 1 {
		t.Errorf("ToolCallsCount = %d, want 1", res.ToolCallsCount)
	}
	if res.IterationsUsed != 2 {
		t.Errorf("IterationsUsed = %d, want 2", res.IterationsUsed)
	}
	if res.Result == nil || res.Result.Summary != "all good" {
		t.Errorf("unexpected result: %+v", res.Result)
	}
}

func TestAgentLoop_MaxIterations(t *testing.T) {
	reg := investigation.NewToolRegistry()

	// LLM always requests a tool call — loop should terminate at MaxIterations.
	infinite := make([]investigation.AgentResponse, 15)
	for i := range infinite {
		infinite[i] = investigation.AgentResponse{
			Kind: investigation.AgentResponseToolCalls,
			ToolCalls: []investigation.ToolCallRequest{
				{ID: "c1", Name: "echo", Params: map[string]any{}},
			},
		}
	}
	llm := &mockAgentLLM{responses: infinite}

	cfg := investigation.DefaultAgentLoopConfig()
	cfg.MaxIterations = 3
	loop := investigation.NewAgentLoop(llm, reg, cfg)

	res, err := loop.Run(context.Background(), newTestAlert(), nil)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if res.TerminationKind != "max_iterations" {
		t.Errorf("TerminationKind = %q, want max_iterations", res.TerminationKind)
	}
	if res.IterationsUsed != 3 {
		t.Errorf("IterationsUsed = %d, want 3", res.IterationsUsed)
	}
}

func TestAgentLoop_LLMError(t *testing.T) {
	reg := investigation.NewToolRegistry()
	llm := &mockAgentLLM{responses: nil} // no responses → error immediately

	cfg := investigation.DefaultAgentLoopConfig()
	loop := investigation.NewAgentLoop(llm, reg, cfg)

	res, err := loop.Run(context.Background(), newTestAlert(), nil)
	if err == nil {
		t.Fatal("expected error from Run, got nil")
	}
	if res.TerminationKind != "error" {
		t.Errorf("TerminationKind = %q, want error", res.TerminationKind)
	}
}

func TestAgentLoop_ToolError_ContinuesLoop(t *testing.T) {
	reg := investigation.NewToolRegistry()
	// Don't register "echo" — it will return an error observation.

	llm := &mockAgentLLM{
		responses: []investigation.AgentResponse{
			{
				Kind: investigation.AgentResponseToolCalls,
				ToolCalls: []investigation.ToolCallRequest{
					{ID: "c1", Name: "echo", Params: map[string]any{}},
				},
			},
			{
				Kind:    investigation.AgentResponseFinalAnswer,
				Content: `{"summary":"partial","confidence":0.4}`,
			},
		},
	}

	cfg := investigation.DefaultAgentLoopConfig()
	loop := investigation.NewAgentLoop(llm, reg, cfg)

	res, err := loop.Run(context.Background(), newTestAlert(), nil)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if res.TerminationKind != "final_answer" {
		t.Errorf("TerminationKind = %q, want final_answer", res.TerminationKind)
	}

	// Find observation step and confirm it is an error.
	var foundErrObs bool
	for _, s := range res.Steps {
		if s.Type == investigation.StepTypeObservation && s.IsError {
			foundErrObs = true
		}
	}
	if !foundErrObs {
		t.Error("expected an error observation step for unknown tool")
	}
}

func TestAgentLoop_DirectFinalAnswer(t *testing.T) {
	reg := investigation.NewToolRegistry()
	llm := &mockAgentLLM{
		responses: []investigation.AgentResponse{
			{Kind: investigation.AgentResponseFinalAnswer, Content: "raw text answer"},
		},
	}

	cfg := investigation.DefaultAgentLoopConfig()
	loop := investigation.NewAgentLoop(llm, reg, cfg)

	res, err := loop.Run(context.Background(), newTestAlert(), nil)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if res.TerminationKind != "final_answer" {
		t.Errorf("TerminationKind = %q, want final_answer", res.TerminationKind)
	}
	// Unparseable JSON → raw content as summary.
	if res.Result == nil || res.Result.Summary != "raw text answer" {
		t.Errorf("unexpected result: %+v", res.Result)
	}
	if res.ToolCallsCount != 0 {
		t.Errorf("ToolCallsCount = %d, want 0", res.ToolCallsCount)
	}
}

// ---- NewAgentLoop validation tests ----

func TestNewAgentLoop_PanicsOnZeroMaxHistoryMsgs(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic with MaxHistoryMsgs=0")
		}
	}()
	cfg := investigation.AgentLoopConfig{
		MaxIterations:  10,
		MaxHistoryMsgs: 0,
	}
	_ = investigation.NewAgentLoop(nil, nil, cfg)
}

// ---- trimHistory tests ----

func TestTrimHistory_BelowLimit(t *testing.T) {
	msgs := makeMessages(investigation.RoleUser, 3)
	got := callTrimHistory(msgs, 10)
	if len(got) != 3 {
		t.Errorf("len = %d, want 3", len(got))
	}
}

func TestTrimHistory_WithSystemMessage(t *testing.T) {
	sys := investigation.AgentMessage{Role: investigation.RoleSystem, Content: "system"}
	rest := makeMessages(investigation.RoleUser, 10)
	history := append([]investigation.AgentMessage{sys}, rest...)

	got := callTrimHistory(history, 5)
	if len(got) != 5 {
		t.Errorf("len = %d, want 5", len(got))
	}
	if got[0].Role != investigation.RoleSystem {
		t.Error("first message should be system")
	}
}

func TestTrimHistory_WithoutSystemMessage(t *testing.T) {
	history := makeMessages(investigation.RoleUser, 10)
	got := callTrimHistory(history, 4)
	if len(got) != 4 {
		t.Errorf("len = %d, want 4", len(got))
	}
}

// helpers

func makeMessages(role investigation.MessageRole, n int) []investigation.AgentMessage {
	out := make([]investigation.AgentMessage, n)
	for i := range out {
		out[i] = investigation.AgentMessage{Role: role}
	}
	return out
}

// callTrimHistory exercises trimHistory via a stub run that returns immediately.
// We use a dedicated mock that counts on trimming side-effects by inspecting step count.
// Simpler: export a thin wrapper for testing.
// Since trimHistory is unexported, we test it indirectly by constructing a loop with
// a history-aware mock and verifying behaviour. However, the simplest approach is
// to just expose a test-only helper in the production file using build tags.
// Instead, we test trimHistory indirectly via the exported AgentLoop.Run() path:
// the function below uses reflection-free approach — we test via a trivial loop run
// that would panic or misbehave if trimHistory were broken with a large history.
func callTrimHistory(history []investigation.AgentMessage, maxMsgs int) []investigation.AgentMessage {
	return investigation.TrimHistoryForTest(history, maxMsgs)
}
