package application

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	appconfig "github.com/ipiton/AMP/internal/config"
	"github.com/ipiton/AMP/internal/core"
	coresilencing "github.com/ipiton/AMP/internal/core/silencing"
	"github.com/ipiton/AMP/internal/infrastructure/cache"
	infrasilencing "github.com/ipiton/AMP/internal/infrastructure/silencing"
	"github.com/ipiton/AMP/internal/infrastructure/storage/memory"
)

// fakeSharedSilenceRepo is a mutable, concurrency-safe fake of
// infrasilencing.SilenceRepository representing the ONE shared PostgreSQL
// database that every replica's silenceRepo points at in production. Tests
// below run two independent *ServiceRegistry "replicas" against the same
// instance to exercise task 6.3's cross-replica sync without a real
// database.
type fakeSharedSilenceRepo struct {
	infrasilencing.SilenceRepository

	mu       sync.Mutex
	silences map[string]*coresilencing.Silence
}

func newFakeSharedSilenceRepo() *fakeSharedSilenceRepo {
	return &fakeSharedSilenceRepo{silences: make(map[string]*coresilencing.Silence)}
}

func (f *fakeSharedSilenceRepo) put(s *coresilencing.Silence) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *s
	f.silences[s.ID] = &cp
}

func (f *fakeSharedSilenceRepo) remove(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.silences, id)
}

func (f *fakeSharedSilenceRepo) GetSilenceByID(_ context.Context, id string) (*coresilencing.Silence, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.silences[id]
	if !ok {
		return nil, infrasilencing.ErrSilenceNotFound
	}
	cp := *s
	return &cp, nil
}

func (f *fakeSharedSilenceRepo) ListSilences(_ context.Context, _ infrasilencing.SilenceFilter) ([]*coresilencing.Silence, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*coresilencing.Silence, 0, len(f.silences))
	for _, s := range f.silences {
		cp := *s
		out = append(out, &cp)
	}
	return out, nil
}

func testSilence(id string, startsAt, endsAt time.Time, matcherName, matcherValue string) *coresilencing.Silence {
	return &coresilencing.Silence{
		ID:        id,
		CreatedBy: "tester",
		Comment:   "task 6.3 sync test",
		StartsAt:  startsAt,
		EndsAt:    endsAt,
		Matchers:  []coresilencing.Matcher{{Name: matcherName, Value: matcherValue, Type: coresilencing.MatcherTypeEqual}},
	}
}

func waitForSync(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !cond() {
		t.Fatalf("condition not met within %s", timeout)
	}
}

// --- applySilenceEvent: targeted single-ID apply -------------------------

func TestApplySilenceEvent_UpsertMirrorsFromRepo(t *testing.T) {
	now := time.Now().UTC()
	repo := newFakeSharedSilenceRepo()
	s := testSilence("550e8400-e29b-41d4-a716-446655440000", now.Add(-time.Minute), now.Add(time.Hour), "alertname", "NodeDown")
	repo.put(s)

	r := &ServiceRegistry{
		logger:       slog.Default(),
		silenceRepo:  repo,
		silenceStore: memory.NewSilenceStore(),
	}

	r.applySilenceEvent(context.Background(), infrasilencing.SilenceEvent{ID: s.ID, Op: infrasilencing.SilenceEventUpsert})

	got, ok := r.silenceStore.Get(s.ID, now)
	if !ok {
		t.Fatal("silence not mirrored into local store")
	}
	if got.Status.State != "active" {
		t.Fatalf("state = %q, want active", got.Status.State)
	}
	if !r.silenceStore.HasActiveMatch(map[string]string{"alertname": "NodeDown"}, now) {
		t.Fatal("silenced check must flip to true after applying the upsert event")
	}
}

func TestApplySilenceEvent_DeleteEvictsLocalEntry(t *testing.T) {
	now := time.Now().UTC()
	store := memory.NewSilenceStore()
	if _, err := store.Upsert(coreSilenceInputFor(t, "550e8400-e29b-41d4-a716-446655440000", now), now); err != nil {
		t.Fatalf("seed store: %v", err)
	}

	r := &ServiceRegistry{
		logger:       slog.Default(),
		silenceRepo:  newFakeSharedSilenceRepo(), // empty: delete never needs to fetch by ID
		silenceStore: store,
	}

	r.applySilenceEvent(context.Background(), infrasilencing.SilenceEvent{
		ID: "550e8400-e29b-41d4-a716-446655440000",
		Op: infrasilencing.SilenceEventDelete,
	})

	if _, ok := store.Get("550e8400-e29b-41d4-a716-446655440000", now); ok {
		t.Fatal("silence must be evicted after a delete event")
	}
}

