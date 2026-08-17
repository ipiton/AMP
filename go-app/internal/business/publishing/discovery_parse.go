package publishing

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ipiton/AMP/internal/core"
	corev1 "k8s.io/api/core/v1"
)

// AmpReceiverLabel is the K8s Secret annotation/label key used to scope a
// publishing target to one or more Alertmanager receivers.
//
// Two sources, same key, different rules:
//   - ANNOTATION `amp.receiver` (primary source): comma-separated list of
//     receiver names, e.g. "slack-critical,pagerduty-oncall". Annotation
//     values accept arbitrary characters, so this is the only form that
//     supports multiple receivers.
//   - LABEL `amp.receiver` (fallback source): a SINGLE receiver name only.
//     K8s label values must match `(([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])?`
//     — commas are illegal, so a comma-separated list here would be
//     rejected by the API server on Secret creation/update.
//
// Precedence: if BOTH are present on the same Secret, the ANNOTATION wins
// outright (the label is ignored, not merged).
//
// A target Secret with NEITHER present is unscoped: it belongs to ALL
// receivers (backward compatibility with targets predating receiver-based
// routing). See PublishingCoordinator.targetMatchesReceiver for the
// filtering rules that consume this field (matching is case-sensitive —
// receiver names are compared with exact string equality).
const AmpReceiverLabel = "amp.receiver"

// parseSecret extracts PublishingTarget from K8s secret.
//
// Pipeline:
//  1. Extract secret.Data["config"] ([]byte)
//  2. Base64 decode (if needed) → JSON string
//  3. JSON unmarshal → PublishingTarget struct
//  4. Apply defaults (enabled=true if missing)
//  5. Return target (validation happens separately)
//
// Error Handling:
//   - Missing 'config' field → ErrInvalidSecretFormat
//   - Invalid base64 → ErrInvalidSecretFormat
//   - Invalid JSON → ErrInvalidSecretFormat
//   - All errors are typed (use errors.As)
//
// Performance:
//   - Target: <1ms per secret
//   - Goal (150%): <500µs per secret
//   - Bottleneck: JSON unmarshal (~300µs)
//
// Note: K8s client-go auto-decodes base64, but we handle both cases
// (raw base64 string vs already-decoded bytes) for flexibility.
//
// Example:
//
//	secret := getSecretFromK8s()
//	target, err := parseSecret(secret)
//	if err != nil {
//	    var formatErr *ErrInvalidSecretFormat
//	    if errors.As(err, &formatErr) {
//	        log.Warn("Skipping invalid secret",
//	            "secret", formatErr.SecretName,
//	            "reason", formatErr.Reason)
//	    }
//	    return nil
//	}
//
//	// Validate target (separate step)
//	errs := validateTarget(target)
//	if len(errs) > 0 {
//	    log.Warn("Target validation failed", "errors", errs)
//	    return nil
//	}
func parseSecret(secret corev1.Secret) (*core.PublishingTarget, error) {
	// Extract config field from secret data
	configData, ok := secret.Data["config"]
	if !ok {
		return nil, NewInvalidSecretFormatError(
			secret.Name,
			"missing 'config' field in secret.Data",
		)
	}

	// Check if empty
	if len(configData) == 0 {
		return nil, NewInvalidSecretFormatError(
			secret.Name,
			"'config' field is empty",
		)
	}

	// Decode base64 if needed
	// K8s client-go auto-decodes secret.Data, but we handle both cases
	var jsonData []byte
	if isBase64Encoded(configData) {
		decoded, err := base64.StdEncoding.DecodeString(string(configData))
		if err != nil {
			return nil, NewInvalidSecretFormatError(
				secret.Name,
				fmt.Sprintf("invalid base64 encoding: %v", err),
			)
		}
		jsonData = decoded
	} else {
		// Already decoded by client-go
		jsonData = configData
	}

	// JSON unmarshal
	var target core.PublishingTarget
	if err := json.Unmarshal(jsonData, &target); err != nil {
		return nil, NewInvalidSecretFormatError(
			secret.Name,
			fmt.Sprintf("invalid JSON: %v", err),
		)
	}

	// Apply defaults
	applyDefaults(&target)

	// Receiver scoping: `amp.receiver` annotation (primary, multi-name) or
	// label (fallback, single-name) — see AmpReceiverLabel doc. Annotation
	// wins if both are present. Neither present -> target.Receivers stays
	// nil (belongs to all receivers).
	if raw, ok := secret.Annotations[AmpReceiverLabel]; ok {
		target.Receivers = parseReceiverNames(raw)
	} else if raw, ok := secret.Labels[AmpReceiverLabel]; ok {
		target.Receivers = parseReceiverNames(raw)
	}

	return &target, nil
}

