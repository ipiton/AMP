package publishing

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"log/slog"
)

// Mode represents the current publishing mode
type Mode int

const (
	// ModeNormal indicates normal publishing mode (targets available)
	ModeNormal Mode = iota
	// ModeMetricsOnly indicates metrics-only mode (no targets available)
	ModeMetricsOnly
)

func (m Mode) String() string {
	switch m {
	case ModeNormal:
		return "normal"
	case ModeMetricsOnly:
		return "metrics-only"
	default:
		return "unknown"
	}
}

// ModeChangeCallback is called when mode changes
type ModeChangeCallback func(from Mode, to Mode, reason string)

// UnsubscribeFunc unsubscribes from mode changes
type UnsubscribeFunc func()

// ModeMetrics contains metrics about mode state
type ModeMetrics struct {
	CurrentMode          Mode
	CurrentModeDuration  time.Duration
	TransitionCount      int64
	LastTransitionTime   time.Time
	LastTransitionReason string
	ModeCheckDuration    time.Duration
}

// ModeManager manages publishing mode state
type ModeManager interface {
	// GetCurrentMode returns the current mode (cached, fast)
	GetCurrentMode() Mode

	// IsMetricsOnly returns true if in metrics-only mode
	IsMetricsOnly() bool

	// CheckModeTransition checks if mode should change and returns new mode
	// Returns: (newMode, changed, error)
	CheckModeTransition() (Mode, bool, error)

	// OnTargetsChanged is called when targets change (event-driven)
	OnTargetsChanged() error

	// Subscribe subscribes to mode change events
	Subscribe(callback ModeChangeCallback) UnsubscribeFunc

	// GetModeMetrics returns current mode metrics
	GetModeMetrics() ModeMetrics

	// Start starts the mode manager (periodic checking)
	Start(ctx context.Context) error

	// Stop stops the mode manager
	Stop() error
}

// DefaultModeManager implements ModeManager
type DefaultModeManager struct {
	discoveryManager TargetDiscoveryManager
	logger           *slog.Logger

	// State (protected by mu)
	currentMode          Mode
	modeChangedAt        time.Time
	transitionCount      int64
	lastTransitionReason string

	// Subscribers (protected by subscribersMu)
	subscribers    []ModeChangeCallback
	subscribersMu  sync.RWMutex

	// Caching (for performance)
	cachedMode   Mode
	cachedModeAt time.Time
	cacheTTL     time.Duration

	// Metrics (protected by mu)
	modeCheckDuration time.Duration
	metrics           *PublishingModeMetrics // TN-060: Prometheus metrics

	// Control
	mu     sync.RWMutex
	stopCh chan struct{}
	wg     sync.WaitGroup
}

// NewModeManager creates a new mode manager
func NewModeManager(
	discoveryManager TargetDiscoveryManager,
	logger *slog.Logger,
	metrics *PublishingModeMetrics,
) ModeManager {
	if logger == nil {
		logger = slog.Default()
	}

	return &DefaultModeManager{
		discoveryManager: discoveryManager,
		logger:           logger,
		metrics:          metrics,
		currentMode:      ModeNormal, // Default to normal
		modeChangedAt:    time.Now(),
		cacheTTL:         time.Second, // Cache for 1s
		stopCh:           make(chan struct{}),
	}
}

// GetCurrentMode returns cached mode (fast path)
func (m *DefaultModeManager) GetCurrentMode() Mode {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Return cached mode if still valid
	if time.Since(m.cachedModeAt) < m.cacheTTL {
		return m.cachedMode
	}

	// Cache expired, return current mode
	return m.currentMode
}

// IsMetricsOnly returns true if in metrics-only mode
func (m *DefaultModeManager) IsMetricsOnly() bool {
	return m.GetCurrentMode() == ModeMetricsOnly
}

