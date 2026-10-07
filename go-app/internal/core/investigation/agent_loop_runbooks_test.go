package investigation_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/core/investigation"
	"github.com/ipiton/AMP/internal/core/investigation/runbook"
	"github.com/ipiton/AMP/internal/infrastructure/investigation/tools"
)

// twoIterationLLM returns one tool call, then a final answer.
func twoIterationLLM() *mockAgentLLM {
	return &mockAgentLLM{responses: []investigation.AgentResponse{
		{
			Kind:      investigation.AgentResponseToolCalls,
			ToolCalls: []investigation.ToolCallRequest{{ID: "c1", Name: "echo", Params: map[string]any{"message": "hi"}}},
		},
		{Kind: investigation.AgentResponseFinalAnswer, Content: `{"summary":"done","confidence":0.5}`},
	}}
}

func runbookLoop(llm *mockAgentLLM) *investigation.AgentLoop {
	reg := investigation.NewToolRegistry()
	reg.Register(tools.EchoTool{})
	return investigation.NewAgentLoop(llm, reg, investigation.DefaultAgentLoopConfig())
}

func testRunbookSet() *runbook.Set {
	return runbook.NewSet([]runbook.Runbook{
		{Name: "Generic Test", Match: map[string]string{"alertname": "TestAlert"}, Body: "generic steps"},
		{Name: "Critical Test", Match: map[string]string{"alertname": "TestAlert", "severity": "critical"}, Body: strings.Repeat("x", 50)},
		{Name: "Other", Match: map[string]string{"alertname": "Other"}, Body: "other steps"},
	})
}

func TestAgentLoop_RunbooksInjectedEveryIteration(t *testing.T) {
	llm := twoIterationLLM()
	loop := runbookLoop(llm)
	loop.SetRunbooks(testRunbookSet(), 3, 10)

	alert := &core.Alert{Fingerprint: "fp", AlertName: "TestAlert", Labels: map[string]string{"severity": "critical"}}
	res, err := loop.Run(context.Background(), alert, nil)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if want := []string{"Critical Test", "Generic Test"}; !reflect.DeepEqual(res.RunbooksUsed, want) {
		t.Errorf("RunbooksUsed = %v, want %v", res.RunbooksUsed, want)
	}
	if len(llm.prompts) != 2 {
		t.Fatalf("LLM called %d times, want 2", len(llm.prompts))
	}
	want := runbook.Render(testRunbookSet().Match(alert, 3), 10)
	for i, pc := range llm.prompts {
		if pc.Runbooks != want {
			t.Errorf("iteration %d: Runbooks = %q, want %q", i+1, pc.Runbooks, want)
		}
	}
	if !strings.Contains(want, "…[truncated]") {
		t.Error("maxChars must be applied to runbook bodies")
	}
}

func TestAgentLoop_RunbooksLimit(t *testing.T) {
	llm := twoIterationLLM()
	loop := runbookLoop(llm)
	loop.SetRunbooks(testRunbookSet(), 1, 0)

	alert := &core.Alert{AlertName: "TestAlert", Labels: map[string]string{"severity": "critical"}}
	res, err := loop.Run(context.Background(), alert, nil)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if want := []string{"Critical Test"}; !reflect.DeepEqual(res.RunbooksUsed, want) {
		t.Errorf("RunbooksUsed = %v, want %v", res.RunbooksUsed, want)
	}
}

func TestAgentLoop_NoRunbooksConfigured(t *testing.T) {
	llm := twoIterationLLM()
	res, err := runbookLoop(llm).Run(context.Background(), newTestAlert(), nil)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	assertEmptyRunbooks(t, llm, res)
}

func TestAgentLoop_RunbooksNoMatch(t *testing.T) {
	llm := twoIterationLLM()
	loop := runbookLoop(llm)
	loop.SetRunbooks(testRunbookSet(), 3, 100)
	res, err := loop.Run(context.Background(), &core.Alert{AlertName: "Unrelated"}, nil)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	assertEmptyRunbooks(t, llm, res)
}

func TestAgentLoop_RunbooksUsedOnMaxIterations(t *testing.T) {
	llm := &mockAgentLLM{responses: []investigation.AgentResponse{{
		Kind:      investigation.AgentResponseToolCalls,
		ToolCalls: []investigation.ToolCallRequest{{ID: "c1", Name: "echo", Params: map[string]any{"message": "hi"}}},
	}}}
	reg := investigation.NewToolRegistry()
	reg.Register(tools.EchoTool{})
	cfg := investigation.DefaultAgentLoopConfig()
	cfg.MaxIterations = 1
	loop := investigation.NewAgentLoop(llm, reg, cfg)
	loop.SetRunbooks(testRunbookSet(), 3, 100)

	res, err := loop.Run(context.Background(), &core.Alert{AlertName: "TestAlert"}, nil)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if res.TerminationKind != "max_iterations" {
		t.Fatalf("TerminationKind = %q", res.TerminationKind)
	}
	if want := []string{"Generic Test"}; !reflect.DeepEqual(res.RunbooksUsed, want) {
		t.Errorf("RunbooksUsed = %v, want %v", res.RunbooksUsed, want)
	}
}

func TestAgentLoop_RunbooksUsedOnLLMError(t *testing.T) {
	llm := &mockAgentLLM{} // no responses ⇒ error on first call
	loop := runbookLoop(llm)
	loop.SetRunbooks(testRunbookSet(), 3, 100)

	res, err := loop.Run(context.Background(), &core.Alert{AlertName: "TestAlert"}, nil)
	if err == nil {
		t.Fatal("Run returned nil error")
	}
	if res.TerminationKind != "error" {
		t.Fatalf("TerminationKind = %q", res.TerminationKind)
	}
	if want := []string{"Generic Test"}; !reflect.DeepEqual(res.RunbooksUsed, want) {
		t.Errorf("RunbooksUsed = %v, want %v", res.RunbooksUsed, want)
	}
}

func assertEmptyRunbooks(t *testing.T, llm *mockAgentLLM, res *investigation.AgentRunResult) {
	t.Helper()
	if len(res.RunbooksUsed) != 0 {
		t.Errorf("RunbooksUsed = %v, want empty", res.RunbooksUsed)
	}
	if len(llm.prompts) == 0 {
		t.Fatal("LLM was not called")
	}
	for i, pc := range llm.prompts {
		if pc != (investigation.PromptContext{}) {
			t.Errorf("iteration %d: PromptContext = %+v, want zero value", i+1, pc)
		}
	}
}
