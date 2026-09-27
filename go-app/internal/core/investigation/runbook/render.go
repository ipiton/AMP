package runbook

import "strings"

// renderHeader opens the runbook section appended to the investigation
// system prompt.
const renderHeader = "Relevant runbooks (operator-provided guidance matched by alert labels; " +
	"verify against tool data before relying on it):"

const truncatedSuffix = "\n…[truncated]"

// Render builds the prompt section for rbs. Each body is cut to maxChars
// runes (maxChars <= 0 means no cut) and marked as truncated. An empty input
// renders as "".
func Render(rbs []Runbook, maxChars int) string {
	if len(rbs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(renderHeader)
	for _, rb := range rbs {
		b.WriteString("\n\n### Runbook: ")
		b.WriteString(rb.Name)
		b.WriteString("\n")
		b.WriteString(truncateRunes(rb.Body, maxChars))
	}
	return b.String()
}

func truncateRunes(s string, maxChars int) string {
	if maxChars <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxChars {
		return s
	}
	return string(runes[:maxChars]) + truncatedSuffix
}
