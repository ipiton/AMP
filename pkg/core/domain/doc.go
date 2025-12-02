// Package domain contains core domain models for Alert History Service.
//
// Design Principles:
//   - Zero external dependencies (stdlib only)
//   - Framework-agnostic (no HTTP, no DB, no cache knowledge)
//   - Self-validating (Validate() methods)
//   - Immutable where possible (use Copy() + With*() methods)
//   - 100% Alertmanager-compatible (where applicable)
//
// Domain Models:
//   - Alert: Core alert data model
//   - Silence: Silence rules for alert suppression
//   - ClassificationResult: Alert classification results
//   - EnrichedAlert: Alert + classification + metadata
//
// All domain models are production-ready and battle-tested.
//
// Example Usage:
//
//	// Create an alert
//	alert := &domain.Alert{
//	    Fingerprint: "abc123",
//	    AlertName:   "HighCPU",
//	    Status:      domain.StatusFiring,
//	    Labels: map[string]string{
//	        "alertname": "HighCPU",
//	        "severity":  "critical",
//	    },
//	    StartsAt: time.Now(),
//	}
//
//	// Validate
//	if err := alert.Validate(); err != nil {
//	    log.Fatal(err)
//	}
//
//	// Create a silence
//	silence := &domain.Silence{
//	    ID:        uuid.New().String(),
//	    CreatedBy: "ops@example.com",
//	    Comment:   "Planned maintenance",
//	    StartsAt:  time.Now(),
//	    EndsAt:    time.Now().Add(2 * time.Hour),
//	    Matchers: []domain.Matcher{
//	        {Name: "alertname", Value: "HighCPU", Type: domain.MatcherTypeEqual},
//	    },
//	}
//
//	// Check if silence matches alert
//	matches, _ := silence.MatchesAlert(alert)
package domain
