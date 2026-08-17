package application

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	appconfig "github.com/ipiton/AMP/internal/config"
	"github.com/ipiton/AMP/internal/core"
	infracache "github.com/ipiton/AMP/internal/infrastructure/cache"
	"github.com/ipiton/AMP/internal/infrastructure/grouping"
	infraroute "github.com/ipiton/AMP/internal/infrastructure/routing"
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
