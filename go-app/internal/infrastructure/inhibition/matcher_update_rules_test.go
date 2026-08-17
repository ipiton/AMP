package inhibition

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/core"
)

type staticAlertCache struct {
	alerts []*core.Alert
}

func (c *staticAlertCache) GetFiringAlerts(ctx context.Context) ([]*core.Alert, error) {
	return c.alerts, nil
}

func (c *staticAlertCache) AddFiringAlert(ctx context.Context, alert *core.Alert) error {
	return nil
}

func (c *staticAlertCache) RemoveAlert(ctx context.Context, fingerprint string) error {
	return nil
}

// TestUpdateRules_HotReload verifies PARITY-A2: rules replaced at runtime take
// effect on the next ShouldInhibit call.
func TestUpdateRules_HotReload(t *testing.T) {
	source := &core.Alert{
		Fingerprint: "src",
		Labels:      map[string]string{"alertname": "NodeDown", "cluster": "a"},
	}
	target := &core.Alert{
		Fingerprint: "tgt",
		Labels:      map[string]string{"alertname": "HighLatency", "cluster": "a"},
	}
	cache := &staticAlertCache{alerts: []*core.Alert{source}}

	// Start with no rules: target is not inhibited.
	m := NewMatcher(cache, nil, nil)
	res, err := m.ShouldInhibit(context.Background(), target)
	require.NoError(t, err)
	assert.False(t, res.Matched)

	// Hot-reload a rule that inhibits HighLatency while NodeDown fires.
	m.UpdateRules([]InhibitionRule{{
		Name:        "node-down-inhibits-latency",
		SourceMatch: map[string]string{"alertname": "NodeDown"},
		TargetMatch: map[string]string{"alertname": "HighLatency"},
		Equal:       []string{"cluster"},
	}})

	res, err = m.ShouldInhibit(context.Background(), target)
	require.NoError(t, err)
	assert.True(t, res.Matched, "new rules must apply after UpdateRules")

	// Hot-reload back to empty: inhibition disappears again.
	m.UpdateRules(nil)
	res, err = m.ShouldInhibit(context.Background(), target)
	require.NoError(t, err)
	assert.False(t, res.Matched)
}

// TestUpdateRules_ConcurrentWithMatching exercises the mutex under -race.
func TestUpdateRules_ConcurrentWithMatching(t *testing.T) {
	source := &core.Alert{
		Fingerprint: "src",
		Labels:      map[string]string{"alertname": "NodeDown"},
	}
	target := &core.Alert{
		Fingerprint: "tgt",
		Labels:      map[string]string{"alertname": "HighLatency"},
	}
	cache := &staticAlertCache{alerts: []*core.Alert{source}}
	m := NewMatcher(cache, nil, nil)

	rule := InhibitionRule{
		Name:        "r",
		SourceMatch: map[string]string{"alertname": "NodeDown"},
		TargetMatch: map[string]string{"alertname": "HighLatency"},
	}

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				m.UpdateRules([]InhibitionRule{rule})
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_, err := m.ShouldInhibit(context.Background(), target)
				assert.NoError(t, err)
			}
		}()
	}
	wg.Wait()
}
