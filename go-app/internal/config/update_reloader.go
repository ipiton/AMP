package config

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// ================================================================================
// Configuration Hot Reloader
// ================================================================================
// Orchestrates hot reload across multiple Reloadable components (TN-150,
// reworked by INF-A slice 1).
//
// Features:
// - Component registry (register/unregister), ordered by ReloadPriority
// - Section-driven applicability: a component is reloaded only when one of
//   its RelevantSections actually changed between old and new config
// - Sequential, deterministic, fail-fast execution with timeout
// - Error collection naming the component that rejected the reload
//
// Performance Target: < 300ms p95 for typical reload

// defaultReloadPriority is the ReloadPriority assumed for components that do
// not implement OrderedReloadable. Chosen mid-range so such a component lands
// after the logger (10) and before storage (90+) without having to opt in.
const defaultReloadPriority = 100

// reloadTimeout bounds the whole ReloadAll pass. Kept at the pre-INF-A
// per-component value: reloads are sequential now, so this is a budget for
// all of them together — which is the number an operator actually cares about
// (how long can a SIGHUP hold before I know it failed).
const reloadTimeout = 30 * time.Second

// registeredComponent pairs a Reloadable with its resolved ordering key.
type registeredComponent struct {
	component Reloadable
	priority  int
}

// DefaultConfigReloader implements ConfigReloader interface
type DefaultConfigReloader struct {
	components []registeredComponent
	mu         sync.RWMutex
	logger     *slog.Logger
}

// NewConfigReloader creates a new ConfigReloader instance
func NewConfigReloader(logger *slog.Logger) *DefaultConfigReloader {
	if logger == nil {
		logger = slog.Default()
	}

	return &DefaultConfigReloader{
		components: make([]registeredComponent, 0),
		logger:     logger,
	}
}

// Register implements ConfigReloader.Register
//
// Registers a component for hot reload. Registering the same Name() twice is
// a no-op (idempotent). The registry is kept sorted by ReloadPriority so
// ReloadAll's order is deterministic regardless of registration order; ties
// keep registration order (stable sort).
func (r *DefaultConfigReloader) Register(component Reloadable) {
	if component == nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Check if already registered (by name)
	for _, existing := range r.components {
		if existing.component.Name() == component.Name() {
			r.logger.Warn("component already registered, skipping",
				"component", component.Name(),
			)
			return
		}
	}

	// A typo in RelevantSections would make the component never reload —
	// the worst failure mode for a hot-reload hook, because it looks like
	// success. Name it at registration time, where it is cheap to spot.
	for _, section := range component.RelevantSections() {
		if !IsKnownSection(section) {
			r.logger.Error("component declares an unknown config section; it will be treated as always-relevant",
				"component", component.Name(),
				"section", section,
				"known_sections", KnownSections(),
			)
		}
	}

	priority := defaultReloadPriority
	if ordered, ok := component.(OrderedReloadable); ok {
		priority = ordered.ReloadPriority()
	}

	r.components = append(r.components, registeredComponent{
		component: component,
		priority:  priority,
	})
	sort.SliceStable(r.components, func(i, j int) bool {
		return r.components[i].priority < r.components[j].priority
	})

	r.logger.Info("component registered for hot reload",
		"component", component.Name(),
		"critical", component.IsCritical(),
		"sections", component.RelevantSections(),
		"priority", priority,
		"total_components", len(r.components),
	)
}

// Unregister implements ConfigReloader.Unregister
//
// Removes a component from hot reload registry
// No-op if component not registered
func (r *DefaultConfigReloader) Unregister(componentName string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i, entry := range r.components {
		if entry.component.Name() == componentName {
			// Remove component from slice
			r.components = append(r.components[:i], r.components[i+1:]...)
			r.logger.Info("component unregistered from hot reload",
				"component", componentName,
				"total_components", len(r.components),
			)
			return
		}
	}

	r.logger.Warn("component not found for unregister",
		"component", componentName,
	)
}

