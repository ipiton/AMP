package memory

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/ipiton/AMP/internal/core"
)

type SilenceStore struct {
	mu       sync.RWMutex
	silences map[string]*core.StoredSilenceState
	onChange func()
}

func NewSilenceStore() *SilenceStore {
	return &SilenceStore{
		silences: make(map[string]*core.StoredSilenceState),
	}
}

func (s *SilenceStore) CreateOrUpdate(in *core.SilenceInput, now time.Time) (string, error) {
	return s.createOrUpdateInternal(in, now, true, true, false)
}

// Upsert inserts or replaces a silence with a caller-provided ID without
// requiring the ID to already exist in the store. It is used by the DB-first
// write path (SPLIT-BRAIN-RISK slice 2): the persistent repository generates
// the silence ID, and the memory store mirrors it so that memory and database
// always agree on identifiers.
func (s *SilenceStore) Upsert(in *core.SilenceInput, now time.Time) (string, error) {
	return s.createOrUpdateInternal(in, now, true, false, false)
}

func (s *SilenceStore) createOrUpdateInternal(in *core.SilenceInput, now time.Time, notify, enforceExistingID, allowPastEndsAt bool) (string, error) {
	if in == nil {
		return "", fmt.Errorf("silence payload is required")
	}

	updateRequested := strings.TrimSpace(in.ID) != ""
	normalized, err := normalizeSilenceInput(in, now, allowPastEndsAt)
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	if enforceExistingID && updateRequested {
		if _, ok := s.silences[normalized.ID]; !ok {
			s.mu.Unlock()
			return "", fmt.Errorf("silence not found")
		}
	}
	s.silences[normalized.ID] = normalized
	s.mu.Unlock()

	if notify {
		s.notifyChange()
	}
	return normalized.ID, nil
}

func (s *SilenceStore) List(now time.Time) []core.APISilence {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]core.APISilence, 0, len(s.silences))
	for _, silence := range s.silences {
		out = append(out, toAPISilence(silence, now))
	}

	sortSilencesForList(out)

	return out
}

func (s *SilenceStore) Get(id string, now time.Time) (core.APISilence, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	silence, ok := s.silences[id]
	if !ok {
		return core.APISilence{}, false
	}

	return toAPISilence(silence, now), true
}

// Expire transitions a silence to the "expired" state in place, instead of
// removing it, matching upstream Alertmanager's DELETE /api/v2/silence/{id}
// semantics (silence/silence.go's expire(), v0.34.0): it forces early expiry
// by moving EndsAt to now if it hasn't already passed, AND — this is the
// part easy to get wrong — ALSO moves StartsAt to now if the silence was
// still pending (StartsAt in the future). Upstream does both unconditionally
// on expire() specifically so a pending silence becomes "expired"
// immediately instead of sitting in "pending" until its original StartsAt
// arrives and only then flipping to "expired". Both moves use the
// "never later than current value" direction (min), so an already-active or
// already-expired silence is left alone — this is naturally idempotent.
//
// The silence stays in the store, so it remains queryable via GET
// /api/v2/silences with status.state == "expired" until the store's
// retention/GC policy removes it (there is none for the memory-only/lite
// profile: an already-expired-by-time silence was never evicted here either
// — only an explicit Delete call removed entries, so this keeps that same
// lifetime characteristic). Returns false if the ID does not exist.
func (s *SilenceStore) Expire(id string, now time.Time) bool {
	s.mu.Lock()
	silence, ok := s.silences[id]
	if !ok {
		s.mu.Unlock()
		return false
	}

	now = now.UTC()
	if now.Before(silence.StartsAt) {
		silence.StartsAt = now
	}
	if now.Before(silence.EndsAt) {
		silence.EndsAt = now
	}
	silence.UpdatedAt = now
	s.mu.Unlock()

	s.notifyChange()
	return true
}

