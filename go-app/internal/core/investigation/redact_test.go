package investigation

import (
	"strings"
	"testing"
)

func TestRedactSecrets(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"key=value", `DB_PASSWORD=foo bar`, `DB_PASSWORD=[REDACTED] bar`},
		{"key: value with comma after", `client_secret: abc, next`, `client_secret: [REDACTED], next`},
		{"JSON pair, quoted with spaces", `"api_key":"sk 123, 456" "x":1`, `"api_key":"[REDACTED]" "x":1`},
		{"single-quoted", `password='a b' z`, `password='[REDACTED]' z`},
		{"escaped quotes in a JSON-encoded log line", `{"logs":"db password=\"hunter 2, x\" ok"}`, `{"logs":"db password=\"[REDACTED]\" ok"}`},
		{"JSON escapes of & < > stay in the value", `{"logs":"password=ab\u0026cd\u003cef next"}`, `{"logs":"password=[REDACTED] next"}`},
		{"Authorization Bearer", `Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.abc`, `Authorization: [REDACTED]`},
		{"Authorization other scheme, to end of line", `{"logs":"Authorization: Token abc123 \nnext line"}`, `{"logs":"Authorization: [REDACTED]\nnext line"}`},
		{"bare long bearer token", `got bearer abcdefghijklmnopqrstuvwxyz from cache`, `got bearer [REDACTED] from cache`},
		{"URL password", `postgres://amp:pw@db/amp`, `postgres://amp:[REDACTED]@db/amp`},
		{"URL password, empty user", `redis://:s3cret@redis:6379/0`, `redis://:[REDACTED]@redis:6379/0`},
		{"AWS access key id", `key AKIAABCDEFGHIJKLMNOP end`, `key [REDACTED] end`},
		{"PEM private key", "x -----BEGIN RSA PRIVATE KEY-----\nMIIx\n-----END RSA PRIVATE KEY----- after", "x [REDACTED PRIVATE KEY] after"},
		{"PEM private key cut by a size cap", "-----BEGIN PRIVATE KEY-----\nMIIx...", "[REDACTED PRIVATE KEY]"},
		{"plain prose untouched", `basic authentication failed; bearer authentication failed`, `basic authentication failed; bearer authentication failed`},
		{"pod summary untouched", `{"name":"api-1","phase":"Running","restarts":2}`, `{"name":"api-1","phase":"Running","restarts":2}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := redactSecrets(tc.in); got != tc.want {
				t.Errorf("redactSecrets(%q)\n got: %q\nwant: %q", tc.in, got, tc.want)
			}
		})
	}
}

// The known false positive stays visible: it is the price of matching any
// key that contains "token".
func TestRedactSecrets_KnownFalsePositive(t *testing.T) {
	if got := redactSecrets(`prompt_tokens: 50`); !strings.Contains(got, redactedMarker) {
		t.Errorf("expected the documented false positive, got %q", got)
	}
}
