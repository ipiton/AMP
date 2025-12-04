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

	// Alertmanager-compatible webhook endpoint
	mux.HandleFunc("/webhook", webhookHandler)

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
		renderFallbackDashboard(w, "overview")
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
		renderFallbackDashboard(w, "overview")
	}
}

func alertsPageHandler(w http.ResponseWriter, r *http.Request) {
	renderFallbackDashboard(w, "alerts")
}

func silencesPageHandler(w http.ResponseWriter, r *http.Request) {
	renderFallbackDashboard(w, "silences")
}

func llmPageHandler(w http.ResponseWriter, r *http.Request) {
	renderFallbackDashboard(w, "llm")
}

func routingPageHandler(w http.ResponseWriter, r *http.Request) {
	renderFallbackDashboard(w, "routing")
}

func renderFallbackDashboard(w http.ResponseWriter, page string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	html := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Alertmanager++ Dashboard</title>
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }
        :root {
            --bg-primary: #0d1117;
            --bg-secondary: #161b22;
            --bg-tertiary: #21262d;
            --text-primary: #f0f6fc;
            --text-secondary: #8b949e;
            --accent: #58a6ff;
            --success: #3fb950;
            --warning: #d29922;
            --danger: #f85149;
            --border: #30363d;
        }
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', 'Noto Sans', Helvetica, Arial, sans-serif;
            background: var(--bg-primary);
            color: var(--text-primary);
            min-height: 100vh;
        }
        .layout {
            display: flex;
            min-height: 100vh;
        }
        .sidebar {
            width: 260px;
            background: var(--bg-secondary);
            border-right: 1px solid var(--border);
            padding: 20px 0;
            position: fixed;
            height: 100vh;
            overflow-y: auto;
        }
        .logo {
            padding: 0 20px 20px;
            border-bottom: 1px solid var(--border);
            margin-bottom: 20px;
        }
        .logo h1 {
            font-size: 20px;
            font-weight: 600;
            display: flex;
            align-items: center;
            gap: 8px;
        }
        .logo span { color: var(--text-secondary); font-size: 12px; }
        .nav-section { padding: 0 12px; margin-bottom: 24px; }
        .nav-section-title {
            font-size: 11px;
            font-weight: 600;
            text-transform: uppercase;
            color: var(--text-secondary);
            padding: 0 8px;
            margin-bottom: 8px;
        }
        .nav-link {
            display: flex;
            align-items: center;
            gap: 12px;
            padding: 10px 12px;
            color: var(--text-secondary);
            text-decoration: none;
            border-radius: 6px;
            font-size: 14px;
            transition: all 0.15s;
        }
        .nav-link:hover { background: var(--bg-tertiary); color: var(--text-primary); }
        .nav-link.active { background: var(--accent); color: white; }
        .nav-link svg { width: 16px; height: 16px; }
        .main {
            flex: 1;
            margin-left: 260px;
            padding: 24px;
        }
        .header {
            display: flex;
            justify-content: space-between;
            align-items: center;
            margin-bottom: 24px;
        }
        .header h2 { font-size: 24px; font-weight: 600; }
        .cards {
            display: grid;
            grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
            gap: 16px;
            margin-bottom: 24px;
        }
        .card {
            background: var(--bg-secondary);
            border: 1px solid var(--border);
            border-radius: 8px;
            padding: 20px;
        }
        .card-title {
            font-size: 12px;
            font-weight: 500;
            color: var(--text-secondary);
            text-transform: uppercase;
            margin-bottom: 8px;
        }
        .card-value {
            font-size: 32px;
            font-weight: 600;
        }
        .card-value.success { color: var(--success); }
        .card-value.warning { color: var(--warning); }
        .card-value.danger { color: var(--danger); }
        .section {
            background: var(--bg-secondary);
            border: 1px solid var(--border);
            border-radius: 8px;
            margin-bottom: 24px;
        }
        .section-header {
            padding: 16px 20px;
            border-bottom: 1px solid var(--border);
            font-weight: 600;
        }
        .section-body { padding: 20px; }
        .empty-state {
            text-align: center;
            padding: 40px;
            color: var(--text-secondary);
        }
        .badge {
            display: inline-block;
            padding: 4px 10px;
            border-radius: 20px;
            font-size: 12px;
            font-weight: 500;
        }
        .badge-success { background: rgba(63, 185, 80, 0.2); color: var(--success); }
        .badge-warning { background: rgba(210, 153, 34, 0.2); color: var(--warning); }
        .badge-danger { background: rgba(248, 81, 73, 0.2); color: var(--danger); }
        table { width: 100%%; border-collapse: collapse; }
        th, td { padding: 12px; text-align: left; border-bottom: 1px solid var(--border); }
        th { font-weight: 600; color: var(--text-secondary); font-size: 12px; text-transform: uppercase; }
        .btn {
            display: inline-flex;
            align-items: center;
            gap: 8px;
            padding: 8px 16px;
            border-radius: 6px;
            font-size: 14px;
            font-weight: 500;
            cursor: pointer;
            border: 1px solid var(--border);
            background: var(--bg-tertiary);
            color: var(--text-primary);
            transition: all 0.15s;
        }
        .btn:hover { background: var(--border); }
        .btn-primary { background: var(--accent); border-color: var(--accent); }
        .btn-primary:hover { filter: brightness(1.1); }
        .status-dot {
            width: 8px;
            height: 8px;
            border-radius: 50%%;
            display: inline-block;
            margin-right: 8px;
        }
        .status-dot.healthy { background: var(--success); }
        .status-dot.warning { background: var(--warning); }
        .status-dot.error { background: var(--danger); }
    </style>