func (s *SilenceStore) Delete(id string) bool {
	s.mu.Lock()
	if _, ok := s.silences[id]; !ok {
		s.mu.Unlock()
		return false
	}

	delete(s.silences, id)
	s.mu.Unlock()
	s.notifyChange()
	return true
}

func (s *SilenceStore) SetOnChange(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onChange = fn
}

func (s *SilenceStore) notifyChange() {
	s.mu.RLock()
	fn := s.onChange
	s.mu.RUnlock()

	if fn != nil {
		fn()
	}
}

func (s *SilenceStore) ExportForPersistence(now time.Time) []core.APISilence {
	return s.List(now)
}

// UpsertFromAPI mirrors a single silence (already in its external API
// shape) into the store. It exists for callers that already hold a
// core.APISilence — e.g. the cross-replica event subscriber (task 6.3),
// which derives one from repo.GetSilenceByID + handlers.DomainSilenceToAPI —
// and would otherwise have to hand-build a core.SilenceInput just to call
// Upsert.
//
// Unlike Upsert, this allows an EndsAt already in the past (F3,
// alertmanager-parity amtool audit): item is a direct mirror of a row the
// database already accepted, which may legitimately be expired-in-place
// (handleSilenceDelete's ExpireSilence) or naturally elapsed by the time
// this replica applies the event/resync. Rejecting it here would silently
// re-introduce the very bug the expire-in-place fix closes — the mirror
// would just fail (see the caller's error log) and the local cache would
// disagree with the database until the next resync.
func (s *SilenceStore) UpsertFromAPI(item core.APISilence, now time.Time) (string, error) {
	return s.createOrUpdateInternal(apiSilenceToInput(item), now, true, false, true)
}

// Rebuild replaces the entire in-memory silence set with items in a single
// atomic swap (task 6.3: full resync after a subscription (re)connect).
//
// Unlike RestoreFromPersistence — additive, used once at boot when the store
// is guaranteed empty — Rebuild also evicts entries NOT present in items, so
// it correctly converges a store that may hold stale data (e.g. a silence
// deleted on another replica while this replica's pub/sub subscription was
// down).
func (s *SilenceStore) Rebuild(items []core.APISilence, now time.Time) error {
	fresh := make(map[string]*core.StoredSilenceState, len(items))
	for i, item := range items {
		normalized, err := normalizeSilenceInput(apiSilenceToInput(item), now, true)
		if err != nil {
			return fmt.Errorf("rebuild silence[%d]: %w", i, err)
		}
		fresh[normalized.ID] = normalized
	}

	s.mu.Lock()
	s.silences = fresh
	s.mu.Unlock()

	s.notifyChange()
	return nil
}

func (s *SilenceStore) Stats(now time.Time) (total, active, pending, expired int) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, silence := range s.silences {
		total++
		switch silenceState(silence, now) {
		case "active":
			active++
		case "pending":
			pending++
		case "expired":
			expired++
		}
	}
	return total, active, pending, expired
}

func (s *SilenceStore) RestoreFromPersistence(items []core.APISilence, now time.Time) error {
	for i, item := range items {
		if _, err := s.createOrUpdateInternal(apiSilenceToInput(item), now, false, false, true); err != nil {
			return fmt.Errorf("persisted silence[%d]: %w", i, err)
		}
	}
	return nil
}

// apiSilenceToInput converts an external-shape core.APISilence back into a
// core.SilenceInput, the shape normalizeSilenceInput/createOrUpdateInternal
// consume. Shared by RestoreFromPersistence, UpsertFromAPI, and Rebuild.
func apiSilenceToInput(item core.APISilence) *core.SilenceInput {
	matchers := make([]core.SilenceMatcherInput, 0, len(item.Matchers))
	for _, matcher := range item.Matchers {
		isEqual := matcher.IsEqual
		matchers = append(matchers, core.SilenceMatcherInput{
			Name:    matcher.Name,
			Value:   matcher.Value,
			IsRegex: matcher.IsRegex,
			IsEqual: &isEqual,
		})
	}

	return &core.SilenceInput{
		ID:        item.ID,
		Matchers:  matchers,
		StartsAt:  item.StartsAt,
		EndsAt:    item.EndsAt,
		CreatedBy: item.CreatedBy,
		Comment:   item.Comment,
	}
}

