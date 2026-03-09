package application

import (
	"context"
	"fmt"

	"github.com/ipiton/AMP/internal/core"
)

type storageRuntime interface {
	core.AlertStorage
	Health(ctx context.Context) error
	Disconnect(ctx context.Context) error
}

func (r *ServiceRegistry) addDegradedReason(format string, args ...any) {
	if r == nil {
		return
	}

	reason := fmt.Sprintf(format, args...)
	for _, existing := range r.degradedReasons {
		if existing == reason {
			return
		}
	}
	r.degradedReasons = append(r.degradedReasons, reason)
}

func (r *ServiceRegistry) Liveness(ctx context.Context) error {
	_ = ctx
	if !r.initialized {
		return fmt.Errorf("service registry not initialized")
	}
	return nil
}

func (r *ServiceRegistry) Readiness(ctx context.Context) error {
	if !r.initialized {
		return fmt.Errorf("service registry not initialized")
	}
	if r.storageRuntime == nil || r.storage == nil {
		return fmt.Errorf("storage runtime not initialized")
	}
	if err := r.storageRuntime.Health(ctx); err != nil {
		return fmt.Errorf("storage unhealthy: %w", err)
	}

	if r.requiresDatabase() {
		if r.database == nil {
			return fmt.Errorf("database not initialized")
		}
		if err := r.database.Health(ctx); err != nil {
			return fmt.Errorf("database unhealthy: %w", err)
		}
	}

	return nil
}

func (r *ServiceRegistry) LivenessReport(ctx context.Context) map[string]any {
	return r.buildHealthReport(ctx, false)
}

func (r *ServiceRegistry) ReadinessReport(ctx context.Context) map[string]any {
	return r.buildHealthReport(ctx, true)
}

func (r *ServiceRegistry) buildHealthReport(ctx context.Context, readiness bool) map[string]any {
	checks := map[string]map[string]any{
		"bootstrap": {
			"status":   "unhealthy",
			"required": true,
		},
		"storage": {
			"status":   "unhealthy",
			"required": true,
		},
	}

	requiredHealthy := true
	if r.initialized {
		checks["bootstrap"]["status"] = "healthy"
	} else {
		checks["bootstrap"]["error"] = "service registry not initialized"
		requiredHealthy = false
	}

	if r.storageRuntime == nil || r.storage == nil {
		checks["storage"]["error"] = "storage runtime not initialized"
		requiredHealthy = false
	} else if err := r.storageRuntime.Health(ctx); err != nil {
		checks["storage"]["error"] = err.Error()
		requiredHealthy = false
	} else {
		checks["storage"]["status"] = "healthy"
	}

	if r.requiresDatabase() {
		checks["database"] = map[string]any{
			"status":   "unhealthy",
			"required": true,
		}

		if r.database == nil {
			checks["database"]["error"] = "database not initialized"
			requiredHealthy = false
		} else if err := r.database.Health(ctx); err != nil {
			checks["database"]["error"] = err.Error()
			requiredHealthy = false
		} else {
			checks["database"]["status"] = "healthy"
		}
	}

	status := "healthy"
	if !requiredHealthy {
		status = "unhealthy"
	} else if len(r.degradedReasons) > 0 {
		status = "degraded"
	}

	report := map[string]any{
		"status":           status,
		"mode":             "liveness",
		"profile":          r.profileName(),
		"initialized":      r.initialized,
		"checks":           checks,
		"degraded_reasons": append([]string(nil), r.degradedReasons...),
	}

	if readiness {
		report["mode"] = "readiness"
		report["ready"] = requiredHealthy
	}

	return report
}

func (r *ServiceRegistry) requiresDatabase() bool {
	return r != nil && r.config != nil && r.config.UsesPostgresStorage()
}

func (r *ServiceRegistry) profileName() string {
	if r == nil || r.config == nil {
		return ""
	}
	return string(r.config.Profile)
}
