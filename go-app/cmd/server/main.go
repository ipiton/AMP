package main

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"gopkg.in/yaml.v3"

	"github.com/ipiton/AMP/internal/config"
	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/infrastructure/inhibition"
	cfgmatcher "github.com/ipiton/AMP/pkg/configvalidator/matcher"
	"github.com/ipiton/AMP/pkg/metrics"
	"github.com/ipiton/AMP/pkg/middleware"
)

const (
	appName    = "Alertmanager++"
	appVersion = "0.0.1"
)

const runtimeConfigFileEnv = "AMP_CONFIG_FILE"
const runtimeClusterListenAddressEnv = "AMP_CLUSTER_LISTEN_ADDRESS"
const runtimeClusterAdvertiseAddressEnv = "AMP_CLUSTER_ADVERTISE_ADDRESS"
const runtimeClusterNameEnv = "AMP_CLUSTER_NAME"

const defaultRuntimeClusterListenAddress = "0.0.0.0:9094"
const defaultRuntimeClusterSettlingDuration = 10 * time.Second

const maxConfigApplyHistoryEntries = 500
const maxConfigRevisionEntries = 100

var (
	buildRevision = "unknown"
	buildBranch   = "unknown"
	buildUser     = "unknown"
	buildDate     = "unknown"
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
	cfg, err := config.LoadConfig(resolveRuntimeConfigPath())
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
	configPath := resolveRuntimeConfigPath()
	alertStore := newAlertStore()
	silenceStore := newSilenceStore()
	inhibitionEngine := loadRuntimeInhibitionEngine(configPath)
	receiverCatalog := loadRuntimeReceiverCatalog(configPath)
	alertGroupByCatalog := loadRuntimeAlertGroupByCatalog(configPath)
	clusterCtx := loadRuntimeClusterContext()
	setupRuntimeStatePersistence(alertStore, silenceStore)
	persistencePath := resolveRuntimeStatePath()
	statusCtx := &runtimeStatusContext{
		startedAt:          time.Now().UTC(),
		persistenceEnabled: persistencePath != "",
		persistencePath:    persistencePath,
		configOriginal:     readRuntimeConfigOriginalAt(configPath),
		configApplyStatus:  "unknown",
		configApplySource:  "startup",
	}
	if err := applyRuntimeConfigReload(configPath, statusCtx, inhibitionEngine, receiverCatalog, alertGroupByCatalog); err != nil {
		slog.Warn("Initial runtime config apply failed",
			"config", configPath,
			"error", err,
		)
		statusCtx.setConfigApplyResult("startup", err)
	} else {
		statusCtx.setConfigApplyResult("startup", nil)
	}

	// Static files
	staticSub, err := fs.Sub(staticFS, "static")
	if err != nil {
		slog.Error("Failed to mount static files", "error", err)
	} else {
		mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))
		registerUpstreamStaticCompatRoutes(mux, staticSub)
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
	mux.HandleFunc("/-/reload", alertmanagerReloadHandler(configPath, statusCtx, inhibitionEngine, receiverCatalog, alertGroupByCatalog))
	mux.HandleFunc("/debug/", debugCompatHandler)

	// Metrics endpoint
	mux.Handle("/metrics", promhttp.Handler())

	// API endpoints
	mux.HandleFunc("/api/v2/alerts", alertsHandler(alertStore, silenceStore, inhibitionEngine))
	// Alertmanager v1 compatibility ingest endpoint (intentionally limited)
	mux.HandleFunc("/api/v1/alerts", alertsV1Handler(alertStore, silenceStore, inhibitionEngine))
	mux.HandleFunc("/api/v2/alerts/groups", alertGroupsHandler(alertStore, silenceStore, inhibitionEngine, alertGroupByCatalog))
	mux.HandleFunc("/api/v2/silences", silencesHandler(silenceStore))
	mux.HandleFunc("/api/v2/silence/", silenceByIDHandler(silenceStore))
	mux.HandleFunc("/api/v2/receivers", receiversHandler(receiverCatalog))
	mux.HandleFunc("/api/v2/status", statusHandler(alertStore, silenceStore, statusCtx, clusterCtx))
	mux.HandleFunc("/api/v2/config", configHandler(configPath, statusCtx, inhibitionEngine, receiverCatalog, alertGroupByCatalog))
	mux.HandleFunc("/api/v2/config/status", configStatusHandler(configPath, statusCtx, inhibitionEngine, receiverCatalog))
	mux.HandleFunc("/api/v2/config/history", configHistoryHandler(configPath, statusCtx))
	mux.HandleFunc("/api/v2/config/revisions", configRevisionsHandler(configPath, statusCtx))
	mux.HandleFunc("/api/v2/config/revisions/prune", configRevisionsPruneHandler(configPath, statusCtx))
	mux.HandleFunc("/api/v2/config/rollback", configRollbackHandler(configPath, statusCtx, inhibitionEngine, receiverCatalog, alertGroupByCatalog))
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
	mu                 sync.RWMutex
	startedAt          time.Time
	persistenceEnabled bool
	persistencePath    string
	configOriginal     string
	configApplyStatus  string
	configApplySource  string
	configApplyError   string
	configApplyAt      time.Time
	configApplyHistory []runtimeConfigApplyHistoryEntry
	configRevisions    []runtimeConfigRevision
}

type runtimeClusterContext struct {
	status      string
	name        string
	peers       []map[string]string
	settleUntil time.Time
}

type runtimeConfigApplyHistoryEntry struct {
	Status     string `json:"status"`
	Source     string `json:"source"`
	AppliedAt  string `json:"appliedAt"`
	Error      string `json:"error"`
	ConfigHash string `json:"configHash"`
}

type runtimeConfigRevision struct {
	ConfigHash    string
	ConfigContent string
	Source        string
	AppliedAt     time.Time
}

type runtimeConfigRevisionSummary struct {
	ConfigHash string `json:"configHash"`
	Source     string `json:"source"`
	AppliedAt  string `json:"appliedAt"`
	IsCurrent  bool   `json:"isCurrent"`
}

type runtimeInhibitionEngine struct {
	mu    sync.RWMutex
	rules []inhibition.InhibitionRule
}

type runtimeReceiverCatalog struct {
	mu         sync.RWMutex
	configured []string
}

type runtimeAlertGroupByCatalog struct {
	mu         sync.RWMutex
	groupBy    []string
	configured bool
}

type alertmanagerRuntimeConfig struct {
	Route     alertmanagerRouteConfig      `yaml:"route"`
	Receivers []alertmanagerReceiverConfig `yaml:"receivers"`
}

type alertmanagerRouteConfig struct {
	Receiver string                    `yaml:"receiver"`
	GroupBy  []string                  `yaml:"group_by"`
	Routes   []alertmanagerRouteConfig `yaml:"routes"`
}

type alertmanagerReceiverConfig struct {
	Name string `yaml:"name"`
}

type apiAlertStatus struct {
	State       string   `json:"state"`
	SilencedBy  []string `json:"silencedBy"`
	InhibitedBy []string `json:"inhibitedBy"`
	MutedBy     []string `json:"mutedBy"`
}

type apiGettableAlert struct {
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	Receivers    []apiReceiver     `json:"receivers"`
	StartsAt     string            `json:"startsAt"`
	UpdatedAt    string            `json:"updatedAt"`
	EndsAt       string            `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL,omitempty"`
	Fingerprint  string            `json:"fingerprint"`
	Status       apiAlertStatus    `json:"status"`
}

type apiAlertGroup struct {
	Labels   map[string]string `json:"labels"`
	Receiver apiReceiver       `json:"receiver"`
	Alerts   []apiAlert        `json:"alerts"`
}

type apiGettableAlertGroup struct {
	Labels   map[string]string  `json:"labels"`
	Receiver apiReceiver        `json:"receiver"`
	Alerts   []apiGettableAlert `json:"alerts"`
}

type apiReceiver struct {
	Name string `json:"name"`
}

