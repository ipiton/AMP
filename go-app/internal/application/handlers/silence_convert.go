package handlers

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/ipiton/AMP/internal/core"
	coresilencing "github.com/ipiton/AMP/internal/core/silencing"
)

// This file bridges the two silence representations that exist in the codebase:
//
//   - core.SilenceInput / core.APISilence — Alertmanager API v2 DTOs used by the
//     HTTP handlers and the in-memory read cache (memory.SilenceStore). Matchers
//     are encoded as (isRegex, isEqual) boolean pairs, timestamps as RFC3339
//     strings, and status is recomputed from timestamps on every read.
//
//   - coresilencing.Silence — the persistence-layer domain model used by
//     infrastructure/silencing.SilenceRepository. Matchers are encoded as a
//     Type operator (=, !=, =~, !~), timestamps as time.Time, and status is a
//     stored column maintained by the GC worker.
//
// Status semantics are identical on both sides:
//   pending: now <  StartsAt
//   active:  StartsAt <= now < EndsAt
//   expired: now >= EndsAt

// SilenceInputToDomain converts an Alertmanager API silence payload into the
// domain silence used by the persistence layer.
//
// Validation intentionally mirrors memory.SilenceStore input normalization
// (see normalizeSilenceInput) so that a payload accepted by the database path
// is always accepted by the in-memory cache afterwards, and vice versa:
//   - StartsAt defaults to now when empty; timestamps must be RFC3339
//   - EndsAt must be strictly after StartsAt and must not be in the past
//   - at least one matcher; names non-empty; values non-empty; regex compiles
func SilenceInputToDomain(in *core.SilenceInput, now time.Time) (*coresilencing.Silence, error) {
	if in == nil {
		return nil, fmt.Errorf("silence payload is required")
	}

	startsAt := now.UTC()
	if raw := strings.TrimSpace(in.StartsAt); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return nil, err
		}
		startsAt = parsed.UTC()
	}

	var endsAt time.Time
	if raw := strings.TrimSpace(in.EndsAt); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return nil, err
		}
		endsAt = parsed.UTC()
	}
	if !endsAt.After(startsAt) {
		return nil, fmt.Errorf("start time must be before end time")
	}
	if endsAt.Before(now.UTC()) {
		return nil, fmt.Errorf("end time can't be in the past")
	}

	if len(in.Matchers) == 0 {
		return nil, fmt.Errorf("at least 1 matcher is required")
	}

	matchers := make([]coresilencing.Matcher, 0, len(in.Matchers))
	for i, matcher := range in.Matchers {
		name := strings.TrimSpace(matcher.Name)
		if name == "" {
			return nil, fmt.Errorf("matcher %d: invalid label name", i)
		}

		if strings.TrimSpace(matcher.Value) == "" {
			return nil, fmt.Errorf("at least one matcher must not match the empty string")
		}

		if matcher.IsRegex {
			if _, err := regexp.Compile(matcher.Value); err != nil {
				return nil, fmt.Errorf("matcher %d: invalid regex: %w", i, err)
			}
		}

		isEqual := true
		if matcher.IsEqual != nil {
			isEqual = *matcher.IsEqual
		}

		matchers = append(matchers, coresilencing.Matcher{
			Name:    name,
			Value:   matcher.Value,
			Type:    matcherTypeFromFlags(matcher.IsRegex, isEqual),
			IsRegex: matcher.IsRegex,
		})
	}

	return &coresilencing.Silence{
		ID:        strings.TrimSpace(in.ID),
		CreatedBy: in.CreatedBy,
		Comment:   in.Comment,
		StartsAt:  startsAt,
		EndsAt:    endsAt,
		Matchers:  matchers,
	}, nil
}

// DomainSilenceToAPI converts a persisted domain silence into the Alertmanager
// API DTO consumed by memory.SilenceStore.RestoreFromPersistence and API reads.
//
// The status is recomputed from StartsAt/EndsAt against the supplied now
// instead of trusting the stored Status column, which can lag behind real time
// until the GC worker runs.
func DomainSilenceToAPI(s *coresilencing.Silence, now time.Time) core.APISilence {
	matchers := make([]core.APISilenceMatcher, 0, len(s.Matchers))
	for _, matcher := range s.Matchers {
		matchers = append(matchers, core.APISilenceMatcher{
			Name:    matcher.Name,
			Value:   matcher.Value,
			IsRegex: matcher.Type.IsRegexType(),
			IsEqual: matcherIsEqual(matcher.Type),
		})
	}

	updatedAt := s.CreatedAt
	if s.UpdatedAt != nil {
		updatedAt = *s.UpdatedAt
	}
	if updatedAt.IsZero() {
		updatedAt = now
	}

	return core.APISilence{
		ID:        s.ID,
		Matchers:  matchers,
		StartsAt:  s.StartsAt.UTC().Format(time.RFC3339),
		EndsAt:    s.EndsAt.UTC().Format(time.RFC3339),
		UpdatedAt: updatedAt.UTC().Format(time.RFC3339),
		CreatedBy: s.CreatedBy,
		Comment:   s.Comment,
		Status: core.APISilenceStatus{
			State: silenceStateAt(s.StartsAt, s.EndsAt, now),
		},
	}
}

// matcherTypeFromFlags maps the API (isRegex, isEqual) pair onto the domain
// matcher operator: (=, !=, =~, !~).
func matcherTypeFromFlags(isRegex, isEqual bool) coresilencing.MatcherType {
	switch {
	case isRegex && isEqual:
		return coresilencing.MatcherTypeRegex
	case isRegex && !isEqual:
		return coresilencing.MatcherTypeNotRegex
	case !isRegex && isEqual:
		return coresilencing.MatcherTypeEqual
	default:
		return coresilencing.MatcherTypeNotEqual
	}
}

// matcherIsEqual reports whether the domain matcher operator is a positive
// match (= or =~) in API terms.
func matcherIsEqual(t coresilencing.MatcherType) bool {
	return t == coresilencing.MatcherTypeEqual || t == coresilencing.MatcherTypeRegex
}

// silenceStateAt computes the Alertmanager silence state for the given time,
// matching both memory.SilenceStore and coresilencing.Silence.CalculateStatus.
func silenceStateAt(startsAt, endsAt, now time.Time) string {
	ts := now.UTC()
	if ts.Before(startsAt) {
		return "pending"
	}
	if !ts.Before(endsAt) {
		return "expired"
	}
	return "active"
}
