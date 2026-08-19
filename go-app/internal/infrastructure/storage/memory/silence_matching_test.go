package memory

import (
	"context"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/core/silencing"
)

// Review fix round 3 (R6, Important): internal/infrastructure/storage/memory
// used to carry a SECOND, independent silence evaluator (silenceMatchesLabels)
// that compiled `=~`/`!~` UNANCHORED — the one actually wired into the live
// suppression path (notify-chain Step 2 filterSilenced -> HasActiveMatch,
// plus status.silencedBy and ?silenced= via ActiveMatchingSilenceIDs) —
// while fix rounds 1-2 had already fixed the evaluator backing
// POST /api/v2/silences/check (internal/core/silencing.DefaultSilenceMatcher).
// Result: a silence on `job=~"prod"` still suppressed `job="preprod-2"` in
// production even after the preview endpoint stopped agreeing. This file
// pins the fix: silenceMatchesLabels now delegates to the SAME
// silencing.DefaultSilenceMatcher instance, so a fifth copy of this
// divergence class can't appear.

// TestSilenceMatchesLabels_AbsentLabelUpstreamSemantics is the third (see
// internal/infrastructure/inhibition and internal/business/routing) and now
// fourth (see internal/core/silencing itself) 4-operator x absent-label
// table for this divergence class, run directly against this package's own
// evaluator to prove the shared-instance wiring actually carries the fix
// through, not just the package it delegates to.
func TestSilenceMatchesLabels_AbsentLabelUpstreamSemantics(t *testing.T) {
	tests := []struct {
		name    string
		matcher core.StoredSilenceMatcher
		want    bool
		explain string
	}{
		{
			name:    `job!="" on absent label`,
			matcher: core.StoredSilenceMatcher{Name: "job", Value: "", IsRegex: false, IsEqual: false},
			want:    false,
			explain: `upstream: "" != "" is false -> NOT matched (the pre-fix version returned true)`,
		},
		{
			name:    `foo=~".*" on absent label`,
			matcher: core.StoredSilenceMatcher{Name: "foo", Value: ".*", IsRegex: true, IsEqual: true},
			want:    true,
			explain: `upstream: anchored ".*" matches "" -> matched (the pre-fix version returned false)`,
		},
		{
			name:    `foo="" on absent label`,
			matcher: core.StoredSilenceMatcher{Name: "foo", Value: "", IsRegex: false, IsEqual: true},
			want:    true,
			explain: `upstream: "" == "" -> matched (the pre-fix version returned false)`,
		},
		{
			name:    `foo!~".*" on absent label`,
			matcher: core.StoredSilenceMatcher{Name: "foo", Value: ".*", IsRegex: true, IsEqual: false},
			want:    false,
			explain: `upstream: anchored ".*" matches "" so negated is false -> NOT matched (the pre-fix version returned true)`,
		},
		{
			name:    `foo="bar" on absent label (non-empty operand, tables agree)`,
			matcher: core.StoredSilenceMatcher{Name: "foo", Value: "bar", IsRegex: false, IsEqual: true},
			want:    false,
		},
		{
			name:    `foo!="bar" on absent label (non-empty operand, tables agree)`,
			matcher: core.StoredSilenceMatcher{Name: "foo", Value: "bar", IsRegex: false, IsEqual: false},
			want:    true,
		},
		{
			name:    `foo=~"bar" on absent label (non-empty operand, tables agree)`,
			matcher: core.StoredSilenceMatcher{Name: "foo", Value: "bar", IsRegex: true, IsEqual: true},
			want:    false,
		},
		{
			name:    `foo!~"bar" on absent label (non-empty operand, tables agree)`,
			matcher: core.StoredSilenceMatcher{Name: "foo", Value: "bar", IsRegex: true, IsEqual: false},
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := silenceMatchesLabels([]core.StoredSilenceMatcher{tt.matcher}, map[string]string{"unrelated": "value"})
			if got != tt.want {
				t.Errorf("silenceMatchesLabels() = %v, want %v (%s)", got, tt.want, tt.explain)
			}
		})
	}
}

