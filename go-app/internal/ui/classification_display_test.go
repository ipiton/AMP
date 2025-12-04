package ui

import (
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

func TestToAlertCardData(t *testing.T) {
	alert := &core.Alert{
		Fingerprint: "test-fp",
		AlertName:   "TestAlert",
		Status:      core.StatusFiring,
		StartsAt:    time.Now(),
		Labels: map[string]string{
			"severity": "warning",
		},
	}

	enriched := &EnrichedAlert{
		Alert:            alert,
		HasClassification: false,
	}

	data := ToAlertCardData(enriched)
	if data == nil {
		t.Fatal("ToAlertCardData returned nil")
	}

	if data.Fingerprint != alert.Fingerprint {
		t.Errorf("Expected fingerprint %s, got %s", alert.Fingerprint, data.Fingerprint)
	}

	if data.Severity != "warning" {
		t.Errorf("Expected severity warning, got %s", data.Severity)
	}

	if data.Classification != nil {
		t.Error("Expected Classification to be nil")
	}
}

func TestToAlertCardData_WithClassification(t *testing.T) {
	alert := &core.Alert{
		Fingerprint: "test-fp",
		AlertName:   "TestAlert",
		Status:      core.StatusFiring,
		StartsAt:    time.Now(),
	}

	classification := &core.ClassificationResult{
		Severity:        core.SeverityCritical,
		Confidence:      0.85,
		Reasoning:       "Test reasoning",
		Recommendations: []string{"Recommendation 1", "Recommendation 2"},
		ProcessingTime:  0.234,
	}

	enriched := &EnrichedAlert{
		Alert:            alert,
		Classification:   classification,
		HasClassification: true,
		ClassificationSource: "llm",
	}

	data := ToAlertCardData(enriched)
	if data == nil {
		t.Fatal("ToAlertCardData returned nil")
	}

	if data.Classification == nil {
		t.Fatal("Expected Classification to be set")
	}

	if data.Classification.Severity != "critical" {
		t.Errorf("Expected severity critical, got %s", data.Classification.Severity)
	}

	if data.Classification.ConfidencePercent != 85 {
		t.Errorf("Expected confidence 85%%, got %d%%", data.Classification.ConfidencePercent)
	}

	if !data.Classification.HasRecommendations {
		t.Error("Expected HasRecommendations to be true")
	}

	if len(data.Classification.Recommendations) != 2 {
		t.Errorf("Expected 2 recommendations, got %d", len(data.Classification.Recommendations))
	}
}

func TestToAlertCardData_NilEnriched(t *testing.T) {
	data := ToAlertCardData(nil)
	if data != nil {
		t.Error("Expected ToAlertCardData(nil) to return nil")
	}
}

func TestToAlertCardData_NilAlert(t *testing.T) {
	enriched := &EnrichedAlert{
		Alert:            nil,
		HasClassification: false,
	}

	data := ToAlertCardData(enriched)
	if data != nil {
		t.Error("Expected ToAlertCardData with nil alert to return nil")
	}
}

func TestToClassificationDisplayData(t *testing.T) {
	classification := &core.ClassificationResult{
		Severity:        core.SeverityWarning,
		Confidence:      0.75,
		Reasoning:       "Test reasoning",
		Recommendations: []string{"Rec 1"},
		ProcessingTime:  0.123,
	}

	display := ToClassificationDisplayData(classification, "llm")
	if display == nil {
		t.Fatal("ToClassificationDisplayData returned nil")
	}

	if display.Severity != "warning" {
		t.Errorf("Expected severity warning, got %s", display.Severity)
	}

	if display.ConfidencePercent != 75 {
		t.Errorf("Expected confidence 75%%, got %d%%", display.ConfidencePercent)
	}

	if display.ProcessingTimeMs != 123 {
		t.Errorf("Expected processing time 123ms, got %dms", display.ProcessingTimeMs)
	}

	if !display.HasRecommendations {
		t.Error("Expected HasRecommendations to be true")
	}

	if display.Source != "llm" {
		t.Errorf("Expected source llm, got %s", display.Source)
	}
}

func TestToClassificationDisplayData_Nil(t *testing.T) {
	display := ToClassificationDisplayData(nil, "llm")
	if display != nil {
		t.Error("Expected ToClassificationDisplayData(nil) to return nil")
	}
}

func TestToAlertCardDataList(t *testing.T) {
	alerts := []*EnrichedAlert{
		{
			Alert:            &core.Alert{Fingerprint: "fp1", AlertName: "Alert1", Status: core.StatusFiring, StartsAt: time.Now()},
			HasClassification: false,
		},
		{
			Alert:            &core.Alert{Fingerprint: "fp2", AlertName: "Alert2", Status: core.StatusFiring, StartsAt: time.Now()},
			HasClassification: true,
			Classification: &core.ClassificationResult{
				Severity:  core.SeverityCritical,
				Confidence: 0.9,
			},
		},
	}

	dataList := ToAlertCardDataList(alerts)
	if len(dataList) != len(alerts) {
		t.Fatalf("Expected %d items, got %d", len(alerts), len(dataList))
	}

	if dataList[0].Fingerprint != "fp1" {
		t.Errorf("Expected fp1, got %s", dataList[0].Fingerprint)
	}

	if dataList[1].Classification == nil {
		t.Error("Expected fp2 to have classification")
	}
}

func TestToAlertCardDataList_Empty(t *testing.T) {
	dataList := ToAlertCardDataList([]*EnrichedAlert{})
	if len(dataList) != 0 {
		t.Errorf("Expected empty list, got %d items", len(dataList))
	}
}