func (c *runtimeStatusContext) getConfigOriginal() string {
	if c == nil {
		return ""
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.configOriginal
}

func (c *runtimeStatusContext) setConfigOriginal(configOriginal string) {
	if c == nil {
		return
	}

	c.mu.Lock()
	c.configOriginal = configOriginal
	c.mu.Unlock()
}

func (c *runtimeStatusContext) setConfigApplyResult(source string, err error) {
	if c == nil {
		return
	}

	c.mu.Lock()
	c.configApplySource = strings.TrimSpace(source)
	if c.configApplySource == "" {
		c.configApplySource = "unknown"
	}
	c.configApplyAt = time.Now().UTC()
	if err != nil {
		c.configApplyStatus = "failed"
		c.configApplyError = err.Error()
	} else {
		c.configApplyStatus = "ok"
		c.configApplyError = ""
		c.appendConfigRevisionLocked(c.configApplySource, c.configApplyAt)
	}

	c.configApplyHistory = append(c.configApplyHistory, runtimeConfigApplyHistoryEntry{
		Status:     c.configApplyStatus,
		Source:     c.configApplySource,
		AppliedAt:  formatAPITimestamp(c.configApplyAt),
		Error:      c.configApplyError,
		ConfigHash: configSHA256(c.configOriginal),
	})
	if len(c.configApplyHistory) > maxConfigApplyHistoryEntries {
		c.configApplyHistory = append([]runtimeConfigApplyHistoryEntry(nil), c.configApplyHistory[len(c.configApplyHistory)-maxConfigApplyHistoryEntries:]...)
	}
	c.mu.Unlock()
}

func (c *runtimeStatusContext) appendConfigRevisionLocked(source string, appliedAt time.Time) {
	if c == nil {
		return
	}

	configHash := configSHA256(c.configOriginal)
	if len(c.configRevisions) > 0 {
		last := c.configRevisions[len(c.configRevisions)-1]
		if last.ConfigHash == configHash {
			return
		}
	}

	c.configRevisions = append(c.configRevisions, runtimeConfigRevision{
		ConfigHash:    configHash,
		ConfigContent: c.configOriginal,
		Source:        source,
		AppliedAt:     appliedAt,
	})
	if len(c.configRevisions) > maxConfigRevisionEntries {
		c.configRevisions = append(
			[]runtimeConfigRevision(nil),
			c.configRevisions[len(c.configRevisions)-maxConfigRevisionEntries:]...,
		)
	}
}

func (c *runtimeStatusContext) getConfigApplyResult() (status, source, errorText string, at time.Time) {
	if c == nil {
		return "unknown", "unknown", "", time.Time{}
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	if strings.TrimSpace(c.configApplyStatus) == "" {
		status = "unknown"
	} else {
		status = c.configApplyStatus
	}
	if strings.TrimSpace(c.configApplySource) == "" {
		source = "unknown"
	} else {
		source = c.configApplySource
	}
	return status, source, c.configApplyError, c.configApplyAt
}

func (c *runtimeStatusContext) getConfigApplyHistory(limit int, statusFilter, sourceFilter string) []runtimeConfigApplyHistoryEntry {
	if c == nil {
		return nil
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.configApplyHistory) == 0 {
		return nil
	}

	statusFilter = strings.ToLower(strings.TrimSpace(statusFilter))
	sourceFilter = strings.ToLower(strings.TrimSpace(sourceFilter))

	if limit <= 0 || limit > len(c.configApplyHistory) {
		limit = len(c.configApplyHistory)
	}

	result := make([]runtimeConfigApplyHistoryEntry, 0, limit)
	for i := len(c.configApplyHistory) - 1; i >= 0 && len(result) < limit; i-- {
		entry := c.configApplyHistory[i]
		if statusFilter != "" && !strings.EqualFold(entry.Status, statusFilter) {
			continue
		}
		if sourceFilter != "" && !strings.EqualFold(entry.Source, sourceFilter) {
			continue
		}
		result = append(result, entry)
	}
	return result
}

func (c *runtimeStatusContext) getPreviousConfigRevision() (runtimeConfigRevision, bool) {
	if c == nil {
		return runtimeConfigRevision{}, false
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	if len(c.configRevisions) < 2 {
		return runtimeConfigRevision{}, false
	}

	currentHash := configSHA256(c.configOriginal)
	currentIndex := -1
	for i := len(c.configRevisions) - 1; i >= 0; i-- {
		if c.configRevisions[i].ConfigHash == currentHash {
			currentIndex = i
			break
		}
	}
	if currentIndex == -1 {
		currentIndex = len(c.configRevisions)
	}

	for i := currentIndex - 1; i >= 0; i-- {
		if c.configRevisions[i].ConfigHash != currentHash {
			return c.configRevisions[i], true
		}
	}

	return runtimeConfigRevision{}, false
}

func (c *runtimeStatusContext) getConfigRevisionByHash(configHash string) (runtimeConfigRevision, bool) {
	if c == nil {
		return runtimeConfigRevision{}, false
	}

	normalizedHash := strings.ToLower(strings.TrimSpace(configHash))
	if normalizedHash == "" {
		return runtimeConfigRevision{}, false
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	for i := len(c.configRevisions) - 1; i >= 0; i-- {
		if c.configRevisions[i].ConfigHash == normalizedHash {
			return c.configRevisions[i], true
		}
	}

	return runtimeConfigRevision{}, false
}

func (c *runtimeStatusContext) getConfigRevisions(limit int) []runtimeConfigRevisionSummary {
	if c == nil {
		return nil
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	if len(c.configRevisions) == 0 {
		return nil
	}
	if limit <= 0 || limit > len(c.configRevisions) {
		limit = len(c.configRevisions)
	}

	currentHash := configSHA256(c.configOriginal)
	seen := make(map[string]struct{}, len(c.configRevisions))
	result := make([]runtimeConfigRevisionSummary, 0, limit)
	for i := len(c.configRevisions) - 1; i >= 0 && len(result) < limit; i-- {
		revision := c.configRevisions[i]
		if _, exists := seen[revision.ConfigHash]; exists {
			continue
		}
		seen[revision.ConfigHash] = struct{}{}

		appliedAt := ""
		if !revision.AppliedAt.IsZero() {
			appliedAt = formatAPITimestamp(revision.AppliedAt)
		}

		result = append(result, runtimeConfigRevisionSummary{
			ConfigHash: revision.ConfigHash,
			Source:     revision.Source,
			AppliedAt:  appliedAt,
			IsCurrent:  revision.ConfigHash == currentHash,
		})
	}

	return result
}

func (c *runtimeStatusContext) pruneConfigRevisions(keep int) (before, after int, currentHash string) {
	if c == nil {
		return 0, 0, ""
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	before = len(c.configRevisions)
	currentHash = configSHA256(c.configOriginal)
	compact := computePrunedConfigRevisions(c.configRevisions, currentHash, keep)
	c.configRevisions = compact
	return before, len(c.configRevisions), currentHash
}

func (c *runtimeStatusContext) previewConfigRevisionsPrune(keep int) (before, after int, currentHash string) {
	if c == nil {
		return 0, 0, ""
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	before = len(c.configRevisions)
	currentHash = configSHA256(c.configOriginal)
	compact := computePrunedConfigRevisions(c.configRevisions, currentHash, keep)
	return before, len(compact), currentHash
}

func computePrunedConfigRevisions(
	revisions []runtimeConfigRevision,
	currentHash string,
	keep int,
) []runtimeConfigRevision {
	if len(revisions) == 0 {
		return nil
	}
	if keep < 1 {
		keep = 1
	}
	if keep > maxConfigRevisionEntries {
		keep = maxConfigRevisionEntries
	}

	latestIndexByHash := make(map[string]int, len(revisions))
	uniqueNewest := make([]string, 0, len(revisions))
	seen := make(map[string]struct{}, len(revisions))
	for i := len(revisions) - 1; i >= 0; i-- {
		hash := revisions[i].ConfigHash
		if _, ok := latestIndexByHash[hash]; !ok {
			latestIndexByHash[hash] = i
		}
		if _, ok := seen[hash]; ok {
			continue
		}
		seen[hash] = struct{}{}
		uniqueNewest = append(uniqueNewest, hash)
	}

	keepSet := make(map[string]struct{}, keep+1)
	for i := 0; i < len(uniqueNewest) && i < keep; i++ {
		keepSet[uniqueNewest[i]] = struct{}{}
	}
	// Always keep current active revision if it exists in revision history.
	if _, ok := latestIndexByHash[currentHash]; ok {
		keepSet[currentHash] = struct{}{}
	}

	compact := make([]runtimeConfigRevision, 0, len(keepSet))
	for i := 0; i < len(revisions); i++ {
		revision := revisions[i]
		if _, ok := keepSet[revision.ConfigHash]; !ok {
			continue
		}
		if latestIndexByHash[revision.ConfigHash] != i {
			continue
		}
		compact = append(compact, revision)
	}
	return compact
}

func (e *runtimeInhibitionEngine) getRules() []inhibition.InhibitionRule {
	if e == nil {
		return nil
	}

	e.mu.RLock()
	defer e.mu.RUnlock()
	return append([]inhibition.InhibitionRule(nil), e.rules...)
}

func (e *runtimeInhibitionEngine) setRules(rules []inhibition.InhibitionRule) {
	if e == nil {
		return
	}

	e.mu.Lock()
	e.rules = append([]inhibition.InhibitionRule(nil), rules...)
	e.mu.Unlock()
}

func (c *runtimeReceiverCatalog) getConfigured() []string {
	if c == nil {
		return nil
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]string(nil), c.configured...)
}

func (c *runtimeReceiverCatalog) setConfigured(configured []string) {
	if c == nil {
		return
	}

	c.mu.Lock()
	c.configured = append([]string(nil), configured...)
	c.mu.Unlock()
}

func (c *runtimeAlertGroupByCatalog) getGroupBy() ([]string, bool) {
	if c == nil {
		return nil, false
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	groupBy := append([]string(nil), c.groupBy...)
	return groupBy, c.configured
}

func (c *runtimeAlertGroupByCatalog) setGroupBy(groupBy []string, configured bool) {
	if c == nil {
		return
	}

	c.mu.Lock()
	c.groupBy = append([]string(nil), groupBy...)
	c.configured = configured
	c.mu.Unlock()
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
	statusCtx *runtimeStatusContext,
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

func alertmanagerReloadHandler(
	configPath string,
	statusCtx *runtimeStatusContext,
	inhibitions *runtimeInhibitionEngine,
	receivers *runtimeReceiverCatalog,
	groupBy *runtimeAlertGroupByCatalog,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		err := applyRuntimeConfigReload(configPath, statusCtx, inhibitions, receivers, groupBy)
		if err != nil {
			statusCtx.setConfigApplyResult("reload", err)
			http.Error(w, fmt.Sprintf("failed to reload config: %s", err), http.StatusInternalServerError)
			return
		}
		statusCtx.setConfigApplyResult("reload", nil)

		w.WriteHeader(http.StatusOK)
	}
}

func debugCompatHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	subpath := strings.TrimPrefix(r.URL.Path, "/debug/")
	r.URL.Path = path.Join("/debug", subpath)
	if strings.HasSuffix(subpath, "/") && !strings.HasSuffix(r.URL.Path, "/") {
		r.URL.Path += "/"
	}
	http.DefaultServeMux.ServeHTTP(w, r)
}

func registerUpstreamStaticCompatRoutes(mux *http.ServeMux, staticSub fs.FS) {
	fileServer := http.FileServer(http.FS(staticSub))

	// Alertmanager-compatible static entrypoint aliases.
	mux.HandleFunc("/script.js", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		req := r.Clone(r.Context())
		req.URL.Path = "/js/realtime-client.js"
		fileServer.ServeHTTP(w, req)
	})

	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		req := r.Clone(r.Context())
		req.URL.Path = "/favicon.ico"
		fileServer.ServeHTTP(w, req)
	})

	mux.HandleFunc("/lib/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		subpath := strings.TrimPrefix(r.URL.Path, "/lib/")
		req := r.Clone(r.Context())
		req.URL.Path = path.Join("/lib", subpath)
		if strings.HasSuffix(subpath, "/") && !strings.HasSuffix(req.URL.Path, "/") {
			req.URL.Path += "/"
		}
		fileServer.ServeHTTP(w, req)
	})
}

// API handlers
func alertsV1Handler(store *alertStore, silences *silenceStore, inhibitions *runtimeInhibitionEngine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		handleAlertsPost(store, silences, inhibitions, w, r)
	}
}

func alertsHandler(store *alertStore, silences *silenceStore, inhibitions *runtimeInhibitionEngine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handleAlertsGet(store, silences, inhibitions, w, r)
		case http.MethodPost:
			handleAlertsPost(store, silences, inhibitions, w, r)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func handleAlertsGet(
	store *alertStore,
	silences *silenceStore,
	inhibitions *runtimeInhibitionEngine,
	w http.ResponseWriter,
	r *http.Request,
) {
	status := parseAlertsStatusQuery(r.URL.Query().Get("status"))
	includeResolved := parseBoolQueryLenient(r.URL.Query().Get("resolved"), false)
	if status == "resolved" {
		includeResolved = true
	}

	stateFilters, err := parseAlertsStateFilters(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": err.Error(),
		})
		return
	}

	receiverRegex, err := parseRegexQuery(r.URL.Query().Get("receiver"))
	if err != nil {
		writeJSONString(w, http.StatusBadRequest, err.Error())
		return
	}
	labelMatchers, err := parseAlertLabelMatchers(r)
	if err != nil {
		writeJSONString(w, http.StatusBadRequest, err.Error())
		return
	}

	now := time.Now().UTC()
	allAlerts := store.list("", true)
	inhibitedByIndex := buildAlertInhibitedByIndex(inhibitions, allAlerts)
	alerts := store.list(status, includeResolved)
	if receiverRegex != nil {
		filtered := make([]apiAlert, 0, len(alerts))
		for _, alert := range alerts {
			if receiverRegex.MatchString(alertReceiverName(alert)) {
				filtered = append(filtered, alert)
			}
		}
		alerts = filtered
	}
	alerts = filterAlertsByLabelMatchers(alerts, labelMatchers)
	alerts = filterAlertsByStateFilters(alerts, stateFilters, silences, now, inhibitedByIndex)

	writeJSON(w, http.StatusOK, toGettableAlerts(alerts, silences, now, inhibitedByIndex))
}

