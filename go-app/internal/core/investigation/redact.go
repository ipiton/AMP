package investigation

import "regexp"

// redactedMarker replaces secret values in tool output.
const redactedMarker = "[REDACTED]"

// secretPatterns match credentials that show up in logs, events and query
// output. Best effort (INVESTIGATION-PROMPT-ISOLATION): it narrows what a
// tool leaks to the LLM provider and the stored trace, it is not a guarantee.
// Order matters: auth schemes run before the generic key=value rule, which
// would otherwise eat the scheme word and leave the token.
var secretPatterns = []struct {
	re   *regexp.Regexp
	repl string
}{
	// PEM private keys, including one cut off by a size cap.
	{regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----(?s:.*?)(?:-----END [A-Z ]*PRIVATE KEY-----|$)`), "[REDACTED PRIVATE KEY]"},
	// Authorization schemes: "Bearer <token>", "Basic <base64>".
	{regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}`), "${1} " + redactedMarker},
	// Credentials in URLs and DSNs: scheme://user:password@host.
	{regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://[^/\s:@]+):[^/\s@]+@`), "${1}:" + redactedMarker + "@"},
	// AWS access key IDs.
	{regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`), redactedMarker},
	// key=value / key: value / "key":"value", the key containing a secret
	// word (db_password, X-Api-Key, client_secret). Quotes and the
	// backslashes of JSON-escaped log lines around the value are kept out of
	// it, so the value itself is what gets replaced.
	{regexp.MustCompile(`(?i)([A-Za-z0-9_.-]*(?:password|passwd|pwd|secret|token|api[_-]?key|access[_-]?key|private[_-]?key)[A-Za-z0-9_.-]*["'\\]*\s*[:=]\s*["'\\]*)[^\s"'\\,;&}]+`), "${1}" + redactedMarker},
}

// redactSecrets masks credentials in s.
func redactSecrets(s string) string {
	for _, p := range secretPatterns {
		s = p.re.ReplaceAllString(s, p.repl)
	}
	return s
}
