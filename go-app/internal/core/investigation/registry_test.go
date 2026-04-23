package investigation_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ipiton/AMP/internal/core/investigation"
	"github.com/ipiton/AMP/internal/infrastructure/investigation/tools"
)

func TestRegistry_HappyPath(t *testing.T) {
	r := investigation.NewToolRegistry()
	r.Register(tools.EchoTool{})

	res := r.Execute(context.Background(), "echo", "call-1", map[string]any{"message": "hello"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Error)
	}
	if res.ToolName != "echo" {
		t.Errorf("ToolName = %q, want %q", res.ToolName, "echo")
	}
	if res.CallID != "call-1" {
		t.Errorf("CallID = %q, want %q", res.CallID, "call-1")
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(res.Content), &got); err != nil {
		t.Fatalf("content not valid JSON: %v", err)
	}
	if got["message"] != "hello" {
		t.Errorf("content[message] = %v, want %q", got["message"], "hello")
	}
}

func TestRegistry_UnknownTool(t *testing.T) {
	r := investigation.NewToolRegistry()

	res := r.Execute(context.Background(), "no_such_tool", "id-1", nil)
	if !res.IsError {
		t.Fatal("expected IsError=true for unknown tool")
	}
	if !strings.Contains(res.Error, "no_such_tool") {
		t.Errorf("error should mention tool name, got: %s", res.Error)
	}
}

func TestRegistry_DuplicateRegisterPanics(t *testing.T) {
	r := investigation.NewToolRegistry()
	r.Register(tools.EchoTool{})

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on duplicate registration")
		}
	}()
	r.Register(tools.EchoTool{})
}

func TestRegistry_Definitions(t *testing.T) {
	r := investigation.NewToolRegistry()
	r.Register(tools.EchoTool{})

	defs := r.Definitions()
	if len(defs) != 1 {
		t.Fatalf("len(Definitions) = %d, want 1", len(defs))
	}
	if defs[0].Name != "echo" {
		t.Errorf("defs[0].Name = %q, want %q", defs[0].Name, "echo")
	}
}

func TestEchoTool_Execute(t *testing.T) {
	tool := tools.EchoTool{}
	params := map[string]any{"key": "value", "num": float64(42)}

	res, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("IsError=true: %s", res.Error)
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(res.Content), &got); err != nil {
		t.Fatalf("content not valid JSON: %v", err)
	}
	if got["key"] != "value" {
		t.Errorf("got[key] = %v, want %q", got["key"], "value")
	}
}
