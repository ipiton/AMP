package runbook_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ipiton/AMP/internal/core/investigation/runbook"
)

const validRunbook = `---
name: High Memory Usage
match:
  alertname: HighMemoryUsage
  severity: critical
tags: [memory, oom]
owner: platform-team
---

## Symptoms
Pod memory usage exceeds 90% of limit.
`

func TestParse_Valid(t *testing.T) {
	rb, err := runbook.Parse("memory/high.md", []byte(validRunbook))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if rb.Name != "High Memory Usage" {
		t.Errorf("Name = %q", rb.Name)
	}
	wantMatch := map[string]string{"alertname": "HighMemoryUsage", "severity": "critical"}
	if !reflect.DeepEqual(rb.Match, wantMatch) {
		t.Errorf("Match = %v, want %v", rb.Match, wantMatch)
	}
	if !reflect.DeepEqual(rb.Tags, []string{"memory", "oom"}) {
		t.Errorf("Tags = %v", rb.Tags)
	}
	if rb.Body != "## Symptoms\nPod memory usage exceeds 90% of limit." {
		t.Errorf("Body = %q", rb.Body)
	}
	if rb.Source != "memory/high.md" {
		t.Errorf("Source = %q", rb.Source)
	}
}

func TestParse_CRLFAndBOM(t *testing.T) {
	data := "\uFEFF" + strings.ReplaceAll(validRunbook, "\n", "\r\n")
	rb, err := runbook.Parse("crlf.md", []byte(data))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if rb.Name != "High Memory Usage" || strings.Contains(rb.Body, "\r") {
		t.Errorf("unexpected runbook: name=%q body=%q", rb.Name, rb.Body)
	}
}

func TestParse_EmptyBodyAllowed(t *testing.T) {
	rb, err := runbook.Parse("empty.md", []byte("---\nname: X\nmatch:\n  alertname: A\n---\n"))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if rb.Body != "" {
		t.Errorf("Body = %q, want empty", rb.Body)
	}
}

func TestParse_Invalid(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		wantErr string
	}{
		{"no frontmatter", "# Just markdown\n", "missing frontmatter"},
		{"empty file", "", "missing frontmatter"},
		{"unterminated", "---\nname: X\nmatch:\n  alertname: A\n", "unterminated frontmatter"},
		{"bad yaml", "---\nname: [unclosed\n---\nbody", "invalid frontmatter"},
		{"missing name", "---\nmatch:\n  alertname: A\n---\n", "name is required"},
		{"blank name", "---\nname: '  '\nmatch:\n  alertname: A\n---\n", "name is required"},
		{"missing match", "---\nname: X\n---\n", "at least one label"},
		{"empty match", "---\nname: X\nmatch: {}\n---\n", "at least one label"},
		{"empty match key", "---\nname: X\nmatch:\n  '': A\n---\n", "empty label name"},
		{"empty match value", "---\nname: X\nmatch:\n  alertname: ''\n---\n", "empty value"},
		{"too large", "---\nname: X\nmatch:\n  alertname: A\n---\n" + strings.Repeat("x", runbook.MaxFileSize), "exceeds"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := runbook.Parse("bad.md", []byte(tt.data))
			if err == nil {
				t.Fatal("Parse returned nil error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, tt.wantErr)
			}
			if !strings.Contains(err.Error(), "bad.md") {
				t.Errorf("error = %q, want it to name the source", err)
			}
		})
	}
}