func handleAlertsPost(
	store *alertStore,
	silences *silenceStore,
	_ *runtimeInhibitionEngine,
	w http.ResponseWriter,
	r *http.Request,
) {
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
		var apiErr *alertAPIError
		if errors.As(err, &apiErr) {
			writeJSON(w, apiErr.status, apiErr.payload)
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": err.Error(),
		})
		return
	}
	if err := validateAlertIngestInputs(payload); err != nil {
		var apiErr *alertAPIError
		if errors.As(err, &apiErr) {
			writeJSON(w, apiErr.status, apiErr.payload)
			return
		}
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
			labelMatchers, err := parseAlertLabelMatchers(r)
			if err != nil {
				writeJSONString(w, http.StatusBadRequest, err.Error())
				return
			}

			silences := store.list(time.Now().UTC())
			silences = filterSilencesByLabelMatchers(silences, labelMatchers)
			writeJSON(w, http.StatusOK, silences)
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
		writeJSONString(w, http.StatusRequestEntityTooLarge, "request payload too large")
		return
	}

	payload, err := parseSilencePayload(body)
	if err != nil {
		var apiErr *silenceAPIError
		if errors.As(err, &apiErr) {
			writeJSON(w, apiErr.status, apiErr.payload)
			return
		}
		writeJSONString(w, http.StatusBadRequest, err.Error())
		return
	}

	silenceID, err := store.createOrUpdate(payload, time.Now().UTC())
	if err != nil {
		var apiErr *silenceAPIError
		if errors.As(err, &apiErr) {
			writeJSON(w, apiErr.status, apiErr.payload)
			return
		}
		if errors.Is(err, errSilenceNotFound) {
			writeJSONString(w, http.StatusNotFound, err.Error())
			return
		}

		writeJSONString(w, http.StatusBadRequest, err.Error())
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
			writeJSON(w, http.StatusNotFound, map[string]any{
				"code":    http.StatusNotFound,
				"message": fmt.Sprintf("path %s was not found", r.URL.Path),
			})
			return
		}

		switch r.Method {
		case http.MethodGet:
			if _, err := uuid.Parse(id); err != nil {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
					"code":    601,
					"message": fmt.Sprintf("silenceID in path must be of type uuid: %q", id),
				})
				return
			}
			silence, ok := store.get(id, time.Now().UTC())
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			writeJSON(w, http.StatusOK, silence)
		case http.MethodDelete:
			if _, err := uuid.Parse(id); err != nil {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
					"code":    601,
					"message": fmt.Sprintf("silenceID in path must be of type uuid: %q", id),
				})
				return
			}
			if !store.delete(id) {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func receiversHandler(catalog *runtimeReceiverCatalog) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		configured := []string{}
		if catalog != nil {
			configured = catalog.getConfigured()
		}

		seen := make(map[string]struct{}, len(configured))
		receivers := make([]apiReceiver, 0, len(configured))
		for _, name := range configured {
			trimmed := strings.TrimSpace(name)
			if trimmed == "" {
				continue
			}
			if _, exists := seen[trimmed]; exists {
				continue
			}
			seen[trimmed] = struct{}{}
			receivers = append(receivers, apiReceiver{Name: trimmed})
		}

		writeJSON(w, http.StatusOK, receivers)
	}
}

