package application

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

const legacyDashboardListLimit = 25

type LegacyDashboardOverviewSummary struct {
	Profile              string
	StorageBackend       string
	Ready                bool
	LivenessStatus       string
	LivenessStatusClass  string
	ReadinessStatus      string
	ReadinessStatusClass string
	AlertTotal           int
	FiringAlerts         int
	ResolvedAlerts       int
	SilenceTotal         int
	ActiveSilences       int
	PendingSilences      int
	ExpiredSilences      int
	DegradedReasons      []string
}

type LegacyDashboardAlertsSummary struct {
	RuntimeStatus      string
	RuntimeStatusClass string
	RuntimeDetail      string
	Total              int
	Firing             int
	Resolved           int
	Truncated          bool
	HiddenCount        int
	Alerts             []LegacyDashboardAlertItem
}

type LegacyDashboardAlertItem struct {
	Fingerprint string
	AlertName   string
	Severity    string
	Namespace   string
	Service     string
	Summary     string
	Status      string
	StatusClass string
	StartsAt    string
	UpdatedAt   string
}

type LegacyDashboardSilencesSummary struct {
	RuntimeStatus      string
	RuntimeStatusClass string
	RuntimeDetail      string
	Total              int
	Active             int
	Pending            int
	Expired            int
	Truncated          bool
	HiddenCount        int
	Silences           []LegacyDashboardSilenceItem
}

type LegacyDashboardSilenceItem struct {
	ID              string
	Status          string
	StatusClass     string
	CreatedBy       string
	Comment         string
	MatchersSummary string
	StartsAt        string
	EndsAt          string
	UpdatedAt       string
}

type LegacyDashboardLLMSummary struct {
	Enabled            bool
	Provider           string
	BaseURL            string
	Model              string
	Timeout            string
	MaxTokens          int
	Temperature        string
	MaxRetries         int
	RuntimeStatus      string
	RuntimeStatusClass string
	RuntimeDetail      string
	StatsAvailable     bool
	TotalRequests      int64
	CacheHitRate       string
	LLMSuccessRate     string
	FallbackRate       string
	AvgResponseTime    string
	LastError          string
	LastErrorTime      string
}

type LegacyDashboardRoutingSummary struct {
	Enabled              bool
	Profile              string
	Namespace            string
	LabelSelector        string
	QueueWorkers         int
	MaxConcurrent        int
	RefreshEnabled       bool
	HealthEnabled        bool
	RuntimeStatus        string
	RuntimeStatusClass   string
	RuntimeDetail        string
	Mode                 string
	ModeClass            string
	ModeDuration         string
	TransitionCount      int64
	LastTransitionTime   string
	LastTransitionReason string
	TargetCount          int
	ValidTargets         int
	InvalidTargets       int
	DiscoveryErrors      int
	LastDiscovery        string
	CollectorCount       int
	CollectorNames       []string
}

func (r *ServiceRegistry) LegacyDashboardOverview(ctx context.Context, now time.Time) LegacyDashboardOverviewSummary {
	summary := LegacyDashboardOverviewSummary{
		Profile:              "unknown",
		StorageBackend:       "Unknown",
		LivenessStatus:       "unhealthy",
		LivenessStatusClass:  "unhealthy",
		ReadinessStatus:      "unhealthy",
		ReadinessStatusClass: "unhealthy",
	}

	if r == nil {
		return summary
	}

	if profile := strings.TrimSpace(r.profileName()); profile != "" {
		summary.Profile = profile
	}
	if r.config != nil {
		summary.StorageBackend = getStorageType(r.config.Profile)
	}

	livenessReport := r.LivenessReport(ctx)
	summary.LivenessStatus = dashboardReportStatus(livenessReport)
	summary.LivenessStatusClass = summary.LivenessStatus

	readinessReport := r.ReadinessReport(ctx)
	summary.ReadinessStatus = dashboardReportStatus(readinessReport)
	summary.ReadinessStatusClass = summary.ReadinessStatus
	summary.Ready = dashboardReportReady(readinessReport)

	if r.alertStore != nil {
		summary.AlertTotal, summary.FiringAlerts, summary.ResolvedAlerts = r.alertStore.Stats()
	}
	if r.silenceStore != nil {
		summary.SilenceTotal, summary.ActiveSilences, summary.PendingSilences, summary.ExpiredSilences = r.silenceStore.Stats(now)
	}
	summary.DegradedReasons = append([]string(nil), r.degradedReasons...)

	return summary
}

