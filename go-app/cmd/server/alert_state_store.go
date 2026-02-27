package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type alertIngestInput struct {
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     string            `json:"startsAt"`
	EndsAt       string            `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
	Fingerprint  string            `json:"fingerprint"`
	Status       string            `json:"status"`
}

type storedAlert struct {
	DedupKey        string
	BaseFingerprint string
	Labels          map[string]string
	Annotations     map[string]string
	StartsAt        time.Time
	EndsAt          *time.Time
	GeneratorURL    string
	Status          string
	UpdatedAt       time.Time
}

type apiAlert struct {
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations,omitempty"`
	Receivers    []apiReceiver     `json:"receivers,omitempty"`
	StartsAt     string            `json:"startsAt"`
	UpdatedAt    string            `json:"updatedAt,omitempty"`
	EndsAt       *string           `json:"endsAt,omitempty"`
	GeneratorURL string            `json:"generatorURL,omitempty"`
	Fingerprint  string            `json:"fingerprint,omitempty"`
	Status       string            `json:"status"`
}

type alertStore struct {
	mu sync.RWMutex
	// all keeps last known state by dedup key (firing/resolved).
	all map[string]*storedAlert
	// activeByBase indexes currently firing alerts by base fingerprint.
	activeByBase map[string]map[string]struct{}
	onChange     func()
}

type alertAPIError struct {
	status  int
	payload any
	message string
}

func (e *alertAPIError) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.message) != "" {
		return e.message
	}
	switch p := e.payload.(type) {
	case string:
		return p
	case map[string]any:
		if msg, ok := p["message"].(string); ok && strings.TrimSpace(msg) != "" {
			return msg
		}
	}
	return "alert api error"
}

func newAlertAPIError(status int, payload any, message string) *alertAPIError {
	return &alertAPIError{
		status:  status,
		payload: payload,
		message: strings.TrimSpace(message),
	}
}

func newAlertCodeMessageError(status int, code int, message string) *alertAPIError {
	return newAlertAPIError(status, map[string]any{
		"code":    code,
		"message": message,
	}, message)
}

func newAlertStringError(status int, message string) *alertAPIError {
	return newAlertAPIError(status, message, message)
}

func newAlertStore() *alertStore {
	return &alertStore{
		all:          make(map[string]*storedAlert),
		activeByBase: make(map[string]map[string]struct{}),
	}
}

func (s *alertStore) ingestBatch(inputs []alertIngestInput, now time.Time) error {
	return s.ingestBatchInternal(inputs, now, true)
}

func (s *alertStore) ingestBatchInternal(inputs []alertIngestInput, now time.Time, notify bool) error {
	if len(inputs) == 0 {
		return nil
	}

	for i := range inputs {
		norm, err := normalizeIngestInput(inputs[i], now)
		if err != nil {
			return fmt.Errorf("alert[%d]: %w", i, err)
		}
		s.apply(norm, now)
	}

	if notify {
		s.notifyChange()
	}
	return nil
}

func (s *alertStore) setOnChange(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onChange = fn
}

func (s *alertStore) notifyChange() {
	s.mu.RLock()
	fn := s.onChange
	s.mu.RUnlock()

	if fn != nil {
		fn()
	}
}

func (s *alertStore) apply(in *storedAlert, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Resolved must close firing by dedup key, and fallback by base fingerprint.
	if in.Status == "resolved" {
		s.resolveAlertLocked(in, now)
		return
	}

	// Firing path: create/update idempotently by dedup key.
	existing, ok := s.all[in.DedupKey]
	if !ok {
		s.all[in.DedupKey] = in
		s.markActiveLocked(in.BaseFingerprint, in.DedupKey)
		return
	}

	if isSameAlertPayload(existing, in) {
		return // exact duplicate
	}

	existing.Labels = cloneStringMap(in.Labels)
	existing.Annotations = cloneStringMap(in.Annotations)
	existing.StartsAt = in.StartsAt
	existing.EndsAt = cloneTimePtr(in.EndsAt)
	existing.GeneratorURL = in.GeneratorURL
	existing.Status = "firing"
	existing.UpdatedAt = now
	s.markActiveLocked(existing.BaseFingerprint, existing.DedupKey)
}

func (s *alertStore) resolveAlertLocked(in *storedAlert, now time.Time) {
	keys := make([]string, 0, 1)
	if _, ok := s.all[in.DedupKey]; ok {
		keys = append(keys, in.DedupKey)
	} else if activeSet, ok := s.activeByBase[in.BaseFingerprint]; ok {
		for k := range activeSet {
			keys = append(keys, k)
		}
	}

	// No active firing found: still persist resolved snapshot for history/idempotency.
	if len(keys) == 0 {
		existing, ok := s.all[in.DedupKey]
		if ok && isSameAlertPayload(existing, in) {
			return
		}
		s.all[in.DedupKey] = in
		return
	}

	endsAt := in.EndsAt
	if endsAt == nil {
		t := now
		endsAt = &t
	}

	for _, key := range keys {
		existing, ok := s.all[key]
		if !ok {
			continue
		}
		existing.Status = "resolved"
		existing.EndsAt = cloneTimePtr(endsAt)
		if len(in.Annotations) > 0 {
			existing.Annotations = cloneStringMap(in.Annotations)
		}
		if in.GeneratorURL != "" {
			existing.GeneratorURL = in.GeneratorURL
		}
		existing.UpdatedAt = now

		if activeSet, ok := s.activeByBase[existing.BaseFingerprint]; ok {
			delete(activeSet, key)
			if len(activeSet) == 0 {
				delete(s.activeByBase, existing.BaseFingerprint)
			}
		}
	}
}