func TestApplySilenceEvent_UpsertNotFoundEvictsStaleLocalEntry(t *testing.T) {
	now := time.Now().UTC()
	store := memory.NewSilenceStore()
	if _, err := store.Upsert(coreSilenceInputFor(t, "550e8400-e29b-41d4-a716-446655440000", now), now); err != nil {
		t.Fatalf("seed store: %v", err)
	}

	r := &ServiceRegistry{
		logger:       slog.Default(),
		silenceRepo:  newFakeSharedSilenceRepo(), // empty: GetSilenceByID returns ErrSilenceNotFound
		silenceStore: store,
	}

	// An upsert event whose row is now gone (e.g. deleted on the writing
	// replica between publish and this replica's fetch) must converge to
	// "not present", exactly like a delete event would.
	r.applySilenceEvent(context.Background(), infrasilencing.SilenceEvent{
		ID: "550e8400-e29b-41d4-a716-446655440000",
		Op: infrasilencing.SilenceEventUpsert,
	})

	if _, ok := store.Get("550e8400-e29b-41d4-a716-446655440000", now); ok {
		t.Fatal("stale local entry must be evicted when the row no longer exists in the repository")
	}
}

func TestApplySilenceEvent_UpsertExpiredEvictsLocalEntry(t *testing.T) {
	now := time.Now().UTC()
	repo := newFakeSharedSilenceRepo()
	// EndsAt already in the past: expired by the time this replica applies it.
	s := testSilence("550e8400-e29b-41d4-a716-446655440000", now.Add(-2*time.Hour), now.Add(-time.Hour), "alertname", "NodeDown")
	repo.put(s)

	store := memory.NewSilenceStore()
	if _, err := store.Upsert(coreSilenceInputFor(t, s.ID, now), now); err != nil {
		t.Fatalf("seed store: %v", err)
	}

	r := &ServiceRegistry{
		logger:       slog.Default(),
		silenceRepo:  repo,
		silenceStore: store,
	}

	r.applySilenceEvent(context.Background(), infrasilencing.SilenceEvent{ID: s.ID, Op: infrasilencing.SilenceEventUpsert})

	if _, ok := store.Get(s.ID, now); ok {
		t.Fatal("expired silence must not remain in the active/pending read cache")
	}
}

func TestApplySilenceEvent_EmptyIDIsNoop(t *testing.T) {
	r := &ServiceRegistry{
		logger:       slog.Default(),
		silenceRepo:  newFakeSharedSilenceRepo(),
		silenceStore: memory.NewSilenceStore(),
	}
	// Must not panic and must not call the repository.
	r.applySilenceEvent(context.Background(), infrasilencing.SilenceEvent{ID: "", Op: infrasilencing.SilenceEventUpsert})
}

// --- resyncSilenceStore: full resync --------------------------------------

func TestResyncSilenceStore_EvictsStaleAndAddsNew(t *testing.T) {
	now := time.Now().UTC()
	repo := newFakeSharedSilenceRepo()
	fresh := testSilence("660e8400-e29b-41d4-a716-446655440001", now.Add(-time.Minute), now.Add(time.Hour), "alertname", "Fresh")
	repo.put(fresh)

	store := memory.NewSilenceStore()
	// Stale entry present locally but NOT in the repository (e.g. deleted on
	// another replica while this one's subscription was down).
	if _, err := store.Upsert(coreSilenceInputFor(t, "550e8400-e29b-41d4-a716-446655440000", now), now); err != nil {
		t.Fatalf("seed store: %v", err)
	}

	r := &ServiceRegistry{logger: slog.Default(), silenceRepo: repo, silenceStore: store}
	r.resyncSilenceStore(context.Background())

	if _, ok := store.Get("550e8400-e29b-41d4-a716-446655440000", now); ok {
		t.Fatal("stale entry must be evicted by a full resync")
	}
	if _, ok := store.Get(fresh.ID, now); !ok {
		t.Fatal("entry present in the repository must be added by a full resync")
	}
}

func TestResyncSilenceStore_RepoErrorLeavesStoreUnchanged(t *testing.T) {
	now := time.Now().UTC()
	store := memory.NewSilenceStore()
	if _, err := store.Upsert(coreSilenceInputFor(t, "550e8400-e29b-41d4-a716-446655440000", now), now); err != nil {
		t.Fatalf("seed store: %v", err)
	}

	r := &ServiceRegistry{
		logger:       slog.Default(),
		silenceRepo:  &fakeSilenceRepository{listErr: errors.New("connection refused")},
		silenceStore: store,
	}
	r.resyncSilenceStore(context.Background()) // must log+return, not panic

	if _, ok := store.Get("550e8400-e29b-41d4-a716-446655440000", now); !ok {
		t.Fatal("store must be left untouched when the resync fetch fails")
	}
}