func (s *SilenceStore) ActiveMatchingSilenceIDs(labels map[string]string, now time.Time) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(labels) == 0 || len(s.silences) == 0 {
		return nil
	}

	out := make([]string, 0, 1)
	for _, silence := range s.silences {
		if silenceState(silence, now) != "active" {
			continue
		}
		if silenceMatchesLabels(silence.Matchers, labels) {
			out = append(out, silence.ID)
		}
	}

	sort.Strings(out)
	return out
}

func (s *SilenceStore) HasActiveMatch(labels map[string]string, now time.Time) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(labels) == 0 || len(s.silences) == 0 {
		return false
	}

	for _, silence := range s.silences {
		if silenceState(silence, now) != "active" {
			continue
		}
		if silenceMatchesLabels(silence.Matchers, labels) {
			return true
		}
	}
	return false
}

// Internal helpers

func silenceMatchesLabels(matchers []core.StoredSilenceMatcher, labels map[string]string) bool {
	for _, matcher := range matchers {
		labelValue := labels[matcher.Name]

		match := false
		if matcher.IsRegex {
			re, err := regexp.Compile(matcher.Value)
			if err != nil {
				return false
			}
			match = re.MatchString(labelValue)
		} else {
			match = labelValue == matcher.Value
		}

		if matcher.IsEqual {
			if !match {
				return false
			}
		} else if match {
			return false
		}
	}

	return true
}

func normalizeSilenceInput(in *core.SilenceInput, now time.Time, allowPastEndsAt bool) (*core.StoredSilenceState, error) {
	id := strings.TrimSpace(in.ID)
	if id == "" {
		uid, err := uuid.NewRandom()
		if err != nil {
			return nil, fmt.Errorf("failed to generate silence id: %w", err)
		}
		id = uid.String()
	}

	startsAt := now.UTC()
	if startsAtRaw := strings.TrimSpace(in.StartsAt); startsAtRaw != "" {
		parsedStartsAt, err := time.Parse(time.RFC3339, startsAtRaw)
		if err != nil {
			return nil, err
		}
		startsAt = parsedStartsAt.UTC()
	}

	var endsAt time.Time
	if endsAtRaw := strings.TrimSpace(in.EndsAt); endsAtRaw != "" {
		parsedEndsAt, err := time.Parse(time.RFC3339, endsAtRaw)
		if err != nil {
			return nil, err
		}
		endsAt = parsedEndsAt.UTC()
	}
	// allowPastEndsAt marks a "mirror an already-persisted/already-forced
	// row" call site (UpsertFromAPI, Rebuild, RestoreFromPersistence) rather
	// than a genuine user-submitted create/update. Those trusted-mirror
	// paths must tolerate EndsAt == StartsAt: Expire (this package) and
	// PostgresSilenceRepository.ExpireSilence both force BOTH timestamps to
	// the same "now" for a pending silence, matching upstream Alertmanager's
	// expire() (silence/silence.go, v0.34.0) — a real, valid state that a
	// strict "must be strictly after" check would otherwise reject and
	// silently fail to mirror. A genuine create/update (allowPastEndsAt
	// false) keeps the strict rule: a zero-duration silence can't be
	// created via the API.
	if allowPastEndsAt {
		if endsAt.Before(startsAt) {
			return nil, fmt.Errorf("start time must be before end time")
		}
	} else if !endsAt.After(startsAt) {
		return nil, fmt.Errorf("start time must be before end time")
	}
	if !allowPastEndsAt && endsAt.Before(now.UTC()) {
		return nil, fmt.Errorf("end time can't be in the past")
	}

	if len(in.Matchers) == 0 {
		return nil, fmt.Errorf("at least 1 matcher is required")
	}

	matchers := make([]core.StoredSilenceMatcher, 0, len(in.Matchers))
	for i, matcher := range in.Matchers {
		name := strings.TrimSpace(matcher.Name)
		if name == "" {
			return nil, fmt.Errorf("matcher %d: invalid label name", i)
		}

		isEqual := true
		if matcher.IsEqual != nil {
			isEqual = *matcher.IsEqual
		}
		value := matcher.Value

		if strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("at least one matcher must not match the empty string")
		}

		if matcher.IsRegex {
			if _, err := regexp.Compile(value); err != nil {
				return nil, fmt.Errorf("matcher %d: invalid regex: %w", i, err)
			}
		}

		matchers = append(matchers, core.StoredSilenceMatcher{
			Name:    name,
			Value:   value,
			IsRegex: matcher.IsRegex,
			IsEqual: isEqual,
		})
	}

	return &core.StoredSilenceState{
		ID:        id,
		Matchers:  matchers,
		StartsAt:  startsAt.UTC(),
		EndsAt:    endsAt.UTC(),
		CreatedBy: in.CreatedBy,
		Comment:   in.Comment,
		UpdatedAt: now.UTC(),
	}, nil
}

