package publishing

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/ipiton/AMP/internal/core"
)

// Test Suite: Cache

func TestCacheGet_Found(t *testing.T) {
	cache := newTargetCache()
	target := &core.PublishingTarget{
		Name: "test-target",
		Type: "rootly",
		URL:  "https://example.com",
	}

	cache.Set([]*core.PublishingTarget{target})

	result := cache.Get("test-target")
	assert.NotNil(t, result)
	assert.Equal(t, "test-target", result.Name)
}

func TestCacheGet_NotFound(t *testing.T) {
	cache := newTargetCache()

	result := cache.Get("nonexistent")
	assert.Nil(t, result)
}

func TestCacheSet(t *testing.T) {
	cache := newTargetCache()

	targets := []*core.PublishingTarget{
		{Name: "target-1", Type: "rootly", URL: "https://example1.com"},
		{Name: "target-2", Type: "slack", URL: "https://example2.com"},
	}

	cache.Set(targets)

	assert.Equal(t, 2, cache.Len())
	assert.NotNil(t, cache.Get("target-1"))
	assert.NotNil(t, cache.Get("target-2"))
}

func TestCacheSet_ReplaceAll(t *testing.T) {
	cache := newTargetCache()

	// Initial set
	initial := []*core.PublishingTarget{
		{Name: "target-1", Type: "rootly", URL: "https://example1.com"},
		{Name: "target-2", Type: "slack", URL: "https://example2.com"},
	}
	cache.Set(initial)
	assert.Equal(t, 2, cache.Len())

	// Replace with new set
	replacement := []*core.PublishingTarget{
		{Name: "target-3", Type: "webhook", URL: "https://example3.com"},
	}
	cache.Set(replacement)

	// Old targets should be gone
	assert.Nil(t, cache.Get("target-1"))
	assert.Nil(t, cache.Get("target-2"))
	// New target should exist
	assert.NotNil(t, cache.Get("target-3"))
	assert.Equal(t, 1, cache.Len())
}

func TestCacheSet_WithNilTarget(t *testing.T) {
	cache := newTargetCache()

	targets := []*core.PublishingTarget{
		{Name: "target-1", Type: "rootly", URL: "https://example1.com"},
		nil, // nil target should be skipped
		{Name: "target-2", Type: "slack", URL: "https://example2.com"},
	}

	cache.Set(targets)

	// Should have 2 targets (nil skipped)
	assert.Equal(t, 2, cache.Len())
	assert.NotNil(t, cache.Get("target-1"))
	assert.NotNil(t, cache.Get("target-2"))
}

func TestCacheSet_WithEmptyName(t *testing.T) {
	cache := newTargetCache()

	targets := []*core.PublishingTarget{
		{Name: "target-1", Type: "rootly", URL: "https://example1.com"},
		{Name: "", Type: "slack", URL: "https://example2.com"}, // empty name should be skipped
	}

	cache.Set(targets)

	// Should have 1 target (empty name skipped)
	assert.Equal(t, 1, cache.Len())
	assert.NotNil(t, cache.Get("target-1"))
}

func TestCacheList(t *testing.T) {
	cache := newTargetCache()

	targets := []*core.PublishingTarget{
		{Name: "target-1", Type: "rootly", URL: "https://example1.com"},
		{Name: "target-2", Type: "slack", URL: "https://example2.com"},
		{Name: "target-3", Type: "webhook", URL: "https://example3.com"},
	}

	cache.Set(targets)

	result := cache.List()
	assert.Len(t, result, 3)

	// Check all target names are present (order may vary)
	names := make(map[string]bool)
	for _, target := range result {
		names[target.Name] = true
	}
	assert.True(t, names["target-1"])
	assert.True(t, names["target-2"])
	assert.True(t, names["target-3"])
}

func TestCacheList_Empty(t *testing.T) {
	cache := newTargetCache()

	result := cache.List()
	assert.NotNil(t, result)
	assert.Len(t, result, 0)
}

func TestCacheGetByType(t *testing.T) {
	cache := newTargetCache()

	targets := []*core.PublishingTarget{
		{Name: "rootly-1", Type: "rootly", URL: "https://example1.com"},
		{Name: "slack-1", Type: "slack", URL: "https://example2.com"},
		{Name: "slack-2", Type: "slack", URL: "https://example3.com"},
		{Name: "webhook-1", Type: "webhook", URL: "https://example4.com"},
	}

	cache.Set(targets)

	// Get Slack targets
	slackTargets := cache.GetByType("slack")
	assert.Len(t, slackTargets, 2)
	for _, target := range slackTargets {
		assert.Equal(t, "slack", target.Type)
	}

	// Get Rootly targets
	rootlyTargets := cache.GetByType("rootly")
	assert.Len(t, rootlyTargets, 1)
	assert.Equal(t, "rootly", rootlyTargets[0].Type)

	// Get non-existent type
	pdTargets := cache.GetByType("pagerduty")
	assert.NotNil(t, pdTargets)
	assert.Len(t, pdTargets, 0)
}

func TestCacheLen(t *testing.T) {
	cache := newTargetCache()

	// Initially empty
	assert.Equal(t, 0, cache.Len())

	// Add targets
	targets := []*core.PublishingTarget{
		{Name: "target-1", Type: "rootly", URL: "https://example1.com"},
		{Name: "target-2", Type: "slack", URL: "https://example2.com"},
	}
	cache.Set(targets)

	assert.Equal(t, 2, cache.Len())

	// Clear (set empty)
	cache.Set([]*core.PublishingTarget{})

	assert.Equal(t, 0, cache.Len())
}

func TestCacheClear(t *testing.T) {
	cache := newTargetCache()

	targets := []*core.PublishingTarget{
		{Name: "target-1", Type: "rootly", URL: "https://example1.com"},
		{Name: "target-2", Type: "slack", URL: "https://example2.com"},
	}
	cache.Set(targets)
	assert.Equal(t, 2, cache.Len())

	// Clear cache
	cache.Clear()

	assert.Equal(t, 0, cache.Len())
	assert.Nil(t, cache.Get("target-1"))
	assert.Nil(t, cache.Get("target-2"))
}