func (r *ServiceRegistry) LegacyDashboardAlerts(_ time.Time) LegacyDashboardAlertsSummary {
	summary := LegacyDashboardAlertsSummary{
		RuntimeStatus:      "limited",
		RuntimeStatusClass: "limited",
		RuntimeDetail:      "Alert store is not available in the current runtime.",
	}

	if r == nil || r.alertStore == nil {
		return summary
	}

	summary.RuntimeStatus = "ready"
	summary.RuntimeStatusClass = "ready"
	summary.Total, summary.Firing, summary.Resolved = r.alertStore.Stats()

	alerts := r.alertStore.List("", true)
	if len(alerts) == 0 {
		summary.RuntimeDetail = "No alerts have been ingested yet."
		return summary
	}

	summary.RuntimeDetail = "Showing alert snapshots from the active compatibility store."
	if len(alerts) > legacyDashboardListLimit {
		summary.Truncated = true
		summary.HiddenCount = len(alerts) - legacyDashboardListLimit
		alerts = alerts[:legacyDashboardListLimit]
	}

	summary.Alerts = make([]LegacyDashboardAlertItem, 0, len(alerts))
	for _, alert := range alerts {
		updatedAt := strings.TrimSpace(alert.UpdatedAt)
		if updatedAt == "" {
			updatedAt = strings.TrimSpace(alert.StartsAt)
		}

		summary.Alerts = append(summary.Alerts, LegacyDashboardAlertItem{
			Fingerprint: defaultDisplay(alert.Fingerprint),
			AlertName:   firstNonEmpty(alert.Labels["alertname"], "unnamed-alert"),
			Severity:    firstNonEmpty(alert.Labels["severity"], "-"),
			Namespace:   firstNonEmpty(alert.Labels["namespace"], "-"),
			Service:     firstNonEmpty(alert.Labels["service"], "-"),
			Summary:     firstNonEmpty(alert.Annotations["summary"], "No summary provided."),
			Status:      defaultDisplay(strings.TrimSpace(alert.Status)),
			StatusClass: normalizeStatusClass(alert.Status),
			StartsAt:    defaultDisplay(alert.StartsAt),
			UpdatedAt:   defaultDisplay(updatedAt),
		})
	}

	return summary
}

func (r *ServiceRegistry) LegacyDashboardSilences(now time.Time) LegacyDashboardSilencesSummary {
	summary := LegacyDashboardSilencesSummary{
		RuntimeStatus:      "limited",
		RuntimeStatusClass: "limited",
		RuntimeDetail:      "Silence store is not available in the current runtime.",
	}

	if r == nil || r.silenceStore == nil {
		return summary
	}

	summary.RuntimeStatus = "ready"
	summary.RuntimeStatusClass = "ready"
	summary.Total, summary.Active, summary.Pending, summary.Expired = r.silenceStore.Stats(now)

	silences := r.silenceStore.List(now)
	if len(silences) == 0 {
		summary.RuntimeDetail = "No silences are configured right now."
		return summary
	}

	summary.RuntimeDetail = "Showing silence state from the active compatibility store."
	if len(silences) > legacyDashboardListLimit {
		summary.Truncated = true
		summary.HiddenCount = len(silences) - legacyDashboardListLimit
		silences = silences[:legacyDashboardListLimit]
	}

	summary.Silences = make([]LegacyDashboardSilenceItem, 0, len(silences))
	for _, silence := range silences {
		summary.Silences = append(summary.Silences, LegacyDashboardSilenceItem{
			ID:              defaultDisplay(silence.ID),
			Status:          defaultDisplay(strings.TrimSpace(silence.Status.State)),
			StatusClass:     normalizeStatusClass(silence.Status.State),
			CreatedBy:       firstNonEmpty(silence.CreatedBy, "-"),
			Comment:         firstNonEmpty(silence.Comment, "No comment provided."),
			MatchersSummary: formatSilenceMatchers(silence.Matchers),
			StartsAt:        defaultDisplay(silence.StartsAt),
			EndsAt:          defaultDisplay(silence.EndsAt),
			UpdatedAt:       defaultDisplay(silence.UpdatedAt),
		})
	}

	return summary
}

