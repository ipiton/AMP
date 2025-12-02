package llm

import (
	"fmt"
	"strconv"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

// LLMAlertRequest represents alert data for LLM API (external format)
type LLMAlertRequest struct {
	AlertName   string            `json:"alertname"`
	Status      string            `json:"status"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	StartsAt    string            `json:"startsAt"`
	EndsAt      string            `json:"endsAt,omitempty"`
	Fingerprint string            `json:"fingerprint"`
}

// LLMClassificationResponse represents classification from LLM API (external format)
type LLMClassificationResponse struct {
	Severity    int      `json:"severity"`
	Category    string   `json:"category"`
	Summary     string   `json:"summary"`
	Confidence  float64  `json:"confidence"`
	Reasoning   string   `json:"reasoning"`
	Suggestions []string `json:"suggestions"`
}

// CoreAlertToLLMRequest converts core.Alert to LLM API request format
func CoreAlertToLLMRequest(alert *core.Alert) *LLMAlertRequest {
	if alert == nil {
		return nil
	}

	req := &LLMAlertRequest{
		AlertName:   alert.AlertName,
		Status:      string(alert.Status),
		Labels:      alert.Labels,
		Annotations: alert.Annotations,
		StartsAt:    alert.StartsAt.Format(time.RFC3339),
		Fingerprint: alert.Fingerprint,
	}

	// Handle optional EndsAt
	if alert.EndsAt != nil {
		req.EndsAt = alert.EndsAt.Format(time.RFC3339)
	}

	return req
}

// LLMResponseToCoreClassification converts LLM API response to core.ClassificationResult
func LLMResponseToCoreClassification(llmResp *LLMClassificationResponse) (*core.ClassificationResult, error) {
	if llmResp == nil {
		return nil, fmt.Errorf("LLM response is nil")
	}

	// Map integer severity to AlertSeverity
	severity, err := mapIntToSeverity(llmResp.Severity)
	if err != nil {
		return nil, fmt.Errorf("invalid severity: %w", err)
	}

	result := &core.ClassificationResult{
		Severity:        severity,
		Confidence:      llmResp.Confidence,
		Reasoning:       llmResp.Reasoning,
		Recommendations: llmResp.Suggestions,
		Metadata: map[string]any{
			"category": llmResp.Category,
			"summary":  llmResp.Summary,
		},
	}

	return result, nil
}

// mapIntToSeverity converts LLM API integer severity to AlertSeverity
// LLM API uses: 1=noise, 2=info, 3=warning, 4=critical
func mapIntToSeverity(severity int) (core.AlertSeverity, error) {
	switch severity {
	case 1:
		return core.SeverityNoise, nil
	case 2:
		return core.SeverityInfo, nil
	case 3:
		return core.SeverityWarning, nil
	case 4:
		return core.SeverityCritical, nil
	default:
		return "", fmt.Errorf("unknown severity level: %d", severity)
	}
}

// mapSeverityToInt converts AlertSeverity to LLM API integer
func mapSeverityToInt(severity core.AlertSeverity) int {
	switch severity {
	case core.SeverityNoise:
		return 1
	case core.SeverityInfo:
		return 2
	case core.SeverityWarning:
		return 3
	case core.SeverityCritical:
		return 4
	default:
		return 2 // default to info
	}
}

// CoreClassificationToLLMResponse converts core.ClassificationResult to LLM API response format
// (useful for testing or reverse mapping)
func CoreClassificationToLLMResponse(result *core.ClassificationResult) *LLMClassificationResponse {
	if result == nil {
		return nil
	}

	resp := &LLMClassificationResponse{
		Severity:    mapSeverityToInt(result.Severity),
		Confidence:  result.Confidence,
		Reasoning:   result.Reasoning,
		Suggestions: result.Recommendations,
	}

	// Extract category and summary from metadata if present
	if result.Metadata != nil {
		if category, ok := result.Metadata["category"].(string); ok {
			resp.Category = category
		}
		if summary, ok := result.Metadata["summary"].(string); ok {
			resp.Summary = summary
		}
	}

	return resp
}

// ParseProcessingTime converts string duration to float64 seconds
func ParseProcessingTime(processingTimeStr string) (float64, error) {
	if processingTimeStr == "" {
		return 0, nil
	}

	// Try to parse as duration (e.g., "234ms", "1.5s")
	duration, err := time.ParseDuration(processingTimeStr)
	if err == nil {
		return duration.Seconds(), nil
	}

	// Try to parse as float (e.g., "0.234")
	seconds, err := strconv.ParseFloat(processingTimeStr, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid processing time format: %s", processingTimeStr)
	}

	return seconds, nil
}

// FormatProcessingTime converts float64 seconds to string
func FormatProcessingTime(seconds float64) string {
	return fmt.Sprintf("%.3fs", seconds)
}
