package main

import (
	"bytes"
	"context"
	"embed"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/ipiton/AMP/internal/application"
)

//go:embed templates/*
var templatesFS embed.FS

//go:embed static/*
var staticFS embed.FS

var templates *template.Template

type legacyDashboardProvider interface {
	LegacyDashboardOverview(ctx context.Context, now time.Time) application.LegacyDashboardOverviewSummary
	LegacyDashboardAlerts(now time.Time) application.LegacyDashboardAlertsSummary
	LegacyDashboardSilences(now time.Time) application.LegacyDashboardSilencesSummary
	LegacyDashboardLLM() application.LegacyDashboardLLMSummary
	LegacyDashboardRouting() application.LegacyDashboardRoutingSummary
}

type legacyDashboardPageData struct {
	Title       string
	Heading     string
	Description string
	Version     string
	CurrentPage string
	GeneratedAt string
	Content     any
}

type legacyDashboardHandlers struct {
	provider legacyDashboardProvider
}

func registerLegacyDashboardRoutes(mux *http.ServeMux, provider legacyDashboardProvider) {
	staticSub, err := fs.Sub(staticFS, "static")
	if err != nil {
		slog.Error("Failed to mount static files", "error", err)
	} else {
		mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))
	}

	handlers := legacyDashboardHandlers{provider: provider}

	mux.HandleFunc("/", handlers.dashboardHandler)
	mux.HandleFunc("/dashboard", handlers.dashboardHandler)
	mux.HandleFunc("/dashboard/alerts", handlers.alertsPageHandler)
	mux.HandleFunc("/dashboard/silences", handlers.silencesPageHandler)
	mux.HandleFunc("/dashboard/llm", handlers.llmPageHandler)
	mux.HandleFunc("/dashboard/routing", handlers.routingPageHandler)
}

func initTemplates() {
	var err error

	templates, err = template.New("").ParseFS(
		templatesFS,
		"templates/legacy/*.html",
	)
	if err != nil {
		slog.Warn("Failed to load embedded legacy dashboard templates, trying disk", "error", err)
		templates, err = template.New("").ParseGlob("templates/legacy/*.html")
	}
}

func (h legacyDashboardHandlers) dashboardHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/dashboard" {
		http.NotFound(w, r)
		return
	}

	now := time.Now().UTC()
	renderTemplate(w, "dashboard-overview.html", legacyDashboardPageData{
		Title:       "Dashboard - Alertmanager++",
		Heading:     "Dashboard",
		Description: "Active runtime summary for the current server path.",
		Version:     appVersion,
		CurrentPage: "overview",
		GeneratedAt: now.Format(time.RFC3339),
		Content:     h.provider.LegacyDashboardOverview(r.Context(), now),
	})
}

func (h legacyDashboardHandlers) alertsPageHandler(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	renderTemplate(w, "dashboard-alerts.html", legacyDashboardPageData{
		Title:       "Alerts - Alertmanager++",
		Heading:     "Alerts",
		Description: "Read-only alert inventory from the active compatibility store.",
		Version:     appVersion,
		CurrentPage: "alerts",
		GeneratedAt: now.Format(time.RFC3339),
		Content:     h.provider.LegacyDashboardAlerts(now),
	})
}

func (h legacyDashboardHandlers) silencesPageHandler(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	renderTemplate(w, "dashboard-silences.html", legacyDashboardPageData{
		Title:       "Silences - Alertmanager++",
		Heading:     "Silences",
		Description: "Read-only silence state for the active runtime.",
		Version:     appVersion,
		CurrentPage: "silences",
		GeneratedAt: now.Format(time.RFC3339),
		Content:     h.provider.LegacyDashboardSilences(now),
	})
}

func (h legacyDashboardHandlers) llmPageHandler(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	renderTemplate(w, "dashboard-llm.html", legacyDashboardPageData{
		Title:       "LLM - Alertmanager++",
		Heading:     "LLM",
		Description: "Config and coarse runtime state for alert classification.",
		Version:     appVersion,
		CurrentPage: "llm",
		GeneratedAt: now.Format(time.RFC3339),
		Content:     h.provider.LegacyDashboardLLM(),
	})
}

func (h legacyDashboardHandlers) routingPageHandler(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	renderTemplate(w, "dashboard-routing.html", legacyDashboardPageData{
		Title:       "Routing - Alertmanager++",
		Heading:     "Routing",
		Description: "Read-only summary of publishing and routing reality in the active runtime.",
		Version:     appVersion,
		CurrentPage: "routing",
		GeneratedAt: now.Format(time.RFC3339),
		Content:     h.provider.LegacyDashboardRouting(),
	})
}

func renderTemplate(w http.ResponseWriter, name string, data legacyDashboardPageData) {
	if templates == nil {
		http.Error(w, "Templates not loaded", http.StatusInternalServerError)
		return
	}

	var buf bytes.Buffer
	if err := templates.ExecuteTemplate(&buf, name, data); err != nil {
		slog.Error("Dashboard template error", "template", name, "error", err)
		http.Error(w, "Failed to render dashboard page", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}