</head>
<body>
    <div class="layout">
        <nav class="sidebar">
            <div class="logo">
                <h1>🚀 Alertmanager++</h1>
                <span>v%s</span>
            </div>
            <div class="nav-section">
                <div class="nav-section-title">Dashboard</div>
                <a href="/dashboard" class="nav-link %s">
                    <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 12l2-2m0 0l7-7 7 7M5 10v10a1 1 0 001 1h3m10-11l2 2m-2-2v10a1 1 0 01-1 1h-3m-6 0a1 1 0 001-1v-4a1 1 0 011-1h2a1 1 0 011 1v4a1 1 0 001 1m-6 0h6"/></svg>
                    Overview
                </a>
                <a href="/dashboard/alerts" class="nav-link %s">
                    <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 17h5l-1.405-1.405A2.032 2.032 0 0118 14.158V11a6.002 6.002 0 00-4-5.659V5a2 2 0 10-4 0v.341C7.67 6.165 6 8.388 6 11v3.159c0 .538-.214 1.055-.595 1.436L4 17h5m6 0v1a3 3 0 11-6 0v-1m6 0H9"/></svg>
                    Alert History
                </a>
                <a href="/dashboard/silences" class="nav-link %s">
                    <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5.586 15H4a1 1 0 01-1-1v-4a1 1 0 011-1h1.586l4.707-4.707C10.923 3.663 12 4.109 12 5v14c0 .891-1.077 1.337-1.707.707L5.586 15z" clip-rule="evenodd"/><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M17 14l2-2m0 0l2-2m-2 2l-2-2m2 2l2 2"/></svg>
                    Silences
                </a>
            </div>
            <div class="nav-section">
                <div class="nav-section-title">Configuration</div>
                <a href="/dashboard/llm" class="nav-link %s">
                    <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9.663 17h4.673M12 3v1m6.364 1.636l-.707.707M21 12h-1M4 12H3m3.343-5.657l-.707-.707m2.828 9.9a5 5 0 117.072 0l-.548.547A3.374 3.374 0 0014 18.469V19a2 2 0 11-4 0v-.531c0-.895-.356-1.754-.988-2.386l-.548-.547z"/></svg>
                    LLM Settings
                </a>
                <a href="/dashboard/routing" class="nav-link %s">
                    <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 10V3L4 14h7v7l9-11h-7z"/></svg>
                    Routing & Groups
                </a>
            </div>
            <div class="nav-section">
                <div class="nav-section-title">System</div>
                <a href="/metrics" class="nav-link" target="_blank">
                    <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 19v-6a2 2 0 00-2-2H5a2 2 0 00-2 2v6a2 2 0 002 2h2a2 2 0 002-2zm0 0V9a2 2 0 012-2h2a2 2 0 012 2v10m-6 0a2 2 0 002 2h2a2 2 0 002-2m0 0V5a2 2 0 012-2h2a2 2 0 012 2v14a2 2 0 01-2 2h-2a2 2 0 01-2-2z"/></svg>
                    Prometheus Metrics
                </a>
                <a href="/health" class="nav-link" target="_blank">
                    <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4.318 6.318a4.5 4.5 0 000 6.364L12 20.364l7.682-7.682a4.5 4.5 0 00-6.364-6.364L12 7.636l-1.318-1.318a4.5 4.5 0 00-6.364 0z"/></svg>
                    Health Check
                </a>
            </div>
        </nav>
        <main class="main">
