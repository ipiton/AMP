package application

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	appconfig "github.com/ipiton/AMP/internal/config"
	"github.com/ipiton/AMP/internal/core"
	infracache "github.com/ipiton/AMP/internal/infrastructure/cache"
	"github.com/ipiton/AMP/internal/infrastructure/grouping"
	infrapublishing "github.com/ipiton/AMP/internal/infrastructure/publishing"
	infraroute "github.com/ipiton/AMP/internal/infrastructure/routing"
	"github.com/ipiton/AMP/internal/infrastructure/routing/timeinterval"
)

// newTestRegistryForGrouping builds a bare ServiceRegistry (no full
// Initialize()) with just enough state for initializeGrouping's dependencies:
// config and logger. Cache is set separately per test.
func newTestRegistryForGrouping(cfg *appconfig.Config) *ServiceRegistry {
	return &ServiceRegistry{
		config: cfg,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// minimalRouteTree returns a route: tree config sufficient for
// BuildGroupingConfig (task 2.2's config adapter) — just a receiver and a
// group_by label.
func minimalRouteTree() *infraroute.RouteConfig {
	return &infraroute.RouteConfig{
		Route: &grouping.Route{
			Receiver: "default",
			GroupBy:  []string{"alertname"},
		},
	}
}

func TestInitializeGrouping_DisabledByDefault(t *testing.T) {
	cfg := &appconfig.Config{
		Profile:  appconfig.ProfileLite,
		Grouping: appconfig.GroupingConfig{Enabled: false},
		Routing:  minimalRouteTree(),
	}
	r := newTestRegistryForGrouping(cfg)

	if err := r.initializeGrouping(context.Background()); err != nil {
		t.Fatalf("initializeGrouping() error = %v, want nil (disabled is a clean skip)", err)
	}
	if r.groupManager != nil {
		t.Fatalf("groupManager must stay nil when grouping.enabled=false")
	}
	if r.groupTimerManager != nil {
		t.Fatalf("groupTimerManager must stay nil when grouping.enabled=false")
	}
}

func TestInitializeGrouping_NoRouteTreeIsCleanSkip(t *testing.T) {
	cfg := &appconfig.Config{
		Profile:  appconfig.ProfileLite,
		Grouping: appconfig.GroupingConfig{Enabled: true},
		Routing:  nil, // no route: section
	}
	r := newTestRegistryForGrouping(cfg)

	if err := r.initializeGrouping(context.Background()); err != nil {
		t.Fatalf("initializeGrouping() error = %v, want nil (missing route tree is a clean skip)", err)
	}
	if r.groupManager != nil {
		t.Fatalf("groupManager must stay nil without a route tree")
	}
	if r.groupTimerManager != nil {
		t.Fatalf("groupTimerManager must stay nil without a route tree")
	}
}

func TestInitializeGrouping_LiteUsesMemoryStorageAndIsFunctional(t *testing.T) {
	cfg := &appconfig.Config{
		Profile:  appconfig.ProfileLite,
		Grouping: appconfig.GroupingConfig{Enabled: true},
		Routing:  minimalRouteTree(),
	}
	r := newTestRegistryForGrouping(cfg)
	// No cache configured — lite profile must not need one for grouping.

	ctx := context.Background()
	if err := r.initializeGrouping(ctx); err != nil {
		t.Fatalf("initializeGrouping() error = %v", err)
	}
	if r.groupManager == nil {
		t.Fatalf("groupManager must be initialized in lite profile with grouping enabled")
	}
	if r.groupTimerManager == nil {
		t.Fatalf("groupTimerManager must be initialized in lite profile with grouping enabled")
	}
	// Lite is memory-by-design, not a degradation from an expected Redis
	// path — no degraded reason should be recorded for it.
	if len(r.degradedReasons) != 0 {
		t.Fatalf("degradedReasons = %v, want empty for lite profile (memory storage is by design)", r.degradedReasons)
	}

	// Functional round-trip: memory storage actually works end to end.
	alert := &core.Alert{
		Fingerprint: "fp-1",
		AlertName:   "HighCPU",
		Status:      core.StatusFiring,
		Labels:      map[string]string{"alertname": "HighCPU"},
		StartsAt:    time.Now(),
	}
	groupKey := grouping.GroupKey("alertname=HighCPU")
	if _, err := r.groupManager.AddAlertToGroup(ctx, alert, groupKey); err != nil {
		t.Fatalf("AddAlertToGroup() error = %v", err)
	}
	group, err := r.groupManager.GetGroup(ctx, groupKey)
	if err != nil {
		t.Fatalf("GetGroup() error = %v", err)
	}
	if group.Size() != 1 {
		t.Fatalf("group.Size() = %d, want 1", group.Size())
	}

	// Shutdown must be clean (no error, no leaked goroutines beyond timeout).
	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := r.groupTimerManager.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("groupTimerManager.Shutdown() error = %v", err)
	}
}

// recordingGroupPublisher implements grouping.GroupNotificationPublisher
// (task 2.4) for the end-to-end test below.
type recordingGroupPublisher struct {
	mu    sync.Mutex
	calls [][]*core.Alert
}

func (p *recordingGroupPublisher) PublishGroup(_ context.Context, _ string, alerts []*core.Alert, _ string, _ map[string]string, skipTarget func(string) bool) ([]grouping.TargetPublishOutcome, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if skipTarget != nil && skipTarget("test") {
		return nil, nil
	}
	p.calls = append(p.calls, alerts)
	return []grouping.TargetPublishOutcome{{Target: "test", Success: true}}, nil
}

func (p *recordingGroupPublisher) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls)
}