func alertGroupsHandler(
	store *alertStore,
	silences *silenceStore,
	inhibitions *runtimeInhibitionEngine,
	groupByCatalog *runtimeAlertGroupByCatalog,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		includeResolved := parseBoolQueryLenient(r.URL.Query().Get("resolved"), false)

		receiverRegex, err := parseRegexQuery(r.URL.Query().Get("receiver"))
		if err != nil {
			writeJSONString(w, http.StatusBadRequest, err.Error())
			return
		}
		labelMatchers, err := parseAlertLabelMatchers(r)
		if err != nil {
			writeJSONString(w, http.StatusBadRequest, err.Error())
			return
		}

		stateFilters, err := parseAlertGroupStateFilters(r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": err.Error(),
			})
			return
		}

		now := time.Now().UTC()
		allAlerts := store.list("", true)
		inhibitedByIndex := buildAlertInhibitedByIndex(inhibitions, allAlerts)
		alerts := store.list("", includeResolved)
		alerts = filterAlertsByLabelMatchers(alerts, labelMatchers)
		alerts = filterAlertsByGroupStateFilters(alerts, stateFilters, silences, now, inhibitedByIndex)
		groupBy := resolveAlertGroupBy(groupByCatalog)

		groupsMap := make(map[string]*apiAlertGroup)
		for _, alert := range alerts {
			groupLabels := buildAlertGroupLabels(alert, groupBy)
			receiver := alertReceiverName(alert)
			if receiverRegex != nil && !receiverRegex.MatchString(receiver) {
				continue
			}

			key := alertGroupKey(groupLabels, receiver)
			group, ok := groupsMap[key]
			if !ok {
				group = &apiAlertGroup{
					Labels:   cloneStringMap(groupLabels),
					Receiver: apiReceiver{Name: receiver},
					Alerts:   make([]apiAlert, 0, 1),
				}
				groupsMap[key] = group
			}
			group.Alerts = append(group.Alerts, alert)
		}

		groups := make([]apiAlertGroup, 0, len(groupsMap))
		for _, group := range groupsMap {
			if !stateFilters.muted && isMutedAlertGroup(group, silences, now, inhibitedByIndex) {
				continue
			}
			groups = append(groups, *group)
		}
		sort.Slice(groups, func(i, j int) bool {
			return alertGroupSortKey(groups[i]) < alertGroupSortKey(groups[j])
		})

		responseGroups := make([]apiGettableAlertGroup, 0, len(groups))
		for _, group := range groups {
			responseGroups = append(responseGroups, apiGettableAlertGroup{
				Labels:   cloneStringMap(group.Labels),
				Receiver: group.Receiver,
				Alerts:   toGettableAlerts(group.Alerts, silences, now, inhibitedByIndex),
			})
		}

		writeJSON(w, http.StatusOK, responseGroups)
	}
}

var legacyAlertGroupBy = []string{"alertname", "service", "namespace"}

func resolveAlertGroupBy(catalog *runtimeAlertGroupByCatalog) []string {
	if catalog == nil {
		return append([]string(nil), legacyAlertGroupBy...)
	}

	groupBy, configured := catalog.getGroupBy()
	if !configured {
		return append([]string(nil), legacyAlertGroupBy...)
	}
	return groupBy
}

func buildAlertGroupLabels(alert apiAlert, groupBy []string) map[string]string {
	if len(groupBy) == 0 {
		return map[string]string{}
	}
	if len(groupBy) == 1 && groupBy[0] == "..." {
		labels := cloneStringMap(alert.Labels)
		if labels == nil {
			return map[string]string{}
		}
		return labels
	}

	labels := make(map[string]string, len(groupBy))
	for _, labelName := range groupBy {
		labels[labelName] = alert.Labels[labelName]
	}
	return labels
}

func alertGroupKey(labels map[string]string, receiver string) string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys)+1)
	parts = append(parts, receiver)
	for _, key := range keys {
		parts = append(parts, key+"="+labels[key])
	}
	return strings.Join(parts, "|")
}

func alertGroupSortKey(group apiAlertGroup) string {
	return alertGroupKey(group.Labels, group.Receiver.Name)
}

func statusHandler(
	alertStore *alertStore,
	silenceStore *silenceStore,
	statusCtx *runtimeStatusContext,
	clusterCtx *runtimeClusterContext,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		now := time.Now().UTC()
		alertTotal, alertFiring, alertResolved := alertStore.stats()
		silenceTotal, silenceActive, silencePending, silenceExpired := silenceStore.stats(now)

		cluster := buildRuntimeClusterStatusPayload(clusterCtx, now)

		writeJSON(w, http.StatusOK, map[string]any{
			"cluster": cluster,
			"versionInfo": map[string]string{
				"version":   appVersion,
				"revision":  buildRevision,
				"branch":    buildBranch,
				"buildUser": buildUser,
				"buildDate": buildDate,
				"goVersion": runtime.Version(),
			},
			"config": map[string]string{
				"original": statusCtx.getConfigOriginal(),
			},
			"uptime": formatAPITimestamp(statusCtx.startedAt),
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

func configHandler(
	configPath string,
	statusCtx *runtimeStatusContext,
	inhibitions *runtimeInhibitionEngine,
	receivers *runtimeReceiverCatalog,
	groupBy *runtimeAlertGroupByCatalog,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
			if format == "" {
				format = "json"
			}

			switch format {
			case "json":
				writeJSON(w, http.StatusOK, map[string]string{
					"original": runtimeConfigOriginalSnapshot(statusCtx, configPath),
				})
			case "yaml":
				original := runtimeConfigOriginalSnapshot(statusCtx, configPath)
				if strings.TrimSpace(original) == "" {
					original = "{}\n"
				} else if !strings.HasSuffix(original, "\n") {
					original += "\n"
				}

				w.Header().Set("Content-Type", "application/yaml")
				w.WriteHeader(http.StatusOK)
				if _, err := io.WriteString(w, original); err != nil {
					slog.Error("Failed to write config response", "error", err)
				}
			default:
				writeJSON(w, http.StatusBadRequest, map[string]string{
					"error": "invalid format query value",
				})
			}
		case http.MethodPost:
			body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 10*1024*1024))
			if err != nil {
				writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{
					"error": "request payload too large",
				})
				return
			}
			defer r.Body.Close()

			if strings.TrimSpace(string(body)) == "" {
				writeJSON(w, http.StatusBadRequest, map[string]string{
					"error": "config payload is required",
				})
				return
			}

			if err := validateRuntimeConfigPayload(body); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{
					"error": fmt.Sprintf("invalid config payload: %s", err),
				})
				return
			}

			previousConfig, err := readRuntimeConfigOriginalForReload(configPath)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{
					"error": fmt.Sprintf("failed to read existing config: %s", err),
				})
				return
			}

			if err := writeRuntimeConfig(configPath, body); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{
					"error": fmt.Sprintf("failed to persist config: %s", err),
				})
				return
			}

			if err := applyRuntimeConfigReload(configPath, statusCtx, inhibitions, receivers, groupBy); err != nil {
				// Best-effort rollback to keep runtime consistent if apply fails after write.
				if rollbackErr := writeRuntimeConfig(configPath, []byte(previousConfig)); rollbackErr == nil {
					_ = applyRuntimeConfigReload(configPath, statusCtx, inhibitions, receivers, groupBy)
				}
				statusCtx.setConfigApplyResult("api", err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{
					"error": fmt.Sprintf("failed to apply config: %s", err),
				})
				return
			}
			statusCtx.setConfigApplyResult("api", nil)

			writeJSON(w, http.StatusOK, map[string]string{
				"status": "applied",
			})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func configStatusHandler(
	configPath string,
	statusCtx *runtimeStatusContext,
	inhibitions *runtimeInhibitionEngine,
	receivers *runtimeReceiverCatalog,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		status, source, errorText, appliedAt := statusCtx.getConfigApplyResult()
		appliedAtStr := ""
		if !appliedAt.IsZero() {
			appliedAtStr = formatAPITimestamp(appliedAt)
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"status":              status,
			"source":              source,
			"appliedAt":           appliedAtStr,
			"error":               errorText,
			"configPath":          configPath,
			"inhibitionRuleCount": runtimeInhibitionRuleCount(inhibitions),
			"receiverCount":       runtimeReceiverCount(receivers),
		})
	}
}