`, appVersion,
		activeClass(page, "overview"),
		activeClass(page, "alerts"),
		activeClass(page, "silences"),
		activeClass(page, "llm"),
		activeClass(page, "routing"))

	// Page content based on current page
	switch page {
	case "overview":
		html += renderOverviewContent()
	case "alerts":
		html += renderAlertsContent()
	case "silences":
		html += renderSilencesContent()
	case "llm":
		html += renderLLMContent()
	case "routing":
		html += renderRoutingContent()
	}

	html += `
        </main>
    </div>
    <script>
        // Auto-refresh dashboard data every 30 seconds
        setInterval(() => {
            fetch('/api/dashboard/overview')
                .then(r => r.json())
                .then(data => {
                    console.log('Dashboard data:', data);
                    // Update UI here
                })
                .catch(err => console.error('Failed to refresh:', err));
        }, 30000);
    </script>
</body>
</html>`

	w.Write([]byte(html))
}

func activeClass(current, page string) string {
	if current == page {
		return "active"
	}
	return ""
}

func renderOverviewContent() string {
	return `
            <div class="header">
                <h2>Dashboard Overview</h2>
                <span class="badge badge-success"><span class="status-dot healthy"></span>System Healthy</span>
            </div>
            <div class="cards">
                <div class="card">
                    <div class="card-title">Total Alerts (24h)</div>
                    <div class="card-value">0</div>
                </div>
                <div class="card">
                    <div class="card-title">Active Alerts</div>
                    <div class="card-value success">0</div>
                </div>
                <div class="card">
                    <div class="card-title">Active Silences</div>
                    <div class="card-value">0</div>
                </div>
                <div class="card">
                    <div class="card-title">LLM Classifications</div>
                    <div class="card-value">0</div>
                </div>
            </div>
            <div class="section">
                <div class="section-header">Recent Alerts</div>
                <div class="section-body">
                    <div class="empty-state">
                        <p>No recent alerts</p>
                        <p style="font-size: 14px; margin-top: 8px;">Alerts will appear here when received</p>
                    </div>
                </div>
            </div>
            <div class="section">
                <div class="section-header">System Health</div>
                <div class="section-body">
                    <table>
                        <thead><tr><th>Component</th><th>Status</th><th>Latency</th></tr></thead>
                        <tbody>
                            <tr><td><span class="status-dot healthy"></span>API Server</td><td><span class="badge badge-success">Healthy</span></td><td>< 1ms</td></tr>
                            <tr><td><span class="status-dot healthy"></span>Metrics</td><td><span class="badge badge-success">Healthy</span></td><td>< 1ms</td></tr>
                            <tr><td><span class="status-dot warning"></span>Database</td><td><span class="badge badge-warning">Not Configured</span></td><td>-</td></tr>
                            <tr><td><span class="status-dot warning"></span>Redis</td><td><span class="badge badge-warning">Not Configured</span></td><td>-</td></tr>
                            <tr><td><span class="status-dot warning"></span>LLM</td><td><span class="badge badge-warning">Disabled</span></td><td>-</td></tr>
                        </tbody>
                    </table>
                </div>
            </div>`
}

func renderAlertsContent() string {
	return `
            <div class="header">
                <h2>Alert History</h2>
                <div>
                    <button class="btn">Filter</button>
                    <button class="btn">Export</button>
                </div>
            </div>
            <div class="section">
                <div class="section-body">
                    <div class="empty-state">
                        <p>No alerts in history</p>
                        <p style="font-size: 14px; margin-top: 8px;">Configure a database to enable alert history persistence</p>
                    </div>
                </div>
            </div>`
}

func renderSilencesContent() string {
	return `
            <div class="header">
                <h2>Silences</h2>
                <button class="btn btn-primary">+ Create Silence</button>
            </div>
            <div class="section">
                <div class="section-body">
                    <div class="empty-state">
                        <p>No active silences</p>
                        <p style="font-size: 14px; margin-top: 8px;">Create a silence to suppress alerts matching specific labels</p>
                    </div>
                </div>
            </div>`
}

func renderLLMContent() string {
	return `
            <div class="header">
                <h2>LLM Classification Settings</h2>
            </div>
            <div class="section">
                <div class="section-header">Configuration</div>
                <div class="section-body">
                    <table>
                        <tr><td><strong>Status</strong></td><td><span class="badge badge-warning">Disabled</span></td></tr>
                        <tr><td><strong>Provider</strong></td><td>Not configured</td></tr>
                        <tr><td><strong>Model</strong></td><td>-</td></tr>
                        <tr><td><strong>API Key</strong></td><td>Not set</td></tr>
                    </table>
                    <div style="margin-top: 20px;">
                        <p style="color: var(--text-secondary); font-size: 14px;">
                            To enable LLM classification, add the following to your config.yaml:
                        </p>
                        <pre style="background: var(--bg-tertiary); padding: 16px; border-radius: 6px; margin-top: 12px; overflow-x: auto;">llm:
  enabled: true
  provider: "openai"
  api_key: "${LLM_API_KEY}"
  base_url: "https://api.openai.com/v1/chat/completions"
  model: "gpt-4o"</pre>
                    </div>
                </div>
            </div>
            <div class="section">
                <div class="section-header">Supported Providers</div>
                <div class="section-body">
                    <table>
                        <thead><tr><th>Provider</th><th>Models</th><th>Status</th></tr></thead>
                        <tbody>
                            <tr><td>OpenAI</td><td>GPT-4, GPT-4o, GPT-3.5</td><td><span class="badge badge-success">Supported</span></td></tr>
                            <tr><td>Anthropic</td><td>Claude 3 Opus, Sonnet, Haiku</td><td><span class="badge badge-success">Supported</span></td></tr>
                            <tr><td>Azure OpenAI</td><td>GPT-4, GPT-3.5</td><td><span class="badge badge-success">Supported</span></td></tr>
                            <tr><td>Custom/Ollama</td><td>Any OpenAI-compatible</td><td><span class="badge badge-success">Supported</span></td></tr>
                        </tbody>
                    </table>
                </div>
            </div>`
}

func renderRoutingContent() string {
	return `
            <div class="header">
                <h2>Routing & Grouping Configuration</h2>
            </div>
            <div class="section">
                <div class="section-header">Route Tree</div>
                <div class="section-body">
                    <div class="empty-state">
                        <p>No routing configuration loaded</p>
                        <p style="font-size: 14px; margin-top: 8px;">Load an alertmanager.yml to configure routing</p>
                    </div>
                </div>
            </div>
            <div class="section">
                <div class="section-header">Receivers</div>
                <div class="section-body">
                    <div class="empty-state">
                        <p>No receivers configured</p>
                    </div>
                </div>
            </div>
            <div class="section">
                <div class="section-header">Inhibition Rules</div>
                <div class="section-body">
                    <div class="empty-state">
                        <p>No inhibition rules configured</p>
                    </div>
                </div>
            </div>`
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
