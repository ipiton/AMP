package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/ipiton/AMP/internal/config"
	"github.com/ipiton/AMP/pkg/metrics"
	"github.com/ipiton/AMP/pkg/middleware"
)

const (
	appName    = "Alertmanager++"
	appVersion = "0.0.1"
)

//go:embed templates/*
var templatesFS embed.FS

//go:embed static/*
var staticFS embed.FS

var templates *template.Template

func main() {
	// Setup structured logging
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	slog.Info("🚀 Starting Alertmanager++",
		"version", appVersion,
		"profile", "OSS Core",
	)

	// Load configuration
	cfg, err := config.LoadConfig("config.yaml")
	if err != nil {
		slog.Warn("Config file not found, using defaults", "error", err)
		cfg = &config.Config{
			Server: config.ServerConfig{Port: 9093},
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize templates
	initTemplates()

	// Initialize business metrics
	businessMetrics := metrics.NewBusinessMetrics()
	_ = businessMetrics // Used for metrics recording
	slog.Info("✅ Metrics initialized")

	// Create HTTP mux
	mux := http.NewServeMux()
	registerRoutes(mux)

	// Start server
	port := cfg.Server.Port
	if port == 0 {
		port = 9093
	}

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Graceful shutdown
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan

		slog.Info("Shutting down server...")
		shutdownCtx, shutdownCancel := context.WithTimeout(ctx, 30*time.Second)
		defer shutdownCancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			slog.Error("Server shutdown error", "error", err)
		}
	}()

	slog.Info("🎯 Server listening",
		"port", port,
		"dashboard", fmt.Sprintf("http://localhost:%d/dashboard", port),
		"health", "/health",
		"metrics", "/metrics",
	)

	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		slog.Error("Server error", "error", err)
		os.Exit(1)
	}

	slog.Info("Server stopped gracefully")
}

func initTemplates() {
	// Try to load from embedded FS, fallback to disk for development
	var err error
	funcMap := webTemplateFuncMap()
	templates, err = template.New("").Funcs(funcMap).ParseFS(
		templatesFS,
		"templates/layouts/*.html",
		"templates/pages/*.html",
		"templates/partials/*.html",
	)
	if err != nil {
		slog.Warn("Failed to load embedded templates, trying disk", "error", err)
		templates, err = template.New("").Funcs(funcMap).ParseGlob("templates/**/*.html")
		if err != nil {
			slog.Warn("Templates not loaded, dashboard will use fallback", "error", err)
		}
	}
}

func webTemplateFuncMap() template.FuncMap {
	return template.FuncMap{
		"add": func(a, b int) int { return a + b },
		"sub": func(a, b int) int { return a - b },
		"mul": func(a, b int) int { return a * b },
		"default": func(def, val interface{}) interface{} {
			if val == nil {
				return def
			}

			switch v := val.(type) {
			case string:
				if v == "" {
					return def
				}
			case int:
				if v == 0 {
					return def
				}
			}

			return val
		},
		"truncate": func(s string, maxLen int) string {
			if len(s) <= maxLen {
				return s
			}
			if maxLen < 3 {
				return s[:maxLen]
			}
			return s[:maxLen-3] + "..."
		},
		"timeAgo": func(t time.Time) string {
			duration := time.Since(t)
			switch {
			case duration < time.Minute:
				return "just now"
			case duration < time.Hour:
				return fmt.Sprintf("%d minutes ago", int(duration.Minutes()))
			case duration < 24*time.Hour:
				return fmt.Sprintf("%d hours ago", int(duration.Hours()))
			default:
				return fmt.Sprintf("%d days ago", int(duration.Hours()/24))
			}
		},
		"formatDateTime": func(t time.Time) string {
			return t.Format(time.RFC3339)
		},
		"dict": func(values ...interface{}) (map[string]interface{}, error) {
			if len(values)%2 != 0 {
				return nil, fmt.Errorf("dict requires an even number of arguments")
			}

			result := make(map[string]interface{}, len(values)/2)
			for i := 0; i < len(values); i += 2 {
				key, ok := values[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict keys must be strings")
				}
				result[key] = values[i+1]
			}
			return result, nil
		},
		"until": func(n int) []int {
			if n <= 0 {
				return []int{}
			}
			result := make([]int, n)
			for i := 0; i < n; i++ {
				result[i] = i
			}
			return result
		},
		"upper": strings.ToUpper,
	}
}