func configHistoryHandler(configPath string, statusCtx *runtimeStatusContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		limit, err := parsePositiveIntQuery(r.URL.Query().Get("limit"), 20, 1, maxConfigApplyHistoryEntries)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": err.Error(),
			})
			return
		}

		statusFilter, err := parseConfigApplyStatusQuery(r.URL.Query().Get("status"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": err.Error(),
			})
			return
		}

		sourceFilter := parseConfigApplySourceQuery(r.URL.Query().Get("source"))
		entries := statusCtx.getConfigApplyHistory(limit, statusFilter, sourceFilter)
		writeJSON(w, http.StatusOK, map[string]any{
			"total":      len(entries),
			"limit":      limit,
			"status":     statusFilter,
			"source":     sourceFilter,
			"configPath": configPath,
			"entries":    entries,
		})
	}
}

func configRevisionsHandler(configPath string, statusCtx *runtimeStatusContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if statusCtx == nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error": "runtime status context is unavailable",
			})
			return
		}

		limit, err := parsePositiveIntQuery(r.URL.Query().Get("limit"), 20, 1, maxConfigRevisionEntries)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": err.Error(),
			})
			return
		}

		revisions := statusCtx.getConfigRevisions(limit)
		writeJSON(w, http.StatusOK, map[string]any{
			"total":             len(revisions),
			"limit":             limit,
			"currentConfigHash": configSHA256(statusCtx.getConfigOriginal()),
			"configPath":        configPath,
			"revisions":         revisions,
		})
	}
}

func configRevisionsPruneHandler(configPath string, statusCtx *runtimeStatusContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if statusCtx == nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error": "runtime status context is unavailable",
			})
			return
		}

		keep, err := parsePositiveIntQuery(r.URL.Query().Get("keep"), 20, 1, maxConfigRevisionEntries)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": err.Error(),
			})
			return
		}

		dryRun, err := parseBoolQuery(r.URL.Query().Get("dryRun"), false)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": err.Error(),
			})
			return
		}

		var before, after int
		var currentHash string
		status := "pruned"
		if dryRun {
			before, after, currentHash = statusCtx.previewConfigRevisionsPrune(keep)
			status = "dry_run"
		} else {
			before, after, currentHash = statusCtx.pruneConfigRevisions(keep)
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"status":            status,
			"action":            "prune_revisions",
			"dryRun":            dryRun,
			"keep":              keep,
			"before":            before,
			"after":             after,
			"removed":           before - after,
			"currentConfigHash": currentHash,
			"configPath":        configPath,
		})
	}
}

func configRollbackHandler(
	configPath string,
	statusCtx *runtimeStatusContext,
	inhibitions *runtimeInhibitionEngine,
	receivers *runtimeReceiverCatalog,
	groupBy *runtimeAlertGroupByCatalog,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if statusCtx == nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error": "runtime status context is unavailable",
			})
			return
		}

		currentConfig, err := readRuntimeConfigOriginalForReload(configPath)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error": fmt.Sprintf("failed to read current config: %s", err),
			})
			return
		}
		fromConfigHash := configSHA256(currentConfig)

		targetConfigHash, err := parseConfigHashQuery(r.URL.Query().Get("configHash"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": err.Error(),
			})
			return
		}

		dryRun, err := parseBoolQuery(r.URL.Query().Get("dryRun"), false)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": err.Error(),
			})
			return
		}

		var targetRevision runtimeConfigRevision
		if targetConfigHash == "" {
			var ok bool
			targetRevision, ok = statusCtx.getPreviousConfigRevision()
			if !ok {
				writeJSON(w, http.StatusConflict, map[string]string{
					"error": "no previous config revision available for rollback",
				})
				return
			}
		} else {
			if targetConfigHash == fromConfigHash {
				writeJSON(w, http.StatusConflict, map[string]string{
					"error": "requested config revision is already active",
				})
				return
			}

			var ok bool
			targetRevision, ok = statusCtx.getConfigRevisionByHash(targetConfigHash)
			if !ok {
				writeJSON(w, http.StatusNotFound, map[string]string{
					"error": "config revision not found",
				})
				return
			}
		}

		targetAppliedAt := ""
		if !targetRevision.AppliedAt.IsZero() {
			targetAppliedAt = formatAPITimestamp(targetRevision.AppliedAt)
		}
		if dryRun {
			writeJSON(w, http.StatusOK, map[string]any{
				"status":          "dry_run",
				"action":          "rollback",
				"dryRun":          true,
				"fromConfigHash":  fromConfigHash,
				"toConfigHash":    targetRevision.ConfigHash,
				"targetSource":    targetRevision.Source,
				"targetAppliedAt": targetAppliedAt,
				"configPath":      configPath,
			})
			return
		}

		if err := writeRuntimeConfig(configPath, []byte(targetRevision.ConfigContent)); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error": fmt.Sprintf("failed to persist rollback config: %s", err),
			})
			return
		}

		if err := applyRuntimeConfigReload(configPath, statusCtx, inhibitions, receivers, groupBy); err != nil {
			// Best-effort rollback of rollback to keep runtime consistent.
			if rollbackErr := writeRuntimeConfig(configPath, []byte(currentConfig)); rollbackErr == nil {
				_ = applyRuntimeConfigReload(configPath, statusCtx, inhibitions, receivers, groupBy)
			}
			statusCtx.setConfigApplyResult("rollback", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error": fmt.Sprintf("failed to apply rollback config: %s", err),
			})
			return
		}
		statusCtx.setConfigApplyResult("rollback", nil)

		writeJSON(w, http.StatusOK, map[string]any{
			"status":          "rolled_back",
			"action":          "rollback",
			"dryRun":          false,
			"fromConfigHash":  fromConfigHash,
			"toConfigHash":    targetRevision.ConfigHash,
			"targetSource":    targetRevision.Source,
			"targetAppliedAt": targetAppliedAt,
			"configPath":      configPath,
		})
	}
}

func runtimeConfigOriginalSnapshot(statusCtx *runtimeStatusContext, configPath string) string {
	if statusCtx != nil {
		if original := statusCtx.getConfigOriginal(); original != "" {
			return original
		}
	}
	return readRuntimeConfigOriginalAt(configPath)
}

func runtimeInhibitionRuleCount(inhibitions *runtimeInhibitionEngine) int {
	if inhibitions == nil {
		return 0
	}
	return len(inhibitions.getRules())
}

func runtimeReceiverCount(receivers *runtimeReceiverCatalog) int {
	if receivers == nil {
		return 0
	}
	return len(receivers.getConfigured())
}

func configSHA256(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func applyRuntimeConfigReload(
	configPath string,
	statusCtx *runtimeStatusContext,
	inhibitions *runtimeInhibitionEngine,
	receivers *runtimeReceiverCatalog,
	groupBy *runtimeAlertGroupByCatalog,
) error {
	reloadedRules, err := parseRuntimeInhibitionRules(configPath)
	if err != nil {
		return err
	}
	reloadedReceivers, err := parseRuntimeConfiguredReceivers(configPath)
	if err != nil {
		return err
	}
	reloadedGroupBy, groupByConfigured, err := parseRuntimeAlertGroupBy(configPath)
	if err != nil {
		return err
	}
	configOriginal, err := readRuntimeConfigOriginalForReload(configPath)
	if err != nil {
		return err
	}

	if inhibitions != nil {
		inhibitions.setRules(reloadedRules)
	}
	if receivers != nil {
		receivers.setConfigured(reloadedReceivers)
	}
	if groupBy != nil {
		groupBy.setGroupBy(reloadedGroupBy, groupByConfigured)
	}
	if statusCtx != nil {
		statusCtx.setConfigOriginal(configOriginal)
	}

	return nil
}

func validateRuntimeConfigPayload(content []byte) error {
	if _, err := parseRuntimeInhibitionRulesFromData(content); err != nil {
		return err
	}
	if _, err := parseRuntimeConfiguredReceiversFromData(content); err != nil {
		return err
	}
	return nil
}

func writeRuntimeConfig(configPath string, content []byte) error {
	configDir := filepath.Dir(configPath)
	if configDir == "" {
		configDir = "."
	}
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return err
	}

	tempFile, err := os.CreateTemp(configDir, filepath.Base(configPath)+".tmp-*")
	if err != nil {
		return err
	}
	tempPath := tempFile.Name()

	cleanup := func() {
		_ = tempFile.Close()
		_ = os.Remove(tempPath)
	}

	if _, err := tempFile.Write(content); err != nil {
		cleanup()
		return err
	}
	if err := tempFile.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tempFile.Close(); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	if err := os.Rename(tempPath, configPath); err != nil {
		_ = os.Remove(tempPath)
		return err
	}

	return nil
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

	includeResolved, err := parseBoolQuery(r.URL.Query().Get("resolved"), defaultIncludeResolved)
	if err != nil {
		return "", false, err
	}
	if status == "resolved" {
		includeResolved = true
	}

	return status, includeResolved, nil
}

