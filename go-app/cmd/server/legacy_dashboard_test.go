package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/application"
)

type stubLegacyDashboardProvider struct {
	overview application.LegacyDashboardOverviewSummary
	alerts   application.LegacyDashboardAlertsSummary
	silences application.LegacyDashboardSilencesSummary
	llm      application.LegacyDashboardLLMSummary
	routing  application.LegacyDashboardRoutingSummary
}

func (s stubLegacyDashboardProvider) LegacyDashboardOverview(context.Context, time.Time) application.LegacyDashboardOverviewSummary {
	return s.overview
}

func (s stubLegacyDashboardProvider) LegacyDashboardAlerts(time.Time) application.LegacyDashboardAlertsSummary {
	return s.alerts
}

func (s stubLegacyDashboardProvider) LegacyDashboardSilences(time.Time) application.LegacyDashboardSilencesSummary {
	return s.silences
}

func (s stubLegacyDashboardProvider) LegacyDashboardLLM() application.LegacyDashboardLLMSummary {
	return s.llm
}

func (s stubLegacyDashboardProvider) LegacyDashboardRouting() application.LegacyDashboardRoutingSummary {
	return s.routing
}

func newLegacyDashboardTestMux(t *testing.T, provider legacyDashboardProvider) *http.ServeMux {
	t.Helper()

	initTemplates()

	mux := http.NewServeMux()
	registerLegacyDashboardRoutes(mux, provider)
	return mux
}

func TestLegacyDashboardPlaceholderRoutes_RenderReadOnlyPages(t *testing.T) {
	provider := stubLegacyDashboardProvider{
		silences: application.LegacyDashboardSilencesSummary{
			RuntimeStatus:      "ready",
			RuntimeStatusClass: "ready",
			RuntimeDetail:      "Showing silence state from the active compatibility store.",
			Total:              1,
			Active:             1,
			Silences: []application.LegacyDashboardSilenceItem{
				{
					ID:              "sil-1",
					Status:          "active",
					StatusClass:     "active",
					CreatedBy:       "ops",
					Comment:         "maintenance window",
					MatchersSummary: "alertname=Watchdog",
					StartsAt:        "2026-03-09T10:00:00Z",
					EndsAt:          "2026-03-09T11:00:00Z",
					UpdatedAt:       "2026-03-09T10:05:00Z",
				},
			},
		},
		llm: application.LegacyDashboardLLMSummary{
			Enabled:            true,
			Provider:           "openai",
			BaseURL:            "https://api.openai.example/v1",
			Model:              "gpt-4o-mini",
			Timeout:            "5s",
			MaxTokens:          512,
			Temperature:        "0.10",
			MaxRetries:         3,
			RuntimeStatus:      "degraded",
			RuntimeStatusClass: "degraded",
			RuntimeDetail:      "Classification runtime is not initialized in the current process.",
		},
		routing: application.LegacyDashboardRoutingSummary{
			Enabled:              true,
			Profile:              "standard",
			Namespace:            "alerts-prod",
			LabelSelector:        "publishing-target=true",
			QueueWorkers:         4,
			MaxConcurrent:        8,
			RefreshEnabled:       true,
			HealthEnabled:        true,
			RuntimeStatus:        "metrics-only",
			RuntimeStatusClass:   "metrics-only",
			RuntimeDetail:        "Publishing is using metrics-only fallback (publishing stack unavailable).",
			Mode:                 "metrics-only",
			ModeClass:            "metrics-only",
			ModeDuration:         "5s",
			TransitionCount:      2,
			LastTransitionTime:   "2026-03-09T10:00:00Z",
			LastTransitionReason: "publishing stack unavailable",
			TargetCount:          0,
			ValidTargets:         0,
			InvalidTargets:       0,
			DiscoveryErrors:      1,
			LastDiscovery:        "2026-03-09T09:59:00Z",
			CollectorCount:       2,
			CollectorNames:       []string{"discovery", "mode"},
		},
	}

	mux := newLegacyDashboardTestMux(t, provider)

	tests := []struct {
		name       string
		path       string
		wantParts  []string
		avoidParts []string
	}{
		{
			name:       "silences ready",
			path:       "/dashboard/silences",
			wantParts:  []string{"Silence inventory", "maintenance window", "alertname=Watchdog", "/api/v2/silences"},
			avoidParts: []string{"not yet implemented"},
		},
		{
			name:       "llm limited",
			path:       "/dashboard/llm",
			wantParts:  []string{"Legacy UI boundary", "Classification runtime is not initialized in the current process.", "openai", "gpt-4o-mini"},
			avoidParts: []string{"not yet implemented", "Total classification requests"},
		},
		{
			name:       "routing metrics only",
			path:       "/dashboard/routing",
			wantParts:  []string{"Routing config", "metrics-only", "alerts-prod", "publishing stack unavailable", "discovery"},
			avoidParts: []string{"not yet implemented"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()

			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s status = %d, want %d", tt.path, rec.Code, http.StatusOK)
			}
			if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "text/html") {
				t.Fatalf("GET %s content-type = %q, want html", tt.path, got)
			}

			body := rec.Body.String()
			for _, part := range tt.wantParts {
				if !strings.Contains(body, part) {
					t.Fatalf("GET %s body missing %q\nbody=%s", tt.path, part, body)
				}
			}
			for _, part := range tt.avoidParts {
				if strings.Contains(body, part) {
					t.Fatalf("GET %s body must not contain %q\nbody=%s", tt.path, part, body)
				}
			}
		})
	}
}

func TestLegacyDashboardSilencesRoute_EmptyState(t *testing.T) {
	provider := stubLegacyDashboardProvider{
		silences: application.LegacyDashboardSilencesSummary{
			RuntimeStatus:      "ready",
			RuntimeStatusClass: "ready",
			RuntimeDetail:      "No silences are configured right now.",
		},
	}

	mux := newLegacyDashboardTestMux(t, provider)

	req := httptest.NewRequest(http.MethodGet, "/dashboard/silences", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /dashboard/silences status = %d, want %d", rec.Code, http.StatusOK)
	}

	body := rec.Body.String()
	for _, part := range []string{"No silences yet", "No silences are configured right now.", "Page state"} {
		if !strings.Contains(body, part) {
			t.Fatalf("GET /dashboard/silences body missing %q\nbody=%s", part, body)
		}
	}
	if strings.Contains(body, "not yet implemented") {
		t.Fatalf("GET /dashboard/silences still contains placeholder body\nbody=%s", body)
	}
}

func TestRenderTemplate_WhenTemplatesNotLoaded_ReturnsInternalServerError(t *testing.T) {
	previous := templates
	templates = nil
	t.Cleanup(func() {
		templates = previous
	})

	rec := httptest.NewRecorder()

	renderTemplate(rec, "dashboard-overview.html", legacyDashboardPageData{
		Title: "Dashboard - Alertmanager++",
	})

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("renderTemplate status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if !strings.Contains(rec.Body.String(), "Templates not loaded") {
		t.Fatalf("renderTemplate body = %q, want missing templates error", rec.Body.String())
	}
}