// CheckModeTransition checks if mode should change
func (m *DefaultModeManager) CheckModeTransition() (Mode, bool, error) {
	start := time.Now()
	defer func() {
		duration := time.Since(start)
		m.mu.Lock()
		m.modeCheckDuration = duration
		m.mu.Unlock()
		// Record metrics
		if m.metrics != nil {
			m.metrics.RecordModeCheckDuration(duration.Seconds())
		}
	}()

	// Count enabled targets
	targets := m.discoveryManager.ListTargets()
	enabledCount := 0
	for _, t := range targets {
		if t.Enabled {
			enabledCount++
		}
	}

	// Determine new mode
	var newMode Mode
	if enabledCount > 0 {
		newMode = ModeNormal
	} else {
		newMode = ModeMetricsOnly
	}

	// Check if mode changed
	m.mu.Lock()
	changed := m.currentMode != newMode
	if changed {
		oldMode := m.currentMode
		oldModeDuration := time.Since(m.modeChangedAt).Seconds()
		m.currentMode = newMode
		m.modeChangedAt = time.Now()
		atomic.AddInt64(&m.transitionCount, 1)
		m.lastTransitionReason = m.getTransitionReason(enabledCount)
		m.cachedMode = newMode
		m.cachedModeAt = time.Now()

		// Record metrics
		if m.metrics != nil {
			m.metrics.RecordModeTransition(oldMode, newMode, oldModeDuration)
		}

		// Notify subscribers
		m.notifySubscribers(oldMode, newMode, m.lastTransitionReason)

		m.logger.Info("Mode transition detected",
			"from", oldMode.String(),
			"to", newMode.String(),
			"enabled_targets", enabledCount,
			"reason", m.lastTransitionReason,
		)
	} else {
		// Update cache even if no change
		m.cachedMode = newMode
		m.cachedModeAt = time.Now()
	}
	m.mu.Unlock()

	return newMode, changed, nil
}

// OnTargetsChanged is called when targets change
func (m *DefaultModeManager) OnTargetsChanged() error {
	_, _, err := m.CheckModeTransition()
	return err
}

// Subscribe subscribes to mode change events
func (m *DefaultModeManager) Subscribe(callback ModeChangeCallback) UnsubscribeFunc {
	m.subscribersMu.Lock()
	index := len(m.subscribers)
	m.subscribers = append(m.subscribers, callback)
	m.subscribersMu.Unlock()

	return func() {
		m.subscribersMu.Lock()
		defer m.subscribersMu.Unlock()

		// Remove callback (swap with last and truncate)
		if index < len(m.subscribers) {
			m.subscribers[index] = m.subscribers[len(m.subscribers)-1]
			m.subscribers = m.subscribers[:len(m.subscribers)-1]
		}
	}
}

// GetModeMetrics returns current mode metrics
func (m *DefaultModeManager) GetModeMetrics() ModeMetrics {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return ModeMetrics{
		CurrentMode:          m.currentMode,
		CurrentModeDuration:  time.Since(m.modeChangedAt),
		TransitionCount:      atomic.LoadInt64(&m.transitionCount),
		LastTransitionTime:    m.modeChangedAt,
		LastTransitionReason: m.lastTransitionReason,
		ModeCheckDuration:    m.modeCheckDuration, // Already protected by RLock
	}
}

// Start starts periodic mode checking
func (m *DefaultModeManager) Start(ctx context.Context) error {
	m.wg.Add(1)
	go m.periodicCheck(ctx)
	return nil
}

// Stop stops the mode manager
func (m *DefaultModeManager) Stop() error {
	m.mu.Lock()
	select {
	case <-m.stopCh:
		// Already stopped
		m.mu.Unlock()
		return nil
	default:
		close(m.stopCh)
		m.mu.Unlock()
	}
	m.wg.Wait()
	return nil
}

// periodicCheck periodically checks mode
func (m *DefaultModeManager) periodicCheck(ctx context.Context) {
	defer m.wg.Done()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.CheckModeTransition()
		}
	}
}

// notifySubscribers notifies all subscribers
func (m *DefaultModeManager) notifySubscribers(from, to Mode, reason string) {
	m.subscribersMu.RLock()
	subscribers := make([]ModeChangeCallback, len(m.subscribers))
	copy(subscribers, m.subscribers)
	m.subscribersMu.RUnlock()

	for _, callback := range subscribers {
		// Call in goroutine to avoid blocking
		go func(cb ModeChangeCallback) {
			defer func() {
				if r := recover(); r != nil {
					m.logger.Error("Panic in mode change callback",
						"panic", r,
					)
				}
			}()
			cb(from, to, reason)
		}(callback)
	}
}

// getTransitionReason returns reason for transition
func (m *DefaultModeManager) getTransitionReason(enabledCount int) string {
	if enabledCount == 0 {
		return "no_enabled_targets"
	}
	return "targets_available"
}
