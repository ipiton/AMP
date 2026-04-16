package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	appconfig "github.com/ipiton/AMP/internal/config"
	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/core/services"
	"github.com/ipiton/AMP/internal/infrastructure/inhibition"
	"github.com/ipiton/AMP/internal/infrastructure/storage/memory"
	"github.com/ipiton/AMP/internal/infrastructure/webhook"
)

// RegistryProvider is an interface that provides access to the service registry.
// This allows us to inject a mock or the actual ServiceRegistry without circular imports.
type RegistryProvider interface {
	AlertStore() *memory.AlertStore
	SilenceStore() *memory.SilenceStore
	AlertProcessor() *services.AlertProcessor
	Config() *appconfig.Config
	StartTime() time.Time
	ReloadConfig(ctx context.Context) error
	InhibitionState() inhibition.InhibitionStateManager
}

func AlertsHandler(registry RegistryProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		alertStore := registry.AlertStore()
		silenceStore := registry.SilenceStore()

		switch r.Method {
		case http.MethodGet:
			handleAlertsGet(alertStore, silenceStore, w, r)
		case http.MethodPost:
			handleAlertsPost(registry.AlertProcessor(), alertStore, silenceStore, w, r)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func handleAlertsGet(store *memory.AlertStore, silences *memory.SilenceStore, w http.ResponseWriter, r *http.Request) {
	status := parseAlertsStatusQuery(r.URL.Query().Get("status"))
	includeResolved := parseBoolQueryLenient(r.URL.Query().Get("resolved"), false)
	if status == "resolved" {
		includeResolved = true
	}

	// For now, simple list. Advanced filtering (regex, matchers) will be added later
	// as we migrate more helpers from main.go
	alerts := store.List(status, includeResolved)

	now := time.Now().UTC()
	gettableAlerts := make([]core.APIGettableAlert, 0, len(alerts))
	for _, alert := range alerts {
		gettableAlerts = append(gettableAlerts, toGettableAlert(alert, silences, now))
	}

	writeJSON(w, http.StatusOK, gettableAlerts)
}

func AlertGroupsHandler(registry RegistryProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		queryParams := r.URL.Query()
		groupBy := queryParams["group_by"]

		groups := registry.AlertStore().GroupAlerts(groupBy)
		writeJSON(w, http.StatusOK, groups)
	}
}

func handleAlertsPost(processor *services.AlertProcessor, store *memory.AlertStore, silences *memory.SilenceStore, w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	if processor == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "alert processor is not available",
		})
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 10*1024*1024))
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{
			"error": "request payload too large",
		})
		return
	}

	now := time.Now().UTC()
	alerts, err := parseAlertsForProcessing(body, now)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": err.Error(),
		})
		return
	}

	filteredAlerts := make([]*core.Alert, 0, len(alerts))
	for _, alert := range alerts {
		if alert.Status != core.StatusResolved && silences != nil && silences.HasActiveMatch(alert.Labels, now) {
			continue
		}
		filteredAlerts = append(filteredAlerts, alert)
	}

	successfulInputs := make([]core.AlertIngestInput, 0, len(filteredAlerts))
	failedCount := 0
	for _, alert := range filteredAlerts {
		if err := processor.ProcessAlert(r.Context(), alert); err != nil {
			failedCount++
			continue
		}
		successfulInputs = append(successfulInputs, toAlertIngestInput(alert))
	}

	if len(successfulInputs) > 0 {
		if err := store.IngestBatch(successfulInputs, now); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": err.Error(),
			})
			return
		}
	}

	if failedCount == 0 {
		w.WriteHeader(http.StatusOK)
		return
	}

	if len(successfulInputs) > 0 {
		writeJSON(w, http.StatusMultiStatus, map[string]int{
			"received":  len(alerts),
			"processed": len(successfulInputs),
			"failed":    failedCount,
		})
		return
	}

	writeJSON(w, http.StatusInternalServerError, map[string]any{
		"error":    "all alerts failed to process",
		"received": len(alerts),
		"failed":   failedCount,
	})
}

func parseAlertsForProcessing(body []byte, now time.Time) ([]*core.Alert, error) {
	if alerts, err := parsePrometheusAlerts(body); err == nil {
		return alerts, nil
	}

	return parseLegacyAlerts(body, now)
}

func parsePrometheusAlerts(body []byte) ([]*core.Alert, error) {
	parser := webhook.NewPrometheusParser()
	parsedWebhook, err := parser.Parse(body)
	if err != nil {
		return nil, err
	}

	validation := parser.Validate(parsedWebhook)
	if !validation.Valid {
		return nil, fmt.Errorf("invalid prometheus alert payload")
	}

	return parser.ConvertToDomain(parsedWebhook)
}