func (s *alertStore) markActiveLocked(baseFingerprint, dedupKey string) {
	if _, ok := s.activeByBase[baseFingerprint]; !ok {
		s.activeByBase[baseFingerprint] = make(map[string]struct{})
	}
	s.activeByBase[baseFingerprint][dedupKey] = struct{}{}
}

func (s *alertStore) list(statusFilter string, includeResolved bool) []apiAlert {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]apiAlert, 0, len(s.all))
	for _, a := range s.all {
		if statusFilter != "" && a.Status != statusFilter {
			continue
		}
		if statusFilter == "" && !includeResolved && a.Status == "resolved" {
			continue
		}
		out = append(out, toAPIAlert(a))
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].StartsAt != out[j].StartsAt {
			return out[i].StartsAt > out[j].StartsAt
		}
		return out[i].Fingerprint < out[j].Fingerprint
	})

	return out
}

func (s *alertStore) exportForPersistence() []apiAlert {
	return s.list("", true)
}

func (s *alertStore) stats() (total, firing, resolved int) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, alert := range s.all {
		total++
		switch alert.Status {
		case "resolved":
			resolved++
		default:
			firing++
		}
	}
	return total, firing, resolved
}

func (s *alertStore) restoreFromPersistence(alerts []apiAlert, now time.Time) error {
	if len(alerts) == 0 {
		return nil
	}

	inputs := make([]alertIngestInput, 0, len(alerts))
	for i, alert := range alerts {
		if strings.TrimSpace(alert.StartsAt) == "" {
			return fmt.Errorf("persisted alert[%d]: startsAt is required", i)
		}

		in := alertIngestInput{
			Labels:       cloneStringMap(alert.Labels),
			Annotations:  cloneStringMap(alert.Annotations),
			StartsAt:     alert.StartsAt,
			GeneratorURL: alert.GeneratorURL,
			Fingerprint:  alert.Fingerprint,
			Status:       alert.Status,
		}
		if alert.EndsAt != nil {
			in.EndsAt = *alert.EndsAt
		}
		inputs = append(inputs, in)
	}

	return s.ingestBatchInternal(inputs, now, false)
}

func normalizeIngestInput(in alertIngestInput, now time.Time) (*storedAlert, error) {
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

	status := normalizeStatus(in.Status, endsAt, now)
	labels := cloneStringMap(in.Labels)
	if labels == nil {
		labels = map[string]string{}
	}
	annotations := cloneStringMap(in.Annotations)
	if annotations == nil {
		annotations = map[string]string{}
	}

	baseFingerprint := strings.TrimSpace(in.Fingerprint)
	if baseFingerprint == "" {
		baseFingerprint = labelsFingerprint(labels)
	}
	if baseFingerprint == "" {
		baseFingerprint = shortHash(startsAt.UTC().Format(time.RFC3339Nano))
	}

	generatorURL := strings.TrimSpace(in.GeneratorURL)
	if generatorURL != "" {
		if _, err := url.ParseRequestURI(generatorURL); err != nil {
			generatorURL = ""
		}
	}

	dedupKey := dedupKey(baseFingerprint, startsAt)
	return &storedAlert{
		DedupKey:        dedupKey,
		BaseFingerprint: baseFingerprint,
		Labels:          labels,
		Annotations:     annotations,
		StartsAt:        startsAt.UTC(),
		EndsAt:          cloneTimePtr(endsAt),
		GeneratorURL:    generatorURL,
		Status:          status,
		UpdatedAt:       now.UTC(),
	}, nil
}

func parseAlertTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err == nil {
		return t, nil
	}

	// Alertmanager accepts date-only timestamps (YYYY-MM-DD) in ingest payloads.
	t, dateErr := time.Parse("2006-01-02", raw)
	if dateErr != nil {
		return time.Time{}, dateErr
	}
	return t, nil
}

func parseOptionalAlertTime(raw string) (*time.Time, error) {
	t, err := parseAlertTime(raw)
	if err != nil {
		return nil, err
	}
	if t.IsZero() {
		return nil, nil
	}
	tt := t.UTC()
	return &tt, nil
}

func normalizeStatus(raw string, endsAt *time.Time, now time.Time) string {
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
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(labels[k])
		b.WriteByte('|')
	}
	return shortHash(b.String())
}

