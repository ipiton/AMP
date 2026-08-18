package handlers

import (
	"fmt"
	"strings"

	appconfig "github.com/ipiton/AMP/internal/config"
	"gopkg.in/yaml.v3"
)

// RedactedSecretPlaceholder is what every secret-bearing value is replaced with
// in the `config.original` payload of /api/v2/status. Matches upstream
// Alertmanager, whose `Secret`/`SecretURL` types marshal as exactly this, so
// amtool and any other upstream-compatible consumer sees a familiar value.
const RedactedSecretPlaceholder = "<secret>"

// secretKeySubstrings drives redaction: any mapping key whose lowercase name
// CONTAINS one of these is replaced with RedactedSecretPlaceholder, wherever it
// appears.
//
// Substring matching, not an exact allow-list, on purpose — this guards a
// public unauthenticated endpoint, so a new integration field
// (`api_key_file`, `bot_token`, `auth_password`, ...) must be redacted by
// default rather than leak until someone remembers to extend a list.
var secretKeySubstrings = []string{
	"password",
	"passwd",
	"secret",
	"token",
	"api_key",
	"apikey",
	"key_file",
	"credential",
	"routing_key",
	"service_key",
	"integration_key",
	"webhook_url",
	"auth",
	"bearer",
	"cert",
	"private",
}

// sectionSecretKeys lists keys that are secret ONLY inside a specific parent
// section, because the same key name is a credential in one integration and a
// public endpoint in another. Keys are matched exactly (lowercased), unlike
// secretKeySubstrings.
//
// This mirrors upstream Alertmanager's own typing, which is the authority on
// which URLs are secrets: upstream types a field `SecretURL` (marshals as
// `<secret>`) exactly where the URL itself is the credential.
//
//   - `webhook_configs[].url` — upstream `SecretURL`. Anyone holding it can
//     post to the endpoint. Wave re-review, Important 2: this was NOT redacted,
//     because the always-secret substrings above cannot contain a bare "url"
//     without also blanking every harmless endpoint in the document.
//   - `slack_configs[].api_url` — upstream `SecretURL`; a Slack incoming-webhook
//     URL embeds the token.
//   - `global.slack_api_url` — same, at global scope.
//
// Deliberately NOT here, because upstream types them as plain `URL` and they
// carry no credential: `pagerduty_configs[].url` (the public Events API
// endpoint) and `telegram_configs[].api_url` (the public Bot API base — the
// credential is `bot_token`, which the substring list already covers). An
// earlier revision blanket-redacted every `api_url`, which hid those two for no
// security gain and reduced the fidelity of what amtool re-parses.
var sectionSecretKeys = map[string][]string{
	"webhook_configs": {"url"},
	"slack_configs":   {"api_url"},
	"global":          {"slack_api_url", "api_url"},
}

// AlertmanagerConfigYAML renders the Alertmanager-shaped, secret-redacted view
// of cfg for /api/v2/status's `config.original` field.
//
// WHY (final review finding 15 — security + parity, one fix): the handler used
// to os.ReadFile the raw config file and return it verbatim on an
// unauthenticated endpoint. That leaked every credential in the file —
// database.password, redis.password, llm API keys, webhook secrets — to anyone
// who could reach the port. It was also the wrong SHAPE: upstream's
// `config.original` is the Alertmanager config (route/receivers/inhibit_rules/
// time_intervals/global), which is what `amtool config routes show` re-parses.
// AMP's file is an AMP config with an Alertmanager section inside it, so amtool
// could not use it either.
//
// Both problems have the same fix: emit only the Alertmanager section, with
// secrets redacted. Nothing outside that section is exposed at all, so
// database/redis/LLM credentials cannot leak by construction — the redaction
// pass below is defence in depth for the receivers' own integration configs.
//
// Returns a YAML document; never returns an error, because a status endpoint
// must not fail on a rendering problem. A rendering failure degrades to a
// comment explaining why, which is also (deliberately) valid YAML.
func AlertmanagerConfigYAML(cfg *appconfig.Config) string {
	if cfg == nil {
		return "# configuration unavailable\n"
	}

	if cfg.Routing == nil {
		return legacyReceiverConfigYAML(cfg)
	}

	data, err := yaml.Marshal(cfg.Routing)
	if err != nil {
		return "# configuration could not be rendered\n"
	}

	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		// Cannot inspect it, so cannot prove it is secret-free: refuse rather
		// than emit something unredacted.
		return "# configuration could not be rendered\n"
	}

	redactSecrets(doc, "")

	redacted, err := yaml.Marshal(doc)
	if err != nil {
		return "# configuration could not be rendered\n"
	}
	return string(redacted)
}