// TestGroupingEndToEnd_IngestToGroupWaitFiresOneNotification is the task
// 2.4 integration test requested by the plan: ingest (AddAlertToGroup) all
// the way through a real group_wait timer firing produces exactly ONE
// PublishGroup call — not one per alert — even though two alerts were
// added to the same group. Uses short timings (10ms) so the test doesn't
// need to sleep for production-realistic durations.
func TestGroupingEndToEnd_IngestToGroupWaitFiresOneNotification(t *testing.T) {
	cfg := &appconfig.Config{
		Profile:  appconfig.ProfileLite,
		Grouping: appconfig.GroupingConfig{Enabled: true},
		Routing: &infraroute.RouteConfig{
			Route: &grouping.Route{
				Receiver:      "default",
				GroupBy:       []string{"alertname"},
				GroupWait:     &grouping.Duration{Duration: 10 * time.Millisecond},
				GroupInterval: &grouping.Duration{Duration: time.Hour}, // long enough to not fire during this test
			},
		},
	}
	r := newTestRegistryForGrouping(cfg)

	ctx := context.Background()
	if err := r.initializeGrouping(ctx); err != nil {
		t.Fatalf("initializeGrouping() error = %v", err)
	}
	if r.groupManager == nil {
		t.Fatalf("groupManager must be initialized")
	}

	// Task 2.4's registry-ordering workaround: SetPublisher after the fact,
	// mirroring what initializeAlertProcessor does once r.publisher exists.
	pub := &recordingGroupPublisher{}
	r.groupManager.SetPublisher(pub)

	groupKey := grouping.GroupKey("receiver=default/alertname=HighCPU")
	alert1 := &core.Alert{
		Fingerprint: "fp-1",
		AlertName:   "HighCPU",
		Status:      core.StatusFiring,
		Labels:      map[string]string{"alertname": "HighCPU"},
		StartsAt:    time.Now(),
	}
	alert2 := &core.Alert{
		Fingerprint: "fp-2",
		AlertName:   "HighCPU",
		Status:      core.StatusFiring,
		Labels:      map[string]string{"alertname": "HighCPU"},
		StartsAt:    time.Now(),
	}

	if _, err := r.groupManager.AddAlertToGroup(ctx, alert1, groupKey); err != nil {
		t.Fatalf("AddAlertToGroup(alert1) error = %v", err)
	}
	if _, err := r.groupManager.AddAlertToGroup(ctx, alert2, groupKey); err != nil {
		t.Fatalf("AddAlertToGroup(alert2) error = %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && pub.callCount() < 1 {
		time.Sleep(5 * time.Millisecond)
	}

	if pub.callCount() != 1 {
		t.Fatalf("PublishGroup call count = %d, want exactly 1 (one grouped notification, not one per alert)", pub.callCount())
	}
	if len(pub.calls[0]) != 2 {
		t.Fatalf("the one PublishGroup call carried %d alerts, want 2", len(pub.calls[0]))
	}

	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := r.groupTimerManager.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("groupTimerManager.Shutdown() error = %v", err)
	}
}