func (r *ServiceRegistry) LegacyDashboardLLM() LegacyDashboardLLMSummary {
	summary := LegacyDashboardLLMSummary{
		Provider:           "-",
		BaseURL:            "-",
		Model:              "-",
		Timeout:            "-",
		Temperature:        "-",
		RuntimeStatus:      "limited",
		RuntimeStatusClass: "limited",
		RuntimeDetail:      "LLM runtime is not exposed to the legacy dashboard.",
		LastError:          "-",
		LastErrorTime:      "-",
	}

	if r == nil || r.config == nil {
		return summary
	}

	cfg := r.config.LLM
	summary.Enabled = cfg.Enabled
	summary.Provider = defaultDisplay(cfg.Provider)
	summary.BaseURL = defaultDisplay(cfg.BaseURL)
	summary.Model = defaultDisplay(cfg.Model)
	summary.Timeout = formatDuration(cfg.Timeout)
	summary.MaxTokens = cfg.MaxTokens
	summary.Temperature = formatFloat(cfg.Temperature)
	summary.MaxRetries = cfg.MaxRetries

	switch {
	case !cfg.Enabled:
		summary.RuntimeStatus = "disabled"
		summary.RuntimeStatusClass = "disabled"
		summary.RuntimeDetail = "LLM classification is disabled in the active config."
	case r.classificationSvc == nil:
		summary.RuntimeStatus = "degraded"
		summary.RuntimeStatusClass = "degraded"
		summary.RuntimeDetail = firstNonEmpty(
			r.degradedReasonWithPrefix("classification unavailable:"),
			"Classification runtime is not initialized in the current process.",
		)
	default:
		stats := r.classificationSvc.GetStats()
		summary.RuntimeStatus = "ready"
		summary.RuntimeStatusClass = "ready"
		summary.RuntimeDetail = "Classification runtime is initialized."
		summary.StatsAvailable = true
		summary.TotalRequests = stats.TotalRequests
		summary.CacheHitRate = formatPercent(stats.CacheHitRate)
		summary.LLMSuccessRate = formatPercent(stats.LLMSuccessRate)
		summary.FallbackRate = formatPercent(stats.FallbackRate)
		summary.AvgResponseTime = formatDuration(stats.AvgResponseTime)
		if strings.TrimSpace(stats.LastError) != "" {
			summary.LastError = stats.LastError
		}
		if stats.LastErrorTime != nil {
			summary.LastErrorTime = stats.LastErrorTime.UTC().Format(time.RFC3339)
		}
	}

	return summary
}

func (r *ServiceRegistry) LegacyDashboardRouting() LegacyDashboardRoutingSummary {
	summary := LegacyDashboardRoutingSummary{
		Profile:              "unknown",
		Namespace:            "-",
		LabelSelector:        "-",
		RuntimeStatus:        "limited",
		RuntimeStatusClass:   "limited",
		RuntimeDetail:        "Publishing runtime is only partially exposed to the legacy dashboard.",
		Mode:                 "limited",
		ModeClass:            "limited",
		ModeDuration:         "-",
		LastTransitionTime:   "-",
		LastTransitionReason: "-",
		LastDiscovery:        "-",
	}

	if r == nil || r.config == nil {
		return summary
	}

	cfg := r.config.Publishing
	summary.Enabled = cfg.Enabled
	summary.Profile = firstNonEmpty(r.profileName(), "unknown")
	summary.Namespace = defaultDisplay(cfg.Discovery.Namespace)
	summary.LabelSelector = defaultDisplay(cfg.Discovery.LabelSelector)
	summary.QueueWorkers = cfg.Queue.WorkerCount
	summary.MaxConcurrent = cfg.Queue.MaxConcurrent
	summary.RefreshEnabled = cfg.Refresh.Enabled
	summary.HealthEnabled = cfg.Health.Enabled

	if r.publishingMetricsCollector != nil {
		summary.CollectorCount = r.publishingMetricsCollector.CollectorCount()
		summary.CollectorNames = append([]string(nil), r.publishingMetricsCollector.GetCollectorNames()...)
	}
	if r.publishingDiscoveryAdapter != nil {
		summary.TargetCount = r.publishingDiscoveryAdapter.GetTargetCount()
	}
	if r.publishingDiscovery != nil {
		stats := r.publishingDiscovery.GetStats()
		if summary.TargetCount == 0 {
			summary.TargetCount = stats.ValidTargets
		}
		summary.ValidTargets = stats.ValidTargets
		summary.InvalidTargets = stats.InvalidTargets
		summary.DiscoveryErrors = stats.DiscoveryErrors
		if !stats.LastDiscovery.IsZero() {
			summary.LastDiscovery = stats.LastDiscovery.UTC().Format(time.RFC3339)
		}
	}

	switch {
	case !cfg.Enabled:
		summary.RuntimeStatus = "disabled"
		summary.RuntimeStatusClass = "disabled"
		summary.RuntimeDetail = "Publishing is disabled in the active config."
		summary.Mode = "disabled"
		summary.ModeClass = "disabled"
	case r.publishingMode != nil:
		modeMetrics := r.publishingMode.GetModeMetrics()
		summary.Mode = modeMetrics.CurrentMode.String()
		summary.ModeClass = normalizeStatusClass(summary.Mode)
		summary.ModeDuration = formatDuration(modeMetrics.CurrentModeDuration)
		summary.TransitionCount = modeMetrics.TransitionCount
		if !modeMetrics.LastTransitionTime.IsZero() {
			summary.LastTransitionTime = modeMetrics.LastTransitionTime.UTC().Format(time.RFC3339)
		}
		if strings.TrimSpace(modeMetrics.LastTransitionReason) != "" {
			summary.LastTransitionReason = humanizeReason(modeMetrics.LastTransitionReason)
		}
		if summary.Mode == "metrics-only" {
			summary.RuntimeStatus = "metrics-only"
			summary.RuntimeStatusClass = "metrics-only"
			summary.RuntimeDetail = "Publishing runtime is initialized, but it is currently operating in metrics-only mode."
		} else {
			summary.RuntimeStatus = "ready"
			summary.RuntimeStatusClass = "ready"
			summary.RuntimeDetail = "Publishing runtime is initialized."
		}
	case r.metricsOnlyPublishingReason() != "":
		reason := humanizeReason(r.metricsOnlyPublishingReason())
		summary.RuntimeStatus = "metrics-only"
		summary.RuntimeStatusClass = "metrics-only"
		summary.RuntimeDetail = fmt.Sprintf("Publishing is using metrics-only fallback (%s).", reason)
		summary.Mode = "metrics-only"
		summary.ModeClass = "metrics-only"
		summary.LastTransitionReason = reason
	}

	return summary
}

