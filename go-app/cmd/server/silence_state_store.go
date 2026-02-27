package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
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
	IsRegex bool   `json:"isRegex"`
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

type silenceAPIError struct {
	status  int
	payload any
	message string
}

func (e *silenceAPIError) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.message) != "" {
		return e.message
	}
	return fmt.Sprintf("silence api error status=%d", e.status)
}

func newSilenceAPIError(status int, payload any, message string) *silenceAPIError {
	return &silenceAPIError{
		status:  status,
		payload: payload,
		message: message,
	}
}

func newSilenceCodeMessageError(status int, code int, message string) *silenceAPIError {
	return newSilenceAPIError(status, map[string]any{
		"code":    code,
		"message": message,
	}, message)
}

func newSilenceStringError(status int, message string) *silenceAPIError {
	return newSilenceAPIError(status, message, message)
}

func newSilenceStore() *silenceStore {
	return &silenceStore{
		silences: make(map[string]*storedSilence),
	}
}

func (s *silenceStore) createOrUpdate(in *silenceInput, now time.Time) (string, error) {
	return s.createOrUpdateInternal(in, now, true, true, false)
}

func (s *silenceStore) createOrUpdateInternal(in *silenceInput, now time.Time, notify, enforceExistingID, allowPastEndsAt bool) (string, error) {
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

	sortSilencesForList(out)

	return out
}

var silenceStateSortOrder = map[string]int{
	"active":  1,
	"pending": 2,
	"expired": 3,
}