// parseReceiverNames splits a comma-separated `amp.receiver` value (from
// either the annotation or the label) into trimmed, non-empty receiver
// names. Shared by both sources — the label form just happens to only ever
// contain a single segment in practice (K8s rejects commas in label
// values), so splitting on "," is a no-op for it. Returns nil if no names
// remain (e.g. value present but blank), which is treated the same as an
// absent annotation/label by the coordinator's filtering logic (target
// belongs to all receivers).
func parseReceiverNames(raw string) []string {
	parts := strings.Split(raw, ",")
	receivers := make([]string, 0, len(parts))
	for _, p := range parts {
		name := strings.TrimSpace(p)
		if name != "" {
			receivers = append(receivers, name)
		}
	}
	if len(receivers) == 0 {
		return nil
	}
	return receivers
}

// isBase64Encoded checks if data looks like base64-encoded string.
//
// Heuristic:
//   - Try base64 decode
//   - If succeeds without error → probably base64
//   - If fails → probably already decoded (raw bytes)
//
// Note: This is not 100% accurate but works for K8s secrets.
// K8s secrets can be base64-encoded or raw bytes depending on client.
//
// Example:
//
//	data := []byte("eyJuYW1lIjoidGVzdCJ9") // base64
//	if isBase64Encoded(data) {
//	    decoded, _ := base64.StdEncoding.DecodeString(string(data))
//	    // decoded = `{"name":"test"}`
//	}
func isBase64Encoded(data []byte) bool {
	// Quick check: base64 uses specific character set
	// If data contains non-base64 chars (like {, :, "), it's not base64
	for _, b := range data {
		if !isBase64Char(b) {
			return false
		}
	}

	// Try decode (if succeeds → base64, if fails → raw)
	_, err := base64.StdEncoding.DecodeString(string(data))
	return err == nil
}

// isBase64Char checks if byte is valid base64 character.
//
// Base64 alphabet: A-Z, a-z, 0-9, +, /, = (padding)
func isBase64Char(b byte) bool {
	return (b >= 'A' && b <= 'Z') ||
		(b >= 'a' && b <= 'z') ||
		(b >= '0' && b <= '9') ||
		b == '+' || b == '/' || b == '='
}

// applyDefaults sets default values for optional fields.
//
// Defaults:
//   - enabled: true (if not explicitly set to false)
//   - headers: empty map (if nil)
//   - filter_config: empty map (if nil)
//
// This ensures consistent behavior when fields are missing from JSON.
//
// Example:
//
//	target := &core.PublishingTarget{
//	    Name: "test",
//	    Type: "webhook",
//	    URL: "https://example.com",
//	    // enabled not set
//	}
//	applyDefaults(target)
//	// target.Enabled = true (default)
func applyDefaults(target *core.PublishingTarget) {
	// Default enabled=true if not explicitly set
	// Note: We can't distinguish between false and unset in JSON,
	// so we assume unset = true (safe default).
	// If user wants disabled, they must explicitly set "enabled": false.
	if !target.Enabled && target.Headers == nil && target.FilterConfig == nil {
		// Heuristic: If all optional fields are zero-value,
		// then enabled was probably not set (use default true).
		// If headers/filter_config are set, respect enabled=false.
		target.Enabled = true
	}

	// Initialize maps if nil (avoids nil pointer panics)
	if target.Headers == nil {
		target.Headers = make(map[string]string)
	}
	if target.FilterConfig == nil {
		target.FilterConfig = make(map[string]any)
	}
}