// TestSilenceStore_HasActiveMatch_AnchoredRegex is the reviewer's own repro,
// exercised through the full public store API (Upsert), not just the
// internal free function: a silence on `job=~"prod"` must NOT suppress
// `job="preprod-2"` (substring match), and must still suppress the exact
// value `job="prod"`.
func TestSilenceStore_HasActiveMatch_AnchoredRegex(t *testing.T) {
	store := NewSilenceStore()
	now := time.Now().UTC()

	isEqual := true
	_, err := store.Upsert(&core.SilenceInput{
		Matchers: []core.SilenceMatcherInput{
			{Name: "job", Value: "prod", IsRegex: true, IsEqual: &isEqual},
		},
		EndsAt:    now.Add(time.Hour).Format(time.RFC3339),
		CreatedBy: "tester",
		Comment:   "anchoring regression",
	}, now)
	if err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	if store.HasActiveMatch(map[string]string{"job": "preprod-2"}, now) {
		t.Error(`HasActiveMatch(job="preprod-2") = true, want false — job=~"prod" must be anchored, not a substring match`)
	}
	if !store.HasActiveMatch(map[string]string{"job": "prod"}, now) {
		t.Error(`HasActiveMatch(job="prod") = false, want true — the exact value must still match`)
	}
}

// TestSilencePreviewVsPipeline_Agreement is the required preview-vs-pipeline
// agreement test: the SAME silence definition and the SAME alert must
// produce the SAME verdict whether evaluated through the live-pipeline path
// (SilenceStore.HasActiveMatch, storage/memory) or through the
// preview/business-layer evaluator (internal/core/silencing.
// DefaultSilenceMatcher.Matches, what backs POST /api/v2/silences/check via
// internal/business/silencing.DefaultSilenceManager.IsAlertSilenced). Before
// the R6 fix, a substring-style regex ("job=~\"prod\"" against
// "job=\"preprod-2\"") made these two paths DISAGREE — the pipeline silenced
// it, the preview endpoint (already fixed in rounds 1-2) said it would not.
func TestSilencePreviewVsPipeline_Agreement(t *testing.T) {
	tests := []struct {
		name   string
		labels map[string]string
		want   bool
	}{
		{"exact value matches both", map[string]string{"job": "prod"}, true},
		{"substring-only value matches neither (the R6 bug scenario)", map[string]string{"job": "preprod-2"}, false},
		{"unrelated value matches neither", map[string]string{"job": "staging"}, false},
		{"absent label matches neither (regex doesn't match empty string)", map[string]string{"other": "x"}, false},
	}

	// Pipeline path: SilenceStore.HasActiveMatch (storage/memory).
	store := NewSilenceStore()
	now := time.Now().UTC()
	isEqual := true
	_, err := store.Upsert(&core.SilenceInput{
		Matchers: []core.SilenceMatcherInput{
			{Name: "job", Value: "prod", IsRegex: true, IsEqual: &isEqual},
		},
		EndsAt:    now.Add(time.Hour).Format(time.RFC3339),
		CreatedBy: "tester",
		Comment:   "preview vs pipeline agreement",
	}, now)
	if err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}

	// Preview path: the same evaluator internal/business/silencing.
	// DefaultSilenceManager.IsAlertSilenced calls into.
	previewMatcher := silencing.NewSilenceMatcher()
	previewSilence := &silencing.Silence{
		Matchers: []silencing.Matcher{{Name: "job", Value: "prod", Type: silencing.MatcherTypeRegex}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pipelineResult := store.HasActiveMatch(tt.labels, now)

			previewResult, err := previewMatcher.Matches(context.Background(), silencing.Alert{Labels: tt.labels}, previewSilence)
			if err != nil {
				t.Fatalf("preview Matches() error = %v", err)
			}

			if pipelineResult != tt.want {
				t.Errorf("pipeline (HasActiveMatch) = %v, want %v", pipelineResult, tt.want)
			}
			if previewResult != tt.want {
				t.Errorf("preview (DefaultSilenceMatcher.Matches) = %v, want %v", previewResult, tt.want)
			}
			if pipelineResult != previewResult {
				t.Errorf("pipeline and preview DISAGREE: pipeline=%v preview=%v for labels=%v — the exact class of bug R6 fixed", pipelineResult, previewResult, tt.labels)
			}
		})
	}
}