// registerRoutes configures all active HTTP routes for the current runtime.
func registerRoutes(mux *http.ServeMux) {
	alertStore := newAlertStore()
	silenceStore := newSilenceStore()
	setupRuntimeStatePersistence(alertStore, silenceStore)
	persistencePath := resolveRuntimeStatePath()
	statusCtx := runtimeStatusContext{
		startedAt:          time.Now().UTC(),
		persistenceEnabled: persistencePath != "",
		persistencePath:    persistencePath,
	}

	// Static files
	staticSub, err := fs.Sub(staticFS, "static")
	if err != nil {
		slog.Error("Failed to mount static files", "error", err)
	} else {
		mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))
	}

	// Dashboard pages
	mux.HandleFunc("/", dashboardHandler)
	mux.HandleFunc("/dashboard", dashboardHandler)
	mux.HandleFunc("/dashboard/alerts", alertsPageHandler)
	mux.HandleFunc("/dashboard/silences", silencesPageHandler)
	mux.HandleFunc("/dashboard/llm", llmPageHandler)
	mux.HandleFunc("/dashboard/routing", routingPageHandler)

	// Health endpoints
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/ready", readyHandler)
	// Common probe aliases for compatibility with existing deployments
	mux.HandleFunc("/healthz", healthHandler)
	mux.HandleFunc("/readyz", readyHandler)
	// Alertmanager-compatible probe endpoints
	mux.HandleFunc("/-/healthy", alertmanagerHealthyHandler)
	mux.HandleFunc("/-/ready", alertmanagerReadyHandler)
	mux.HandleFunc("/-/reload", alertmanagerReloadHandler)
	mux.HandleFunc("/debug/", debugCompatHandler)

	// Metrics endpoint
	mux.Handle("/metrics", promhttp.Handler())

	// API endpoints
	mux.HandleFunc("/api/v2/alerts", alertsHandler(alertStore, silenceStore))
	// Alertmanager v1 compatibility ingest endpoint (intentionally limited)
	mux.HandleFunc("/api/v1/alerts", alertsV1Handler(alertStore, silenceStore))
	mux.HandleFunc("/api/v2/alerts/groups", alertGroupsHandler(alertStore))
	mux.HandleFunc("/api/v2/silences", silencesHandler(silenceStore))
	mux.HandleFunc("/api/v2/silence/", silenceByIDHandler(silenceStore))
	mux.HandleFunc("/api/v2/receivers", receiversHandler(alertStore))
	mux.HandleFunc("/api/v2/status", statusHandler(alertStore, silenceStore, statusCtx))
	mux.HandleFunc("/history", historyHandler(alertStore))
	mux.HandleFunc("/history/recent", historyRecentHandler(alertStore))

	// Dashboard API
	mux.HandleFunc("/api/dashboard/overview", dashboardOverviewAPI(alertStore, silenceStore, statusCtx))
	mux.HandleFunc("/api/dashboard/alerts/recent", dashboardAlertsRecentAPI(alertStore))

	// Alertmanager-compatible webhook endpoint with rate limiting
	rateLimiter := middleware.NewRateLimiter(middleware.RateLimiterConfig{
		PerIPLimit:  100,  // 100 requests per second per IP
		GlobalLimit: 1000, // 1000 requests per second globally
		Logger:      slog.Default(),
	})
	webhookHandlerWithRateLimit := rateLimiter.Middleware(webhookHandler(alertStore, silenceStore))
	mux.Handle("/webhook", webhookHandlerWithRateLimit)
}

// Page data for templates
type PageData struct {
	Title       string
	Version     string
	CurrentPage string
	Data        interface{}
}

