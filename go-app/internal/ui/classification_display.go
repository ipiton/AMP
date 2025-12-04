// Package ui provides UI-related utilities and components.
package ui

import (
	"time"

	"github.com/ipiton/AMP/internal/core"
)

// ClassificationDisplayData represents classification data formatted for template display.
type ClassificationDisplayData struct {
	Severity             string   `json:"severity"`              // "critical", "warning", "info", "noise"
	Confidence           float64  `json:"confidence"`             // 0.0-1.0
	ConfidencePercent    int      `json:"confidence_percent"`    // 0-100 (для отображения)
	Reasoning            string   `json:"reasoning"`               // HTML-escaped
	Recommendations      []string `json:"recommendations"`        // HTML-escaped
	ProcessingTime       float64  `json:"processing_time"`        // секунды
	ProcessingTimeMs     int      `json:"processing_time_ms"`    // миллисекунды (для отображения)
	Source               string   `json:"source"`                 // "llm", "fallback", "cache"
	HasRecommendations   bool     `json:"has_recommendations"`
}

// AlertCardData represents alert data formatted for alert-card template.
type AlertCardData struct {
	// Base alert fields
	Fingerprint string
	AlertName   string
	Status      string
	Severity    string
	Summary     string
	StartsAt    time.Time

	// Classification fields (optional)
	Classification *ClassificationDisplayData
}

// ToAlertCardData converts EnrichedAlert to AlertCardData for template rendering.
func ToAlertCardData(enriched *EnrichedAlert) *AlertCardData {
	if enriched == nil || enriched.Alert == nil {
		return nil
	}

	// Extract summary from annotations
	summary := ""
	if enriched.Alert.Annotations != nil {
		if s, ok := enriched.Alert.Annotations["summary"]; ok {
			summary = s
		} else if s, ok := enriched.Alert.Annotations["description"]; ok {
			summary = s
		}
	}

	data := &AlertCardData{
		Fingerprint: enriched.Alert.Fingerprint,
		AlertName:   enriched.Alert.AlertName,
		Status:      string(enriched.Alert.Status),
		Summary:     summary,
		StartsAt:    enriched.Alert.StartsAt,
	}

	// Set severity from classification or fallback to label
	if enriched.Classification != nil {
		data.Severity = string(enriched.Classification.Severity)
	} else if enriched.Alert.Labels != nil {
		if sev, ok := enriched.Alert.Labels["severity"]; ok {
			data.Severity = sev
		} else {
			data.Severity = "info" // Default
		}
	} else {
		data.Severity = "info" // Default
	}

	// Add classification display data if available
	if enriched.Classification != nil {
		data.Classification = ToClassificationDisplayData(enriched.Classification, enriched.ClassificationSource)
	}

	return data
}

// ToClassificationDisplayData converts ClassificationResult to ClassificationDisplayData.
func ToClassificationDisplayData(classification *core.ClassificationResult, source string) *ClassificationDisplayData {
	if classification == nil {
		return nil
	}

	display := &ClassificationDisplayData{
		Severity:          string(classification.Severity),
		Confidence:        classification.Confidence,
		ConfidencePercent: int(classification.Confidence * 100),
		Reasoning:         classification.Reasoning, // Will be HTML-escaped in template
		Recommendations:  classification.Recommendations,
		ProcessingTime:    classification.ProcessingTime,
		ProcessingTimeMs:  int(classification.ProcessingTime * 1000),
		Source:            source,
		HasRecommendations: len(classification.Recommendations) > 0,
	}

	return display
}

// ToAlertCardDataList converts a list of EnrichedAlerts to AlertCardData list.
func ToAlertCardDataList(enriched []*EnrichedAlert) []*AlertCardData {
	if len(enriched) == 0 {
		return []*AlertCardData{}
	}

	result := make([]*AlertCardData, len(enriched))
	for i, e := range enriched {
		result[i] = ToAlertCardData(e)
	}

	return result
}
