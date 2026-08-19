package core

import (
	"sort"
	"strings"
)

// ============================================================================
// Notification template field contract (TEMPLATES-EPIC slice 2)
// ============================================================================
//
// These are the keys of PublishingTarget.Templates, and they are upstream
// Alertmanager's own per-integration config field names. Three packages have to
// agree on them and none of them may import another:
//
//   - business/publishing fills the map from the parsed `receivers:` config,
//   - infrastructure/publishing renders the values and overlays them onto the
//     wire payload,
//   - discovery parses the same keys out of a Kubernetes Secret.
//
// business/publishing already imports infrastructure/publishing, so the
// constants cannot live in either of them without creating a cycle — and
// PublishingTarget itself lives here, which makes core the natural home for the
// vocabulary of its own field.

// Template field keys. These are upstream's own config field names — the
// formatter side matches on them, so they are a contract, not labels.
const (
	// Slack.
	TemplateFieldTitle     = "title"
	TemplateFieldTitleLink = "title_link"
	TemplateFieldPretext   = "pretext"
	TemplateFieldText      = "text"
	TemplateFieldColor     = "color"
	TemplateFieldUsername  = "username"
	TemplateFieldChannel   = "channel"
	TemplateFieldIconEmoji = "icon_emoji"
	TemplateFieldIconURL   = "icon_url"
	TemplateFieldFallback  = "fallback"

	// PagerDuty.
	TemplateFieldDescription = "description"
	TemplateFieldSeverity    = "severity"
	TemplateFieldClient      = "client"
	TemplateFieldClientURL   = "client_url"

	// Telegram.
	TemplateFieldMessage = "message"

	// Email.
	TemplateFieldSubject = "subject"
	TemplateFieldHTML    = "html"

	// Prefix for map-valued fields: `details.<key>` (pagerduty),
	// `headers.<Key>` (email). Flattened because Templates is a flat
	// map[string]string, and a flat key keeps the wire/Secret shape simple.
	TemplateFieldDetailsPrefix = "details."
	TemplateFieldHeadersPrefix = "headers."
)

// TemplateFieldNames lists the keys of a Templates map, sorted — for
// deterministic logging and test assertions.
func TemplateFieldNames(templates map[string]string) []string {
	names := make([]string, 0, len(templates))
	for name := range templates {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// IsTemplateExpression reports whether value contains a template action at all.
// Used to keep logs and metrics meaningful: a field carrying a literal (say
// `color: danger`) needs no rendering, cannot fail, and must not be counted as
// a render or a fallback.
func IsTemplateExpression(value string) bool {
	return strings.Contains(value, "{{")
}
