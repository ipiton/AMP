package investigation

import "regexp"

// redactedMarker replaces secret values in tool output.
const redactedMarker = "[REDACTED]"

// secretKey matches a key holding a secret (db_password, X-Api-Key,
// client_secret) and the separator after it, quotes and the backslashes of
// JSON-escaped log lines included.
const secretKey = `(?i)([A-Za-z0-9_.-]*(?:password|passwd|pwd|secret|token|api[_-]?key|access[_-]?key|private[_-]?key)[A-Za-z0-9_.-]*["'\\]*\s*[:=]\s*)`

// secretPatterns match credentials that show up in logs, events and query
// output. Best effort (INVESTIGATION-PROMPT-ISOLATION): it narrows what a
// tool leaks to the LLM provider and the stored trace, it is not a guarantee.
// Order matters: whole-value rules (PEM, Authorization, quoted values) run
// before the unquoted key=value rule, which stops at the first delimiter.
var secretPatterns = []struct {
	re   *regexp.Regexp
	repl string
}{
	// PEM private keys, including one cut off by a size cap.
	{regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----(?s:.*?)(?:-----END [A-Z ]*PRIVATE KEY-----|$)`), "[REDACTED PRIVATE KEY]"},
	// Authorization header, any scheme: the rest of the value up to a quote
	// or the end of the line.
	{regexp.MustCompile(`(?i)(authorization["'\\]*\s*[:=]\s*["'\\]*)[^"'\\\r\n]+`), "${1}" + redactedMarker},
	// A bare bearer token outside a header (JWTs and opaque tokens are long;
	// the length keeps "bearer authentication failed" intact).
	{regexp.MustCompile(`(?i)\b(bearer)\s+[A-Za-z0-9._~+/=-]{16,}`), "${1} " + redactedMarker},
	// Credentials in URLs and DSNs: scheme://user:password@host, user may be
	// empty (redis://:password@host).
	{regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://[^/\s:@]*):[^/\s@]+@`), "${1}:" + redactedMarker + "@"},
	// AWS access key IDs.
	{regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`), redactedMarker},
	// Quoted values, which may hold spaces and commas: \"…\" inside a
	// JSON-escaped log line, then "…" and '…'.
	{regexp.MustCompile(secretKey + `\\"(?:[^\\]|\\[^"])*\\"`), `${1}\"` + redactedMarker + `\"`},
	{regexp.MustCompile(secretKey + `"[^"]*"`), `${1}"` + redactedMarker + `"`},
	{regexp.MustCompile(secretKey + `'[^']*'`), `${1}'` + redactedMarker + `'`},
	// Unquoted values up to the first delimiter. \u00XX escapes are part of
	// the value: tool output passes through json.Marshal, which turns & < >
	// into \u0026 \u003c \u003e.
	{regexp.MustCompile(secretKey + `(?:\\u[0-9a-fA-F]{4}|[^\s"'\\,;&}])+`), "${1}" + redactedMarker},
}

// redactSecrets masks credentials in s.
func redactSecrets(s string) string {
	for _, p := range secretPatterns {
		s = p.re.ReplaceAllString(s, p.repl)
	}
	return s
}
