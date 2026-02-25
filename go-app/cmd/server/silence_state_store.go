package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type silenceMatcherInput struct {
	Name    string `json:"name"`
	Value   string `json:"value"`
	IsRegex bool   `json:"isRegex,omitempty"`
	IsEqual *bool  `json:"isEqual,omitempty"`
}

type silenceInput struct {
	ID        string                `json:"id,omitempty"`
	Matchers  []silenceMatcherInput `json:"matchers"`
	StartsAt  string                `json:"startsAt"`
	EndsAt    string                `json:"endsAt"`
	CreatedBy string                `json:"createdBy"`
	Comment   string                `json:"comment"`
}

type storedSilenceMatcher struct {
	Name    string
	Value   string
	IsRegex bool
	IsEqual bool
}

type storedSilence struct {
	ID        string
	Matchers  []storedSilenceMatcher
	StartsAt  time.Time
	EndsAt    time.Time
	CreatedBy string
	Comment   string
	UpdatedAt time.Time
}

type apiSilenceMatcher struct {
	Name    string `json:"name"`
	Value   string `json:"value"`
	IsRegex bool   `json:"isRegex,omitempty"`
	IsEqual bool   `json:"isEqual"`
}

type apiSilenceStatus struct {
	State string `json:"state"`
}

type apiSilence struct {
	ID        string              `json:"id"`
	Matchers  []apiSilenceMatcher `json:"matchers"`
	StartsAt  string              `json:"startsAt"`
	EndsAt    string              `json:"endsAt"`
	UpdatedAt string              `json:"updatedAt"`
	CreatedBy string              `json:"createdBy"`
	Comment   string              `json:"comment"`
	Status    apiSilenceStatus    `json:"status"`
}

type silenceStore struct {
	mu       sync.RWMutex
	silences map[string]*storedSilence
	onChange func()
}

var errSilenceNotFound = errors.New("silence not found")

func newSilenceStore() *silenceStore {
	return &silenceStore{
		silences: make(map[string]*storedSilence),
	}
}

func (s *silenceStore) createOrUpdate(in *silenceInput, now time.Time) (string, error) {
	return s.createOrUpdateInternal(in, now, true, true)
}

func (s *silenceStore) createOrUpdateInternal(in *silenceInput, now time.Time, notify, enforceExistingID bool) (string, error) {
	if in == nil {
		return "", fmt.Errorf("silence payload is required")
	}

	updateRequested := strings.TrimSpace(in.ID) != ""
	normalized, err := normalizeSilenceInput(in, now)
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	if enforceExistingID && updateRequested {
		if _, ok := s.silences[normalized.ID]; !ok {
			s.mu.Unlock()
			return "", errSilenceNotFound
		}
	}
	s.silences[normalized.ID] = normalized
	s.mu.Unlock()

	if notify {
		s.notifyChange()
	}
	return normalized.ID, nil
}

func (s *silenceStore) list(now time.Time) []apiSilence {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]apiSilence, 0, len(s.silences))
	for _, silence := range s.silences {
		out = append(out, toAPISilence(silence, now))
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].StartsAt > out[j].StartsAt
	})

	return out
}

func (s *silenceStore) get(id string, now time.Time) (apiSilence, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	silence, ok := s.silences[id]
	if !ok {
		return apiSilence{}, false
	}

	return toAPISilence(silence, now), true
}