func parseBoolQuery(raw string, def bool) (bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def, nil
	}

	v, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("invalid boolean query value")
	}
	return v, nil
}

func parseRegexQuery(raw string) (*regexp.Regexp, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	// Upstream compiles receiver regex as full-match: ^(?:<query>)$
	re, err := regexp.Compile("^(?:" + raw + ")$")
	if err != nil {
		return nil, fmt.Errorf("failed to parse receiver param: %v", err)
	}
	return re, nil
}

func parseConfigHashQuery(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		return "", nil
	}
	if len(value) != sha256.Size*2 {
		return "", fmt.Errorf("invalid configHash query value")
	}

	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		return "", fmt.Errorf("invalid configHash query value")
	}

	return value, nil
}

func parseConfigApplyStatusQuery(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch value {
	case "", "ok", "failed":
		return value, nil
	default:
		return "", fmt.Errorf("invalid status query value")
	}
}

func parseConfigApplySourceQuery(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func parseAlertLabelMatchers(r *http.Request) ([]*cfgmatcher.Matcher, error) {
	rawMatchers := r.URL.Query()["filter"]
	if len(rawMatchers) == 0 {
		return nil, nil
	}

	parsed := make([]*cfgmatcher.Matcher, 0, len(rawMatchers))
	for _, raw := range rawMatchers {
		trimmed := strings.TrimSpace(raw)
		m, err := cfgmatcher.Parse(trimmed)
		if err != nil {
			return nil, fmt.Errorf("bad matcher format: %s", trimmed)
		}

		value, err := parseAlertLabelMatcherValue(m.Value)
		if err != nil {
			return nil, err
		}
		m.Value = value

		if m.IsRegex() {
			// Upstream compiles label regex as full match.
			re, err := regexp.Compile("^(?:" + value + ")$")
			if err != nil {
				return nil, err
			}
			m.CompiledRegex = re
		}

		parsed = append(parsed, m)
	}
	return parsed, nil
}

func parseAlertLabelMatcherValue(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", fmt.Errorf("empty matcher value")
	}
	if len(value) < 2 {
		return value, nil
	}

	first := value[0]
	last := value[len(value)-1]
	if first == '"' && last != '"' {
		return "", fmt.Errorf("matcher value contains unescaped double quote: %s", value)
	}
	if first != '"' && first != '\'' {
		return value, nil
	}
	if first != last {
		return "", fmt.Errorf("invalid quoted matcher value")
	}

	if first == '"' {
		unquoted, err := strconv.Unquote(value)
		if err != nil {
			return "", fmt.Errorf("invalid quoted matcher value")
		}
		return unquoted, nil
	}

	// Support single-quoted API values.
	return strings.ReplaceAll(value[1:len(value)-1], `\'`, `'`), nil
}

type alertsStateFilters struct {
	active      bool
	silenced    bool
	inhibited   bool
	unprocessed bool
}

type alertGroupStateFilters struct {
	active    bool
	silenced  bool
	inhibited bool
	muted     bool
}

func parseAlertsStatusQuery(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "firing":
		return "firing"
	case "resolved":
		return "resolved"
	default:
		// Upstream ignores unknown values for non-standard status query.
		return ""
	}
}

func parseAlertsStateFilters(r *http.Request) (alertsStateFilters, error) {
	active := parseBoolQueryLenient(r.URL.Query().Get("active"), true)
	silenced := parseBoolQueryLenient(r.URL.Query().Get("silenced"), true)
	inhibited := parseBoolQueryLenient(r.URL.Query().Get("inhibited"), true)
	unprocessed := parseBoolQueryLenient(r.URL.Query().Get("unprocessed"), true)

	return alertsStateFilters{
		active:      active,
		silenced:    silenced,
		inhibited:   inhibited,
		unprocessed: unprocessed,
	}, nil
}

func filterAlertsByStateFilters(
	in []apiAlert,
	f alertsStateFilters,
	silences *silenceStore,
	now time.Time,
	inhibitedByIndex map[string][]string,
) []apiAlert {
	if len(in) == 0 {
		return in
	}

	out := make([]apiAlert, 0, len(in))
	for i := range in {
		alert := in[i]

		state := alertRuntimeStateForFilters(alert, silences, now, inhibitedByIndex)
		if !f.active && state.active {
			continue
		}
		if !f.silenced && state.silenced {
			continue
		}
		if !f.inhibited && state.inhibited {
			continue
		}
		if !f.unprocessed && state.unprocessed {
			continue
		}

		out = append(out, alert)
	}

	return out
}

func toGettableAlerts(
	in []apiAlert,
	silences *silenceStore,
	now time.Time,
	inhibitedByIndex map[string][]string,
) []apiGettableAlert {
	if len(in) == 0 {
		return []apiGettableAlert{}
	}

	out := make([]apiGettableAlert, 0, len(in))
	for i := range in {
		out = append(out, toGettableAlert(in[i], silences, now, inhibitedByIndex))
	}
	return out
}

func toGettableAlert(
	alert apiAlert,
	silences *silenceStore,
	now time.Time,
	inhibitedByIndex map[string][]string,
) apiGettableAlert {
	silencedBy := make([]string, 0)
	inhibitedBy := copyStringSlice(inhibitedByForAlert(alert, inhibitedByIndex))

	if alert.Status == "firing" && silences != nil {
		silencedBy = silences.activeMatchingSilenceIDs(alert.Labels, now)
	}

	mutedBy := mergeUniqueStringSlices(silencedBy, inhibitedBy)

	state := "unprocessed"
	if alert.Status == "firing" {
		if len(mutedBy) > 0 {
			state = "suppressed"
		} else {
			state = "active"
		}
	}

	endsAt := alert.UpdatedAt
	if alert.EndsAt != nil && strings.TrimSpace(*alert.EndsAt) != "" {
		endsAt = *alert.EndsAt
	}

	annotations := cloneStringMap(alert.Annotations)
	if annotations == nil {
		annotations = map[string]string{}
	}

	return apiGettableAlert{
		Labels:       cloneStringMap(alert.Labels),
		Annotations:  annotations,
		Receivers:    append([]apiReceiver(nil), alert.Receivers...),
		StartsAt:     alert.StartsAt,
		UpdatedAt:    alert.UpdatedAt,
		EndsAt:       endsAt,
		GeneratorURL: alert.GeneratorURL,
		Fingerprint:  alert.Fingerprint,
		Status: apiAlertStatus{
			State:       state,
			SilencedBy:  copyStringSlice(silencedBy),
			InhibitedBy: copyStringSlice(inhibitedBy),
			MutedBy:     copyStringSlice(mutedBy),
		},
	}
}

func filterAlertsByLabelMatchers(in []apiAlert, matchers []*cfgmatcher.Matcher) []apiAlert {
	if len(in) == 0 || len(matchers) == 0 {
		return in
	}

	out := make([]apiAlert, 0, len(in))
	for i := range in {
		if alertMatchesLabelMatchers(in[i], matchers) {
			out = append(out, in[i])
		}
	}
	return out
}

func filterSilencesByLabelMatchers(in []apiSilence, matchers []*cfgmatcher.Matcher) []apiSilence {
	if len(in) == 0 || len(matchers) == 0 {
		return in
	}

	out := make([]apiSilence, 0, len(in))
	for i := range in {
		if silenceMatchesLabelMatchers(in[i], matchers) {
			out = append(out, in[i])
		}
	}
	return out
}

