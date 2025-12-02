package services

import (
	"log/slog"
	"strings"

	"github.com/ipiton/AMP/internal/core"
)

// FallbackEngine provides rule-based alert classification when LLM is unavailable.
type FallbackEngine interface {
	// Classify classifies an alert using rules.
	Classify(alert *core.Alert) *core.ClassificationResult

	// GetConfidence returns the confidence level of fallback classifications.
	GetConfidence() float64
}

// RuleBasedFallback implements intelligent rule-based classification.
type RuleBasedFallback struct {
	rules      []ClassificationRule
	confidence float64
	logger     *slog.Logger
}

// ClassificationRule defines a classification rule.
type ClassificationRule struct {
	Name       string
	Condition  func(*core.Alert) bool
	Severity   core.AlertSeverity
	Category   string
	Confidence float64
	Reasoning  string
}

// NewRuleBasedFallback creates a new rule-based fallback engine.
func NewRuleBasedFallback(logger *slog.Logger) *RuleBasedFallback {
	if logger == nil {
		logger = slog.Default()
	}

	return &RuleBasedFallback{
		rules:      getDefaultRules(),
		confidence: 0.6, // Default fallback confidence
		logger:     logger,
	}
}

// Classify classifies alert using rules.
func (f *RuleBasedFallback) Classify(alert *core.Alert) *core.ClassificationResult {
	// Try each rule in order
	for _, rule := range f.rules {
		if rule.Condition(alert) {
			f.logger.Info("Fallback rule matched",
				"rule", rule.Name,
				"alert", alert.AlertName,
				"severity", rule.Severity)

			return &core.ClassificationResult{
				Severity:   rule.Severity,
				Confidence: rule.Confidence,
				Reasoning:  rule.Reasoning,
				Recommendations: []string{
					"This is a fallback classification",
					"LLM classification unavailable",
					"Check alert patterns for accuracy",
				},
				ProcessingTime: 0.001, // < 1ms
				Metadata: map[string]any{
					"category": rule.Category,
					"rule":     rule.Name,
					"fallback": true,
				},
			}
		}
	}

	// No rule matched - return default classification
	f.logger.Warn("No fallback rule matched, using default",
		"alert", alert.AlertName)

	return f.getDefaultClassification(alert)
}

// GetConfidence returns fallback confidence level.
func (f *RuleBasedFallback) GetConfidence() float64 {
	return f.confidence
}

// getDefaultClassification returns default classification when no rules match.
func (f *RuleBasedFallback) getDefaultClassification(alert *core.Alert) *core.ClassificationResult {
	// Infer severity from alert status and labels
	severity := core.SeverityInfo
	if alert.Status == core.StatusFiring {
		severity = core.SeverityWarning // Assume warning for firing alerts
	}

	// Check for critical indicators in labels
	if isCriticalAlert(alert) {
		severity = core.SeverityCritical
	}

	return &core.ClassificationResult{
		Severity:   severity,
		Confidence: 0.4, // Low confidence for default
		Reasoning:  "Default fallback classification - no specific rule matched",
		Recommendations: []string{
			"Review alert configuration",
			"Add specific classification rules",
			"Enable LLM classification for better accuracy",
		},
		ProcessingTime: 0.001,
		Metadata: map[string]any{
			"category": "unknown",
			"fallback": true,
			"default":  true,
		},
	}
}