type runtimeStatusContext struct {
	startedAt          time.Time
	persistenceEnabled bool
	persistencePath    string
}

type apiAlertGroup struct {
	Labels   map[string]string `json:"labels"`
	Receiver string            `json:"receiver"`
	Alerts   []apiAlert        `json:"alerts"`
}

type apiReceiver struct {
	Name string `json:"name"`
}

// Dashboard handlers
func dashboardHandler(w http.ResponseWriter, r *http.Request) {
	// "/"" is a catch-all pattern in net/http ServeMux. Guard unknown paths here
	// so unmatched API/ops routes return 404 instead of silently rendering dashboard.
	if r.URL.Path != "/" && r.URL.Path != "/dashboard" {
		handleNotFound(w, r)
		return
	}

	if templates == nil {
		renderTemplateError(w, "Templates not loaded")
		return
	}

	data := PageData{
		Title:       "Dashboard - Alertmanager++",
		Version:     appVersion,
		CurrentPage: "overview",
		Data: map[string]interface{}{
			"AlertsTotal":    0,
			"ActiveAlerts":   0,
			"SilencesActive": 0,
			"LLMEnabled":     false,
		},
	}

	if err := templates.ExecuteTemplate(w, "dashboard.html", data); err != nil {
		slog.Error("Failed to render dashboard template", "error", err)
		renderTemplateError(w, "Failed to render dashboard")
	}
}

func handleNotFound(w http.ResponseWriter, r *http.Request) {
	// API/ops style paths should keep machine-readable error responses.
	if strings.HasPrefix(r.URL.Path, "/api/") ||
		strings.HasPrefix(r.URL.Path, "/-/") ||
		strings.HasPrefix(r.URL.Path, "/debug/") ||
		strings.HasPrefix(r.URL.Path, "/history") ||
		r.URL.Path == "/webhook" ||
		r.URL.Path == "/metrics" {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": "not found",
		})
		return
	}

	http.NotFound(w, r)
}

func alertsPageHandler(w http.ResponseWriter, r *http.Request) {
	if templates == nil {
		renderTemplateError(w, "Templates not loaded")
		return
	}

	data := PageData{
		Title:       "Alert History - Alertmanager++",
		Version:     appVersion,
		CurrentPage: "alerts",
		Data:        map[string]interface{}{},
	}

	if err := templates.ExecuteTemplate(w, "alert-list.html", data); err != nil {
		slog.Error("Failed to render alerts template", "error", err)
		renderTemplateError(w, "Failed to render alerts page")
	}
}

func silencesPageHandler(w http.ResponseWriter, r *http.Request) {
	renderTemplateError(w, "Silences page not yet implemented")
}

func llmPageHandler(w http.ResponseWriter, r *http.Request) {
	renderTemplateError(w, "LLM settings page not yet implemented")
}

func routingPageHandler(w http.ResponseWriter, r *http.Request) {
	renderTemplateError(w, "Routing page not yet implemented")
}

// renderTemplateError renders a simple error page when templates fail to load or render
func renderTemplateError(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	html := `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Error - Alertmanager++</title>
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
            background: #0d1117;
            color: #f0f6fc;
            display: flex;
            align-items: center;
            justify-content: center;
            min-height: 100vh;
            padding: 20px;
        }
        .error-container {
            text-align: center;
            max-width: 600px;
        }
        .error-icon {
            font-size: 64px;
            margin-bottom: 24px;
        }
        h1 {
            font-size: 32px;
            margin-bottom: 16px;
            color: #f85149;
        }
        p {
            font-size: 16px;
            color: #8b949e;
            margin-bottom: 32px;
            line-height: 1.6;
        }
        .btn {
            display: inline-block;
            padding: 12px 24px;
            background: #58a6ff;
            color: white;
            text-decoration: none;
            border-radius: 6px;
            font-weight: 500;
            transition: background 0.2s;
        }
        .btn:hover {
            background: #4a8dd8;
        }
        .details {
            margin-top: 32px;
            padding: 16px;
            background: #161b22;
            border: 1px solid #30363d;
            border-radius: 6px;
            font-family: monospace;
            font-size: 14px;
            color: #f85149;
        }
    </style>
</head>
<body>
    <div class="error-container">
        <div class="error-icon">⚠️</div>
        <h1>Template Error</h1>
        <p>The dashboard template system encountered an error and cannot render this page.</p>
        <div class="details">` + message + `</div>
        <p style="margin-top: 24px;">
            <a href="/health" class="btn">Check System Health</a>
            <a href="/metrics" class="btn" style="margin-left: 12px; background: #21262d;">View Metrics</a>
        </p>
    </div>
</body>
</html>`
	w.Write([]byte(html))
}