func (s *silenceStore) delete(id string) bool {
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

func (s *silenceStore) setOnChange(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onChange = fn
}

func (s *silenceStore) notifyChange() {
	s.mu.RLock()
	fn := s.onChange
	s.mu.RUnlock()

	if fn != nil {
		fn()
	}
}

func (s *silenceStore) exportForPersistence(now time.Time) []apiSilence {
	return s.list(now)
}

func (s *silenceStore) stats(now time.Time) (total, active, pending, expired int) {
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

func (s *silenceStore) restoreFromPersistence(items []apiSilence, now time.Time) error {
	for i, item := range items {
		matchers := make([]silenceMatcherInput, 0, len(item.Matchers))
		for _, matcher := range item.Matchers {
			isEqual := matcher.IsEqual
			matchers = append(matchers, silenceMatcherInput{
				Name:    matcher.Name,
				Value:   matcher.Value,
				IsRegex: matcher.IsRegex,
				IsEqual: &isEqual,
			})
		}

		in := &silenceInput{
			ID:        item.ID,
			Matchers:  matchers,
			StartsAt:  item.StartsAt,
			EndsAt:    item.EndsAt,
			CreatedBy: item.CreatedBy,
			Comment:   item.Comment,
		}
		if _, err := s.createOrUpdateInternal(in, now, false, false); err != nil {
			return fmt.Errorf("persisted silence[%d]: %w", i, err)
		}
	}
	return nil
}

func (s *silenceStore) activeMatchingSilenceIDs(labels map[string]string, now time.Time) []string {
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

func (s *silenceStore) hasActiveMatch(labels map[string]string, now time.Time) bool {
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

func silenceMatchesLabels(matchers []storedSilenceMatcher, labels map[string]string) bool {
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

func normalizeSilenceInput(in *silenceInput, now time.Time) (*storedSilence, error) {
	id := strings.TrimSpace(in.ID)
	if id == "" {
		var err error
		id, err = generateSilenceID()
		if err != nil {
			return nil, fmt.Errorf("failed to generate silence id: %w", err)
		}
	}

	createdBy := strings.TrimSpace(in.CreatedBy)
	if createdBy == "" {
		return nil, fmt.Errorf("createdBy is required")
	}

	comment := strings.TrimSpace(in.Comment)
	if comment == "" {
		return nil, fmt.Errorf("comment is required")
	}

	startsAt, err := time.Parse(time.RFC3339, strings.TrimSpace(in.StartsAt))
	if err != nil {
		return nil, fmt.Errorf("invalid startsAt: %w", err)
	}

	endsAt, err := time.Parse(time.RFC3339, strings.TrimSpace(in.EndsAt))
	if err != nil {
		return nil, fmt.Errorf("invalid endsAt: %w", err)
	}
	if !endsAt.After(startsAt) {
		return nil, fmt.Errorf("endsAt must be after startsAt")
	}

	if len(in.Matchers) == 0 {
		return nil, fmt.Errorf("at least one matcher is required")
	}

	matchers := make([]storedSilenceMatcher, 0, len(in.Matchers))
	for i, matcher := range in.Matchers {
		name := strings.TrimSpace(matcher.Name)
		if name == "" {
			return nil, fmt.Errorf("matcher[%d].name is required", i)
		}

		isEqual := true
		if matcher.IsEqual != nil {
			isEqual = *matcher.IsEqual
		}

		matchers = append(matchers, storedSilenceMatcher{
			Name:    name,
			Value:   matcher.Value,
			IsRegex: matcher.IsRegex,
			IsEqual: isEqual,
		})
	}

	return &storedSilence{
		ID:        id,
		Matchers:  matchers,
		StartsAt:  startsAt.UTC(),
		EndsAt:    endsAt.UTC(),
		CreatedBy: createdBy,
		Comment:   comment,
		UpdatedAt: now.UTC(),
	}, nil
}

func generateSilenceID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func toAPISilence(in *storedSilence, now time.Time) apiSilence {
	matchers := make([]apiSilenceMatcher, 0, len(in.Matchers))
	for _, matcher := range in.Matchers {
		matchers = append(matchers, apiSilenceMatcher{
			Name:    matcher.Name,
			Value:   matcher.Value,
			IsRegex: matcher.IsRegex,
			IsEqual: matcher.IsEqual,
		})
	}

	return apiSilence{
		ID:        in.ID,
		Matchers:  matchers,
		StartsAt:  in.StartsAt.UTC().Format(time.RFC3339),
		EndsAt:    in.EndsAt.UTC().Format(time.RFC3339),
		UpdatedAt: in.UpdatedAt.UTC().Format(time.RFC3339),
		CreatedBy: in.CreatedBy,
		Comment:   in.Comment,
		Status: apiSilenceStatus{
			State: silenceState(in, now),
		},
	}
}

func silenceState(silence *storedSilence, now time.Time) string {
	ts := now.UTC()
	if ts.Before(silence.StartsAt) {
		return "pending"
	}
	if !ts.Before(silence.EndsAt) {
		return "expired"
	}
	return "active"
}

func parseSilencePayload(body []byte) (*silenceInput, error) {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return nil, fmt.Errorf("request body is empty")
	}

	var payload silenceInput
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("invalid silence payload: %w", err)
	}

	return &payload, nil
}