func sortSilencesForList(silences []apiSilence) {
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

		endsAtI := parseSilenceAPITime(silences[i].EndsAt)
		endsAtJ := parseSilenceAPITime(silences[j].EndsAt)
		startsAtI := parseSilenceAPITime(silences[i].StartsAt)
		startsAtJ := parseSilenceAPITime(silences[j].StartsAt)

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

func parseSilenceAPITime(raw string) time.Time {
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}
	}
	return parsed
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
		if _, err := s.createOrUpdateInternal(in, now, false, false, true); err != nil {
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

func normalizeSilenceInput(in *silenceInput, now time.Time, allowPastEndsAt bool) (*storedSilence, error) {
	id := strings.TrimSpace(in.ID)
	if id == "" {
		var err error
		id, err = generateSilenceID()
		if err != nil {
			return nil, fmt.Errorf("failed to generate silence id: %w", err)
		}
	}

	startsAt := now.UTC()
	if startsAtRaw := strings.TrimSpace(in.StartsAt); startsAtRaw != "" {
		parsedStartsAt, err := time.Parse(time.RFC3339, startsAtRaw)
		if err != nil {
			return nil, newSilenceCodeMessageError(
				http.StatusBadRequest,
				http.StatusBadRequest,
				fmt.Sprintf("parsing silence body from \"\" failed, because %v", err),
			)
		}
		startsAt = parsedStartsAt.UTC()
	}

	var endsAt time.Time
	if endsAtRaw := strings.TrimSpace(in.EndsAt); endsAtRaw != "" {
		parsedEndsAt, err := time.Parse(time.RFC3339, endsAtRaw)
		if err != nil {
			return nil, newSilenceCodeMessageError(
				http.StatusBadRequest,
				http.StatusBadRequest,
				fmt.Sprintf("parsing silence body from \"\" failed, because %v", err),
			)
		}
		endsAt = parsedEndsAt.UTC()
	}
	if !endsAt.After(startsAt) {
		return nil, newSilenceStringError(http.StatusBadRequest, "Failed to create silence: start time must be before end time")
	}
	if !allowPastEndsAt && endsAt.Before(now.UTC()) {
		return nil, newSilenceStringError(http.StatusBadRequest, "Failed to create silence: end time can't be in the past")
	}

	if len(in.Matchers) == 0 {
		return nil, newSilenceCodeMessageError(http.StatusUnprocessableEntity, 612, "matchers in body should have at least 1 items")
	}

	matchers := make([]storedSilenceMatcher, 0, len(in.Matchers))
	for i, matcher := range in.Matchers {
		name := strings.TrimSpace(matcher.Name)
		if name == "" {
			return nil, newSilenceStringError(http.StatusBadRequest, fmt.Sprintf("invalid silence: invalid label matcher %d: invalid label name %q", i, matcher.Name))
		}

		isEqual := true
		if matcher.IsEqual != nil {
			isEqual = *matcher.IsEqual
		}
		value := matcher.Value

		if strings.TrimSpace(value) == "" {
			return nil, newSilenceStringError(http.StatusBadRequest, "invalid silence: at least one matcher must not match the empty string")
		}

		if matcher.IsRegex {
			if _, err := regexp.Compile(value); err != nil {
				return nil, newSilenceStringError(http.StatusBadRequest, fmt.Sprintf("invalid silence: invalid label matcher %d: invalid regular expression %q: %v", i, value, err))
			}
		}

		matchers = append(matchers, storedSilenceMatcher{
			Name:    name,
			Value:   value,
			IsRegex: matcher.IsRegex,
			IsEqual: isEqual,
		})
	}

	return &storedSilence{
		ID:        id,
		Matchers:  matchers,
		StartsAt:  startsAt.UTC(),
		EndsAt:    endsAt.UTC(),
		CreatedBy: in.CreatedBy,
		Comment:   in.Comment,
		UpdatedAt: now.UTC(),
	}, nil
}

func generateSilenceID() (string, error) {
	uid, err := uuid.NewRandom()
	if err != nil {
		return "", err
	}
	return uid.String(), nil
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
		StartsAt:  formatAPITimestamp(in.StartsAt.UTC()),
		EndsAt:    formatAPITimestamp(in.EndsAt.UTC()),
		UpdatedAt: formatAPITimestamp(in.UpdatedAt.UTC()),
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
		return nil, newSilenceCodeMessageError(http.StatusUnprocessableEntity, 602, "silence in body is required")
	}

	var rawPayload map[string]json.RawMessage
	if err := json.Unmarshal(body, &rawPayload); err != nil {
		return nil, newSilenceCodeMessageError(
			http.StatusBadRequest,
			http.StatusBadRequest,
			fmt.Sprintf("parsing silence body from \"\" failed, because %v", err),
		)
	}

	requiredFields := []string{"comment", "createdBy", "startsAt", "endsAt", "matchers"}
	for _, field := range requiredFields {
		if _, ok := rawPayload[field]; ok {
			continue
		}
		return nil, newSilenceCodeMessageError(http.StatusUnprocessableEntity, 602, fmt.Sprintf("%s in body is required", field))
	}

	var rawMatchers []json.RawMessage
	if err := json.Unmarshal(rawPayload["matchers"], &rawMatchers); err != nil {
		return nil, newSilenceCodeMessageError(
			http.StatusBadRequest,
			http.StatusBadRequest,
			fmt.Sprintf("parsing silence body from \"\" failed, because %v", err),
		)
	}
	if len(rawMatchers) == 0 {
		return nil, newSilenceCodeMessageError(http.StatusUnprocessableEntity, 612, "matchers in body should have at least 1 items")
	}

	for idx, rawMatcher := range rawMatchers {
		var matcherPayload map[string]json.RawMessage
		if err := json.Unmarshal(rawMatcher, &matcherPayload); err != nil {
			return nil, newSilenceCodeMessageError(
				http.StatusBadRequest,
				http.StatusBadRequest,
				fmt.Sprintf("parsing silence body from \"\" failed, because %v", err),
			)
		}
		if _, ok := matcherPayload["name"]; !ok {
			return nil, newSilenceCodeMessageError(http.StatusUnprocessableEntity, 602, fmt.Sprintf("matchers.%d.name in body is required", idx))
		}
		if _, ok := matcherPayload["value"]; !ok {
			return nil, newSilenceCodeMessageError(http.StatusUnprocessableEntity, 602, fmt.Sprintf("matchers.%d.value in body is required", idx))
		}
	}

	var payload silenceInput
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, newSilenceCodeMessageError(
			http.StatusBadRequest,
			http.StatusBadRequest,
			fmt.Sprintf("parsing silence body from \"\" failed, because %v", err),
		)
	}

	return &payload, nil
}