// API handlers
func dashboardOverviewAPI(
	alertStore *alertStore,
	silenceStore *silenceStore,
	statusCtx runtimeStatusContext,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		now := time.Now().UTC()
		alertTotal, alertFiring, alertResolved := alertStore.stats()
		silenceTotal, silenceActive, silencePending, silenceExpired := silenceStore.stats(now)

		writeJSON(w, http.StatusOK, map[string]any{
			"status": "success",
			"data": map[string]any{
				"alerts_total_24h":     alertTotal,
				"active_alerts":        alertFiring,
				"resolved_alerts":      alertResolved,
				"active_silences":      silenceActive,
				"silences_total":       silenceTotal,
				"silences_pending":     silencePending,
				"silences_expired":     silenceExpired,
				"llm_classifications":  0,
				"system_health":        "healthy",
				"runtime_uptime":       now.Sub(statusCtx.startedAt).String(),
				"persistence_enabled":  statusCtx.persistenceEnabled,
				"persistence_location": statusCtx.persistencePath,
			},
		})
	}
}

func dashboardAlertsRecentAPI(alertStore *alertStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		status, includeResolved, err := parseHistoryFilters(r, false)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": err.Error(),
			})
			return
		}

		limit, err := parsePositiveIntQuery(r.URL.Query().Get("limit"), 10, 1, 100)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": err.Error(),
			})
			return
		}

		alerts := alertStore.list(status, includeResolved)
		total := len(alerts)
		if len(alerts) > limit {
			alerts = alerts[:limit]
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"status": "success",
			"data": map[string]any{
				"alerts":   alerts,
				"total":    total,
				"returned": len(alerts),
				"limit":    limit,
			},
		})
	}
}

// Health handlers
func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"healthy","version":"` + appVersion + `"}`))
}

func readyHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"ready":true}`))
}

func alertmanagerHealthyHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		w.Write([]byte("OK"))
	}
}

func alertmanagerReadyHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		w.Write([]byte("OK"))
	}
}

func alertmanagerReloadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "reloaded",
		"mode":   "noop",
	})
}

func debugCompatHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "available",
		"path":   r.URL.Path,
	})
}

// API handlers
func alertsV1Handler(store *alertStore, silences *silenceStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		handleAlertsPost(store, silences, w, r)
	}
}

func alertsHandler(store *alertStore, silences *silenceStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handleAlertsGet(store, w, r)
		case http.MethodPost:
			handleAlertsPost(store, silences, w, r)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func handleAlertsGet(store *alertStore, w http.ResponseWriter, r *http.Request) {
	status := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))
	switch status {
	case "", "firing", "resolved":
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid status filter",
		})
		return
	}

	includeResolved := parseBoolWithDefault(r.URL.Query().Get("resolved"), false)
	if status == "resolved" {
		includeResolved = true
	}

	writeJSON(w, http.StatusOK, store.list(status, includeResolved))
}

func handleAlertsPost(store *alertStore, silences *silenceStore, w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 10*1024*1024))
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{
			"error": "request payload too large",
		})
		return
	}

	payload, err := parseAlertIngestPayload(body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": err.Error(),
		})
		return
	}

	now := time.Now().UTC()
	filteredPayload, silencedCount := filterSilencedAlerts(payload, silences, now)
	if silencedCount > 0 {
		slog.Info("Suppressed alerts by active silences", "count", silencedCount)
	}

	if err := store.ingestBatch(filteredPayload, now); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": err.Error(),
		})
		return
	}

	w.WriteHeader(http.StatusOK)
}