func alertMatchesLabelMatchers(alert apiAlert, matchers []*cfgmatcher.Matcher) bool {
	for _, matcher := range matchers {
		labelValue, labelExists := alert.Labels[matcher.Label]

		switch matcher.Type {
		case cfgmatcher.MatchEqual:
			if !labelExists || labelValue != matcher.Value {
				return false
			}
		case cfgmatcher.MatchNotEqual:
			if labelExists && labelValue == matcher.Value {
				return false
			}
		case cfgmatcher.MatchRegexp:
			if !labelExists || matcher.CompiledRegex == nil || !matcher.CompiledRegex.MatchString(labelValue) {
				return false
			}
		case cfgmatcher.MatchNotRegexp:
			if labelExists && matcher.CompiledRegex != nil && matcher.CompiledRegex.MatchString(labelValue) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func silenceMatchesLabelMatchers(silence apiSilence, matchers []*cfgmatcher.Matcher) bool {
	for _, filterMatcher := range matchers {
		matched := false
		for _, silenceMatcher := range silence.Matchers {
			if !silenceMatcherMatchesFilter(silenceMatcher, filterMatcher) {
				continue
			}
			matched = true
			break
		}
		if !matched {
			return false
		}
	}
	return true
}

func silenceMatcherMatchesFilter(silenceMatcher apiSilenceMatcher, filterMatcher *cfgmatcher.Matcher) bool {
	if silenceMatcher.Name != filterMatcher.Label {
		return false
	}
	if silenceMatcherType(silenceMatcher) != filterMatcher.Type {
		return false
	}
	return silenceMatcher.Value == filterMatcher.Value
}

func silenceMatcherType(m apiSilenceMatcher) cfgmatcher.MatcherType {
	if m.IsRegex {
		if m.IsEqual {
			return cfgmatcher.MatchRegexp
		}
		return cfgmatcher.MatchNotRegexp
	}
	if m.IsEqual {
		return cfgmatcher.MatchEqual
	}
	return cfgmatcher.MatchNotEqual
}

func parseAlertGroupStateFilters(r *http.Request) (alertGroupStateFilters, error) {
	active := parseBoolQueryLenient(r.URL.Query().Get("active"), true)
	silenced := parseBoolQueryLenient(r.URL.Query().Get("silenced"), true)
	inhibited := parseBoolQueryLenient(r.URL.Query().Get("inhibited"), true)
	muted := parseBoolQueryLenient(r.URL.Query().Get("muted"), true)

	return alertGroupStateFilters{
		active:    active,
		silenced:  silenced,
		inhibited: inhibited,
		muted:     muted,
	}, nil
}

func parseBoolQueryLenient(raw string, def bool) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def
	}

	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		// Upstream ParseBool usage on explicit query values falls back to false on parse errors.
		return false
	}

	return parsed
}

func filterAlertsByGroupStateFilters(
	in []apiAlert,
	f alertGroupStateFilters,
	silences *silenceStore,
	now time.Time,
	inhibitedByIndex map[string][]string,
) []apiAlert {
	return filterAlertsByStateFilters(in, alertsStateFilters{
		active:      f.active,
		silenced:    f.silenced,
		inhibited:   f.inhibited,
		unprocessed: true,
	}, silences, now, inhibitedByIndex)
}

func isMutedAlertGroup(
	group *apiAlertGroup,
	silences *silenceStore,
	now time.Time,
	inhibitedByIndex map[string][]string,
) bool {
	if group == nil || len(group.Alerts) == 0 {
		return false
	}

	for _, alert := range group.Alerts {
		if !isAlertMuted(alert, silences, now, inhibitedByIndex) {
			return false
		}
	}
	return true
}

type alertRuntimeState struct {
	active      bool
	silenced    bool
	inhibited   bool
	unprocessed bool
}

func alertRuntimeStateForFilters(
	alert apiAlert,
	silences *silenceStore,
	now time.Time,
	inhibitedByIndex map[string][]string,
) alertRuntimeState {
	if alert.Status != "firing" {
		// Resolved snapshots are treated as unprocessed in the compatibility runtime.
		return alertRuntimeState{
			active:      false,
			silenced:    false,
			inhibited:   false,
			unprocessed: true,
		}
	}

	silenced := false
	if silences != nil {
		silenced = silences.hasActiveMatch(alert.Labels, now)
	}

	inhibited := len(inhibitedByForAlert(alert, inhibitedByIndex)) > 0
	unprocessed := false
	active := !silenced && !inhibited

	return alertRuntimeState{
		active:      active,
		silenced:    silenced,
		inhibited:   inhibited,
		unprocessed: unprocessed,
	}
}

func isAlertMuted(
	alert apiAlert,
	silences *silenceStore,
	now time.Time,
	inhibitedByIndex map[string][]string,
) bool {
	state := alertRuntimeStateForFilters(alert, silences, now, inhibitedByIndex)
	return state.silenced || state.inhibited
}

func alertReceiverName(alert apiAlert) string {
	receiver := strings.TrimSpace(alert.Labels["receiver"])
	if receiver == "" {
		return "default"
	}
	return receiver
}

func copyStringSlice(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func mergeUniqueStringSlices(parts ...[]string) []string {
	if len(parts) == 0 {
		return []string{}
	}

	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, part := range parts {
		for _, raw := range part {
			value := strings.TrimSpace(raw)
			if value == "" {
				continue
			}
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			out = append(out, value)
		}
	}
	return out
}

func alertInhibitedByIDs(alert apiAlert) []string {
	if alert.Status != "firing" {
		return []string{}
	}

	rawValues := []string{
		alert.Annotations["inhibitedBy"],
		alert.Annotations["amp_inhibited_by"],
		alert.Annotations["amp.inhibited_by"],
		alert.Labels["inhibitedBy"],
		alert.Labels["amp_inhibited_by"],
		alert.Labels["amp.inhibited_by"],
	}

	for _, raw := range rawValues {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}

		parts := strings.FieldsFunc(raw, func(r rune) bool {
			return r == ',' || r == ';'
		})
		return mergeUniqueStringSlices(parts)
	}

	return []string{}
}

func inhibitedByForAlert(alert apiAlert, index map[string][]string) []string {
	if len(index) == 0 {
		return alertInhibitedByIDs(alert)
	}

	if ids, ok := index[alertIdentityKey(alert)]; ok {
		return ids
	}
	return alertInhibitedByIDs(alert)
}

func alertIdentityKey(alert apiAlert) string {
	return strings.TrimSpace(alert.Fingerprint) + "|" + strings.TrimSpace(alert.StartsAt)
}

func buildAlertInhibitedByIndex(
	engine *runtimeInhibitionEngine,
	allAlerts []apiAlert,
) map[string][]string {
	index := make(map[string][]string, len(allAlerts))
	if len(allAlerts) == 0 {
		return index
	}
	rules := engine.getRules()

	sources := make([]*core.Alert, 0, len(allAlerts))
	for i := range allAlerts {
		metadataIDs := alertInhibitedByIDs(allAlerts[i])
		if len(metadataIDs) > 0 {
			index[alertIdentityKey(allAlerts[i])] = metadataIDs
		}

		if allAlerts[i].Status != "firing" {
			continue
		}
		sources = append(sources, toCoreAlert(allAlerts[i]))
	}

	if len(rules) == 0 || len(sources) == 0 {
		return index
	}

	matcher := inhibition.NewMatcher(nil, rules, nil)
	for i := range allAlerts {
		target := allAlerts[i]
		if target.Status != "firing" {
			continue
		}

		targetCore := toCoreAlert(target)
		acc := copyStringSlice(index[alertIdentityKey(target)])

		for _, source := range sources {
			if source == nil || source.Fingerprint == "" {
				continue
			}
			// Self-inhibition is not supported.
			if source.Fingerprint == targetCore.Fingerprint {
				continue
			}

			for ruleIdx := range rules {
				rule := &rules[ruleIdx]
				if matcher.MatchRule(rule, source, targetCore) {
					acc = append(acc, source.Fingerprint)
					break
				}
			}
		}

		if len(acc) > 0 {
			index[alertIdentityKey(target)] = mergeUniqueStringSlices(acc)
		}
	}

	return index
}

