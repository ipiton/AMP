// Package alertconv holds the single source of truth for alert DTO
// conversions shared by the ingest handlers, the webhook parsers and the
// in-memory alert store (DTO-FRAGMENTATION consolidation).
//
// Every ingest path MUST use the same time parsing, status normalization
// and fingerprint algorithm so that the same alert gets the same identity
// regardless of how it entered the system.
package alertconv

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"time"

	"github.com/ipiton/AMP/internal/core"
	"github.com/prometheus/common/model"
)

// ParseAlertTime parses an alert timestamp leniently, matching Alertmanager
// ingest semantics: RFC3339 (fractional seconds included, so RFC3339Nano is
// covered) plus date-only YYYY-MM-DD payloads. An empty string yields the
// zero time with no error. Non-zero results are normalized to UTC.
func ParseAlertTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}

	t, err := time.Parse(time.RFC3339, raw)
	if err == nil {
		return t.UTC(), nil
	}

	// Alertmanager accepts date-only timestamps (YYYY-MM-DD) in ingest payloads.
	t, dateErr := time.Parse("2006-01-02", raw)
	if dateErr != nil {
		return time.Time{}, dateErr
	}
	return t.UTC(), nil
}

// ParseOptionalAlertTime is ParseAlertTime for optional fields: an empty or
// zero timestamp yields nil.
func ParseOptionalAlertTime(raw string) (*time.Time, error) {
	t, err := ParseAlertTime(raw)
	if err != nil {
		return nil, err
	}
	if t.IsZero() {
		return nil, nil
	}
	return &t, nil
}

// NormalizeStatus maps a raw status string to "firing"/"resolved". Unknown
// statuses are inferred from endsAt: an endsAt in the past means resolved.
func NormalizeStatus(raw string, endsAt *time.Time, now time.Time) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "firing":
		return "firing"
	case "resolved":
		return "resolved"
	}
	if endsAt != nil && !endsAt.After(now) {
		return "resolved"
	}
	return "firing"
}

// Fingerprint computes the canonical alert fingerprint: full SHA-256 over
// "alertname|k=v|..." with labels sorted by key (Alertmanager semantics).
// The alertname is taken from the labels map itself, so all ingest paths
// produce the same fingerprint for the same alert. Empty labels yield "".
func Fingerprint(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}

	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var builder strings.Builder
	builder.WriteString(labels["alertname"])
	builder.WriteByte('|')
	for _, key := range keys {
		builder.WriteString(key)
		builder.WriteByte('=')
		builder.WriteString(labels[key])
		builder.WriteByte('|')
	}

	sum := sha256.Sum256([]byte(builder.String()))
	return hex.EncodeToString(sum[:])
}

// UpstreamFingerprint computes the Alertmanager/Prometheus-compatible alert
// fingerprint: FNV-1a 64-bit over the label set, via prometheus/common's
// model.LabelSet.Fingerprint() (the exact algorithm/library upstream
// Alertmanager uses), formatted as 16 lowercase hex characters
// (model.Fingerprint.String()'s "%016x").
//
// This is intentionally a DIFFERENT value from Fingerprint() above (SHA-256,
// 64 hex chars): that one remains the internal identity/dedup key used
// everywhere a fingerprint is stored or looked up (AlertStore dedup keys,
// GetAlertByFingerprint, classification/publishing cache keys, ...) — changing
// it would require a migration of every persisted key. UpstreamFingerprint is
// used ONLY for the value serialized in API responses (see
// ToGettableAlert), so external consumers that validate/compare against
// upstream's 16-hex format (dashboards, amtool, correlation logic) see the
// shape they expect, without touching internal storage identity.
func UpstreamFingerprint(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}

	ls := make(model.LabelSet, len(labels))
	for name, value := range labels {
		ls[model.LabelName(name)] = model.LabelValue(value)
	}

	return ls.Fingerprint().String()
}

// CloneStringMap returns a defensive copy of src. The result is always a
// non-nil map so DTOs never marshal labels/annotations as JSON null.
func CloneStringMap(src map[string]string) map[string]string {
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

// SilenceMatcher is the read-side of the silence store needed to compute
// alert state. *memory.SilenceStore satisfies it; nil means "no silences".
type SilenceMatcher interface {
	ActiveMatchingSilenceIDs(labels map[string]string, now time.Time) []string
}

// ToGettableAlert converts an APIAlert into the Alertmanager API v2 gettable
// shape, computing state/silencedBy from the silence store. It is
// nil-tolerant to silences (nil ⇒ state derived from status only).
// SilencedBy/InhibitedBy/MutedBy are always empty slices, never nil, per the
// Alertmanager API v2 schema.
//
// Fingerprint is recomputed from alert.Labels via UpstreamFingerprint (16 hex
// chars), NOT passed through from alert.Fingerprint (the internal SHA-256
// dedup key, 64 hex chars) — see UpstreamFingerprint's doc comment. This is
// the only place that substitution happens: every other consumer of
// alert.Fingerprint keeps using the internal key untouched.
func ToGettableAlert(alert core.APIAlert, silences SilenceMatcher, now time.Time) core.APIGettableAlert {
	silencedBy := make([]string, 0)
	if alert.Status == "firing" && silences != nil {
		if ids := silences.ActiveMatchingSilenceIDs(alert.Labels, now); len(ids) > 0 {
			silencedBy = append(silencedBy, ids...)
		}
	}

	state := "active"
	if len(silencedBy) > 0 {
		state = "suppressed"
	} else if alert.Status == "resolved" {
		state = "unprocessed" // Simplification for now
	}

	endsAt := alert.UpdatedAt
	if alert.EndsAt != nil && *alert.EndsAt != "" {
		endsAt = *alert.EndsAt
	}

	return core.APIGettableAlert{
		Labels:       alert.Labels,
		Annotations:  alert.Annotations,
		Receivers:    alert.Receivers,
		StartsAt:     alert.StartsAt,
		UpdatedAt:    alert.UpdatedAt,
		EndsAt:       endsAt,
		GeneratorURL: alert.GeneratorURL,
		Fingerprint:  UpstreamFingerprint(alert.Labels),
		Status: core.APIAlertStatus{
			State:       state,
			SilencedBy:  silencedBy,
			InhibitedBy: []string{},
			MutedBy:     []string{},
		},
	}
}
