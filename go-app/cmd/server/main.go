package main

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ipiton/AMP/internal/application"
	"github.com/ipiton/AMP/internal/config"
)

const (
	appName    = "Alertmanager++"
	appVersion = "0.0.1"
)

const runtimeConfigFileEnv = "AMP_CONFIG_FILE"

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
	cfg, err := config.LoadConfig(resolveRuntimeConfigPath())
	if err != nil {
		slog.Warn("Config file not found, using defaults", "error", err)
		cfg = &config.Config{
			Server: config.ServerConfig{Port: 9093},
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize Service Registry
	registry, err := application.NewServiceRegistry(cfg, logger)
	if err != nil {
		slog.Error("Failed to create service registry", "error", err)
		os.Exit(1)
	}

	if err := registry.Initialize(ctx); err != nil {
		slog.Error("Failed to initialize services", "error", err)
		os.Exit(1)
	}

	// Initialize templates (dashboard)
	initTemplates()

	// Create HTTP mux and router
	mux := http.NewServeMux()
	router := application.NewRouter(registry)
	router.SetupRoutes(mux)

	// Dashboard and static files (legacy/compatibility)
	registerLegacyDashboardRoutes(mux)

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

		if err := registry.Shutdown(shutdownCtx); err != nil {
			slog.Error("Registry shutdown error", "error", err)
		}

		if err := server.Shutdown(shutdownCtx); err != nil {
			slog.Error("Server shutdown error", "error", err)
		}
	}()

	slog.Info("🎯 Server listening",
		"port", port,
		"dashboard", fmt.Sprintf("http://localhost:%d/dashboard", port),
	)

	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		slog.Error("Server error", "error", err)
		os.Exit(1)
	}

	slog.Info("Server stopped gracefully")
}

func resolveRuntimeConfigPath() string {
	path := strings.TrimSpace(os.Getenv(runtimeConfigFileEnv))
	if path != "" {
		return path
	}
	return "config.yaml"
}

func registerLegacyDashboardRoutes(mux *http.ServeMux) {
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
}

func initTemplates() {
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
			return val
		},
		"truncate": func(s string, maxLen int) string {
			if len(s) <= maxLen {
				return s
			}
			return s[:maxLen-3] + "..."
		},
		"timeAgo": func(t time.Time) string {
			return "some time ago"
		},
	}
}

// Page data for templates
type PageData struct {
	Title       string
	Version     string
	CurrentPage string
	Data        interface{}
}

// Dashboard handlers (minimal placeholders until fully migrated to package handlers)

func dashboardHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/dashboard" {
		http.NotFound(w, r)
		return
	}
	renderTemplate(w, "dashboard.html", "Dashboard", "overview", nil)
}

func alertsPageHandler(w http.ResponseWriter, r *http.Request) {
	renderTemplate(w, "alert-list.html", "Alerts", "alerts", nil)
}

func silencesPageHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "Silences page not yet implemented")
}

func llmPageHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "LLM page not yet implemented")
}

func routingPageHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "Routing page not yet implemented")
}

func renderTemplate(w http.ResponseWriter, name, title, current string, data interface{}) {
	if templates == nil {
		http.Error(w, "Templates not loaded", http.StatusInternalServerError)
		return
	}
	pd := PageData{
		Title:       title + " - Alertmanager++",
		Version:     appVersion,
		CurrentPage: current,
		Data:        data,
	}
	if err := templates.ExecuteTemplate(w, name, pd); err != nil {
		slog.Error("Template error", "error", err)
	}
}