func TestInitializeGrouping_StandardWithoutRedisCacheFallsBackToMemory(t *testing.T) {
	cfg := &appconfig.Config{
		Profile:  appconfig.ProfileStandard,
		Grouping: appconfig.GroupingConfig{Enabled: true},
		Routing:  minimalRouteTree(),
	}
	r := newTestRegistryForGrouping(cfg)
	r.cache = infracache.NewMemoryCache(r.logger) // not *cache.RedisCache

	if err := r.initializeGrouping(context.Background()); err != nil {
		t.Fatalf("initializeGrouping() error = %v, want graceful fallback to memory storage", err)
	}
	if r.groupManager == nil {
		t.Fatalf("groupManager must still be initialized via memory-storage fallback")
	}
	if r.groupTimerManager == nil {
		t.Fatalf("groupTimerManager must still be initialized via memory-storage fallback")
	}
	// This fallback is by-design (no Redis cache configured at all) and
	// already covered by Step 1's initializeCache degraded reason when it
	// happens for real — newGroupingStorage must not add a second, redundant
	// reason for the same underlying situation.
	if len(r.degradedReasons) != 0 {
		t.Fatalf("degradedReasons = %v, want empty for the by-design no-Redis-cache fallback", r.degradedReasons)
	}
}

// TestInitializeGrouping_StandardRedisStorageFailureAddsDegradedReason
// verifies the fix-round-1 finding: when the cache backend IS a live
// *cache.RedisCache (so Step 1's initializeCache degraded reason was never
// recorded) but the grouping-specific Redis storage fails to initialize
// anyway, initializeGrouping must add its own degraded reason so the
// resulting "no timer persistence across restart" state is visible via
// /health//readiness instead of vanishing into a Warn log only.
func TestInitializeGrouping_StandardRedisStorageFailureAddsDegradedReason(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run() error = %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	redisCache, err := infracache.NewRedisCache(&infracache.CacheConfig{
		Addr:        mr.Addr(),
		PoolSize:    5,
		DialTimeout: time.Second,
		ReadTimeout: time.Second,
	}, logger)
	if err != nil {
		t.Fatalf("NewRedisCache() error = %v", err)
	}
	defer func() { _ = redisCache.Close() }()

	// Kill the backing Redis server AFTER the cache was constructed
	// successfully: r.cache is still a live *cache.RedisCache (branch 1's
	// type check passes, so Step 1's degraded reason path is not in play),
	// but grouping's own NewRedisGroupStorage — which pings Redis during
	// construction — now fails.
	mr.Close()

	cfg := &appconfig.Config{
		Profile:  appconfig.ProfileStandard,
		Grouping: appconfig.GroupingConfig{Enabled: true},
		Routing:  minimalRouteTree(),
	}
	r := newTestRegistryForGrouping(cfg)
	r.cache = redisCache

	if err := r.initializeGrouping(context.Background()); err != nil {
		t.Fatalf("initializeGrouping() error = %v, want graceful fallback to memory storage", err)
	}
	if r.groupManager == nil || r.groupTimerManager == nil {
		t.Fatalf("groupManager/groupTimerManager must still be initialized via memory-storage fallback")
	}

	if len(r.degradedReasons) == 0 {
		t.Fatalf("degradedReasons must be non-empty: a healthy-cache-typed but failing Redis storage init must be reported")
	}
	found := false
	for _, reason := range r.degradedReasons {
		if strings.Contains(reason, "grouping storage degraded") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("degradedReasons = %v, want an entry containing %q", r.degradedReasons, "grouping storage degraded")
	}
}