func (r *ServiceRegistry) degradedReasonWithPrefix(prefix string) string {
	if r == nil {
		return ""
	}
	for _, reason := range r.degradedReasons {
		if strings.HasPrefix(reason, prefix) {
			return reason
		}
	}
	return ""
}

func (r *ServiceRegistry) metricsOnlyPublishingReason() string {
	if r == nil {
		return ""
	}
	publisher, ok := r.publisher.(*MetricsOnlyPublisher)
	if !ok || publisher == nil {
		return ""
	}
	return publisher.reason
}

func dashboardReportStatus(report map[string]any) string {
	if report == nil {
		return "unknown"
	}
	status, ok := report["status"].(string)
	if !ok || strings.TrimSpace(status) == "" {
		return "unknown"
	}
	return status
}

func dashboardReportReady(report map[string]any) bool {
	if report == nil {
		return false
	}
	ready, _ := report["ready"].(bool)
	return ready
}

func normalizeStatusClass(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "healthy", "ready", "normal", "firing", "active":
		return strings.TrimSpace(strings.ToLower(value))
	case "degraded", "pending", "metrics-only", "resolved":
		return strings.TrimSpace(strings.ToLower(value))
	case "disabled", "expired", "unhealthy":
		return strings.TrimSpace(strings.ToLower(value))
	case "":
		return "limited"
	default:
		return "limited"
	}
}

func defaultDisplay(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func formatPercent(value float64) string {
	if value == 0 {
		return "0.0%"
	}
	return fmt.Sprintf("%.1f%%", value*100)
}

func formatDuration(value time.Duration) string {
	if value <= 0 {
		return "-"
	}
	return value.Round(time.Millisecond).String()
}

func formatFloat(value float64) string {
	return fmt.Sprintf("%.2f", value)
}

func formatSilenceMatchers(matchers []core.APISilenceMatcher) string {
	if len(matchers) == 0 {
		return "-"
	}

	parts := make([]string, 0, len(matchers))
	for _, matcher := range matchers {
		operator := "="
		switch {
		case matcher.IsRegex && !matcher.IsEqual:
			operator = "!~"
		case matcher.IsRegex:
			operator = "=~"
		case !matcher.IsEqual:
			operator = "!="
		}

		parts = append(parts, fmt.Sprintf("%s%s%s", matcher.Name, operator, matcher.Value))
	}

	return strings.Join(parts, ", ")
}

func humanizeReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "-"
	}
	return strings.ReplaceAll(reason, "_", " ")
}