func parseLegacyAlerts(body []byte, now time.Time) ([]*core.Alert, error) {
	payload, err := parseAlertIngestPayload(body)
	if err != nil {
		return nil, err
	}

	alerts := make([]*core.Alert, 0, len(payload))
	for i, in := range payload {
		alert, err := convertIngestInputToAlert(in, now)
		if err != nil {
			return nil, fmt.Errorf("alert[%d]: %w", i, err)
		}
		alerts = append(alerts, alert)
	}

	return alerts, nil
}

func convertIngestInputToAlert(in core.AlertIngestInput, now time.Time) (*core.Alert, error) {
	startsAt, err := parseAlertTime(in.StartsAt)
	if err != nil {
		return nil, fmt.Errorf("invalid startsAt: %w", err)
	}
	if startsAt.IsZero() {
		startsAt = now
	}

	endsAt, err := parseOptionalAlertTime(in.EndsAt)
	if err != nil {
		return nil, fmt.Errorf("invalid endsAt: %w", err)
	}

	alertName := strings.TrimSpace(in.Labels["alertname"])
	if alertName == "" {
		return nil, fmt.Errorf("missing required label alertname")
	}

	status := normalizeAlertStatus(in.Status, endsAt, now)
	fingerprint := strings.TrimSpace(in.Fingerprint)
	if fingerprint == "" {
		fingerprint = labelsFingerprint(in.Labels)
	}

	var generatorURL *string
	if trimmed := strings.TrimSpace(in.GeneratorURL); trimmed != "" {
		generatorURL = &trimmed
	}

	return &core.Alert{
		Fingerprint:  fingerprint,
		AlertName:    alertName,
		Status:       core.AlertStatus(status),
		Labels:       cloneStringMap(in.Labels),
		Annotations:  cloneStringMap(in.Annotations),
		StartsAt:     startsAt,
		EndsAt:       endsAt,
		GeneratorURL: generatorURL,
		Timestamp:    &now,
	}, nil
}

func toAlertIngestInput(alert *core.Alert) core.AlertIngestInput {
	in := core.AlertIngestInput{
		Labels:      cloneStringMap(alert.Labels),
		Annotations: cloneStringMap(alert.Annotations),
		StartsAt:    alert.StartsAt.UTC().Format(time.RFC3339),
		Status:      string(alert.Status),
		Fingerprint: alert.Fingerprint,
	}
	if alert.EndsAt != nil {
		in.EndsAt = alert.EndsAt.UTC().Format(time.RFC3339)
	}
	if alert.GeneratorURL != nil {
		in.GeneratorURL = *alert.GeneratorURL
	}

	return in
}

func parseAlertTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}

	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}

func parseOptionalAlertTime(raw string) (*time.Time, error) {
	t, err := parseAlertTime(raw)
	if err != nil {
		return nil, err
	}
	if t.IsZero() {
		return nil, nil
	}
	return &t, nil
}

func normalizeAlertStatus(raw string, endsAt *time.Time, now time.Time) string {
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

func labelsFingerprint(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}

	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var builder strings.Builder
	for _, key := range keys {
		builder.WriteString(key)
		builder.WriteByte('=')
		builder.WriteString(labels[key])
		builder.WriteByte('|')
	}

	sum := sha256.Sum256([]byte(builder.String()))
	return hex.EncodeToString(sum[:16])
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return map[string]string{}
	}

	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

// Helpers (temporarily here, should move to internal/application/handlers/common.go)

func toGettableAlert(alert core.APIAlert, silences *memory.SilenceStore, now time.Time) core.APIGettableAlert {
	silencedBy := make([]string, 0)
	if alert.Status == "firing" && silences != nil {
		silencedBy = silences.ActiveMatchingSilenceIDs(alert.Labels, now)
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
		Fingerprint:  alert.Fingerprint,
		Status: core.APIAlertStatus{
			State:      state,
			SilencedBy: silencedBy,
		},
	}
}

func parseAlertsStatusQuery(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "firing":
		return "firing"
	case "resolved":
		return "resolved"
	default:
		return ""
	}
}

func parseBoolQueryLenient(raw string, def bool) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def
	}
	switch strings.ToLower(raw) {
	case "true", "1", "yes":
		return true
	case "false", "0", "no":
		return false
	default:
		return false
	}
}

func parseAlertIngestPayload(body []byte) ([]core.AlertIngestInput, error) {
	var alerts []core.AlertIngestInput
	if err := json.Unmarshal(body, &alerts); err == nil {
		if len(alerts) > 0 {
			return alerts, nil
		}
	}

	var envelope struct {
		Alerts []core.AlertIngestInput `json:"alerts"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil && len(envelope.Alerts) > 0 {
		return envelope.Alerts, nil
	}

	return nil, errors.New("invalid alert payload")
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if payload != nil {
		_ = json.NewEncoder(w).Encode(payload)
	}
}
