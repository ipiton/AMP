package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/core/alertconv"
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

	existing.Labels = alertconv.CloneStringMap(in.Labels)
	existing.Annotations = alertconv.CloneStringMap(in.Annotations)
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
			existing.Annotations = alertconv.CloneStringMap(in.Annotations)
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

// AlertGrouping is how ONE alert aggregates: the receiver its route sends it
// to, and the label names that form its group.
type AlertGrouping struct {
	Receiver string
	GroupBy  []string
}

// AlertGroupingResolver answers AlertGrouping for an alert's label set.
//
// It exists so GroupAlerts can reflect the ROUTE TREE (final review finding
// 17): the receiver and group_by both depend on which route an alert matched,
// so they cannot be single values passed in once for the whole store. A nil
// resolver, or one returning an empty Receiver, falls back to "default".
type AlertGroupingResolver func(labels map[string]string) AlertGrouping

// GroupAlerts groups the stored alerts the way the route tree says they should
// be grouped, via resolve. The silences matcher (nil-tolerant) is used so
// groups report the same state/silencedBy as GET /api/v2/alerts.
//
// Final review finding 17: this used to take a single groupBy slice (sourced
// only from the `?group_by=` query parameter, so `labels` came back as `{}` for
// every group on a plain request) and a single receiver name (the FIRST
// configured receiver, hardcoded for every group regardless of routing). Both
// are per-route properties upstream: two groups routed to different receivers
// must report different receivers, and a group's labels come from its route's
// own group_by.
//
// The receiver is part of the aggregation key, matching upstream's per-route
// aggrGroups: the same label set routed to two receivers is two groups.
func (s *AlertStore) GroupAlerts(resolve AlertGroupingResolver, silences alertconv.SilenceMatcher) []core.APIGettableAlertGroup {
	s.mu.RLock()
	defer s.mu.RUnlock()

	groups := make(map[string]*core.APIGettableAlertGroup)
	now := time.Now().UTC()

	for _, a := range s.all {
		// Upstream groups cover active alerts only; resolved entries are kept
		// in the store for /api/v2/alerts?resolved=true but not grouped.
		if a.Status == "resolved" {
			continue
		}

		grouping := AlertGrouping{}
		if resolve != nil {
			grouping = resolve(a.Labels)
		}
		receiver := grouping.Receiver
		if receiver == "" {
			receiver = "default"
		}

		// Calculate grouping labels and key
		groupLabels := make(map[string]string)
		var keyBuilder strings.Builder
		keyBuilder.WriteString("receiver=")
		keyBuilder.WriteString(receiver)
		keyBuilder.WriteByte('/')

		sortedGroupBy := make([]string, len(grouping.GroupBy))
		copy(sortedGroupBy, grouping.GroupBy)
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
				Receiver: core.APIReceiver{Name: receiver},
				Alerts:   make([]core.APIGettableAlert, 0),
			}
			groups[key] = group
		}

		gettable := alertconv.ToGettableAlert(toAPIAlert(a), silences, now)
		group.Alerts = append(group.Alerts, gettable)
	}

	out := make([]core.APIGettableAlertGroup, 0, len(groups))
	for _, g := range groups {
		out = append(out, *g)
	}

	// Receiver breaks the tie: now that it is part of the aggregation key, two
	// groups can share an identical label set (and therefore fingerprint) while
	// routing to different receivers — without this the response order for
	// those two would be nondeterministic.
	sort.Slice(out, func(i, j int) bool {
		fi, fj := alertconv.Fingerprint(out[i].Labels), alertconv.Fingerprint(out[j].Labels)
		if fi != fj {
			return fi < fj
		}
		return out[i].Receiver.Name < out[j].Receiver.Name
	})

	return out
}

// RestoreFromPersistence rehydrates the in-memory store from persisted domain
// alerts after a restart. It intentionally skips onChange notifications and
// takes []*core.Alert directly — no APIAlert/AlertIngestInput string
// round-trip (DTO-FRAGMENTATION item 3).
func (s *AlertStore) RestoreFromPersistence(alerts []*core.Alert, now time.Time) error {
	for i, alert := range alerts {
		if alert == nil {
			continue
		}
		if alert.StartsAt.IsZero() {
			return fmt.Errorf("persisted alert[%d]: startsAt is required", i)
		}
		s.apply(storedStateFromAlert(alert, now), now)
	}
	return nil
}

// storedStateFromAlert converts a persisted domain alert into the internal
// stored state, applying the same normalization as normalizeIngestInput.
func storedStateFromAlert(alert *core.Alert, now time.Time) *core.StoredAlertState {
	labels := alertconv.CloneStringMap(alert.Labels)
	annotations := alertconv.CloneStringMap(alert.Annotations)

	baseFingerprint := strings.TrimSpace(alert.Fingerprint)
	if baseFingerprint == "" {
		baseFingerprint = alertconv.Fingerprint(labels)
	}
	if baseFingerprint == "" {
		baseFingerprint = shortHash(alert.StartsAt.UTC().Format(time.RFC3339Nano))
	}

	endsAt := cloneTimePtr(alert.EndsAt)
	generatorURL := ""
	if alert.GeneratorURL != nil {
		generatorURL = strings.TrimSpace(*alert.GeneratorURL)
	}

	return &core.StoredAlertState{
		DedupKey:        dedupKey(baseFingerprint, alert.StartsAt),
		BaseFingerprint: baseFingerprint,
		Labels:          labels,
		Annotations:     annotations,
		StartsAt:        alert.StartsAt.UTC(),
		EndsAt:          endsAt,
		GeneratorURL:    generatorURL,
		Status:          alertconv.NormalizeStatus(string(alert.Status), endsAt, now),
		UpdatedAt:       now.UTC(),
	}
}

// Internal helpers

func normalizeIngestInput(in core.AlertIngestInput, now time.Time) (*core.StoredAlertState, error) {
	startsAt, err := alertconv.ParseAlertTime(in.StartsAt)
	if err != nil {
		return nil, fmt.Errorf("invalid startsAt: %w", err)
	}
	if startsAt.IsZero() {
		startsAt = now
	}

	endsAt, err := alertconv.ParseOptionalAlertTime(in.EndsAt)
	if err != nil {
		return nil, fmt.Errorf("invalid endsAt: %w", err)
	}

	status := alertconv.NormalizeStatus(in.Status, endsAt, now)
	labels := alertconv.CloneStringMap(in.Labels)
	annotations := alertconv.CloneStringMap(in.Annotations)

	baseFingerprint := strings.TrimSpace(in.Fingerprint)
	if baseFingerprint == "" {
		baseFingerprint = alertconv.Fingerprint(labels)
	}
	if baseFingerprint == "" {
		baseFingerprint = shortHash(startsAt.UTC().Format(time.RFC3339Nano))
	}

	// GeneratorURL is stored as-is (trimmed). The database persists it
	// unvalidated, so silently dropping an unparsable URL here would make the
	// memory view lie about the persisted data.
	generatorURL := strings.TrimSpace(in.GeneratorURL)

	return &core.StoredAlertState{
		DedupKey:        dedupKey(baseFingerprint, startsAt),
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
		Labels:       alertconv.CloneStringMap(a.Labels),
		Annotations:  alertconv.CloneStringMap(a.Annotations),
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

func cloneTimePtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := *t
	return &v
}
