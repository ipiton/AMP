package handlers

import (
	"context"
	"fmt"
	"net/http"
)

type HealthStatusProvider interface {
	Liveness(ctx context.Context) error
	Readiness(ctx context.Context) error
	LivenessReport(ctx context.Context) map[string]any
	ReadinessReport(ctx context.Context) map[string]any
}

func HealthHandler(provider HealthStatusProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if provider == nil {
			InternalErrorHandler(w, "health provider is not available")
			return
		}

		status := http.StatusOK
		if err := provider.Liveness(r.Context()); err != nil {
			status = http.StatusServiceUnavailable
		}
		writeJSON(w, status, provider.LivenessReport(r.Context()))
	}
}

func ReadyHandler(provider HealthStatusProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if provider == nil {
			InternalErrorHandler(w, "health provider is not available")
			return
		}

		status := http.StatusOK
		if err := provider.Readiness(r.Context()); err != nil {
			status = http.StatusServiceUnavailable
		}
		writeJSON(w, status, provider.ReadinessReport(r.Context()))
	}
}

func NotFoundHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
}

func InternalErrorHandler(w http.ResponseWriter, message string) {
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": message})
}

// Alertmanager compatible health endpoints
func AlertmanagerHealthyHandler(provider HealthStatusProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if provider == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = fmt.Fprint(w, "NOT OK")
			return
		}

		if err := provider.Liveness(r.Context()); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = fmt.Fprint(w, "NOT OK")
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "OK")
	}
}

func AlertmanagerReadyHandler(provider HealthStatusProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if provider == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = fmt.Fprint(w, "NOT READY")
			return
		}

		if err := provider.Readiness(r.Context()); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = fmt.Fprint(w, "NOT READY")
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "OK")
	}
}
