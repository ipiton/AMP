package main

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
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

	// Static files
	staticSub, _ := fs.Sub(staticFS, "static")
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))

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

	// Metrics endpoint
	mux.Handle("/metrics", promhttp.Handler())

	// API endpoints
	mux.HandleFunc("/api/v2/alerts", alertsHandler)
	mux.HandleFunc("/api/v2/silences", silencesHandler)
	mux.HandleFunc("/api/v2/status", statusHandler)

	// Dashboard API
	mux.HandleFunc("/api/dashboard/overview", dashboardOverviewAPI)
	mux.HandleFunc("/api/dashboard/alerts/recent", dashboardAlertsRecentAPI)

	// Alertmanager-compatible webhook endpoint with rate limiting
	rateLimiter := middleware.NewRateLimiter(middleware.RateLimiterConfig{
		PerIPLimit:  100,  // 100 requests per second per IP
		GlobalLimit: 1000, // 1000 requests per second globally
		Logger:      slog.Default(),
	})
	webhookHandlerWithRateLimit := rateLimiter.Middleware(http.HandlerFunc(webhookHandler))
	mux.Handle("/webhook", webhookHandlerWithRateLimit)

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
	templates, err = template.ParseFS(templatesFS, "templates/layouts/*.html", "templates/pages/*.html", "templates/partials/*.html")
	if err != nil {
		slog.Warn("Failed to load embedded templates, trying disk", "error", err)
		templates, err = template.ParseGlob("templates/**/*.html")
		if err != nil {
			slog.Warn("Templates not loaded, dashboard will use fallback", "error", err)
		}
	}
}

// Page data for templates
type PageData struct {
	Title       string
	Version     string
	CurrentPage string
	Data        interface{}
}

// Dashboard handlers
func dashboardHandler(w http.ResponseWriter, r *http.Request) {
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
func dashboardOverviewAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{
		"status": "success",
		"data": {
			"alerts_total_24h": 0,
			"active_alerts": 0,
			"active_silences": 0,
			"llm_classifications": 0,
			"system_health": "healthy"
		}
	}`))
}

func dashboardAlertsRecentAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{
		"status": "success",
		"data": {
			"alerts": [],
			"total": 0
		}
	}`))
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

// API handlers
func alertsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case http.MethodGet:
		w.Write([]byte(`{"status":"success","data":[]}`))
	case http.MethodPost:
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"success"}`))
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func silencesHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"success","data":[]}`))
}

func statusHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{
		"cluster": {"status": "ready"},
		"versionInfo": {"version": "` + appVersion + `"},
		"config": {"original": ""},
		"uptime": "0s"
	}`))
}

func webhookHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"received"}`))
}
