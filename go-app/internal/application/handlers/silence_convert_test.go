package handlers

import (
	"strings"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
	coresilencing "github.com/ipiton/AMP/internal/core/silencing"
)

func boolPtr(v bool) *bool { return &v }

func TestSilenceInputToDomain_MatcherTypeMapping(t *testing.T) {
	now := time.Now().UTC()
	cases := []struct {
		name     string
		isRegex  bool
		isEqual  *bool
		wantType coresilencing.MatcherType
	}{
		{"equal default", false, nil, coresilencing.MatcherTypeEqual},
		{"equal explicit", false, boolPtr(true), coresilencing.MatcherTypeEqual},
		{"not equal", false, boolPtr(false), coresilencing.MatcherTypeNotEqual},
		{"regex", true, boolPtr(true), coresilencing.MatcherTypeRegex},
		{"not regex", true, boolPtr(false), coresilencing.MatcherTypeNotRegex},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := &core.SilenceInput{
				Matchers: []core.SilenceMatcherInput{
					{Name: "alertname", Value: "Watchdog", IsRegex: tc.isRegex, IsEqual: tc.isEqual},
				},
				EndsAt:    now.Add(time.Hour).Format(time.RFC3339),
				CreatedBy: "tester",
				Comment:   "mapping",
			}
			domain, err := SilenceInputToDomain(in, now)
			if err != nil {
				t.Fatalf("SilenceInputToDomain() error = %v", err)
			}
			if got := domain.Matchers[0].Type; got != tc.wantType {
				t.Fatalf("matcher type = %q, want %q", got, tc.wantType)
			}
			if domain.Matchers[0].IsRegex != tc.isRegex {
				t.Fatalf("IsRegex = %v, want %v", domain.Matchers[0].IsRegex, tc.isRegex)
			}
		})
	}
}

func TestSilenceInputToDomain_DefaultsAndFields(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	endsAt := now.Add(2 * time.Hour)

	in := &core.SilenceInput{
		ID:        "  550e8400-e29b-41d4-a716-446655440000  ",
		Matchers:  []core.SilenceMatcherInput{{Name: " job ", Value: "api"}},
		EndsAt:    endsAt.Format(time.RFC3339),
		CreatedBy: "ops@example.com",
		Comment:   "maintenance",
	}

	domain, err := SilenceInputToDomain(in, now)
	if err != nil {
		t.Fatalf("SilenceInputToDomain() error = %v", err)
	}

	if domain.ID != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("ID not trimmed: %q", domain.ID)
	}
	// StartsAt empty → defaults to now.
	if !domain.StartsAt.Equal(now) {
		t.Errorf("StartsAt = %v, want %v (default now)", domain.StartsAt, now)
	}
	if !domain.EndsAt.Equal(endsAt) {
		t.Errorf("EndsAt = %v, want %v", domain.EndsAt, endsAt)
	}
	if domain.Matchers[0].Name != "job" {
		t.Errorf("matcher name not trimmed: %q", domain.Matchers[0].Name)
	}
	if domain.CreatedBy != "ops@example.com" || domain.Comment != "maintenance" {
		t.Errorf("metadata not carried over: %+v", domain)
	}
}

func TestSilenceInputToDomain_Errors(t *testing.T) {
	now := time.Now().UTC()
	valid := func() *core.SilenceInput {
		return &core.SilenceInput{
			Matchers:  []core.SilenceMatcherInput{{Name: "alertname", Value: "X"}},
			StartsAt:  now.Format(time.RFC3339),
			EndsAt:    now.Add(time.Hour).Format(time.RFC3339),
			CreatedBy: "tester",
			Comment:   "err cases",
		}
	}

	cases := []struct {
		name    string
		mutate  func(in *core.SilenceInput)
		wantErr string
	}{
		{"bad startsAt", func(in *core.SilenceInput) { in.StartsAt = "not-a-time" }, "parsing time"},
		{"bad endsAt", func(in *core.SilenceInput) { in.EndsAt = "not-a-time" }, "parsing time"},
		{"missing endsAt", func(in *core.SilenceInput) { in.EndsAt = "" }, "start time must be before end time"},
		{"end before start", func(in *core.SilenceInput) {
			in.StartsAt = now.Add(time.Hour).Format(time.RFC3339)
			in.EndsAt = now.Format(time.RFC3339)
		}, "start time must be before end time"},
		{"end in past", func(in *core.SilenceInput) {
			in.StartsAt = now.Add(-2 * time.Hour).Format(time.RFC3339)
			in.EndsAt = now.Add(-time.Hour).Format(time.RFC3339)
		}, "end time can't be in the past"},
		{"no matchers", func(in *core.SilenceInput) { in.Matchers = nil }, "at least 1 matcher is required"},
		{"empty matcher name", func(in *core.SilenceInput) { in.Matchers[0].Name = "  " }, "invalid label name"},
		{"empty matcher value", func(in *core.SilenceInput) { in.Matchers[0].Value = " " }, "must not match the empty string"},
		{"bad regex", func(in *core.SilenceInput) {
			in.Matchers[0].IsRegex = true
			in.Matchers[0].Value = "("
		}, "invalid regex"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := valid()
			tc.mutate(in)
			_, err := SilenceInputToDomain(in, now)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.wantErr)
			}
		})
	}

	if _, err := SilenceInputToDomain(nil, now); err == nil {
		t.Fatal("expected error for nil input")
	}
}

