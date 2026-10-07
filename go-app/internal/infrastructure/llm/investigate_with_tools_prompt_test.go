package llm

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
	inv "github.com/ipiton/AMP/internal/core/investigation"
)

func promptTestAlert() *core.Alert {
	return &core.Alert{
		Fingerprint: "fp-1",
		AlertName:   "HighMemoryUsage",
		Status:      core.StatusFiring,
		Labels:      map[string]string{"severity": "critical"},
		StartsAt:    time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC),
	}
}

// TestBuildOpenAIMessages_NoRunbooksUnchanged pins the pre-PHASE-6B system
// prompt: with an empty PromptContext it must stay byte-for-byte identical.
func TestBuildOpenAIMessages_NoRunbooksUnchanged(t *testing.T) {
	msgs := buildOpenAIMessages(promptTestAlert(), nil, nil, inv.PromptContext{})
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2 (system + initial user)", len(msgs))
	}
	want := "You are an expert SRE investigating a production alert. " +
		"Use the available tools to gather data, then return a final JSON answer with keys: " +
		"summary, findings, recommendations, confidence.\n\n" +
		`Alert: {"alertname":"HighMemoryUsage","status":"firing","labels":{"severity":"critical"},` +
		`"annotations":null,"startsAt":"2026-09-27T10:00:00Z","fingerprint":"fp-1"}` + "\n" +
		"Classification: No classification available."
	if msgs[0].Role != "system" || msgs[0].Content != want {
		t.Errorf("system prompt changed\n got: %q\nwant: %q", msgs[0].Content, want)
	}
}

func TestBuildOpenAIMessages_RunbooksAppendedToSystemPrompt(t *testing.T) {
	alert := promptTestAlert()
	history := []inv.AgentMessage{
		{Role: inv.RoleAssistant, ToolCalls: []inv.ToolCallRequest{{ID: "c1", Name: "echo"}}},
		{Role: inv.RoleTool, Content: "ok", ToolCallID: "c1"},
	}
	section := "Relevant runbooks (...):\n\n### Runbook: High Memory Usage\nCheck OOMKilled."

	base := buildOpenAIMessages(alert, nil, history, inv.PromptContext{})
	got := buildOpenAIMessages(alert, nil, history, inv.PromptContext{Runbooks: section})

	if len(got) != len(base) {
		t.Fatalf("message count = %d, want %d", len(got), len(base))
	}
	if want := base[0].Content + "\n\n" + section; got[0].Content != want {
		t.Errorf("system prompt = %q, want base + section", got[0].Content)
	}
	if !strings.HasSuffix(got[0].Content, section) {
		t.Error("runbook section must end the system prompt")
	}
	if !reflect.DeepEqual(got[1:], base[1:]) {
		t.Error("non-system messages must not change")
	}
}

func TestBuildOpenAIMessages_ExplicitSystemInHistoryIgnoresRunbooks(t *testing.T) {
	history := []inv.AgentMessage{{Role: inv.RoleSystem, Content: "custom system"}}
	got := buildOpenAIMessages(promptTestAlert(), nil, history, inv.PromptContext{Runbooks: "RB"})
	if len(got) != 1 || got[0].Content != "custom system" {
		t.Errorf("caller-provided system message must be kept verbatim, got %+v", got)
	}
}