// legacyReceiverConfigYAML synthesizes a minimal Alertmanager-shaped document
// for a deployment configured WITHOUT a `route:` tree (the legacy
// single-receiver config). amtool needs `route:` plus `receivers:` to parse at
// all, so emitting nothing would break it just as badly as emitting the wrong
// shape.
func legacyReceiverConfigYAML(cfg *appconfig.Config) string {
	names := make([]string, 0, len(cfg.Receivers))
	for _, r := range cfg.Receivers {
		if r.Name != "" {
			names = append(names, r.Name)
		}
	}
	if len(names) == 0 {
		names = append(names, "default")
	}

	var b strings.Builder
	b.WriteString("# AMP is running without a `route:` tree; this is a synthesized\n")
	b.WriteString("# Alertmanager-shaped view of the configured receivers.\n")
	b.WriteString("route:\n")
	fmt.Fprintf(&b, "  receiver: %q\n", names[0])
	b.WriteString("receivers:\n")
	for _, name := range names {
		fmt.Fprintf(&b, "  - name: %q\n", name)
	}
	return b.String()
}

// redactSecrets walks an unmarshalled YAML document in place, replacing the
// value of every secret-named key with RedactedSecretPlaceholder.
//
// Only scalar leaves are replaced; a secret-named key holding a map or list
// (e.g. `http_config.authorization:`) is descended into instead, so its own
// secret-named leaves get redacted individually and its harmless structure
// stays visible.
//
// section is the name of the nearest enclosing mapping key — "webhook_configs"
// for every entry of a `webhook_configs:` list, and so on. It exists so keys
// whose secrecy depends on where they appear can be handled correctly; see
// sectionSecretKeys. List entries inherit their list's section, which is what
// makes `receivers[].webhook_configs[].url` resolve with section
// "webhook_configs" rather than "receivers".
func redactSecrets(node any, section string) {
	switch typed := node.(type) {
	case map[string]any:
		for key, value := range typed {
			if isSecretKey(key, section) && isScalar(value) {
				typed[key] = RedactedSecretPlaceholder
				continue
			}
			redactSecrets(value, key)
		}
	case map[any]any:
		// yaml.v3 produces map[string]any for string keys, but a non-string
		// key (e.g. a numeric label) yields this shape — handle it so nothing
		// escapes redaction through an unusual key type.
		for key, value := range typed {
			keyStr, ok := key.(string)
			if ok && isSecretKey(keyStr, section) && isScalar(value) {
				typed[key] = RedactedSecretPlaceholder
				continue
			}
			redactSecrets(value, keyStr)
		}
	case []any:
		for _, item := range typed {
			redactSecrets(item, section)
		}
	}
}

// isSecretKey reports whether key names a secret, either unconditionally
// (secretKeySubstrings) or within this particular section (sectionSecretKeys).
func isSecretKey(key, section string) bool {
	lower := strings.ToLower(key)
	for _, needle := range secretKeySubstrings {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	for _, contextual := range sectionSecretKeys[strings.ToLower(section)] {
		if lower == contextual {
			return true
		}
	}
	return false
}

// isScalar reports whether v is a leaf value (anything that is not a
// map/sequence we should descend into).
func isScalar(v any) bool {
	switch v.(type) {
	case map[string]any, map[any]any, []any:
		return false
	default:
		return v != nil
	}
}