// TestInitializeGrouping_StandardRedisNflogFailureAddsDegradedReason
// verifies task 6.1's wiring: when the cache backend IS a live
// *cache.RedisCache but the underlying Redis is unreachable, both
// newGroupingStorage's and newNotifyLog's own Redis checks fail
// independently, and initializeGrouping must add a degraded reason
// specifically calling out the nflog (not just grouping storage) so a
// "no cross-replica notification dedup" state is visible via
// /health//readiness rather than only a Warn log.
func TestInitializeGrouping_StandardRedisNflogFailureAddsDegradedReason(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run() error = %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	redisCache, err := infracache.NewRedisCache(&infracache.CacheConfig{
		Addr:        mr.Addr(),
		PoolSize:    5,
		DialTimeout: time.Second,
		ReadTimeout: time.Second,
	}, logger)
	if err != nil {
		t.Fatalf("NewRedisCache() error = %v", err)
	}
	defer func() { _ = redisCache.Close() }()

	// Kill the backing Redis server AFTER the cache was constructed
	// successfully, same as TestInitializeGrouping_StandardRedisStorageFailureAddsDegradedReason.
	mr.Close()

	cfg := &appconfig.Config{
		Profile:  appconfig.ProfileStandard,
		Grouping: appconfig.GroupingConfig{Enabled: true},
		Routing:  minimalRouteTree(),
	}
	r := newTestRegistryForGrouping(cfg)
	r.cache = redisCache

	if err := r.initializeGrouping(context.Background()); err != nil {
		t.Fatalf("initializeGrouping() error = %v, want graceful fallback to memory nflog", err)
	}
	if r.groupManager == nil {
		t.Fatalf("groupManager must still be initialized via memory-nflog fallback")
	}

	found := false
	for _, reason := range r.degradedReasons {
		if strings.Contains(reason, "nflog degraded") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("degradedReasons = %v, want an entry containing %q", r.degradedReasons, "nflog degraded")
	}
}

// TestInitializeGrouping_StandardUsesRedisAndRestoresTimers verifies the
// standard-profile Redis storage path end to end: a timer persisted before
// startup (simulating a prior instance) is picked up by RestoreTimers
// (task 2.2) when initializeGrouping runs against the same Redis backend.
func TestInitializeGrouping_StandardUsesRedisAndRestoresTimers(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run() error = %v", err)
	}
	defer mr.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	redisCache, err := infracache.NewRedisCache(&infracache.CacheConfig{
		Addr:        mr.Addr(),
		PoolSize:    5,
		DialTimeout: time.Second,
		ReadTimeout: time.Second,
	}, logger)
	if err != nil {
		t.Fatalf("NewRedisCache() error = %v", err)
	}
	defer func() { _ = redisCache.Close() }()

	// Seed a still-active timer directly into Redis, as if a previous
	// instance had started it before restarting.
	seedStorage, err := grouping.NewRedisTimerStorage(redisCache, logger)
	if err != nil {
		t.Fatalf("NewRedisTimerStorage() error = %v", err)
	}
	ctx := context.Background()
	now := time.Now()
	seedTimer := &grouping.GroupTimer{
		GroupKey:  "alertname=HighCPU",
		TimerType: grouping.GroupWaitTimer,
		Duration:  30 * time.Second,
		StartedAt: now,
		ExpiresAt: now.Add(30 * time.Second),
		State:     grouping.TimerStateActive,
		Metadata: &grouping.TimerMetadata{
			Version:   1,
			CreatedBy: "prior-instance",
		},
	}
	if err := seedStorage.SaveTimer(ctx, seedTimer); err != nil {
		t.Fatalf("SaveTimer() error = %v", err)
	}

	cfg := &appconfig.Config{
		Profile:  appconfig.ProfileStandard,
		Grouping: appconfig.GroupingConfig{Enabled: true},
		Routing:  minimalRouteTree(),
	}
	r := newTestRegistryForGrouping(cfg)
	r.cache = redisCache

	if err := r.initializeGrouping(ctx); err != nil {
		t.Fatalf("initializeGrouping() error = %v", err)
	}
	if r.groupManager == nil || r.groupTimerManager == nil {
		t.Fatalf("groupManager/groupTimerManager must be initialized with a live Redis cache")
	}

	restoredTimer, err := r.groupTimerManager.GetTimer(ctx, "alertname=HighCPU")
	if err != nil {
		t.Fatalf("GetTimer() error = %v, want the seeded timer to have been restored via RestoreTimers", err)
	}
	if restoredTimer.GroupKey != "alertname=HighCPU" {
		t.Fatalf("restored timer GroupKey = %q, want %q", restoredTimer.GroupKey, "alertname=HighCPU")
	}

	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := r.groupTimerManager.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("groupTimerManager.Shutdown() error = %v", err)
	}
}