// getDefaultRules returns default classification rules (150% enhancement - 15+ rules).
func getDefaultRules() []ClassificationRule {
	return []ClassificationRule{
		// ========== Critical Infrastructure Rules ==========
		{
			Name: "Node Down Critical",
			Condition: func(a *core.Alert) bool {
				return containsAny(a.AlertName, "NodeDown", "InstanceDown", "node_down") ||
					a.Labels["severity"] == "critical"
			},
			Severity:   core.SeverityCritical,
			Category:   "infrastructure",
			Confidence: 0.8,
			Reasoning:  "Node or instance is down - critical infrastructure failure",
		},
		{
			Name: "Kubernetes Node NotReady",
			Condition: func(a *core.Alert) bool {
				return containsAny(a.AlertName, "NodeNotReady", "KubernetesNodeNotReady") ||
					(a.Labels["alertname"] == "NodeNotReady")
			},
			Severity:   core.SeverityCritical,
			Category:   "kubernetes",
			Confidence: 0.8,
			Reasoning:  "Kubernetes node is not ready - pod scheduling affected",
		},
		{
			Name: "Disk Full Critical",
			Condition: func(a *core.Alert) bool {
				return containsAny(a.AlertName, "DiskFull", "disk_full", "FilesystemFull") &&
					a.Status == core.StatusFiring
			},
			Severity:   core.SeverityCritical,
			Category:   "storage",
			Confidence: 0.75,
			Reasoning:  "Disk space critically low or full - immediate action required",
		},

		// ========== High Resource Usage Rules ==========
		{
			Name: "High CPU Usage",
			Condition: func(a *core.Alert) bool {
				return containsAny(a.AlertName, "HighCPU", "CPUThrottling", "cpu_high")
			},
			Severity:   core.SeverityWarning,
			Category:   "resource",
			Confidence: 0.7,
			Reasoning:  "CPU usage is high - may affect performance",
		},
		{
			Name: "High Memory Usage",
			Condition: func(a *core.Alert) bool {
				return containsAny(a.AlertName, "HighMemory", "MemoryPressure", "memory_high", "OOM")
			},
			Severity:   core.SeverityWarning,
			Category:   "resource",
			Confidence: 0.7,
			Reasoning:  "Memory usage is high - risk of OOM",
		},
		{
			Name: "High Disk IO",
			Condition: func(a *core.Alert) bool {
				return containsAny(a.AlertName, "HighIO", "DiskIO", "io_wait")
			},
			Severity:   core.SeverityWarning,
			Category:   "performance",
			Confidence: 0.65,
			Reasoning:  "Disk I/O is high - may cause slowdowns",
		},

		// ========== Application-Level Rules ==========
		{
			Name: "High Error Rate",
			Condition: func(a *core.Alert) bool {
				return containsAny(a.AlertName, "HighErrorRate", "ErrorRate", "errors_high") ||
					containsAny(strings.ToLower(a.Annotations["description"]), "error rate", "errors")
			},
			Severity:   core.SeverityWarning,
			Category:   "application",
			Confidence: 0.7,
			Reasoning:  "Application error rate is elevated - investigate logs",
		},
		{
			Name: "Service Unavailable",
			Condition: func(a *core.Alert) bool {
				return containsAny(a.AlertName, "ServiceUnavailable", "EndpointDown", "TargetDown")
			},
			Severity:   core.SeverityCritical,
			Category:   "availability",
			Confidence: 0.75,
			Reasoning:  "Service or endpoint is unavailable - affecting users",
		},
		{
			Name: "High Response Time",
			Condition: func(a *core.Alert) bool {
				return containsAny(a.AlertName, "HighLatency", "SlowResponse", "ResponseTimeSlow")
			},
			Severity:   core.SeverityWarning,
			Category:   "performance",
			Confidence: 0.65,
			Reasoning:  "Response time is high - user experience degraded",
		},

		// ========== Database Rules ==========
		{
			Name: "Database Connection Issues",
			Condition: func(a *core.Alert) bool {
				return containsAny(a.AlertName, "DatabaseDown", "DBConnectionFailed", "PostgresDown", "MySQLDown")
			},
			Severity:   core.SeverityCritical,
			Category:   "database",
			Confidence: 0.8,
			Reasoning:  "Database connection issues - data access affected",
		},
		{
			Name: "Database Slow Queries",
			Condition: func(a *core.Alert) bool {
				return containsAny(a.AlertName, "SlowQuery", "QueryLatency", "DBSlow")
			},
			Severity:   core.SeverityWarning,
			Category:   "database",
			Confidence: 0.65,
			Reasoning:  "Database queries are slow - performance impact",
		},

		// ========== Security Rules ==========
		{
			Name: "Security Threat",
			Condition: func(a *core.Alert) bool {
				return containsAny(a.AlertName, "SecurityViolation", "UnauthorizedAccess", "BruteForce") ||
					a.Labels["type"] == "security"
			},
			Severity:   core.SeverityCritical,
			Category:   "security",
			Confidence: 0.85,
			Reasoning:  "Security threat detected - immediate investigation required",
		},
		{
			Name: "Certificate Expiring",
			Condition: func(a *core.Alert) bool {
				return containsAny(a.AlertName, "CertificateExpiring", "CertExpiry", "TLSExpiring")
			},
			Severity:   core.SeverityWarning,
			Category:   "security",
			Confidence: 0.7,
			Reasoning:  "TLS certificate expiring soon - renewal required",
		},

		// ========== Network Rules ==========
		{
			Name: "Network Connectivity Issues",
			Condition: func(a *core.Alert) bool {
				return containsAny(a.AlertName, "NetworkDown", "ConnectivityLoss", "PacketLoss")
			},
			Severity:   core.SeverityCritical,
			Category:   "network",
			Confidence: 0.75,
			Reasoning:  "Network connectivity issues - communication affected",
		},

		// ========== Generic Informational Rules ==========
		{
			Name: "Pod Restarting",
			Condition: func(a *core.Alert) bool {
				return containsAny(a.AlertName, "PodRestarting", "ContainerRestart", "CrashLoopBackOff")
			},
			Severity:   core.SeverityWarning,
			Category:   "kubernetes",
			Confidence: 0.7,
			Reasoning:  "Pod is restarting frequently - investigate crash logs",
		},
		{
			Name: "Backup Failed",
			Condition: func(a *core.Alert) bool {
				return containsAny(a.AlertName, "BackupFailed", "BackupJobFailed")
			},
			Severity:   core.SeverityWarning,
			Category:   "operations",
			Confidence: 0.7,
			Reasoning:  "Backup job failed - data recovery risk",
		},
	}
}

// Helper functions

// containsAny checks if string contains any of the substrings (case-insensitive).
func containsAny(s string, substrings ...string) bool {
	lower := strings.ToLower(s)
	for _, substr := range substrings {
		if strings.Contains(lower, strings.ToLower(substr)) {
			return true
		}
	}
	return false
}

// isCriticalAlert checks if alert has critical indicators.
func isCriticalAlert(alert *core.Alert) bool {
	// Check labels
	if alert.Labels["severity"] == "critical" || alert.Labels["priority"] == "P1" {
		return true
	}

	// Check alert name patterns
	criticalPatterns := []string{
		"critical", "down", "failed", "error", "outage",
	}
	alertNameLower := strings.ToLower(alert.AlertName)
	for _, pattern := range criticalPatterns {
		if strings.Contains(alertNameLower, pattern) {
			return true
		}
	}

	return false
}