func filterSilencedAlerts(in []alertIngestInput, silences *silenceStore, now time.Time) ([]alertIngestInput, int) {
	if silences == nil || len(in) == 0 {
		return in, 0
	}

	out := make([]alertIngestInput, 0, len(in))
	silencedCount := 0

	for i := range in {
		normalizedStatus := strings.ToLower(strings.TrimSpace(in[i].Status))
		if normalizedStatus == "resolved" {
			out = append(out, in[i])
			continue
		}

		if len(silences.activeMatchingSilenceIDs(in[i].Labels, now)) == 0 {
			out = append(out, in[i])
			continue
		}

		silencedCount++
	}

	return out, silencedCount
}

func silencesHandler(store *silenceStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, store.list(time.Now().UTC()))
		case http.MethodPost:
			handleSilencePost(store, w, r)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func handleSilencePost(store *silenceStore, w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1024*1024))
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{
			"error": "request payload too large",
		})
		return
	}

	payload, err := parseSilencePayload(body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": err.Error(),
		})
		return
	}

	silenceID, err := store.createOrUpdate(payload, time.Now().UTC())
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"silenceID": silenceID,
	})
}

func silenceByIDHandler(store *silenceStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/api/v2/silence/")
		if id == "" || strings.Contains(id, "/") {
			writeJSON(w, http.StatusNotFound, map[string]string{
				"error": "silence not found",
			})
			return
		}

		switch r.Method {
		case http.MethodGet:
			silence, ok := store.get(id, time.Now().UTC())
			if !ok {
				writeJSON(w, http.StatusNotFound, map[string]string{
					"error": "silence not found",
				})
				return
			}
			writeJSON(w, http.StatusOK, silence)
		case http.MethodDelete:
			if !store.delete(id) {
				writeJSON(w, http.StatusNotFound, map[string]string{
					"error": "silence not found",
				})
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{
				"status": "deleted",
			})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func receiversHandler(store *alertStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		receiversSet := map[string]struct{}{
			"default": {},
		}
		for _, alert := range store.list("", true) {
			receiver := strings.TrimSpace(alert.Labels["receiver"])
			if receiver == "" {
				continue
			}
			receiversSet[receiver] = struct{}{}
		}

		receivers := make([]apiReceiver, 0, len(receiversSet))
		for name := range receiversSet {
			receivers = append(receivers, apiReceiver{Name: name})
		}
		sort.Slice(receivers, func(i, j int) bool {
			return receivers[i].Name < receivers[j].Name
		})

		writeJSON(w, http.StatusOK, receivers)
	}
}

func alertGroupsHandler(store *alertStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		includeResolved := parseBoolWithDefault(r.URL.Query().Get("resolved"), false)
		alerts := store.list("", includeResolved)

		groupsMap := make(map[string]*apiAlertGroup)
		for _, alert := range alerts {
			groupLabels := map[string]string{
				"alertname": alert.Labels["alertname"],
				"service":   alert.Labels["service"],
				"namespace": alert.Labels["namespace"],
			}
			receiver := strings.TrimSpace(alert.Labels["receiver"])
			if receiver == "" {
				receiver = "default"
			}

			key := groupLabels["alertname"] + "|" + groupLabels["service"] + "|" + groupLabels["namespace"] + "|" + receiver
			group, ok := groupsMap[key]
			if !ok {
				group = &apiAlertGroup{
					Labels:   groupLabels,
					Receiver: receiver,
					Alerts:   make([]apiAlert, 0, 1),
				}
				groupsMap[key] = group
			}
			group.Alerts = append(group.Alerts, alert)
		}

		groups := make([]apiAlertGroup, 0, len(groupsMap))
		for _, group := range groupsMap {
			groups = append(groups, *group)
		}
		sort.Slice(groups, func(i, j int) bool {
			a := groups[i].Labels["alertname"] + "|" + groups[i].Labels["service"] + "|" + groups[i].Labels["namespace"] + "|" + groups[i].Receiver
			b := groups[j].Labels["alertname"] + "|" + groups[j].Labels["service"] + "|" + groups[j].Labels["namespace"] + "|" + groups[j].Receiver
			return a < b
		})

		writeJSON(w, http.StatusOK, groups)
	}
}

