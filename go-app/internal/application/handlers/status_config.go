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
// CONTAINS one of these is replaced with RedactedSecretPlaceholder.
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
	"api_url", // Slack/PagerDuty webhook URLs embed the credential itself
	"webhook_url",
	"auth",
	"bearer",
	"cert",
	"private",
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

	redactSecrets(doc)

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
func redactSecrets(node any) {
	switch typed := node.(type) {
	case map[string]any:
		for key, value := range typed {
			if isSecretKey(key) && isScalar(value) {
				typed[key] = RedactedSecretPlaceholder
				continue
			}
			redactSecrets(value)
		}
	case map[any]any:
		// yaml.v3 produces map[string]any for string keys, but a non-string
		// key (e.g. a numeric label) yields this shape — handle it so nothing
		// escapes redaction through an unusual key type.
		for key, value := range typed {
			if keyStr, ok := key.(string); ok && isSecretKey(keyStr) && isScalar(value) {
				typed[key] = RedactedSecretPlaceholder
				continue
			}
			redactSecrets(value)
		}
	case []any:
		for _, item := range typed {
			redactSecrets(item)
		}
	}
}

func isSecretKey(key string) bool {
	lower := strings.ToLower(key)
	for _, needle := range secretKeySubstrings {
		if strings.Contains(lower, needle) {
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