func TestDomainSilenceToAPI_StatusSemantics(t *testing.T) {
	now := time.Now().UTC()
	cases := []struct {
		name      string
		startsAt  time.Time
		endsAt    time.Time
		wantState string
	}{
		{"pending", now.Add(time.Hour), now.Add(2 * time.Hour), "pending"},
		{"active", now.Add(-time.Hour), now.Add(time.Hour), "active"},
		{"expired", now.Add(-2 * time.Hour), now.Add(-time.Hour), "expired"},
		{"expired at boundary", now.Add(-time.Hour), now, "expired"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &coresilencing.Silence{
				ID:       "550e8400-e29b-41d4-a716-446655440000",
				StartsAt: tc.startsAt,
				EndsAt:   tc.endsAt,
				// Stored status is deliberately stale to prove recomputation.
				Status:    coresilencing.SilenceStatusActive,
				CreatedAt: now.Add(-3 * time.Hour),
			}
			api := DomainSilenceToAPI(s, now)
			if api.Status.State != tc.wantState {
				t.Fatalf("state = %q, want %q", api.Status.State, tc.wantState)
			}
		})
	}
}

func TestDomainSilenceToAPI_MatchersAndUpdatedAt(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	created := now.Add(-time.Hour)
	updated := now.Add(-time.Minute)

	s := &coresilencing.Silence{
		ID:        "550e8400-e29b-41d4-a716-446655440000",
		CreatedBy: "ops@example.com",
		Comment:   "roundtrip",
		StartsAt:  now.Add(-time.Hour),
		EndsAt:    now.Add(time.Hour),
		CreatedAt: created,
		UpdatedAt: &updated,
		Matchers: []coresilencing.Matcher{
			{Name: "alertname", Value: "X", Type: coresilencing.MatcherTypeEqual},
			{Name: "env", Value: "prod", Type: coresilencing.MatcherTypeNotEqual},
			{Name: "sev", Value: "crit.*", Type: coresilencing.MatcherTypeRegex},
			{Name: "team", Value: "db.*", Type: coresilencing.MatcherTypeNotRegex},
		},
	}

	api := DomainSilenceToAPI(s, now)

	wantFlags := []struct{ isRegex, isEqual bool }{
		{false, true},
		{false, false},
		{true, true},
		{true, false},
	}
	for i, want := range wantFlags {
		if api.Matchers[i].IsRegex != want.isRegex || api.Matchers[i].IsEqual != want.isEqual {
			t.Errorf("matcher %d flags = (regex=%v equal=%v), want (regex=%v equal=%v)",
				i, api.Matchers[i].IsRegex, api.Matchers[i].IsEqual, want.isRegex, want.isEqual)
		}
	}

	if api.UpdatedAt != updated.Format(time.RFC3339) {
		t.Errorf("UpdatedAt = %q, want %q", api.UpdatedAt, updated.Format(time.RFC3339))
	}

	// UpdatedAt falls back to CreatedAt when the silence was never updated.
	s.UpdatedAt = nil
	api = DomainSilenceToAPI(s, now)
	if api.UpdatedAt != created.Format(time.RFC3339) {
		t.Errorf("UpdatedAt fallback = %q, want CreatedAt %q", api.UpdatedAt, created.Format(time.RFC3339))
	}
}

// TestSilenceConvert_RoundTripThroughMemoryStore proves that a silence
// accepted by the DB path is always accepted by the in-memory cache, keeping
// both stores in agreement (same ID, same matcher semantics).
func TestSilenceConvert_RoundTripThroughMemoryStore(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)

	in := &core.SilenceInput{
		Matchers: []core.SilenceMatcherInput{
			{Name: "alertname", Value: "Watchdog"},
			{Name: "env", Value: "dev", IsEqual: boolPtr(false)},
		},
		StartsAt:  now.Add(-time.Minute).Format(time.RFC3339),
		EndsAt:    now.Add(time.Hour).Format(time.RFC3339),
		CreatedBy: "tester",
		Comment:   "roundtrip",
	}

	domain, err := SilenceInputToDomain(in, now)
	if err != nil {
		t.Fatalf("SilenceInputToDomain() error = %v", err)
	}
	domain.ID = "550e8400-e29b-41d4-a716-446655440000"
	domain.CreatedAt = now

	api := DomainSilenceToAPI(domain, now)
	if api.ID != domain.ID {
		t.Fatalf("API ID = %q, want %q", api.ID, domain.ID)
	}
	if api.Status.State != "active" {
		t.Fatalf("state = %q, want active", api.Status.State)
	}
	if len(api.Matchers) != 2 || api.Matchers[1].IsEqual {
		t.Fatalf("negative matcher lost in conversion: %+v", api.Matchers)
	}
}
