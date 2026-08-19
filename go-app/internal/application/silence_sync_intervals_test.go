package application

import (
	"testing"
	"time"

	appconfig "github.com/ipiton/AMP/internal/config"
)

// === alertmanager-parity wave-5 item FU-SILENCE-SYNC-INTERVALS ===
//
// silenceSubscribeRetryBackoff / silencePeriodicResyncInterval must fall back
// to the pre-config-knob literals (2s/5m) both when r.config is nil (the
// posture every hand-built *ServiceRegistry in silence_event_sync_test.go
// relies on) and when the configured value is left at its zero default.

func TestSilenceSubscribeRetryBackoff_NilConfigFallsBackToDefault(t *testing.T) {
	r := &ServiceRegistry{}
	if got := r.silenceSubscribeRetryBackoff(); got != defaultSilenceSubscribeRetryBackoff {
		t.Errorf("silenceSubscribeRetryBackoff() with nil config = %s, want %s", got, defaultSilenceSubscribeRetryBackoff)
	}
}

func TestSilenceSubscribeRetryBackoff_ZeroConfiguredFallsBackToDefault(t *testing.T) {
	r := &ServiceRegistry{config: &appconfig.Config{}}
	if got := r.silenceSubscribeRetryBackoff(); got != defaultSilenceSubscribeRetryBackoff {
		t.Errorf("silenceSubscribeRetryBackoff() with zero-value config = %s, want %s", got, defaultSilenceSubscribeRetryBackoff)
	}
}

func TestSilenceSubscribeRetryBackoff_ExplicitConfigWins(t *testing.T) {
	explicit := 7 * time.Second
	r := &ServiceRegistry{config: &appconfig.Config{}}
	r.config.Silencing.SubscribeRetryBackoff = explicit
	if got := r.silenceSubscribeRetryBackoff(); got != explicit {
		t.Errorf("silenceSubscribeRetryBackoff() = %s, want the explicit value %s unchanged", got, explicit)
	}
}

func TestSilencePeriodicResyncInterval_NilConfigFallsBackToDefault(t *testing.T) {
	r := &ServiceRegistry{}
	if got := r.silencePeriodicResyncInterval(); got != defaultSilencePeriodicResyncInterval {
		t.Errorf("silencePeriodicResyncInterval() with nil config = %s, want %s", got, defaultSilencePeriodicResyncInterval)
	}
}

func TestSilencePeriodicResyncInterval_ExplicitConfigWins(t *testing.T) {
	explicit := 90 * time.Second
	r := &ServiceRegistry{config: &appconfig.Config{}}
	r.config.Silencing.PeriodicResyncInterval = explicit
	if got := r.silencePeriodicResyncInterval(); got != explicit {
		t.Errorf("silencePeriodicResyncInterval() = %s, want the explicit value %s unchanged", got, explicit)
	}
}
