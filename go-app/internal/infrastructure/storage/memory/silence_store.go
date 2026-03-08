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

		in := &core.SilenceInput{
			ID:        item.ID,
			Matchers:  matchers,
			StartsAt:  item.StartsAt,
			EndsAt:    item.EndsAt,
			CreatedBy: item.CreatedBy,
			Comment:   item.Comment,
		}
		if _, err := s.createOrUpdateInternal(in, now, false, false, true); err != nil {
			return fmt.Errorf("persisted silence[%d]: %w", i, err)
		}
	}
	return nil
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
	if !endsAt.After(startsAt) {
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
		matchers = append(matchers, core.APISilenceMatcher{
			Name:    matcher.Name,
			Value:   matcher.Value,
			IsRegex: matcher.IsRegex,
			IsEqual: matcher.IsEqual,
		})
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