// TestInitializeGrouping_StandardReconciliationAdoptsOrphanFromCrashedReplica
// is task 6.2's end-to-end wiring test: with the standard profile and a
// live Redis cache, ServiceRegistry.initializeGrouping must forward
// cfg.Grouping.ReconciliationInterval/Grace into the TimerManager it
// builds, and that TimerManager's reconciliation loop must actually adopt
// a timer left overdue in shared Redis storage by a replica that is no
// longer running — simulated here by seeding the overdue entry directly
// via a second RedisTimerStorage connection, standing in for a DIFFERENT,
// now-crashed replica (this registry's own timer for the group is
// cancelled first, so nothing on THIS side would fire it otherwise).
func TestInitializeGrouping_StandardReconciliationAdoptsOrphanFromCrashedReplica(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run() error = %v", err)
	}
	defer mr.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	redisCache, err := infracache.NewRedisCache(&infracache.CacheConfig{
		Addr:        mr.Addr(),
		PoolSize:    5,
		DialTimeout: time.Second,
		ReadTimeout: time.Second,
	}, logger)
	if err != nil {
		t.Fatalf("NewRedisCache() error = %v", err)
	}
	defer func() { _ = redisCache.Close() }()

	cfg := &appconfig.Config{
		Profile: appconfig.ProfileStandard,
		Grouping: appconfig.GroupingConfig{
			Enabled:                true,
			ReconciliationInterval: 50 * time.Millisecond,
			ReconciliationGrace:    10 * time.Millisecond,
		},
		Routing: &infraroute.RouteConfig{
			Route: &grouping.Route{
				Receiver:      "default",
				GroupBy:       []string{"alertname"},
				GroupWait:     &grouping.Duration{Duration: time.Hour}, // long enough this replica's own timer never fires during the test
				GroupInterval: &grouping.Duration{Duration: time.Hour},
			},
		},
	}
	r := newTestRegistryForGrouping(cfg)
	r.cache = redisCache

	ctx := context.Background()
	if err := r.initializeGrouping(ctx); err != nil {
		t.Fatalf("initializeGrouping() error = %v", err)
	}
	if r.groupManager == nil || r.groupTimerManager == nil {
		t.Fatalf("groupManager/groupTimerManager must be initialized with a live Redis cache")
	}
	defer func() { _ = r.groupTimerManager.Shutdown(context.Background()) }()

	groupKey := grouping.GroupKey("receiver=default/alertname=OrphanE2E")
	alert := &core.Alert{
		Fingerprint: "fp-orphan",
		AlertName:   "OrphanE2E",
		Status:      core.StatusFiring,
		Labels:      map[string]string{"alertname": "OrphanE2E"},
		StartsAt:    time.Now(),
	}
	if _, err := r.groupManager.AddAlertToGroup(ctx, alert, groupKey); err != nil {
		t.Fatalf("AddAlertToGroup() error = %v", err)
	}

	// AddAlertToGroup just started a real (1h, won't fire here) group_wait
	// timer for this group on THIS replica. Cancel it so nothing on this
	// side would otherwise fire the group, then seed an overdue timer entry
	// directly — this is what shared Redis storage looks like right after
	// some OTHER replica started this group's timer and crashed before it
	// fired.
	if _, err := r.groupTimerManager.CancelTimer(ctx, groupKey); err != nil {
		t.Fatalf("CancelTimer() error = %v", err)
	}

	seedStorage, err := grouping.NewRedisTimerStorage(redisCache, logger)
	if err != nil {
		t.Fatalf("NewRedisTimerStorage() error = %v", err)
	}
	startedAt := time.Now().Add(-5 * time.Minute)
	duration := 3 * time.Minute
	orphan := &grouping.GroupTimer{
		GroupKey:  groupKey,
		TimerType: grouping.GroupWaitTimer,
		Duration:  duration,
		StartedAt: startedAt,
		ExpiresAt: startedAt.Add(duration), // 2 minutes overdue
		State:     grouping.TimerStateActive,
		Metadata:  &grouping.TimerMetadata{Version: 1, CreatedBy: "crashed-replica"},
	}
	if err := seedStorage.SaveTimer(ctx, orphan); err != nil {
		t.Fatalf("SaveTimer() error = %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		stats, statsErr := r.groupTimerManager.GetStats(ctx)
		if statsErr == nil && stats.ReconciledTimers >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ServiceRegistry-wired reconciliation loop never adopted the orphaned timer (stats=%+v, err=%v)", stats, statsErr)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// The adopted timer was a group_wait orphan: firing it runs
	// onGroupWaitExpired, which unconditionally schedules the
	// group_interval continuation regardless of publish outcome (task 6.2
	// fix round 2 — see DefaultTimerManager.onTimerExpired's doc comment).
	// So the storage entry for this groupKey is NOT simply gone after
	// adoption — it now holds that continuation. Asserting "nothing left"
	// here would be checking the pre-fix-round-2 (broken) behavior, where
	// the continuation's StartTimer always failed; the correct
	// post-adoption state is "the group_interval continuation exists."
	continuation, err := seedStorage.LoadTimer(ctx, groupKey)
	if err != nil {
		t.Fatalf("LoadTimer() after adoption error = %v, want the group_interval continuation the adopted group_wait orphan must have scheduled", err)
	}
	if continuation.TimerType != grouping.GroupIntervalTimer {
		t.Fatalf("continuation timer type = %q, want %q", continuation.TimerType, grouping.GroupIntervalTimer)
	}
}

// TestServiceRegistryShutdown_GroupingTimerManagerStopsCleanly verifies
// ServiceRegistry.Shutdown tears down the grouping timer manager (task 2.2)
// without error and clears both fields, alongside the silence manager.
func TestServiceRegistryShutdown_GroupingTimerManagerStopsCleanly(t *testing.T) {
	cfg := &appconfig.Config{
		Profile:  appconfig.ProfileLite,
		Grouping: appconfig.GroupingConfig{Enabled: true},
		Routing:  minimalRouteTree(),
	}
	r := newTestRegistryForGrouping(cfg)

	ctx := context.Background()
	if err := r.initializeGrouping(ctx); err != nil {
		t.Fatalf("initializeGrouping() error = %v", err)
	}
	if r.groupTimerManager == nil {
		t.Fatalf("groupTimerManager must be initialized before Shutdown is exercised")
	}

	if err := r.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if r.groupManager != nil {
		t.Fatalf("groupManager must be nil after Shutdown()")
	}
	if r.groupTimerManager != nil {
		t.Fatalf("groupTimerManager must be nil after Shutdown()")
	}
}

// TestGroupingEndToEnd_TimeIntervalLookupWiredFromRouteTreeManager_MutesNotification
// covers task 3.2's full wiring path: initializeRouting (step 2.6) builds
// r.routeTreeManager with a time_intervals index, initializeGrouping (step
// 2.7) wraps it into a routeTreeTimeIntervalLookup and passes it to
// DefaultGroupManagerConfig.TimeIntervalLookup — and a group whose matched
// route referenced that interval by name must have its group_wait
// notification suppressed by the real (non-fake) wiring, not just the
// grouping package's own unit tests.
func TestGroupingEndToEnd_TimeIntervalLookupWiredFromRouteTreeManager_MutesNotification(t *testing.T) {
	cfg := &appconfig.Config{
		Profile:  appconfig.ProfileLite,
		Grouping: appconfig.GroupingConfig{Enabled: true},
		Routing: &infraroute.RouteConfig{
			Route: &grouping.Route{
				Receiver:      "default",
				GroupBy:       []string{"alertname"},
				GroupWait:     &grouping.Duration{Duration: 10 * time.Millisecond},
				GroupInterval: &grouping.Duration{Duration: time.Hour}, // long enough to not fire during this test
			},
			Receivers: []*infraroute.Receiver{{Name: "default"}},
			TimeIntervalIndex: map[string]timeinterval.TimeInterval{
				"always-on-maintenance": {
					Name:          "always-on-maintenance",
					TimeIntervals: []timeinterval.Interval{{}}, // zero-value Interval matches every time
				},
			},
		},
	}
	r := newTestRegistryForGrouping(cfg)
	ctx := context.Background()

	if err := r.initializeRouting(ctx); err != nil {
		t.Fatalf("initializeRouting() error = %v", err)
	}
	if r.routeTreeManager == nil {
		t.Fatalf("routeTreeManager must be initialized")
	}

	if err := r.initializeGrouping(ctx); err != nil {
		t.Fatalf("initializeGrouping() error = %v", err)
	}
	if r.groupManager == nil {
		t.Fatalf("groupManager must be initialized")
	}

	pub := &recordingGroupPublisher{}
	r.groupManager.SetPublisher(pub)

	groupKey := grouping.GroupKey("receiver=default/alertname=HighCPU")
	alert := &core.Alert{
		Fingerprint: "fp-1",
		AlertName:   "HighCPU",
		Status:      core.StatusFiring,
		Labels:      map[string]string{"alertname": "HighCPU"},
		StartsAt:    time.Now(),
	}

	// Mirrors what AlertProcessor.routeAlertToGroup does with a matched
	// route's decision.MuteTimeIntervals (task 3.2).
	if _, err := r.groupManager.AddAlertToGroup(ctx, alert, groupKey,
		grouping.WithMuteTimeIntervals([]string{"always-on-maintenance"}, nil)); err != nil {
		t.Fatalf("AddAlertToGroup() error = %v", err)
	}

	// Give the group_wait timer time to fire. It must find nothing to
	// publish: the wired TimeIntervalLookup resolves "always-on-maintenance"
	// to an always-matching interval.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}

	if pub.callCount() != 0 {
		t.Fatalf("PublishGroup call count = %d, want 0 (muted via the wired TimeIntervalLookup)", pub.callCount())
	}

	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := r.groupTimerManager.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("groupTimerManager.Shutdown() error = %v", err)
	}
}