// --- End-to-end: two replicas over a real (miniredis) Redis pub/sub ------

// testReplica bundles one "replica" for the cross-replica tests below: its
// own memory.SilenceStore and its own RedisSilenceEventBus (backed by the
// shared miniredis instance), pointed at the one shared repository.
type testReplica struct {
	registry *ServiceRegistry
	cache    *cache.RedisCache
}

func newTestReplica(t *testing.T, mr *miniredis.Miniredis, repo infrasilencing.SilenceRepository) *testReplica {
	t.Helper()

	redisCache, err := cache.NewRedisCache(&cache.CacheConfig{
		Addr:        mr.Addr(),
		DB:          0,
		PoolSize:    5,
		DialTimeout: time.Second,
		ReadTimeout: time.Second,
	}, slog.Default())
	if err != nil {
		t.Fatalf("NewRedisCache: %v", err)
	}

	bus, err := infrasilencing.NewRedisSilenceEventBus(context.Background(), &infrasilencing.SilenceEventBusConfig{
		Client: redisCache.GetClient(),
		Logger: slog.Default(),
	})
	if err != nil {
		t.Fatalf("NewRedisSilenceEventBus: %v", err)
	}

	return &testReplica{
		registry: &ServiceRegistry{
			logger:          slog.Default(),
			silenceRepo:     repo,
			silenceStore:    memory.NewSilenceStore(),
			silenceEventBus: bus,
		},
		cache: redisCache,
	}
}

// startSubscriber runs the replica's real subscribe+periodic-resync loop
// (ServiceRegistry.runSilenceEventSync — the exact code path
// initializeSilenceEventSync wires in production) until ctx is cancelled,
// then waits for the channel's subscriber count to reach 1 so callers know
// it's actually listening before publishing (pub/sub delivers to
// currently-subscribed clients only).
func (tr *testReplica) startSubscriber(t *testing.T, ctx context.Context) {
	t.Helper()
	tr.registry.silenceSyncDone = make(chan struct{})
	go tr.registry.runSilenceEventSync(ctx)

	waitForSync(t, 2*time.Second, func() bool {
		counts, err := tr.cache.GetClient().PubSubNumSub(ctx, "amp:silence:events").Result()
		return err == nil && counts["amp:silence:events"] >= 1
	})
}

func TestSilenceEventSync_CrossReplica_CreateThenDeleteConverges(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run: %v", err)
	}
	defer mr.Close()

	repo := newFakeSharedSilenceRepo()
	replicaA := newTestReplica(t, mr, repo) // the writer
	defer func() { _ = replicaA.cache.Close() }()
	replicaB := newTestReplica(t, mr, repo) // the reader, subscribed
	defer func() { _ = replicaB.cache.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	replicaB.startSubscriber(t, ctx)

	now := time.Now().UTC()
	const silenceID = "550e8400-e29b-41d4-a716-446655440000"
	labels := map[string]string{"alertname": "NodeDown"}

	// Precondition: replica B does not consider the alert silenced yet.
	if replicaB.registry.silenceStore.HasActiveMatch(labels, now) {
		t.Fatal("replica B must not see a silence that was never created")
	}

	// Replica A "commits to the database" (what persistSilenceDBFirst does)
	// then publishes — the exact sequence handleSilencePost follows.
	s := testSilence(silenceID, now.Add(-time.Minute), now.Add(time.Hour), "alertname", "NodeDown")
	repo.put(s)
	if err := replicaA.registry.silenceEventBus.Publish(ctx, infrasilencing.SilenceEvent{
		ID: silenceID, Op: infrasilencing.SilenceEventUpsert,
	}); err != nil {
		t.Fatalf("Publish (create): %v", err)
	}

	waitForSync(t, 2*time.Second, func() bool {
		return replicaB.registry.silenceStore.HasActiveMatch(labels, time.Now().UTC())
	})

	// Replica A deletes; replica B must unsilence without restarting.
	repo.remove(silenceID)
	if err := replicaA.registry.silenceEventBus.Publish(ctx, infrasilencing.SilenceEvent{
		ID: silenceID, Op: infrasilencing.SilenceEventDelete,
	}); err != nil {
		t.Fatalf("Publish (delete): %v", err)
	}

	waitForSync(t, 2*time.Second, func() bool {
		return !replicaB.registry.silenceStore.HasActiveMatch(labels, time.Now().UTC())
	})
}

