package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
	coresilencing "github.com/ipiton/AMP/internal/core/silencing"
	infrasilencing "github.com/ipiton/AMP/internal/infrastructure/silencing"
	"github.com/ipiton/AMP/internal/infrastructure/storage/memory"
)

// mockSilenceRepo implements the subset of infrasilencing.SilenceRepository
// the handlers use. Unimplemented methods panic via the embedded nil
// interface — the DB-first write path must never call them.
type mockSilenceRepo struct {
	infrasilencing.SilenceRepository

	createFn func(ctx context.Context, s *coresilencing.Silence) (*coresilencing.Silence, error)
	getFn    func(ctx context.Context, id string) (*coresilencing.Silence, error)
	updateFn func(ctx context.Context, s *coresilencing.Silence) error
	deleteFn func(ctx context.Context, id string) error

	createCalls int
	deleteCalls int
}

func (m *mockSilenceRepo) CreateSilence(ctx context.Context, s *coresilencing.Silence) (*coresilencing.Silence, error) {
	m.createCalls++
	return m.createFn(ctx, s)
}

func (m *mockSilenceRepo) GetSilenceByID(ctx context.Context, id string) (*coresilencing.Silence, error) {
	return m.getFn(ctx, id)
}

func (m *mockSilenceRepo) UpdateSilence(ctx context.Context, s *coresilencing.Silence) error {
	return m.updateFn(ctx, s)
}

func (m *mockSilenceRepo) DeleteSilence(ctx context.Context, id string) error {
	m.deleteCalls++
	return m.deleteFn(ctx, id)
}

const testRepoID = "550e8400-e29b-41d4-a716-446655440000"

func validSilenceBody(t *testing.T) string {
	t.Helper()
	now := time.Now().UTC()
	return `{"matchers":[{"name":"alertname","value":"TestAlert"}],"startsAt":"` +
		now.Add(-time.Minute).Format(time.RFC3339) + `","endsAt":"` +
		now.Add(time.Hour).Format(time.RFC3339) + `","createdBy":"tester","comment":"db-first"}`
}

func postSilence(t *testing.T, handler http.HandlerFunc, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v2/silences", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

func TestSilencesHandler_DBFirstCreate_RepoIDAdoptedByMemory(t *testing.T) {
	store := memory.NewSilenceStore()
	var repoSaw *coresilencing.Silence
	repo := &mockSilenceRepo{
		createFn: func(_ context.Context, s *coresilencing.Silence) (*coresilencing.Silence, error) {
			repoSaw = s
			created := *s
			created.ID = testRepoID // repository is the ID source of truth
			created.CreatedAt = time.Now().UTC()
			return &created, nil
		},
	}
	registry := &fakeRegistry{
		alertStore:   memory.NewAlertStore(),
		silenceStore: store,
		silenceRepo:  repo,
	}

	rec := postSilence(t, SilencesHandler(registry), validSilenceBody(t))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status = %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["silenceID"] != testRepoID {
		t.Fatalf("silenceID = %q, want repository-generated %q", resp["silenceID"], testRepoID)
	}

	// Memory must hold the silence under the SAME ID as the database,
	// otherwise a later DELETE would break.
	if _, ok := store.Get(testRepoID, time.Now().UTC()); !ok {
		t.Fatal("silence not found in memory store under repository ID")
	}

	if repoSaw == nil || repoSaw.Matchers[0].Type != coresilencing.MatcherTypeEqual {
		t.Fatalf("repository received wrong matcher mapping: %+v", repoSaw)
	}
}

func TestSilencesHandler_DBFirstCreate_RepoErrorLeavesMemoryUntouched(t *testing.T) {
	store := memory.NewSilenceStore()
	repo := &mockSilenceRepo{
		createFn: func(_ context.Context, _ *coresilencing.Silence) (*coresilencing.Silence, error) {
			return nil, fmt.Errorf("insert silence: %w", infrasilencing.ErrDatabaseConnection)
		},
	}
	registry := &fakeRegistry{
		alertStore:   memory.NewAlertStore(),
		silenceStore: store,
		silenceRepo:  repo,
	}

	rec := postSilence(t, SilencesHandler(registry), validSilenceBody(t))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("POST status = %d, want 500; body: %s", rec.Code, rec.Body.String())
	}
	if got := store.List(time.Now().UTC()); len(got) != 0 {
		t.Fatalf("memory store must stay empty after DB failure, has %d silences", len(got))
	}
	// Internal failure details must not leak to the client.
	if strings.Contains(rec.Body.String(), "database connection") {
		t.Fatalf("internal error leaked to client: %s", rec.Body.String())
	}
}

