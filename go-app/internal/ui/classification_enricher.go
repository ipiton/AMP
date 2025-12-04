// Package ui provides UI-related utilities and components.
package ui

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/core/services"
)

// ClassificationEnricher enriches alerts with classification data.
//
// Features:
//   - Batch processing for optimal performance
//   - Request-scoped cache to avoid duplicate requests
//   - Graceful degradation when classification unavailable
//   - Thread-safe concurrent access
//
// Performance:
//   - Cache hit: < 1ms per alert
//   - Cache miss (batch): < 50ms per batch (10 alerts)
//   - Fallback: < 5ms per alert
type ClassificationEnricher interface {
	// EnrichAlerts enriches a list of alerts with classification data.
	EnrichAlerts(ctx context.Context, alerts []*core.Alert) ([]*EnrichedAlert, error)

	// EnrichAlert enriches a single alert with classification data.
	EnrichAlert(ctx context.Context, alert *core.Alert) (*EnrichedAlert, error)

	// BatchEnrich performs batch enrichment with optimization.
	BatchEnrich(ctx context.Context, alerts []*core.Alert, batchSize int) ([]*EnrichedAlert, error)
}

// EnrichedAlert represents an alert enriched with classification data.
type EnrichedAlert struct {
	// Base alert
	Alert *core.Alert `json:"alert"`

	// Classification data
	Classification *core.ClassificationResult `json:"classification,omitempty"`

	// Metadata
	HasClassification   bool   `json:"has_classification"`
	ClassificationSource string `json:"classification_source,omitempty"` // "llm", "fallback", "cache", "none"
	ClassificationCached bool  `json:"classification_cached"`
	ClassificationAge    *time.Duration `json:"classification_age,omitempty"` // время с момента классификации
}

// defaultClassificationEnricher implements ClassificationEnricher interface.
type defaultClassificationEnricher struct {
	classificationSvc services.ClassificationService
	logger            *slog.Logger

	// Request-scoped cache (in-memory, cleared after request)
	requestCache map[string]*core.ClassificationResult
	requestMu   sync.RWMutex
}

// NewClassificationEnricher creates a new ClassificationEnricher.
func NewClassificationEnricher(
	classificationSvc services.ClassificationService,
	logger *slog.Logger,
) ClassificationEnricher {
	if logger == nil {
		logger = slog.Default()
	}

	return &defaultClassificationEnricher{
		classificationSvc: classificationSvc,
		logger:            logger,
		requestCache:      make(map[string]*core.ClassificationResult),
	}
}

// EnrichAlerts enriches a list of alerts with classification data.
func (e *defaultClassificationEnricher) EnrichAlerts(ctx context.Context, alerts []*core.Alert) ([]*EnrichedAlert, error) {
	if len(alerts) == 0 {
		return []*EnrichedAlert{}, nil
	}

	// Use batch enrichment for optimal performance
	return e.BatchEnrich(ctx, alerts, 20) // Default batch size: 20
}

// EnrichAlert enriches a single alert with classification data.
func (e *defaultClassificationEnricher) EnrichAlert(ctx context.Context, alert *core.Alert) (*EnrichedAlert, error) {
	if alert == nil {
		return nil, fmt.Errorf("alert cannot be nil")
	}

	enriched := &EnrichedAlert{
		Alert:            alert,
		HasClassification: false,
	}

	// Check request-scoped cache first
	e.requestMu.RLock()
	cached, found := e.requestCache[alert.Fingerprint]
	e.requestMu.RUnlock()

	if found && cached != nil {
		enriched.Classification = cached
		enriched.HasClassification = true
		enriched.ClassificationSource = "cache"
		enriched.ClassificationCached = true
		return enriched, nil
	}

	// Try to get classification from service cache
	if e.classificationSvc != nil {
		classification, err := e.classificationSvc.GetCachedClassification(ctx, alert.Fingerprint)
		if err == nil && classification != nil {
			// Store in request cache
			e.requestMu.Lock()
			e.requestCache[alert.Fingerprint] = classification
			e.requestMu.Unlock()

			enriched.Classification = classification
			enriched.HasClassification = true
			enriched.ClassificationSource = "cache"
			enriched.ClassificationCached = true
			return enriched, nil
		}
	}

	// Graceful degradation: return alert without classification
	e.logger.Debug("No classification found for alert",
		"fingerprint", alert.Fingerprint,
		"alert_name", alert.AlertName)

	return enriched, nil
}

