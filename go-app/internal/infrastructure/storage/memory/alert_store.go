package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

type AlertStore struct {
	mu sync.RWMutex
	// all keeps last known state by dedup key (firing/resolved).
	all map[string]*core.StoredAlertState
	// activeByBase indexes currently firing alerts by base fingerprint.
	activeByBase map[string]map[string]struct{}
	onChange     func()
}

func NewAlertStore() *AlertStore {
	return &AlertStore{
		all:          make(map[string]*core.StoredAlertState),
		activeByBase: make(map[string]map[string]struct{}),
	}
}

func (s *AlertStore) IngestBatch(inputs []core.AlertIngestInput, now time.Time) error {
	return s.ingestBatchInternal(inputs, now, true)
}

func (s *AlertStore) ingestBatchInternal(inputs []core.AlertIngestInput, now time.Time, notify bool) error {
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

func (s *AlertStore) SetOnChange(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onChange = fn
}

func (s *AlertStore) notifyChange() {
	s.mu.RLock()
	fn := s.onChange
	s.mu.RUnlock()

	if fn != nil {
		fn()
	}
}

func (s *AlertStore) apply(in *core.StoredAlertState, now time.Time) {
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

func (s *AlertStore) resolveAlertLocked(in *core.StoredAlertState, now time.Time) {
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

func (s *AlertStore) markActiveLocked(baseFingerprint, dedupKey string) {
	if _, ok := s.activeByBase[baseFingerprint]; !ok {
		s.activeByBase[baseFingerprint] = make(map[string]struct{})
	}
	s.activeByBase[baseFingerprint][dedupKey] = struct{}{}
}

func (s *AlertStore) List(statusFilter string, includeResolved bool) []core.APIAlert {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]core.APIAlert, 0, len(s.all))
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
		return out[i].Fingerprint < out[j].Fingerprint
	})

	return out
}

func (s *AlertStore) ExportForPersistence() []core.APIAlert {
	return s.List("", true)
}

func (s *AlertStore) Stats() (total, firing, resolved int) {
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

func (s *AlertStore) GroupAlerts(groupBy []string) []core.APIGettableAlertGroup {
	s.mu.RLock()
	defer s.mu.RUnlock()

	groups := make(map[string]*core.APIGettableAlertGroup)
	now := time.Now().UTC()

	for _, a := range s.all {
		// Calculate grouping labels and key
		groupLabels := make(map[string]string)
		var keyBuilder strings.Builder

		sortedGroupBy := make([]string, len(groupBy))
		copy(sortedGroupBy, groupBy)
		sort.Strings(sortedGroupBy)

		for _, l := range sortedGroupBy {
			val := a.Labels[l]
			groupLabels[l] = val
			keyBuilder.WriteString(l)
			keyBuilder.WriteByte('=')
			keyBuilder.WriteString(val)
			keyBuilder.WriteByte('|')
		}
		key := keyBuilder.String()

		group, ok := groups[key]
		if !ok {
			group = &core.APIGettableAlertGroup{
				Labels:   groupLabels,
				Receiver: core.APIReceiver{Name: "default"},
				Alerts:   make([]core.APIGettableAlert, 0),
			}
			groups[key] = group
		}

		gettable := toGettableAlert(toAPIAlert(a), now)
		group.Alerts = append(group.Alerts, gettable)
	}

	out := make([]core.APIGettableAlertGroup, 0, len(groups))
	for _, g := range groups {
		out = append(out, *g)
	}

	sort.Slice(out, func(i, j int) bool {
		return labelsFingerprint(out[i].Labels) < labelsFingerprint(out[j].Labels)
	})

	return out
}

func toGettableAlert(alert core.APIAlert, now time.Time) core.APIGettableAlert {
	state := "active"
	if alert.Status == "resolved" {
		state = "unprocessed"
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
			SilencedBy: []string{},
		},
	}
}

func (s *AlertStore) RestoreFromPersistence(alerts []core.APIAlert, now time.Time) error {
	if len(alerts) == 0 {
		return nil
	}

	inputs := make([]core.AlertIngestInput, 0, len(alerts))
	for i, alert := range alerts {
		if strings.TrimSpace(alert.StartsAt) == "" {
			return fmt.Errorf("persisted alert[%d]: startsAt is required", i)
		}

		in := core.AlertIngestInput{
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

// Internal helpers

func normalizeIngestInput(in core.AlertIngestInput, now time.Time) (*core.StoredAlertState, error) {
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
	return &core.StoredAlertState{
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

func toAPIAlert(a *core.StoredAlertState) core.APIAlert {
	var endsAt *string
	if a.EndsAt != nil {
		s := a.EndsAt.UTC().Format(time.RFC3339)
		endsAt = &s
	}
	receiverName := strings.TrimSpace(a.Labels["receiver"])
	if receiverName == "" {
		receiverName = "default"
	}
	return core.APIAlert{
		Labels:       cloneStringMap(a.Labels),
		Annotations:  cloneStringMap(a.Annotations),
		Receivers:    []core.APIReceiver{{Name: receiverName}},
		StartsAt:     a.StartsAt.UTC().Format(time.RFC3339),
		UpdatedAt:    a.UpdatedAt.UTC().Format(time.RFC3339),
		EndsAt:       endsAt,
		GeneratorURL: a.GeneratorURL,
		Fingerprint:  a.BaseFingerprint,
		Status:       a.Status,
	}
}

func isSameAlertPayload(a, b *core.StoredAlertState) bool {
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
