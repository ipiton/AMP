package inhibition

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/infrastructure/cache"
)

// RedisContainer represents a Redis container for testing
type RedisContainer struct {
	testcontainers.Container
	URI string
}

// setupRedis starts a Redis container
func setupRedis(ctx context.Context) (*RedisContainer, error) {
	req := testcontainers.ContainerRequest{
		Image:        "redis:7-alpine",
		ExposedPorts: []string{"6379/tcp"},
		WaitingFor:   wait.ForLog("Ready to accept connections"),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, err
	}

	mappedPort, err := container.MappedPort(ctx, "6379")
	if err != nil {
		return nil, err
	}

	hostIP, err := container.Host(ctx)
	if err != nil {
		return nil, err
	}

	uri := fmt.Sprintf("redis://%s:%s", hostIP, mappedPort.Port())

	return &RedisContainer{Container: container, URI: uri}, nil
}

// TestIntegration_InhibitionStateManager_Redis verifies Redis persistence
func TestIntegration_InhibitionStateManager_Redis(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()

	// Start Redis
	redisC, err := setupRedis(ctx)
	if err != nil {
		t.Fatalf("Failed to start Redis: %v", err)
	}
	defer func() {
		if err := redisC.Terminate(ctx); err != nil {
			t.Fatalf("Failed to terminate Redis: %v", err)
		}
	}()

	// Create Redis client
	redisClient, err := cache.NewRedisCacheFromURL(redisC.URI, nil)
	if err != nil {
		t.Fatalf("Failed to create Redis client: %v", err)
	}

	// Create StateManager
	sm := NewDefaultStateManager(redisClient, nil, nil) // nil logger, nil metrics

	// 1. Record Inhibition
	fingerprint := "fp-integration-test"
	ruleID := "rule-1"

	state := &InhibitionState{
		TargetFingerprint: fingerprint,
		SourceFingerprint: "source-fp",
		RuleName:          ruleID,
		InhibitedAt:       time.Now(),
	}

	err = sm.RecordInhibition(ctx, state)
	if err != nil {
		t.Fatalf("RecordInhibition failed: %v", err)
	}

	// 2. Verify In-Memory
	inhibited, err := sm.IsInhibited(ctx, fingerprint)
	if err != nil {
		t.Fatalf("IsInhibited failed: %v", err)
	}
	if !inhibited {
		t.Error("Alert should be inhibited in memory")
	}

	// 3. Verify Redis Persistence (by creating a NEW StateManager)
	// This simulates a service restart or a separate instance
	sm2 := NewDefaultStateManager(redisClient, nil, nil)

	// Check IsInhibited on new instance
	inhibited2, err := sm2.IsInhibited(ctx, fingerprint)
	if err != nil {
		t.Fatalf("IsInhibited (sm2) failed: %v", err)
	}
	if !inhibited2 {
		t.Error("Alert should be inhibited in new instance (Redis persistence)")
	}

	// Check GetActiveInhibitions on new instance
	states, err := sm2.GetActiveInhibitions(ctx)
	if err != nil {
		t.Fatalf("GetActiveInhibitions failed: %v", err)
	}
	if len(states) != 1 {
		t.Errorf("Expected 1 active inhibition in new instance, got %d", len(states))
	} else {
		if states[0].TargetFingerprint != fingerprint {
			t.Errorf("Expected fingerprint %s, got %s", fingerprint, states[0].TargetFingerprint)
		}
	}

	// 4. Remove Inhibition
	err = sm.RemoveInhibition(ctx, fingerprint)
	if err != nil {
		t.Fatalf("RemoveInhibition failed: %v", err)
	}

	inhibited3, _ := sm.IsInhibited(ctx, fingerprint)
	if inhibited3 {
		t.Error("Alert should not be inhibited after removal")
	}

	inhibited4, _ := sm2.IsInhibited(ctx, fingerprint)
	if inhibited4 {
		t.Error("Alert should not be inhibited in new instance after removal")
	}
}

// TestIntegration_TwoTierAlertCache_Redis verifies Cache persistence
func TestIntegration_TwoTierAlertCache_Redis(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()

	// Start Redis
	redisC, err := setupRedis(ctx)
	if err != nil {
		t.Fatalf("Failed to start Redis: %v", err)
	}
	defer func() {
		if err := redisC.Terminate(ctx); err != nil {
			t.Fatalf("Failed to terminate Redis: %v", err)
		}
	}()

	// Create Redis client
	redisClient, err := cache.NewRedisCacheFromURL(redisC.URI, nil)
	if err != nil {
		t.Fatalf("Failed to create Redis client: %v", err)
	}

	// Create Cache
	c1 := NewTwoTierAlertCache(redisClient, nil)
	defer c1.Stop()

	// 1. Add Alert
	alert := &core.Alert{
		Fingerprint: "fp-cache-integration",
		Status:      "firing",
		StartsAt:    time.Now(),
		Labels:      map[string]string{"alertname": "CacheTest"},
	}

	err = c1.AddFiringAlert(ctx, alert)
	if err != nil {
		t.Fatalf("AddFiringAlert failed: %v", err)
	}

	// 2. Verify L1
	alerts, err := c1.GetFiringAlerts(ctx)
	if err != nil {
		t.Fatalf("GetFiringAlerts failed: %v", err)
	}
	if len(alerts) != 1 {
		t.Errorf("Expected 1 alert in c1, got %d", len(alerts))
	}

	// 3. Verify L2 (Redis) via new Cache instance
	c2 := NewTwoTierAlertCache(redisClient, nil)
	defer c2.Stop()

	// Initial get should hit Redis and populate L1
	alerts2, err := c2.GetFiringAlerts(ctx)
	if err != nil {
		t.Fatalf("GetFiringAlerts (c2) failed: %v", err)
	}
	if len(alerts2) != 1 {
		t.Errorf("Expected 1 alert in c2 (from Redis), got %d", len(alerts2))
	}
	if len(alerts2) > 0 && alerts2[0].Fingerprint != "fp-cache-integration" {
		t.Errorf("Expected fingerprint match, got %s", alerts2[0].Fingerprint)
	}

	// 4. Remove Alert
	err = c1.RemoveAlert(ctx, "fp-cache-integration")
	if err != nil {
		t.Fatalf("RemoveAlert failed: %v", err)
	}

	// Verify removal in c3 (fresh instance)
	c3 := NewTwoTierAlertCache(redisClient, nil)
	defer c3.Stop()

	alerts3, err := c3.GetFiringAlerts(ctx)
	if err != nil {
		t.Fatalf("GetFiringAlerts (c3) failed: %v", err)
	}
	if len(alerts3) != 0 {
		t.Errorf("Expected 0 alerts in c3 after removal, got %d", len(alerts3))
	}
}
