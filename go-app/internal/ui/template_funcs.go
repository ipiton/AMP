package ui

import (
	"encoding/json"
	"fmt"
	"html/template"
	"strings"
	"time"
)

// createTemplateFuncs creates custom template functions.
//
// Returns template.FuncMap with 15+ custom functions:
//   - Time: formatTime, timeAgo
//   - CSS: severity, statusClass
//   - Format: truncate, jsonPretty, upper, lower
//   - Util: defaultVal, join, contains
//   - Math: add, sub, mul, div
//   - String: plural
func createTemplateFuncs() template.FuncMap {
	return template.FuncMap{
		// Time functions
		"formatTime":   formatTime,
		"timeAgo":      timeAgo,
		"formatDateTime": formatDateTime,

		// CSS helper functions
		"severity":    severity,
		"statusClass": statusClass,

		// Formatting functions
		"truncate":   truncate,
		"jsonPretty": jsonPretty,
		"upper":      upper,
		"lower":      lower,

		// Utility functions
		"defaultVal": defaultVal,
		"join":       join,
		"contains":   contains,

		// Math functions
		"add": add,
		"sub": sub,
		"mul": mul,
		"div": div,
		"min": min,
		"max": max,

		// String helpers
		"plural": plural,

		// Duration formatting
		"humanDuration": humanDuration,

		// Status badge (for silence templates)
		"statusBadge": statusBadge,

		// TN-80: Classification helper functions
		"classificationSeverityClass": classificationSeverityClass,
		"classificationConfidencePercent": classificationConfidencePercent,
		"classificationConfidenceColor": classificationConfidenceColor,
	}
}

// formatTime formats time to human-readable string.
//
// Format: "2006-01-02 15:04:05"
//
// Example:
//
//	{{ formatTime .CreatedAt }} → "2025-11-17 14:30:00"
func formatTime(t time.Time) string {
	return t.Format("2006-01-02 15:04:05")
}

// timeAgo returns relative time from now.
//
// Examples:
//   - < 1 min: "just now"
//   - < 1 hour: "5 minutes ago"
//   - < 24 hours: "3 hours ago"
//   - >= 24 hours: "2 days ago"
//
// Template usage:
//
//	{{ timeAgo .UpdatedAt }} → "2 hours ago"
func timeAgo(t time.Time) string {
	duration := time.Since(t)

	switch {
	case duration < time.Minute:
		return "just now"
	case duration < time.Hour:
		m := int(duration.Minutes())
		return fmt.Sprintf("%d minute%s ago", m, plural(m))
	case duration < 24*time.Hour:
		h := int(duration.Hours())
		return fmt.Sprintf("%d hour%s ago", h, plural(h))
	default:
		d := int(duration.Hours() / 24)
		return fmt.Sprintf("%d day%s ago", d, plural(d))
	}
}

// severity returns CSS class for severity badge.
//
// Mapping:
//   - critical → "badge-critical" (red)
//   - warning → "badge-warning" (orange)
//   - info → "badge-info" (blue)
//   - default → "badge-default" (gray)
//
// Template usage:
//
//	<span class="{{ severity .Severity }}">{{ .Severity }}</span>
func severity(s string) string {
	switch strings.ToLower(s) {
	case "critical":
		return "badge-critical"
	case "warning":
		return "badge-warning"
	case "info":
		return "badge-info"
	default:
		return "badge-default"
	}
}

// statusClass returns CSS class for alert status.
//
// Mapping:
//   - firing → "status-firing" (red)
//   - resolved → "status-resolved" (green)
//   - pending → "status-pending" (yellow)
//   - default → "status-unknown" (gray)
//
// Template usage:
//
//	<div class="{{ statusClass .Status }}">{{ .Status }}</div>
func statusClass(s string) string {
	switch strings.ToLower(s) {
	case "firing":
		return "status-firing"
	case "resolved":
		return "status-resolved"
	case "pending":
		return "status-pending"
	default:
		return "status-unknown"
	}
}

// truncate truncates string to max length with ellipsis.
//
// If string is shorter than maxLen, returns unchanged.
// Otherwise, truncates and appends "...".
//
// Example:
//
//	{{ truncate .Description 50 }} → "This is a very long description that wi..."
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen < 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// jsonPretty pretty-prints JSON with indentation.
//
// Useful for displaying JSON data in templates.
//
// Example:
//
//	<pre>{{ jsonPretty .Labels }}</pre>
func jsonPretty(v interface{}) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	return string(b)
}

// upper converts string to uppercase.
//
// Example:
//
//	{{ upper "hello" }} → "HELLO"
func upper(s string) string {
	return strings.ToUpper(s)
}

// lower converts string to lowercase.
//
// Example:
//
//	{{ lower "HELLO" }} → "hello"
func lower(s string) string {
	return strings.ToLower(s)
}