func TestSilencesHandler_DBFirstCreate_ValidationErrorIs400(t *testing.T) {
	store := memory.NewSilenceStore()
	repo := &mockSilenceRepo{
		createFn: func(_ context.Context, _ *coresilencing.Silence) (*coresilencing.Silence, error) {
			return nil, fmt.Errorf("%w: createdBy is required", infrasilencing.ErrValidation)
		},
	}
	registry := &fakeRegistry{
		alertStore:   memory.NewAlertStore(),
		silenceStore: store,
		silenceRepo:  repo,
	}

	rec := postSilence(t, SilencesHandler(registry), validSilenceBody(t))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
	if got := store.List(time.Now().UTC()); len(got) != 0 {
		t.Fatalf("memory store must stay empty after validation failure, has %d silences", len(got))
	}
}

func TestSilencesHandler_DBFirstCreate_BadPayloadDoesNotHitRepo(t *testing.T) {
	repo := &mockSilenceRepo{
		createFn: func(_ context.Context, _ *coresilencing.Silence) (*coresilencing.Silence, error) {
			t.Fatal("repository must not be called for an invalid payload")
			return nil, nil
		},
	}
	registry := &fakeRegistry{
		alertStore:   memory.NewAlertStore(),
		silenceStore: memory.NewSilenceStore(),
		silenceRepo:  repo,
	}

	rec := postSilence(t, SilencesHandler(registry), `{"matchers":[],"endsAt":""}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
	if repo.createCalls != 0 {
		t.Fatalf("CreateSilence called %d times, want 0", repo.createCalls)
	}
}

func TestSilencesHandler_DBFirstUpdate_UnknownIDIs400(t *testing.T) {
	store := memory.NewSilenceStore()
	repo := &mockSilenceRepo{
		getFn: func(_ context.Context, id string) (*coresilencing.Silence, error) {
			return nil, fmt.Errorf("%w: silence with ID %s", infrasilencing.ErrSilenceNotFound, id)
		},
	}
	registry := &fakeRegistry{
		alertStore:   memory.NewAlertStore(),
		silenceStore: store,
		silenceRepo:  repo,
	}

	now := time.Now().UTC()
	body := `{"id":"` + testRepoID + `","matchers":[{"name":"alertname","value":"X"}],"endsAt":"` +
		now.Add(time.Hour).Format(time.RFC3339) + `","createdBy":"tester","comment":"upd"}`

	rec := postSilence(t, SilencesHandler(registry), body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST status = %d, want 400; body: %s", rec.Code, rec.Body.String())
	}
	// Memory-only path reports "silence not found" for unknown update IDs;
	// the DB-first path keeps that contract.
	if !strings.Contains(rec.Body.String(), "silence not found") {
		t.Fatalf("body = %s, want 'silence not found'", rec.Body.String())
	}
}

func TestSilencesHandler_DBFirstUpdate_Success(t *testing.T) {
	store := memory.NewSilenceStore()
	now := time.Now().UTC()

	// Seed memory with the pre-update version under the repository ID.
	if _, err := store.Upsert(&core.SilenceInput{
		ID:        testRepoID,
		Matchers:  []core.SilenceMatcherInput{{Name: "alertname", Value: "X"}},
		StartsAt:  now.Add(-time.Minute).Format(time.RFC3339),
		EndsAt:    now.Add(time.Hour).Format(time.RFC3339),
		CreatedBy: "tester",
		Comment:   "before",
	}, now); err != nil {
		t.Fatalf("seed memory store: %v", err)
	}

	existing := &coresilencing.Silence{
		ID:        testRepoID,
		CreatedBy: "tester",
		Comment:   "before",
		StartsAt:  now.Add(-time.Minute),
		EndsAt:    now.Add(time.Hour),
		CreatedAt: now.Add(-time.Minute),
		Matchers:  []coresilencing.Matcher{{Name: "alertname", Value: "X", Type: coresilencing.MatcherTypeEqual}},
	}
	var updateSaw *coresilencing.Silence
	repo := &mockSilenceRepo{
		getFn: func(_ context.Context, _ string) (*coresilencing.Silence, error) {
			return existing, nil
		},
		updateFn: func(_ context.Context, s *coresilencing.Silence) error {
			updateSaw = s
			return nil
		},
	}
	registry := &fakeRegistry{
		alertStore:   memory.NewAlertStore(),
		silenceStore: store,
		silenceRepo:  repo,
	}

	body := `{"id":"` + testRepoID + `","matchers":[{"name":"alertname","value":"X"}],"endsAt":"` +
		now.Add(2*time.Hour).Format(time.RFC3339) + `","createdBy":"tester","comment":"after"}`

	rec := postSilence(t, SilencesHandler(registry), body)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status = %d; body: %s", rec.Code, rec.Body.String())
	}
	if updateSaw == nil || updateSaw.Comment != "after" {
		t.Fatalf("repository update payload = %+v, want comment 'after'", updateSaw)
	}
	if !updateSaw.CreatedAt.Equal(existing.CreatedAt) {
		t.Fatalf("CreatedAt not carried from existing row: %v", updateSaw.CreatedAt)
	}

	got, ok := store.Get(testRepoID, time.Now().UTC())
	if !ok || got.Comment != "after" {
		t.Fatalf("memory cache not refreshed after DB update: %+v (found=%v)", got, ok)
	}
}

func TestSilenceByIDHandler_DBFirstDelete_Success(t *testing.T) {
	store := memory.NewSilenceStore()
	now := time.Now().UTC()
	if _, err := store.Upsert(&core.SilenceInput{
		ID:        testRepoID,
		Matchers:  []core.SilenceMatcherInput{{Name: "alertname", Value: "X"}},
		EndsAt:    now.Add(time.Hour).Format(time.RFC3339),
		CreatedBy: "tester",
		Comment:   "delete me",
	}, now); err != nil {
		t.Fatalf("seed memory store: %v", err)
	}

	repo := &mockSilenceRepo{
		deleteFn: func(_ context.Context, id string) error {
			if id != testRepoID {
				t.Fatalf("repository delete got id %q, want %q", id, testRepoID)
			}
			return nil
		},
	}
	registry := &fakeRegistry{
		alertStore:   memory.NewAlertStore(),
		silenceStore: store,
		silenceRepo:  repo,
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/v2/silence/"+testRepoID, nil)
	rec := httptest.NewRecorder()
	SilenceByIDHandler(registry)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, want 200", rec.Code)
	}
	if repo.deleteCalls != 1 {
		t.Fatalf("DeleteSilence called %d times, want 1", repo.deleteCalls)
	}
	if _, ok := store.Get(testRepoID, time.Now().UTC()); ok {
		t.Fatal("silence still present in memory after DB-first delete")
	}
}

func TestSilenceByIDHandler_DBFirstDelete_RepoErrorKeepsMemory(t *testing.T) {
	store := memory.NewSilenceStore()
	now := time.Now().UTC()
	if _, err := store.Upsert(&core.SilenceInput{
		ID:        testRepoID,
		Matchers:  []core.SilenceMatcherInput{{Name: "alertname", Value: "X"}},
		EndsAt:    now.Add(time.Hour).Format(time.RFC3339),
		CreatedBy: "tester",
		Comment:   "keep me",
	}, now); err != nil {
		t.Fatalf("seed memory store: %v", err)
	}

	repo := &mockSilenceRepo{
		deleteFn: func(_ context.Context, _ string) error {
			return errors.New("connection refused")
		},
	}
	registry := &fakeRegistry{
		alertStore:   memory.NewAlertStore(),
		silenceStore: store,
		silenceRepo:  repo,
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/v2/silence/"+testRepoID, nil)
	rec := httptest.NewRecorder()
	SilenceByIDHandler(registry)(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("DELETE status = %d, want 500", rec.Code)
	}
	if _, ok := store.Get(testRepoID, time.Now().UTC()); !ok {
		t.Fatal("memory entry must survive a failed DB delete (DB stays source of truth)")
	}
}

func TestSilenceByIDHandler_DBFirstDelete_NotFoundAnywhereIs404(t *testing.T) {
	repo := &mockSilenceRepo{
		deleteFn: func(_ context.Context, id string) error {
			return fmt.Errorf("%w: silence with ID %s", infrasilencing.ErrSilenceNotFound, id)
		},
	}
	registry := &fakeRegistry{
		alertStore:   memory.NewAlertStore(),
		silenceStore: memory.NewSilenceStore(),
		silenceRepo:  repo,
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/v2/silence/"+testRepoID, nil)
	rec := httptest.NewRecorder()
	SilenceByIDHandler(registry)(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("DELETE status = %d, want 404", rec.Code)
	}
}

func TestSilenceByIDHandler_DBFirstDelete_StaleCacheEntryEvicted(t *testing.T) {
	store := memory.NewSilenceStore()
	now := time.Now().UTC()
	if _, err := store.Upsert(&core.SilenceInput{
		ID:        testRepoID,
		Matchers:  []core.SilenceMatcherInput{{Name: "alertname", Value: "X"}},
		EndsAt:    now.Add(time.Hour).Format(time.RFC3339),
		CreatedBy: "tester",
		Comment:   "stale cache entry",
	}, now); err != nil {
		t.Fatalf("seed memory store: %v", err)
	}

	repo := &mockSilenceRepo{
		deleteFn: func(_ context.Context, id string) error {
			return fmt.Errorf("%w: silence with ID %s", infrasilencing.ErrSilenceNotFound, id)
		},
	}
	registry := &fakeRegistry{
		alertStore:   memory.NewAlertStore(),
		silenceStore: store,
		silenceRepo:  repo,
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/v2/silence/"+testRepoID, nil)
	rec := httptest.NewRecorder()
	SilenceByIDHandler(registry)(rec, req)

	// The entry existed only in memory: deleting it converges memory back to
	// the database state, and the client's intent (silence gone) is satisfied.
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, want 200", rec.Code)
	}
	if _, ok := store.Get(testRepoID, time.Now().UTC()); ok {
		t.Fatal("stale cache entry must be evicted")
	}
}