func toCoreAlert(alert apiAlert) *core.Alert {
	startsAt, _ := time.Parse(time.RFC3339, strings.TrimSpace(alert.StartsAt))
	updatedAt, _ := time.Parse(time.RFC3339, strings.TrimSpace(alert.UpdatedAt))
	if startsAt.IsZero() {
		startsAt = updatedAt
	}
	if startsAt.IsZero() {
		startsAt = time.Now().UTC()
	}

	var endsAt *time.Time
	if alert.EndsAt != nil && strings.TrimSpace(*alert.EndsAt) != "" {
		if parsedEndsAt, err := time.Parse(time.RFC3339, strings.TrimSpace(*alert.EndsAt)); err == nil {
			parsedEndsAt = parsedEndsAt.UTC()
			endsAt = &parsedEndsAt
		}
	}

	status := core.StatusFiring
	if alert.Status == "resolved" {
		status = core.StatusResolved
	}

	var generatorURL *string
	if strings.TrimSpace(alert.GeneratorURL) != "" {
		urlValue := strings.TrimSpace(alert.GeneratorURL)
		generatorURL = &urlValue
	}

	return &core.Alert{
		Fingerprint:  strings.TrimSpace(alert.Fingerprint),
		AlertName:    strings.TrimSpace(alert.Labels["alertname"]),
		Status:       status,
		Labels:       cloneStringMap(alert.Labels),
		Annotations:  cloneStringMap(alert.Annotations),
		StartsAt:     startsAt.UTC(),
		EndsAt:       endsAt,
		GeneratorURL: generatorURL,
	}
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

func resolveRuntimeConfigPath() string {
	path := strings.TrimSpace(os.Getenv(runtimeConfigFileEnv))
	if path != "" {
		return path
	}
	return "config.yaml"
}

func readRuntimeConfigOriginal() string {
	return readRuntimeConfigOriginalAt(resolveRuntimeConfigPath())
}

func readRuntimeConfigOriginalAt(configPath string) string {
	content, err := os.ReadFile(configPath)
	if err != nil {
		return ""
	}
	return string(content)
}

func readRuntimeConfigOriginalForReload(configPath string) (string, error) {
	content, err := os.ReadFile(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return string(content), nil
}

func loadRuntimeInhibitionEngine(configPath string) *runtimeInhibitionEngine {
	engine := &runtimeInhibitionEngine{}

	rules, err := parseRuntimeInhibitionRules(configPath)
	if err != nil {
		slog.Warn("Failed to parse inhibition rules from config",
			"config", configPath,
			"error", err,
		)
		return engine
	}

	engine.setRules(rules)
	loadedRules := engine.getRules()
	slog.Info("Loaded runtime inhibition rules",
		"count", len(loadedRules),
		"config", configPath,
	)
	return engine
}

func parseRuntimeInhibitionRules(configPath string) ([]inhibition.InhibitionRule, error) {
	content, err := os.ReadFile(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return parseRuntimeInhibitionRulesFromData(content)
}

func parseRuntimeInhibitionRulesFromData(content []byte) ([]inhibition.InhibitionRule, error) {
	parser := inhibition.NewParser()
	cfg, err := parser.Parse(content)
	if err != nil {
		if isNoInhibitionRulesError(err) {
			return nil, nil
		}
		return nil, err
	}
	return append([]inhibition.InhibitionRule(nil), cfg.Rules...), nil
}

func isNoInhibitionRulesError(err error) bool {
	var cfgErr *inhibition.ConfigError
	if !errors.As(err, &cfgErr) || cfgErr == nil {
		return false
	}

	return strings.Contains(strings.ToLower(cfgErr.Message), "no inhibition rules found")
}

func loadRuntimeReceiverCatalog(configPath string) *runtimeReceiverCatalog {
	catalog := &runtimeReceiverCatalog{}

	configured, err := parseRuntimeConfiguredReceivers(configPath)
	if err != nil {
		slog.Warn("Failed to parse runtime receivers from config",
			"config", configPath,
			"error", err,
		)
		return catalog
	}
	catalog.setConfigured(configured)

	if len(configured) > 0 {
		slog.Info("Loaded runtime receivers from config",
			"count", len(configured),
			"config", configPath,
		)
	}

	return catalog
}

func loadRuntimeAlertGroupByCatalog(configPath string) *runtimeAlertGroupByCatalog {
	catalog := &runtimeAlertGroupByCatalog{}

	groupBy, configured, err := parseRuntimeAlertGroupBy(configPath)
	if err != nil {
		slog.Warn("Failed to parse runtime alert group_by from config",
			"config", configPath,
			"error", err,
		)
		return catalog
	}

	catalog.setGroupBy(groupBy, configured)

	if configured {
		slog.Info("Loaded runtime alert group_by from config",
			"count", len(groupBy),
			"config", configPath,
		)
	}

	return catalog
}

func loadRuntimeClusterContext() *runtimeClusterContext {
	listenAddress, listenDefined := os.LookupEnv(runtimeClusterListenAddressEnv)
	listenAddress = strings.TrimSpace(listenAddress)

	if listenDefined && listenAddress == "" {
		return &runtimeClusterContext{
			status: "disabled",
			peers:  []map[string]string{},
		}
	}

	if listenAddress == "" {
		listenAddress = defaultRuntimeClusterListenAddress
	}

	clusterName := strings.TrimSpace(os.Getenv(runtimeClusterNameEnv))
	if clusterName == "" {
		clusterName = strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", ""))
	}

	advertiseAddress := strings.TrimSpace(os.Getenv(runtimeClusterAdvertiseAddressEnv))
	if advertiseAddress == "" {
		advertiseAddress = deriveRuntimeClusterAdvertiseAddress(listenAddress)
	}

	return &runtimeClusterContext{
		status: "ready",
		name:   clusterName,
		peers: []map[string]string{
			{
				"name":    clusterName,
				"address": advertiseAddress,
			},
		},
		settleUntil: time.Now().UTC().Add(defaultRuntimeClusterSettlingDuration),
	}
}

func deriveRuntimeClusterAdvertiseAddress(listenAddress string) string {
	listenAddress = strings.TrimSpace(listenAddress)
	if listenAddress == "" {
		return "127.0.0.1:9094"
	}

	host, port, err := net.SplitHostPort(listenAddress)
	if err != nil {
		return "127.0.0.1:9094"
	}
	host = strings.TrimSpace(host)
	port = strings.TrimSpace(port)
	if port == "" {
		port = "9094"
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

func buildRuntimeClusterStatusPayload(clusterCtx *runtimeClusterContext, now time.Time) map[string]any {
	cluster := map[string]any{
		"status": "disabled",
		"peers":  []map[string]string{},
	}
	if clusterCtx == nil {
		return cluster
	}

	status := strings.TrimSpace(clusterCtx.status)
	if status == "" {
		status = "disabled"
	}
	if status == "ready" && !clusterCtx.settleUntil.IsZero() && now.UTC().Before(clusterCtx.settleUntil) {
		status = "settling"
	}
	cluster["status"] = status
	if status != "disabled" && strings.TrimSpace(clusterCtx.name) != "" {
		cluster["name"] = strings.TrimSpace(clusterCtx.name)
	}

	if len(clusterCtx.peers) == 0 {
		cluster["peers"] = []map[string]string{}
		return cluster
	}
	peers := make([]map[string]string, 0, len(clusterCtx.peers))
	for _, peer := range clusterCtx.peers {
		peers = append(peers, map[string]string{
			"name":    strings.TrimSpace(peer["name"]),
			"address": strings.TrimSpace(peer["address"]),
		})
	}
	cluster["peers"] = peers
	return cluster
}

func parseRuntimeConfiguredReceivers(configPath string) ([]string, error) {
	content, err := os.ReadFile(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return parseRuntimeConfiguredReceiversFromData(content)
}

func parseRuntimeConfiguredReceiversFromData(content []byte) ([]string, error) {
	var cfg alertmanagerRuntimeConfig
	if err := yaml.Unmarshal(content, &cfg); err != nil {
		return nil, err
	}

	receivers := make([]string, 0, len(cfg.Receivers))
	receiversSet := map[string]struct{}{}
	for _, receiver := range cfg.Receivers {
		name := strings.TrimSpace(receiver.Name)
		if name == "" {
			continue
		}
		if _, exists := receiversSet[name]; exists {
			continue
		}
		receiversSet[name] = struct{}{}
		receivers = append(receivers, name)
	}

	return receivers, nil
}

func parseRuntimeAlertGroupBy(configPath string) ([]string, bool, error) {
	content, err := os.ReadFile(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}

	return parseRuntimeAlertGroupByFromData(content)
}

func parseRuntimeAlertGroupByFromData(content []byte) ([]string, bool, error) {
	var cfg alertmanagerRuntimeConfig
	if err := yaml.Unmarshal(content, &cfg); err != nil {
		return nil, false, err
	}

	return normalizeRuntimeAlertGroupBy(cfg.Route.GroupBy), true, nil
}

func normalizeRuntimeAlertGroupBy(groupBy []string) []string {
	if len(groupBy) == 0 {
		return []string{}
	}

	normalized := make([]string, 0, len(groupBy))
	seen := map[string]struct{}{}
	for _, rawLabel := range groupBy {
		label := strings.TrimSpace(rawLabel)
		if label == "" {
			continue
		}
		if label == "..." {
			return []string{"..."}
		}
		if _, ok := seen[label]; ok {
			continue
		}
		seen[label] = struct{}{}
		normalized = append(normalized, label)
	}

	return normalized
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

func writeJSONString(w http.ResponseWriter, status int, payload string) {
	writeJSON(w, status, payload)
}