func dedupKey(baseFingerprint string, startsAt time.Time) string {
	return shortHash(baseFingerprint + "|" + startsAt.UTC().Format(time.RFC3339Nano))
}

func shortHash(input string) string {
	sum := sha256.Sum256([]byte(input))
	return hex.EncodeToString(sum[:16])
}

func toAPIAlert(a *storedAlert) apiAlert {
	var endsAt *string
	if a.EndsAt != nil {
		s := formatAPITimestamp(a.EndsAt.UTC())
		endsAt = &s
	}
	receiverName := strings.TrimSpace(a.Labels["receiver"])
	if receiverName == "" {
		receiverName = "default"
	}
	return apiAlert{
		Labels:       cloneStringMap(a.Labels),
		Annotations:  cloneStringMap(a.Annotations),
		Receivers:    []apiReceiver{{Name: receiverName}},
		StartsAt:     formatAPITimestamp(a.StartsAt.UTC()),
		UpdatedAt:    formatAPITimestamp(a.UpdatedAt.UTC()),
		EndsAt:       endsAt,
		GeneratorURL: a.GeneratorURL,
		Fingerprint:  a.BaseFingerprint,
		Status:       a.Status,
	}
}

func isSameAlertPayload(a, b *storedAlert) bool {
	if a == nil || b == nil {
		return false
	}
	if a.Status != b.Status {
		return false
	}
	if !a.StartsAt.Equal(b.StartsAt) {
		return false
	}
	if !timePtrEqual(a.EndsAt, b.EndsAt) {
		return false
	}
	if a.GeneratorURL != b.GeneratorURL {
		return false
	}
	return mapStringEqual(a.Labels, b.Labels) && mapStringEqual(a.Annotations, b.Annotations)
}

func mapStringEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func timePtrEqual(a, b *time.Time) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Equal(*b)
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneTimePtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := *t
	return &v
}

func parseBoolWithDefault(raw string, def bool) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return def
	}
	return v
}

func parseAlertIngestPayload(body []byte) ([]alertIngestInput, error) {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return nil, newAlertCodeMessageError(http.StatusUnprocessableEntity, 602, "alerts in body is required")
	}

	var direct []alertIngestInput
	directErr := json.Unmarshal(body, &direct)
	if directErr == nil {
		return direct, nil
	}

	// Support grouped/envelope payloads for compatibility with clients that
	// proxy alerts through wrapper formats.
	var envelope struct {
		Alerts []alertIngestInput `json:"alerts"`
		Groups []struct {
			Alerts []alertIngestInput `json:"alerts"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, newAlertCodeMessageError(
			http.StatusBadRequest,
			http.StatusBadRequest,
			formatAlertBodyParseError(directErr),
		)
	}

	if len(envelope.Alerts) > 0 {
		return envelope.Alerts, nil
	}

	var grouped []alertIngestInput
	for i := range envelope.Groups {
		grouped = append(grouped, envelope.Groups[i].Alerts...)
	}
	if len(grouped) > 0 {
		return grouped, nil
	}

	return nil, newAlertCodeMessageError(
		http.StatusBadRequest,
		http.StatusBadRequest,
		formatAlertBodyParseError(directErr),
	)
}

func formatAlertBodyParseError(err error) string {
	msg := strings.TrimSpace(fmt.Sprint(err))
	if msg == "" {
		msg = "unknown error"
	}

	// Align payload type wording with upstream Alertmanager error messages.
	msg = strings.ReplaceAll(msg, "[]main.alertIngestInput", "models.PostableAlerts")
	return fmt.Sprintf("parsing alerts body from %q failed, because %s", "", msg)
}

func validateAlertIngestInputs(inputs []alertIngestInput) error {
	for i := range inputs {
		in := inputs[i]

		if in.Labels == nil {
			return newAlertCodeMessageError(
				http.StatusUnprocessableEntity,
				602,
				fmt.Sprintf("%d.labels in body is required", i),
			)
		}
		if len(in.Labels) == 0 {
			return newAlertStringError(http.StatusBadRequest, "at least one label pair required")
		}

		if raw := strings.TrimSpace(in.StartsAt); raw != "" {
			if _, err := parseAlertTime(raw); err != nil {
				return newAlertCodeMessageError(
					http.StatusBadRequest,
					http.StatusBadRequest,
					formatAlertBodyParseError(err),
				)
			}
		}
		if raw := strings.TrimSpace(in.EndsAt); raw != "" {
			if _, err := parseAlertTime(raw); err != nil {
				return newAlertCodeMessageError(
					http.StatusBadRequest,
					http.StatusBadRequest,
					formatAlertBodyParseError(err),
				)
			}
		}

		generatorURL := strings.TrimSpace(in.GeneratorURL)
		if generatorURL != "" {
			if _, err := url.ParseRequestURI(generatorURL); err != nil {
				return newAlertCodeMessageError(
					http.StatusUnprocessableEntity,
					601,
					fmt.Sprintf("%d.generatorURL in body must be of type uri: %q", i, in.GeneratorURL),
				)
			}
		}
	}

	return nil
}
