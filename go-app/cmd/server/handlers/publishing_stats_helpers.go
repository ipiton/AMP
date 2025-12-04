package handlers

import (
	"fmt"
	"strings"

	"github.com/ipiton/AMP/internal/business/publishing"
)

// ============================================================================
// Metric Extraction Helpers
// ============================================================================

// getMetricValue safely gets metric value by key (returns 0.0 if not found).
func getMetricValue(metrics map[string]float64, key string) float64 {
	if val, ok := metrics[key]; ok {
		return val
	}
	return 0.0
}

// countHealthyTargets counts targets with health_status=1.0 (healthy).
func countHealthyTargets(metrics map[string]float64) int {
	count := 0
	for key, value := range metrics {
		if strings.Contains(key, "health_status") && value == 1.0 {
			count++
		}
	}
	return count
}

// countUnhealthyTargets counts targets with health_status=3.0 (unhealthy).
func countUnhealthyTargets(metrics map[string]float64) int {
	count := 0
	for key, value := range metrics {
		if strings.Contains(key, "health_status") && value == 3.0 {
			count++
		}
	}
	return count
}

// calculateSuccessRate computes overall success rate from job metrics.
func calculateSuccessRate(metrics map[string]float64) float64 {
	completed := getMetricValue(metrics, "jobs_completed_total")
	submitted := getMetricValue(metrics, "jobs_submitted_total")

	if submitted == 0 {
		return 100.0 // No jobs = 100% success (neutral)
	}

	return (completed / submitted) * 100.0
}

// ============================================================================
// Formatting Helpers
// ============================================================================

// formatFloatHelper is helper for formatFloat.
func formatFloatHelper(f float64, format string) string {
	return fmt.Sprintf(format, f)
}

// generateHealthMessage creates human-readable health message.
func generateHealthMessage(status string, unhealthyCount int, totalTargets int) string {
	switch status {
	case "healthy":
		return "All systems operational"
	case "degraded":
		if unhealthyCount > 0 {
			return fmt.Sprintf("%d of %d targets unhealthy", unhealthyCount, totalTargets)
		}
		return "Some collectors reporting errors"
	case "unhealthy":
		return fmt.Sprintf("Critical: %d of %d targets unhealthy (>50%%)", unhealthyCount, totalTargets)
	default:
		return "Unknown status"
	}
}

// ============================================================================
// Per-Target Metric Extraction
// ============================================================================

// extractTargetHealthStatus extracts health status for specific target.
func extractTargetHealthStatus(metrics map[string]float64, targetName string) string {
	searchKey := fmt.Sprintf("health_status{target=%q", targetName)
	for key, value := range metrics {
		if strings.Contains(key, searchKey) {
			switch value {
			case 1.0:
				return "healthy"
			case 2.0:
				return "degraded"
			case 3.0:
				return "unhealthy"
			default:
				return "unknown"
			}
		}
	}
	return "unknown"
}

// extractTargetSuccessRate extracts success rate for specific target.
func extractTargetSuccessRate(metrics map[string]float64, targetName string) float64 {
	searchKey := fmt.Sprintf("success_rate{target=%q", targetName)
	for key, value := range metrics {
		if strings.Contains(key, searchKey) {
			return value
		}
	}
	return 0.0
}

// extractConsecutiveFailures extracts consecutive failures for specific target.
func extractConsecutiveFailures(metrics map[string]float64, targetName string) int {
	searchKey := fmt.Sprintf("consecutive_failures{target=%q", targetName)
	for key, value := range metrics {
		if strings.Contains(key, searchKey) {
			return int(value)
		}
	}
	return 0
}

// extractJobsProcessed extracts total jobs processed for target.
func extractJobsProcessed(metrics map[string]float64, targetName string) int {
	searchKey := fmt.Sprintf("jobs_processed_total{target=%q", targetName)
	total := 0
	for key, value := range metrics {
		if strings.Contains(key, searchKey) {
			total += int(value)
		}
	}
	return total
}

// extractJobsSucceeded extracts succeeded jobs for target.
func extractJobsSucceeded(metrics map[string]float64, targetName string) int {
	searchKey := fmt.Sprintf("jobs_processed_total{target=%q", targetName)
	searchSucceeded := ",state=\"succeeded\"}"
	for key, value := range metrics {
		if strings.Contains(key, searchKey) && strings.Contains(key, searchSucceeded) {
			return int(value)
		}
	}
	return 0
}

// extractJobsFailed extracts failed jobs for target.
func extractJobsFailed(metrics map[string]float64, targetName string) int {
	searchKey := fmt.Sprintf("jobs_processed_total{target=%q", targetName)
	searchFailed := ",state=\"failed\"}"
	for key, value := range metrics {
		if strings.Contains(key, searchKey) && strings.Contains(key, searchFailed) {
			return int(value)
		}
	}
	return 0
}

// calculateTargetJobSuccessRate calculates job success rate for target.
func calculateTargetJobSuccessRate(metrics map[string]float64, targetName string) float64 {
	succeeded := extractJobsSucceeded(metrics, targetName)
	total := extractJobsProcessed(metrics, targetName)
	if total == 0 {
		return 100.0 // No jobs = 100% success (neutral)
	}
	return (float64(succeeded) / float64(total)) * 100.0
}

// ============================================================================
// Trends Summary Generation
// ============================================================================

// generateTrendsSummary creates human-readable summary from trend analysis.
func generateTrendsSummary(trends publishing.TrendAnalysis) string {
	parts := make([]string, 0, 4)

	// Success rate summary
	switch trends.SuccessRateTrend {
	case "increasing":
		parts = append(parts, fmt.Sprintf("Success rate improving (+%.1f%%)", trends.SuccessRateChange))
	case "decreasing":
		parts = append(parts, fmt.Sprintf("Success rate declining (%.1f%%)", trends.SuccessRateChange))
	default:
		parts = append(parts, "Success rate stable")
	}

	// Latency summary
	switch trends.LatencyTrend {
	case "improving":
		parts = append(parts, fmt.Sprintf("Latency improving (%.1fms faster)", -trends.LatencyChange))
	case "degrading":
		parts = append(parts, fmt.Sprintf("Latency degrading (+%.1fms)", trends.LatencyChange))
	default:
		parts = append(parts, "Latency stable")
	}

	// Error spike detection
	if trends.ErrorSpikeDetected {
		parts = append(parts, fmt.Sprintf("⚠️ Error spike detected (%.2f%% vs %.2f%% baseline)",
			trends.ErrorRateCurrent, trends.ErrorRateBaseline))
	}

	// Queue growth summary
	switch trends.QueueGrowthTrend {
	case "growing":
		if trends.QueueGrowthRate > 10 {
			parts = append(parts, fmt.Sprintf("⚠️ Queue growing rapidly (+%.1f jobs/min)", trends.QueueGrowthRate))
		} else {
			parts = append(parts, fmt.Sprintf("Queue growing slowly (+%.1f jobs/min)", trends.QueueGrowthRate))
		}
	case "shrinking":
		parts = append(parts, "Queue draining")
	default:
		parts = append(parts, "Queue stable")
	}

	// Join all parts with ". "
	summary := ""
	for i, part := range parts {
		if i > 0 {
			summary += ". "
		}
		summary += part
	}

	return summary + "."
}
