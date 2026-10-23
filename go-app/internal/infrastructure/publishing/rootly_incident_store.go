package publishing

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ipiton/AMP/internal/infrastructure/cache"
)

const (
	// rootlyIncidentKeyPrefix namespaces the incident IDs in the shared cache.
	rootlyIncidentKeyPrefix = "publishing:rootly:incident:"

	// rootlyIncidentTTL bounds how long an incident ID outlives its last
	// firing notification. Every firing publish re-stores the ID (create, or
	// update — see EnhancedRootlyPublisher.updateIncident), so the entry only
	// expires for an alert silent for longer than this, i.e. any
	// repeat_interval below 7 days keeps the incident resolvable.
	rootlyIncidentTTL = 7 * 24 * time.Hour

	// rootlyIncidentStoreTimeout caps one cache round-trip; publishing must not
	// hang on a slow cache.
	rootlyIncidentStoreTimeout = 2 * time.Second
)

// cacheIncidentIDCache keeps Rootly incident IDs in the process-wide cache
// (Redis when configured), so a resolve reaches the incident after a restart
// and on any replica (PROVIDER-RESOLVE-ROOTLY). Rootly assigns the ID on
// create, so unlike PagerDuty's dedup key it cannot be derived from the alert.
//
// A failed Get is returned as an error, not as a miss: a miss on firing opens a
// new incident and overwrites the stored ID, orphaning the open one, and a
// miss on resolve is terminal. An unreachable store must instead fail the
// delivery so it is retried. Set/Delete errors are only logged.
type cacheIncidentIDCache struct {
	store  cache.Cache
	logger *slog.Logger
}

func newCacheIncidentIDCache(store cache.Cache, logger *slog.Logger) IncidentIDCache {
	return &cacheIncidentIDCache{store: store, logger: logger}
}

func (c *cacheIncidentIDCache) Set(key, incidentID string) {
	ctx, cancel := context.WithTimeout(context.Background(), rootlyIncidentStoreTimeout)
	defer cancel()
	if err := c.store.Set(ctx, rootlyIncidentKeyPrefix+key, incidentID, rootlyIncidentTTL); err != nil {
		c.logger.Warn("Failed to store Rootly incident ID", "incident_id", incidentID, "error", err)
	}
}

func (c *cacheIncidentIDCache) Get(key string) (string, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), rootlyIncidentStoreTimeout)
	defer cancel()
	var incidentID string
	if err := c.store.Get(ctx, rootlyIncidentKeyPrefix+key, &incidentID); err != nil {
		if cache.IsNotFound(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read rootly incident ID: %w", err)
	}
	return incidentID, incidentID != "", nil
}

func (c *cacheIncidentIDCache) Delete(key string) {
	ctx, cancel := context.WithTimeout(context.Background(), rootlyIncidentStoreTimeout)
	defer cancel()
	if err := c.store.Delete(ctx, rootlyIncidentKeyPrefix+key); err != nil && !cache.IsNotFound(err) {
		c.logger.Warn("Failed to delete Rootly incident ID", "error", err)
	}
}

// Stop is a no-op: the cache belongs to the service registry, not to this view.
func (c *cacheIncidentIDCache) Stop() {}