// BatchEnrich performs batch enrichment with optimization.
func (e *defaultClassificationEnricher) BatchEnrich(ctx context.Context, alerts []*core.Alert, batchSize int) ([]*EnrichedAlert, error) {
	if len(alerts) == 0 {
		return []*EnrichedAlert{}, nil
	}

	if batchSize <= 0 {
		batchSize = 20 // Default batch size
	}

	enriched := make([]*EnrichedAlert, len(alerts))

	// Process in batches
	for i := 0; i < len(alerts); i += batchSize {
		end := i + batchSize
		if end > len(alerts) {
			end = len(alerts)
		}

		batch := alerts[i:end]
		batchEnriched, err := e.enrichBatch(ctx, batch)
		if err != nil {
			// Log error but continue with graceful degradation
			e.logger.Warn("Failed to enrich batch",
				"batch_start", i,
				"batch_end", end,
				"error", err)
		}

		// Copy enriched alerts to result
		for j, alert := range batch {
			if j < len(batchEnriched) {
				enriched[i+j] = batchEnriched[j]
			} else {
				// Fallback: create enriched alert without classification
				enriched[i+j] = &EnrichedAlert{
					Alert:            alert,
					HasClassification: false,
				}
			}
		}
	}

	return enriched, nil
}

// enrichBatch enriches a batch of alerts.
func (e *defaultClassificationEnricher) enrichBatch(ctx context.Context, alerts []*core.Alert) ([]*EnrichedAlert, error) {
	if len(alerts) == 0 {
		return []*EnrichedAlert{}, nil
	}

	enriched := make([]*EnrichedAlert, len(alerts))

	// Check request cache first
	uncachedAlerts := []*core.Alert{}
	uncachedIndices := []int{}

	for i, alert := range alerts {
		e.requestMu.RLock()
		cached, found := e.requestCache[alert.Fingerprint]
		e.requestMu.RUnlock()

		if found && cached != nil {
			enriched[i] = &EnrichedAlert{
				Alert:              alert,
				Classification:     cached,
				HasClassification:  true,
				ClassificationSource: "cache",
				ClassificationCached: true,
			}
		} else {
			uncachedAlerts = append(uncachedAlerts, alert)
			uncachedIndices = append(uncachedIndices, i)
		}
	}

	// If all alerts are cached, return early
	if len(uncachedAlerts) == 0 {
		return enriched, nil
	}

		// Try batch classification for uncached alerts
		if e.classificationSvc != nil {
			results, err := e.classificationSvc.ClassifyBatch(ctx, uncachedAlerts)
			if err == nil && len(results) == len(uncachedAlerts) {
				// Store results in request cache and create enriched alerts
				for j, result := range results {
					idx := uncachedIndices[j]
					alert := uncachedAlerts[j]

					if result != nil {
						// Store in request cache
						e.requestMu.Lock()
						e.requestCache[alert.Fingerprint] = result
						e.requestMu.Unlock()

						enriched[idx] = &EnrichedAlert{
							Alert:              alert,
							Classification:     result,
							HasClassification:  true,
							ClassificationSource: "llm", // Assume LLM source for batch classification
							ClassificationCached: false,
						}
					} else {
						// No classification available
						enriched[idx] = &EnrichedAlert{
							Alert:            alert,
							HasClassification: false,
						}
					}
				}
		} else {
			// Batch classification failed, try individual lookups
			for j, alert := range uncachedAlerts {
				idx := uncachedIndices[j]
				enrichedAlert, _ := e.EnrichAlert(ctx, alert)
				if enrichedAlert != nil {
					enriched[idx] = enrichedAlert
				} else {
					enriched[idx] = &EnrichedAlert{
						Alert:            alert,
						HasClassification: false,
					}
				}
			}
		}
	} else {
		// No classification service available, return alerts without classification
		for j, alert := range uncachedAlerts {
			idx := uncachedIndices[j]
			enriched[idx] = &EnrichedAlert{
				Alert:            alert,
				HasClassification: false,
			}
		}
	}

	return enriched, nil
}

// ClearRequestCache clears the request-scoped cache.
// This should be called after each request to free memory.
func (e *defaultClassificationEnricher) ClearRequestCache() {
	e.requestMu.Lock()
	defer e.requestMu.Unlock()

	// Clear map but keep it allocated for reuse
	for k := range e.requestCache {
		delete(e.requestCache, k)
	}
}
