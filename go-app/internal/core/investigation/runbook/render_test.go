package runbook_test

import (
	"testing"

	"github.com/ipiton/AMP/internal/core/investigation/runbook"
)

func TestRender_Golden(t *testing.T) {
	got := runbook.Render([]runbook.Runbook{
		{Name: "High Memory Usage", Body: "## Symptoms\nOOM."},
		{Name: "Pod Restarts", Body: "Check events."},
	}, 0)
	want := "Relevant runbooks (operator-provided guidance matched by alert labels; " +
		"verify against tool data before relying on it):\n\n" +
		"### Runbook: High Memory Usage\n## Symptoms\nOOM.\n\n" +
		"### Runbook: Pod Restarts\nCheck events."
	if got != want {
		t.Errorf("Render mismatch\n got: %q\nwant: %q", got, want)
	}
}

func TestRender_Empty(t *testing.T) {
	if got := runbook.Render(nil, 100); got != "" {
		t.Errorf("Render(nil) = %q, want empty", got)
	}
}

func TestRender_TruncatesByRunes(t *testing.T) {
	got := runbook.Render([]runbook.Runbook{{Name: "R", Body: "Проверить память"}}, 9)
	want := "\n\n### Runbook: R\nПроверить\n…[truncated]"
	if len(got) < len(want) || got[len(got)-len(want):] != want {
		t.Errorf("Render = %q, want suffix %q", got, want)
	}
}

func TestRender_BodyWithinLimitUntouched(t *testing.T) {
	got := runbook.Render([]runbook.Runbook{{Name: "R", Body: "короткий"}}, 8)
	want := "\n\n### Runbook: R\nкороткий"
	if got[len(got)-len(want):] != want {
		t.Errorf("Render = %q, want suffix %q", got, want)
	}
}