func statusHandler(alertStore *alertStore, silenceStore *silenceStore, statusCtx runtimeStatusContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		now := time.Now().UTC()
		alertTotal, alertFiring, alertResolved := alertStore.stats()
		silenceTotal, silenceActive, silencePending, silenceExpired := silenceStore.stats(now)

		writeJSON(w, http.StatusOK, map[string]any{
			"cluster": map[string]string{
				"status": "ready",
			},
			"versionInfo": map[string]string{
				"version": appVersion,
			},
			"config": map[string]string{
				"original": "",
			},
			"uptime": now.Sub(statusCtx.startedAt).String(),
			"stats": map[string]any{
				"alerts": map[string]int{
					"total":    alertTotal,
					"firing":   alertFiring,
					"resolved": alertResolved,
				},
				"silences": map[string]int{
					"total":   silenceTotal,
					"active":  silenceActive,
					"pending": silencePending,
					"expired": silenceExpired,
				},
			},
			"runtime": map[string]any{
				"persistenceEnabled": statusCtx.persistenceEnabled,
				"persistencePath":    statusCtx.persistencePath,
			},
		})
	}
}

func historyHandler(store *alertStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		status, includeResolved, err := parseHistoryFilters(r, true)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": err.Error(),
			})
			return
		}

		alerts := store.list(status, includeResolved)
		writeJSON(w, http.StatusOK, map[string]any{
			"total":  len(alerts),
			"alerts": alerts,
		})
	}
}

func historyRecentHandler(store *alertStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		status, includeResolved, err := parseHistoryFilters(r, true)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": err.Error(),
			})
			return
		}

		limit, err := parsePositiveIntQuery(r.URL.Query().Get("limit"), 20, 1, 200)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": err.Error(),
			})
			return
		}

		alerts := store.list(status, includeResolved)
		if len(alerts) > limit {
			alerts = alerts[:limit]
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"total":  len(alerts),
			"limit":  limit,
			"alerts": alerts,
		})
	}
}

func parseHistoryFilters(r *http.Request, defaultIncludeResolved bool) (string, bool, error) {
	status := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))
	switch status {
	case "", "firing", "resolved":
	default:
		return "", false, fmt.Errorf("invalid status filter")
	}

	includeResolved := parseBoolWithDefault(r.URL.Query().Get("resolved"), defaultIncludeResolved)
	if status == "resolved" {
		includeResolved = true
	}

	return status, includeResolved, nil
}

func parsePositiveIntQuery(raw string, def, min, max int) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return def, nil
	}

	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("invalid integer query value")
	}
	if value < min || value > max {
		return 0, fmt.Errorf("query value must be between %d and %d", min, max)
	}

	return value, nil
}

func webhookHandler(alertStore *alertStore, silences *silenceStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		defer r.Body.Close()

		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 10*1024*1024))
		if err != nil {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{
				"error": "request payload too large",
			})
			return
		}

		payload, err := parseAlertIngestPayload(body)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": err.Error(),
			})
			return
		}

		now := time.Now().UTC()
		filteredPayload, silencedCount := filterSilencedAlerts(payload, silences, now)
		if silencedCount > 0 {
			slog.Info("Suppressed webhook alerts by active silences", "count", silencedCount)
		}

		if err := alertStore.ingestBatch(filteredPayload, now); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": err.Error(),
			})
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"status":    "received",
			"alerts":    len(payload),
			"processed": len(filteredPayload),
			"silenced":  silencedCount,
		})
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if payload == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		slog.Error("Failed to encode JSON response", "error", err)
	}
}