// defaultVal returns default if value is nil or zero.
//
// Example:
//
//	{{ defaultVal "N/A" .OptionalField }}
func defaultVal(def, val interface{}) interface{} {
	if val == nil {
		return def
	}
	// Check for zero values
	switch v := val.(type) {
	case string:
		if v == "" {
			return def
		}
	case int, int8, int16, int32, int64:
		if v == 0 {
			return def
		}
	}
	return val
}

// join joins slice with separator.
//
// Example:
//
//	{{ join .Tags ", " }} → "tag1, tag2, tag3"
func join(slice []string, sep string) string {
	return strings.Join(slice, sep)
}

// contains checks if slice contains item.
//
// Example:
//
//	{{ if contains .Roles "admin" }}Admin{{ end }}
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// add adds two integers.
//
// Example:
//
//	{{ add .Count 1 }} → increments count
func add(a, b int) int {
	return a + b
}

// sub subtracts b from a.
//
// Example:
//
//	{{ sub .Total .Used }} → remaining
func sub(a, b int) int {
	return a - b
}

// mul multiplies two integers.
//
// Example:
//
//	{{ mul .Price .Quantity }}
func mul(a, b int) int {
	return a * b
}

// div divides a by b (safe division).
//
// Returns 0 if b is 0 (prevents division by zero).
//
// Example:
//
//	{{ div .Total .Count }} → average
func div(a, b int) int {
	if b == 0 {
		return 0
	}
	return a / b
}

// min returns the minimum of two integers.
//
// Example:
//
//	{{ min .Value 100 }} → returns smaller value
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// max returns the maximum of two integers.
//
// Example:
//
//	{{ max .Value 0 }} → returns larger value
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// plural returns "s" if count != 1, empty string otherwise.
//
// Helper for pluralization.
//
// Example:
//
//	{{ .Count }} alert{{ plural .Count }} → "1 alert" or "5 alerts"
func plural(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

// humanDuration formats duration to human-readable string.
//
// Formats duration as "1h 30m", "45m", "2h", etc.
//
// Example:
//
//	{{humanDuration 5400000000000}} → "1h 30m" (1.5 hours in nanoseconds)
func humanDuration(d time.Duration) string {
	if d < 0 {
		return "0s"
	}

	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60

	var parts []string
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	if minutes > 0 {
		parts = append(parts, fmt.Sprintf("%dm", minutes))
	}
	if seconds > 0 && hours == 0 && minutes == 0 {
		parts = append(parts, fmt.Sprintf("%ds", seconds))
	}

	if len(parts) == 0 {
		return "0s"
	}

	return strings.Join(parts, " ")
}

// statusBadge returns HTML badge for status.
//
// Returns colored badge HTML for status values: active, pending, expired.
//
// Example:
//
//	{{statusBadge "active"}} → <span class="badge badge-active">active</span>
func statusBadge(status string) template.HTML {
	statusLower := strings.ToLower(status)
	var class string
	switch statusLower {
	case "active":
		class = "badge badge-success"
	case "pending":
		class = "badge badge-warning"
	case "expired":
		class = "badge badge-secondary"
	default:
		class = "badge badge-info"
	}
	return template.HTML(fmt.Sprintf(`<span class="%s">%s</span>`, class, template.HTMLEscapeString(status)))
}

// formatDateTime formats time to RFC3339 datetime string.
//
// Format: "2006-01-02T15:04:05Z07:00"
//
// Example:
//
//	{{formatDateTime .CreatedAt}} → "2025-11-19T14:30:00Z"
func formatDateTime(t time.Time) string {
	return t.Format(time.RFC3339)
}

// TN-80: Classification helper functions

// classificationSeverityClass returns CSS class for classification severity.
//
// Mapping:
//   - critical → "severity-critical"
//   - warning → "severity-warning"
//   - info → "severity-info"
//   - noise → "severity-noise"
//
// Template usage:
//
//	{{ classificationSeverityClass .Classification.Severity }} → "severity-critical"
func classificationSeverityClass(severity string) string {
	severityLower := strings.ToLower(severity)
	return "severity-" + severityLower
}

// classificationConfidencePercent converts confidence (0.0-1.0) to percent (0-100).
//
// Template usage:
//
//	{{ classificationConfidencePercent .Classification.Confidence }} → 85
func classificationConfidencePercent(confidence float64) int {
	return int(confidence * 100)
}

// classificationConfidenceColor returns color class for confidence level.
//
// Mapping:
//   - >= 0.8 → "high" (green)
//   - >= 0.5 → "medium" (yellow)
//   - < 0.5 → "low" (red)
//
// Template usage:
//
//	{{ classificationConfidenceColor .Classification.Confidence }} → "high"
func classificationConfidenceColor(confidence float64) string {
	if confidence >= 0.8 {
		return "high"
	} else if confidence >= 0.5 {
		return "medium"
	}
	return "low"
}