func toAPISilence(in *core.StoredSilenceState, now time.Time) core.APISilence {
	matchers := make([]core.APISilenceMatcher, 0, len(in.Matchers))
	for _, matcher := range in.Matchers {
		matchers = append(matchers, core.APISilenceMatcher(matcher))
	}

	return core.APISilence{
		ID:        in.ID,
		Matchers:  matchers,
		StartsAt:  in.StartsAt.UTC().Format(time.RFC3339),
		EndsAt:    in.EndsAt.UTC().Format(time.RFC3339),
		UpdatedAt: in.UpdatedAt.UTC().Format(time.RFC3339),
		CreatedBy: in.CreatedBy,
		Comment:   in.Comment,
		Status: core.APISilenceStatus{
			State: silenceState(in, now),
		},
	}
}

func silenceState(silence *core.StoredSilenceState, now time.Time) string {
	ts := now.UTC()
	if ts.Before(silence.StartsAt) {
		return "pending"
	}
	if !ts.Before(silence.EndsAt) {
		return "expired"
	}
	return "active"
}

var silenceStateSortOrder = map[string]int{
	"active":  1,
	"pending": 2,
	"expired": 3,
}

func sortSilencesForList(silences []core.APISilence) {
	sort.Slice(silences, func(i, j int) bool {
		stateI := silences[i].Status.State
		stateJ := silences[j].Status.State

		if stateI != stateJ {
			orderI := silenceStateSortOrder[stateI]
			orderJ := silenceStateSortOrder[stateJ]
			if orderI == 0 {
				orderI = 99
			}
			if orderJ == 0 {
				orderJ = 99
			}
			return orderI < orderJ
		}

		endsAtI, _ := time.Parse(time.RFC3339, silences[i].EndsAt)
		endsAtJ, _ := time.Parse(time.RFC3339, silences[j].EndsAt)
		startsAtI, _ := time.Parse(time.RFC3339, silences[i].StartsAt)
		startsAtJ, _ := time.Parse(time.RFC3339, silences[j].StartsAt)

		switch stateI {
		case "active":
			if !endsAtI.Equal(endsAtJ) {
				return endsAtI.Before(endsAtJ)
			}
		case "pending":
			if !startsAtI.Equal(startsAtJ) {
				return startsAtI.Before(startsAtJ)
			}
		case "expired":
			if !endsAtI.Equal(endsAtJ) {
				return endsAtI.After(endsAtJ)
			}
		}

		return silences[i].ID < silences[j].ID
	})
}