// === Wave-4 hygiene item 3 (review finding M-a) ===
//
// service_registry.go used to wire grouping.reconciliation_grace straight
// from a static "90s" viper literal, completely independent of the actual
// publishing.queue.delivery_confirmation_timeout — drift caught only by
// validateNotifyTimingBudget at startup, and only after the operator had
// already raised the delivery timeout and hit a confusing failure. These pin
// reconciliationGraceFor, the helper initializeGrouping now calls instead.

// TestReconciliationGraceFor_UnsetConfigDerivesFromActualDeliveryTimeout is
// the heart of the fix: raising delivery_confirmation_timeout alone (leaving
// reconciliation_grace unset) must scale the derived grace with it, instead
// of silently keeping the old default.
func TestReconciliationGraceFor_UnsetConfigDerivesFromActualDeliveryTimeout(t *testing.T) {
	for _, wait := range []time.Duration{
		infrapublishing.DefaultDeliveryConfirmationTimeout, // 45s
		90 * time.Second,
		infrapublishing.MaxDeliveryConfirmationTimeout, // 2m
	} {
		got := reconciliationGraceFor(0, wait)
		want := grouping.ReconciliationGraceFor(wait)
		if got != want {
			t.Errorf("reconciliationGraceFor(0, %s) = %s, want %s (grouping.ReconciliationGraceFor)", wait, got, want)
		}
	}
}