// ReloadAll implements ConfigReloader.ReloadAll
//
// Reloads the applicable components sequentially in ReloadPriority order and
// stops at the first error. See the interface doc for why this is sequential
// and fail-fast.
func (r *DefaultConfigReloader) ReloadAll(
	ctx context.Context,
	oldCfg *Config,
	newCfg *Config,
	affectedComponents []string,
) []ReloadError {
	r.mu.RLock()
	defer r.mu.RUnlock()

	startTime := time.Now()

	// Filter components to those actually affected by this change
	componentsToReload := r.filterComponents(oldCfg, newCfg, affectedComponents)
	if len(componentsToReload) == 0 {
		r.logger.Info("no components need reload",
			"total_components", len(r.components),
			"affected_components", affectedComponents,
		)
		return nil
	}

	r.logger.Info("starting hot reload",
		"total_components", len(r.components),
		"components_to_reload", componentNames(componentsToReload),
		"changed_sections", ChangedSections(oldCfg, newCfg),
	)

	reloadCtx, cancel := context.WithTimeout(ctx, reloadTimeout)
	defer cancel()

	reloadErrors := make([]ReloadError, 0, 1)

	for _, comp := range componentsToReload {
		compStart := time.Now()
		r.logger.Info("reloading component",
			"component", comp.Name(),
			"critical", comp.IsCritical(),
		)

		err := comp.Reload(reloadCtx, oldCfg, newCfg)
		duration := time.Since(compStart)

		if err != nil {
			r.logger.Error("component reload failed, rejecting the reload",
				"component", comp.Name(),
				"critical", comp.IsCritical(),
				"error", err,
				"duration_ms", duration.Milliseconds(),
			)
			reloadErrors = append(reloadErrors, ReloadError{
				Component: comp.Name(),
				Error:     err.Error(),
				Critical:  comp.IsCritical(),
				Duration:  duration,
			})
			// Fail-fast: every further component would apply changes that
			// the coordinator is about to roll back anyway.
			break
		}

		r.logger.Info("component reloaded successfully",
			"component", comp.Name(),
			"duration_ms", duration.Milliseconds(),
		)
	}

	totalDuration := time.Since(startTime)

	if len(reloadErrors) == 0 {
		r.logger.Info("hot reload completed successfully",
			"components_reloaded", len(componentsToReload),
			"duration_ms", totalDuration.Milliseconds(),
		)
	} else {
		r.logger.Warn("hot reload rejected",
			"failed_component", reloadErrors[0].Component,
			"duration_ms", totalDuration.Milliseconds(),
		)
	}

	return reloadErrors
}

// GetRegisteredComponents implements ConfigReloader.GetRegisteredComponents
//
// Returns registered component names in reload order.
func (r *DefaultConfigReloader) GetRegisteredComponents() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, len(r.components))
	for i, entry := range r.components {
		names[i] = entry.component.Name()
	}

	return names
}

// SelectComponents implements ConfigReloader.SelectComponents.
func (r *DefaultConfigReloader) SelectComponents(
	oldCfg *Config,
	newCfg *Config,
	affectedComponents []string,
) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return componentNames(r.filterComponents(oldCfg, newCfg, affectedComponents))
}

// ================================================================================
// Helper Functions
// ================================================================================

// filterComponents selects the components to reload, in reload order.
//
// Two independent gates, both must pass:
//  1. Name gate: when affectedComponents is non-empty the component's Name()
//     must appear in it. This preserves the pre-INF-A behaviour (the
//     coordinator/update service passes the diff's affected-component list)
//     and lets a caller narrow a reload deliberately.
//  2. Section gate: at least one of the component's RelevantSections must
//     differ between oldCfg and newCfg. A component with no declared
//     sections, or an unknown section, always passes (SectionChanged returns
//     true for unknown names and for a nil oldCfg).
//
// Caller must hold at least r.mu.RLock.
func (r *DefaultConfigReloader) filterComponents(
	oldCfg *Config,
	newCfg *Config,
	affectedComponents []string,
) []Reloadable {
	var affectedSet map[string]bool
	if len(affectedComponents) > 0 {
		affectedSet = make(map[string]bool, len(affectedComponents))
		for _, name := range affectedComponents {
			affectedSet[name] = true
		}
	}

	filtered := make([]Reloadable, 0, len(r.components))
	for _, entry := range r.components {
		component := entry.component
		if affectedSet != nil && !affectedSet[component.Name()] {
			continue
		}
		if !sectionsChanged(oldCfg, newCfg, component.RelevantSections()) {
			continue
		}
		filtered = append(filtered, component)
	}

	return filtered
}

// sectionsChanged reports whether any of the given sections changed. An empty
// section list means "always relevant".
func sectionsChanged(oldCfg, newCfg *Config, sections []string) bool {
	if len(sections) == 0 {
		return true
	}
	for _, section := range sections {
		if SectionChanged(oldCfg, newCfg, section) {
			return true
		}
	}
	return false
}

// componentNames extracts Name() from a component slice, preserving order.
func componentNames(components []Reloadable) []string {
	names := make([]string, len(components))
	for i, component := range components {
		names[i] = component.Name()
	}
	return names
}

// HasCriticalErrors checks if error list contains critical errors
func HasCriticalErrors(errors []ReloadError) bool {
	for _, err := range errors {
		if err.Critical {
			return true
		}
	}
	return false
}

// FormatReloadErrors formats reload errors into human-readable string
func FormatReloadErrors(errors []ReloadError) string {
	if len(errors) == 0 {
		return "No errors"
	}

	var result string
	for i, err := range errors {
		criticalMarker := ""
		if err.Critical {
			criticalMarker = " [CRITICAL]"
		}
		result += fmt.Sprintf("%d. %s%s: %s (took %v)\n",
			i+1, err.Component, criticalMarker, err.Error, err.Duration)
	}

	return result
}

// ================================================================================
// Type Alias for Interface Implementation
// ================================================================================

// Ensure DefaultConfigReloader implements ConfigReloader interface
var _ ConfigReloader = (*DefaultConfigReloader)(nil)