func TestSilenceEventSync_SubscriptionReconnect_ResyncCatchesUpMissedWrite(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run: %v", err)
	}
	defer mr.Close()

	repo := newFakeSharedSilenceRepo()
	replicaB := newTestReplica(t, mr, repo)
	defer func() { _ = replicaB.cache.Close() }()

	// First subscription session, then drop it (simulating a lost Redis
	// connection) WITHOUT publishing anything — this represents an event
	// that was missed entirely because the subscription was down when it
	// would have been published.
	ctx1, cancel1 := context.WithCancel(context.Background())
	replicaB.startSubscriber(t, ctx1)
	cancel1()
	<-replicaB.registry.silenceSyncDone

	now := time.Now().UTC()
	const silenceID = "550e8400-e29b-41d4-a716-446655440000"
	repo.put(testSilence(silenceID, now.Add(-time.Minute), now.Add(time.Hour), "alertname", "MissedWhileDown"))
	// Deliberately NOT publishing an event for this write — this is exactly
	// the "subscription was down, so the event was never seen" case.

	// Resubscribing (the caller's retry loop in production) must trigger a
	// full resync on (re)connect and catch this up on its own.
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	replicaB.startSubscriber(t, ctx2)

	waitForSync(t, 2*time.Second, func() bool {
		_, ok := replicaB.registry.silenceStore.Get(silenceID, time.Now().UTC())
		return ok
	})
}

// --- initializeSilenceEventSync: profile-driven skip conditions ----------

func TestInitializeSilenceEventSync_LiteProfileIsNoop(t *testing.T) {
	r := &ServiceRegistry{
		logger:       slog.Default(),
		config:       &appconfig.Config{Profile: appconfig.ProfileLite},
		silenceRepo:  newFakeSharedSilenceRepo(),
		silenceStore: memory.NewSilenceStore(),
	}

	if err := r.initializeSilenceEventSync(context.Background()); err != nil {
		t.Fatalf("initializeSilenceEventSync in lite profile: %v", err)
	}
	if r.silenceEventBus != nil {
		t.Fatal("lite profile must not wire a silence event bus")
	}
	if r.silenceSyncCancel != nil {
		t.Fatal("lite profile must not start a subscriber goroutine")
	}
	if r.SilenceEventPublisher() != nil {
		t.Fatal("SilenceEventPublisher() must return a genuine nil interface in lite profile")
	}
}

func TestInitializeSilenceEventSync_NilRepoIsNoop(t *testing.T) {
	r := &ServiceRegistry{
		logger: slog.Default(),
		config: &appconfig.Config{Profile: appconfig.ProfileStandard},
		// silenceRepo intentionally left nil: persistence init failed/skipped.
	}

	if err := r.initializeSilenceEventSync(context.Background()); err != nil {
		t.Fatalf("initializeSilenceEventSync with nil repo: %v", err)
	}
	if r.silenceEventBus != nil {
		t.Fatal("nil silenceRepo must not wire a silence event bus")
	}
}

func TestInitializeSilenceEventSync_StandardProfileWithoutRedisIsNoop(t *testing.T) {
	// Standard profile, but r.cache is not a *cache.RedisCache (e.g. Redis
	// init already failed and fell back to memory cache — Step 1's own
	// degraded reason, not duplicated here).
	r := &ServiceRegistry{
		logger:       slog.Default(),
		config:       &appconfig.Config{Profile: appconfig.ProfileStandard},
		silenceRepo:  newFakeSharedSilenceRepo(),
		silenceStore: memory.NewSilenceStore(),
		cache:        nil,
	}

	if err := r.initializeSilenceEventSync(context.Background()); err != nil {
		t.Fatalf("initializeSilenceEventSync without Redis cache: %v", err)
	}
	if r.silenceEventBus != nil {
		t.Fatal("must not wire a silence event bus without a live Redis cache backend")
	}
}

// coreSilenceInputFor builds a minimal valid *core.SilenceInput for seeding
// memory.SilenceStore directly in the tests above.
func coreSilenceInputFor(t *testing.T, id string, now time.Time) *core.SilenceInput {
	t.Helper()
	return &core.SilenceInput{
		ID:        id,
		Matchers:  []core.SilenceMatcherInput{{Name: "alertname", Value: "X"}},
		EndsAt:    now.Add(time.Hour).Format(time.RFC3339),
		CreatedBy: "tester",
		Comment:   "seed",
	}
}