// TestReconciliationGraceFor_ExplicitConfigWins: an operator-supplied value
// must never be overridden by the derivation, however it compares to what the
// formula would have produced.
func TestReconciliationGraceFor_ExplicitConfigWins(t *testing.T) {
	const explicit = 10 * time.Minute
	got := reconciliationGraceFor(explicit, infrapublishing.DefaultDeliveryConfirmationTimeout)
	if got != explicit {
		t.Errorf("reconciliationGraceFor(%s, ...) = %s, want the explicit value unchanged", explicit, got)
	}
}

// TestReconciliationGraceFor_DefaultMatchesHistoricalLiteral kills the exact
// silent drift review finding M-a described: the "90s" that used to be
// hand-typed into config.go's viper default must still equal what the
// formula produces at the shipped delivery-confirmation-timeout default, so
// a change to notify_budget.go's constants that moves this value is caught
// here instead of only at startup validation.
func TestReconciliationGraceFor_DefaultMatchesHistoricalLiteral(t *testing.T) {
	const historicalLiteral = 90 * time.Second
	got := reconciliationGraceFor(0, infrapublishing.DefaultDeliveryConfirmationTimeout)
	if got != historicalLiteral {
		t.Errorf("reconciliationGraceFor(0, DefaultDeliveryConfirmationTimeout) = %s, want %s (config.go's former hardcoded default)", got, historicalLiteral)
	}
}
