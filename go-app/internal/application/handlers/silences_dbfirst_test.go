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
	expireFn func(ctx context.Context, id string, now time.Time) error

	createCalls int
	deleteCalls int
	expireCalls int
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

func (m *mockSilenceRepo) ExpireSilence(ctx context.Context, id string, now time.Time) error {
	m.expireCalls++
	return m.expireFn(ctx, id, now)
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

// testCoreSilenceFor builds the coresilencing.Silence a mockSilenceRepo's
// getFn should return to simulate "the row the standard-profile database
// holds after ExpireSilence just updated it" — the DELETE handler's success
// branch re-fetches by ID to mirror the exact post-update row into memory
// (see handleSilenceDelete's doc comment).
func testCoreSilenceFor(id string, endsAt time.Time) *coresilencing.Silence {
	return &coresilencing.Silence{
		ID:        id,
		CreatedBy: "tester",
		Comment:   "expired via DELETE",
		StartsAt:  endsAt.Add(-time.Hour),
		EndsAt:    endsAt,
		Status:    coresilencing.SilenceStatusExpired,
		Matchers:  []coresilencing.Matcher{{Name: "alertname", Value: "X", Type: coresilencing.MatcherTypeEqual}},
	}
}

func TestSilenceByIDHandler_DBFirstDelete_ExpiresInPlace_Success(t *testing.T) {
	store := memory.NewSilenceStore()
	now := time.Now().UTC()
	if _, err := store.Upsert(&core.SilenceInput{
		ID:        testRepoID,
		Matchers:  []core.SilenceMatcherInput{{Name: "alertname", Value: "X"}},
		EndsAt:    now.Add(time.Hour).Format(time.RFC3339),
		CreatedBy: "tester",
		Comment:   "expire me",
	}, now); err != nil {
		t.Fatalf("seed memory store: %v", err)
	}

	repo := &mockSilenceRepo{
		expireFn: func(_ context.Context, id string, expireNow time.Time) error {
			if id != testRepoID {
				t.Fatalf("repository expire got id %q, want %q", id, testRepoID)
			}
			if expireNow.IsZero() {
				t.Fatal("repository expire got zero time")
			}
			return nil
		},
		getFn: func(_ context.Context, id string) (*coresilencing.Silence, error) {
			return testCoreSilenceFor(id, now), nil
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
	if repo.expireCalls != 1 {
		t.Fatalf("ExpireSilence called %d times, want 1", repo.expireCalls)
	}
	if repo.deleteCalls != 0 {
		t.Fatalf("DeleteSilence called %d times, want 0 (F3: DELETE must expire in place, not hard-delete)", repo.deleteCalls)
	}

	// The row must survive — not be evicted — and be mirrored as expired.
	got, ok := store.Get(testRepoID, time.Now().UTC())
	if !ok {
		t.Fatal("silence must still be present in memory after DB-first expire-in-place delete")
	}
	if got.Status.State != "expired" {
		t.Fatalf("Status.State = %q, want %q", got.Status.State, "expired")
	}

	// GET /api/v2/silences (the amtool `silence query --expired` read path)
	// must also keep listing it — this is the "listing shows state=expired"
	// contract the standard/Postgres profile now shares with lite.
	listRec := httptest.NewRecorder()
	SilencesHandler(registry)(listRec, httptest.NewRequest(http.MethodGet, "/api/v2/silences", nil))
	if listRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/silences status = %d, want 200", listRec.Code)
	}
	var listed []core.APISilence
	if err := json.Unmarshal(listRec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode silences list: %v", err)
	}
	if len(listed) != 1 || listed[0].Status.State != "expired" {
		t.Fatalf("GET /api/v2/silences = %+v, want exactly 1 expired silence", listed)
	}
}

// TestSilenceByIDHandler_DBFirstDelete_PendingSilence_MirroredAsExpired is
// the round-2 review regression guard for the DB-first (standard/Postgres)
// path: the actual starts_at-forcing happens in
// PostgresSilenceRepository.ExpireSilence's SQL (see its dedicated
// skip-for-DB tests in postgres_silence_repository_test.go), but the
// handler's re-fetch-and-mirror step must faithfully carry that result
// (starts_at ALSO moved to now, not just ends_at) into the memory read
// cache — a silence mirrored with its ORIGINAL far-future starts_at would
// keep reporting "pending" (silenceState checks StartsAt before EndsAt)
// even though the database already says "expired".
func TestSilenceByIDHandler_DBFirstDelete_PendingSilence_MirroredAsExpired(t *testing.T) {
	store := memory.NewSilenceStore()
	now := time.Now().UTC()
	farFuture := now.Add(24 * time.Hour)
	if _, err := store.Upsert(&core.SilenceInput{
		ID:        testRepoID,
		Matchers:  []core.SilenceMatcherInput{{Name: "alertname", Value: "X"}},
		StartsAt:  farFuture.Format(time.RFC3339),
		EndsAt:    farFuture.Add(time.Hour).Format(time.RFC3339),
		CreatedBy: "tester",
		Comment:   "still pending",
	}, now); err != nil {
		t.Fatalf("seed memory store: %v", err)
	}

	// Precondition: before DELETE, this reads as pending.
	preDelete, ok := store.Get(testRepoID, now)
	if !ok || preDelete.Status.State != "pending" {
		t.Fatalf("precondition failed: Status.State = %q (found=%v), want %q", preDelete.Status.State, ok, "pending")
	}

	repo := &mockSilenceRepo{
		expireFn: func(_ context.Context, _ string, _ time.Time) error { return nil },
		getFn: func(_ context.Context, id string) (*coresilencing.Silence, error) {
			// Simulates what PostgresSilenceRepository.ExpireSilence's
			// LEAST(starts_at, now)/LEAST(ends_at, now) UPDATE actually
			// produces for a silence that was pending: BOTH timestamps
			// forced to now, matching upstream's expire() semantics.
			return &coresilencing.Silence{
				ID:        id,
				CreatedBy: "tester",
				Comment:   "still pending",
				StartsAt:  now,
				EndsAt:    now,
				Status:    coresilencing.SilenceStatusExpired,
				Matchers:  []coresilencing.Matcher{{Name: "alertname", Value: "X", Type: coresilencing.MatcherTypeEqual}},
			}, nil
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

	got, ok := store.Get(testRepoID, time.Now().UTC())
	if !ok {
		t.Fatal("silence must still be present in memory after DB-first expire-in-place delete")
	}
	if got.Status.State != "expired" {
		t.Fatalf("Status.State = %q, want %q (a pending silence must expire immediately on delete, matching upstream)", got.Status.State, "expired")
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
		expireFn: func(_ context.Context, _ string, _ time.Time) error {
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
	got, ok := store.Get(testRepoID, time.Now().UTC())
	if !ok {
		t.Fatal("memory entry must survive a failed DB expire (DB stays source of truth)")
	}
	if got.Status.State == "expired" {
		t.Fatal("memory entry must be untouched (still active), not force-expired, when the DB call failed")
	}
}

func TestSilenceByIDHandler_DBFirstDelete_NotFoundAnywhereIs404(t *testing.T) {
	repo := &mockSilenceRepo{
		expireFn: func(_ context.Context, id string, _ time.Time) error {
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
		expireFn: func(_ context.Context, id string, _ time.Time) error {
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

// fakeSilenceEventPublisher records Publish calls (task 6.3) for the
// handler-level tests below, optionally failing to verify that a publish
// error never surfaces as an HTTP error — the database write already
// committed by the time Publish is called.
type fakeSilenceEventPublisher struct {
	published []infrasilencing.SilenceEvent
	err       error
}

func (p *fakeSilenceEventPublisher) Publish(_ context.Context, event infrasilencing.SilenceEvent) error {
	p.published = append(p.published, event)
	return p.err
}

func TestSilencesHandler_DBFirstCreate_PublishesUpsertEvent(t *testing.T) {
	store := memory.NewSilenceStore()
	repo := &mockSilenceRepo{
		createFn: func(_ context.Context, s *coresilencing.Silence) (*coresilencing.Silence, error) {
			created := *s
			created.ID = testRepoID
			created.CreatedAt = time.Now().UTC()
			return &created, nil
		},
	}
	pub := &fakeSilenceEventPublisher{}
	registry := &fakeRegistry{
		alertStore:      memory.NewAlertStore(),
		silenceStore:    store,
		silenceRepo:     repo,
		silenceEventPub: pub,
	}

	rec := postSilence(t, SilencesHandler(registry), validSilenceBody(t))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status = %d; body: %s", rec.Code, rec.Body.String())
	}

	if len(pub.published) != 1 {
		t.Fatalf("published %d events, want 1", len(pub.published))
	}
	if pub.published[0] != (infrasilencing.SilenceEvent{ID: testRepoID, Op: infrasilencing.SilenceEventUpsert}) {
		t.Fatalf("published event = %+v, want {%s upsert}", pub.published[0], testRepoID)
	}
}

func TestSilencesHandler_DBFirstCreate_PublishErrorDoesNotFailRequest(t *testing.T) {
	store := memory.NewSilenceStore()
	repo := &mockSilenceRepo{
		createFn: func(_ context.Context, s *coresilencing.Silence) (*coresilencing.Silence, error) {
			created := *s
			created.ID = testRepoID
			created.CreatedAt = time.Now().UTC()
			return &created, nil
		},
	}
	pub := &fakeSilenceEventPublisher{err: errors.New("redis unreachable")}
	registry := &fakeRegistry{
		alertStore:      memory.NewAlertStore(),
		silenceStore:    store,
		silenceRepo:     repo,
		silenceEventPub: pub,
	}

	rec := postSilence(t, SilencesHandler(registry), validSilenceBody(t))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status = %d, want 200 even though publish failed; body: %s", rec.Code, rec.Body.String())
	}
	if _, ok := store.Get(testRepoID, time.Now().UTC()); !ok {
		t.Fatal("memory cache must still be updated even though the publish failed")
	}
}

func TestSilencesHandler_DBFirstCreate_NilPublisherIsNoop(t *testing.T) {
	store := memory.NewSilenceStore()
	repo := &mockSilenceRepo{
		createFn: func(_ context.Context, s *coresilencing.Silence) (*coresilencing.Silence, error) {
			created := *s
			created.ID = testRepoID
			created.CreatedAt = time.Now().UTC()
			return &created, nil
		},
	}
	registry := &fakeRegistry{
		alertStore:   memory.NewAlertStore(),
		silenceStore: store,
		silenceRepo:  repo,
		// silenceEventPub deliberately left nil — lite profile / no Redis.
	}

	rec := postSilence(t, SilencesHandler(registry), validSilenceBody(t))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status = %d; body: %s", rec.Code, rec.Body.String())
	}
}

// TestSilenceByIDHandler_DBFirstDelete_PublishesUpsertEvent guards the F3
// event-op fix: a successful DELETE now expires the silence in place, which
// is an UPDATE to the row (not a removal), so the cross-replica event must
// be SilenceEventUpsert — publishing SilenceEventDelete here would make
// other replicas' applySilenceEvent evict the row instead of mirroring its
// new expired state.
func TestSilenceByIDHandler_DBFirstDelete_PublishesUpsertEvent(t *testing.T) {
	store := memory.NewSilenceStore()
	now := time.Now().UTC()
	if _, err := store.Upsert(&core.SilenceInput{
		ID:        testRepoID,
		Matchers:  []core.SilenceMatcherInput{{Name: "alertname", Value: "X"}},
		EndsAt:    now.Add(time.Hour).Format(time.RFC3339),
		CreatedBy: "tester",
		Comment:   "expire me",
	}, now); err != nil {
		t.Fatalf("seed memory store: %v", err)
	}

	repo := &mockSilenceRepo{
		expireFn: func(_ context.Context, _ string, _ time.Time) error { return nil },
		getFn: func(_ context.Context, id string) (*coresilencing.Silence, error) {
			return testCoreSilenceFor(id, now), nil
		},
	}
	pub := &fakeSilenceEventPublisher{}
	registry := &fakeRegistry{
		alertStore:      memory.NewAlertStore(),
		silenceStore:    store,
		silenceRepo:     repo,
		silenceEventPub: pub,
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/v2/silence/"+testRepoID, nil)
	rec := httptest.NewRecorder()
	SilenceByIDHandler(registry)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, want 200", rec.Code)
	}
	if len(pub.published) != 1 || pub.published[0] != (infrasilencing.SilenceEvent{ID: testRepoID, Op: infrasilencing.SilenceEventUpsert}) {
		t.Fatalf("published events = %+v, want exactly one upsert event for %s", pub.published, testRepoID)
	}
}

func TestSilenceByIDHandler_DBFirstDelete_RepoErrorDoesNotPublish(t *testing.T) {
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
		expireFn: func(_ context.Context, _ string, _ time.Time) error { return errors.New("connection refused") },
	}
	pub := &fakeSilenceEventPublisher{}
	registry := &fakeRegistry{
		alertStore:      memory.NewAlertStore(),
		silenceStore:    store,
		silenceRepo:     repo,
		silenceEventPub: pub,
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/v2/silence/"+testRepoID, nil)
	rec := httptest.NewRecorder()
	SilenceByIDHandler(registry)(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("DELETE status = %d, want 500", rec.Code)
	}
	if len(pub.published) != 0 {
		t.Fatalf("published %d events after a failed DB delete, want 0", len(pub.published))
	}
}
