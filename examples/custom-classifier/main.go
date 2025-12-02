// Package main demonstrates how to implement a custom alert classifier.
//
// This example shows:
//   - How to implement the AlertClassifier interface
//   - How to integrate with Alert History Service
//   - How to add custom classification logic
//   - How to track metrics and performance
//
// Custom classifiers can use:
//   - Machine learning models
//   - External APIs
//   - Rule engines
//   - Any custom logic
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/ipiton/AMP/pkg/core/domain"
	"github.com/ipiton/AMP/pkg/core/interfaces"
)

// ================================================================================
// Custom ML-Based Classifier Example
// ================================================================================

// MLClassifier implements interfaces.AlertClassifier using a machine learning model.
//
// This is a simplified example. In production, you would:
//   - Load a real ML model (TensorFlow, PyTorch, etc.)
//   - Use model serving (TensorFlow Serving, Seldon, etc.)
//   - Implement proper error handling and retries
//   - Add comprehensive metrics and logging
type MLClassifier struct {
	modelName    string
	modelVersion string
	// In real implementation: model *ml.Model
}

// NewMLClassifier creates a new ML-based classifier.
func NewMLClassifier(modelName, modelVersion string) *MLClassifier {
	return &MLClassifier{
		modelName:    modelName,
		modelVersion: modelVersion,
	}
}

// Name returns the classifier name (required by interface).
func (c *MLClassifier) Name() string {
	return "ml-classifier"
}

// Classify classifies an alert using ML model (required by interface).
//
// In this example, we demonstrate the structure.
// Real implementation would:
//   1. Extract features from alert
//   2. Call ML model for prediction
//   3. Parse model output
//   4. Return classification result
func (c *MLClassifier) Classify(ctx context.Context, alert *domain.Alert) (*domain.ClassificationResult, error) {
	startTime := time.Now()

	// Example: Extract features from alert
	features := c.extractFeatures(alert)

	// Example: Call ML model (simplified)
	prediction := c.predictWithModel(ctx, features)

	// Build classification result
	result := &domain.ClassificationResult{
		Severity:        prediction.Severity,
		Confidence:      prediction.Confidence,
		Reasoning:       fmt.Sprintf("ML model prediction: %s (confidence: %.2f)", prediction.Severity, prediction.Confidence),
		Recommendations: c.generateRecommendations(alert, prediction),
		Category:        prediction.Category,
		Priority:        prediction.Priority,
		ClassifierName:  c.Name(),
		ClassifiedAt:    time.Now(),
		ModelVersion:    c.modelVersion,
		ProcessingTime:  time.Since(startTime).Seconds(),
	}

	return result, nil
}

// ClassifyBatch processes multiple alerts efficiently (required by interface).
//
// Batch processing is more efficient for ML models because:
//   - Single model inference call
//   - Better GPU utilization
//   - Reduced network overhead
func (c *MLClassifier) ClassifyBatch(ctx context.Context, alerts []*domain.Alert) ([]*domain.ClassificationResult, error) {
	results := make([]*domain.ClassificationResult, len(alerts))

	// In real implementation: batch inference
	for i, alert := range alerts {
		result, err := c.Classify(ctx, alert)
		if err != nil {
			return nil, fmt.Errorf("failed to classify alert %d: %w", i, err)
		}
		results[i] = result
	}

	return results, nil
}

// Health checks classifier health (required by interface).
func (c *MLClassifier) Health(ctx context.Context) error {
	// In real implementation: check model availability
	// Example: ping model server, verify model loaded, etc.
	return nil
}

// ================================================================================
// Helper Methods (Implementation-specific)
// ================================================================================

// ModelPrediction represents ML model output.
type ModelPrediction struct {
	Severity   domain.AlertSeverity
	Confidence float64
	Category   string
	Priority   string
	Features   map[string]float64
}

// extractFeatures extracts ML features from alert.
//
// Example features:
//   - Has "critical" in labels/annotations
//   - Production environment (namespace=prod)
//   - Time of day (business hours vs night)
//   - Historical frequency
func (c *MLClassifier) extractFeatures(alert *domain.Alert) map[string]float64 {
	features := make(map[string]float64)

	// Feature 1: Is production environment?
	if ns := alert.Namespace(); ns != nil && *ns == "production" {
		features["is_production"] = 1.0
	} else {
		features["is_production"] = 0.0
	}

	// Feature 2: Has high severity label?
	if sev := alert.Severity(); sev != nil && (*sev == "critical" || *sev == "high") {
		features["has_high_severity"] = 1.0
	} else {
		features["has_high_severity"] = 0.0
	}

	// Feature 3: Business hours? (simplified)
	hour := time.Now().Hour()
	if hour >= 9 && hour <= 17 {
		features["business_hours"] = 1.0
	} else {
		features["business_hours"] = 0.0
	}

	// Feature 4: Number of labels (complexity indicator)
	features["label_count"] = float64(len(alert.Labels))

	// Add more features as needed...

	return features
}

// predictWithModel calls ML model for prediction (simplified).
//
// In real implementation:
//   - Serialize features to model input format
//   - Call model inference API (gRPC, HTTP, etc.)
//   - Parse model output
//   - Handle errors and timeouts
func (c *MLClassifier) predictWithModel(ctx context.Context, features map[string]float64) *ModelPrediction {
	// Example: Simple rule-based logic (replace with real ML inference)

	// Weighted sum of features (simplified neural network)
	score := features["is_production"]*0.4 +
		features["has_high_severity"]*0.3 +
		features["business_hours"]*0.2 +
		features["label_count"]*0.1

	// Convert score to severity
	var severity domain.AlertSeverity
	var confidence float64

	switch {
	case score >= 0.7:
		severity = domain.SeverityCritical
		confidence = 0.9
	case score >= 0.5:
		severity = domain.SeverityWarning
		confidence = 0.8
	case score >= 0.3:
		severity = domain.SeverityInfo
		confidence = 0.7
	default:
		severity = domain.SeverityNoise
		confidence = 0.6
	}

	return &ModelPrediction{
		Severity:   severity,
		Confidence: confidence,
		Category:   "infrastructure", // From model
		Priority:   "p1",             // From model
		Features:   features,
	}
}

// generateRecommendations generates action recommendations.
func (c *MLClassifier) generateRecommendations(alert *domain.Alert, prediction *ModelPrediction) []string {
	recommendations := []string{}

	// Add severity-specific recommendations
	switch prediction.Severity {
	case domain.SeverityCritical:
		recommendations = append(recommendations,
			"Immediate action required",
			"Escalate to on-call engineer",
			"Check runbook: https://runbooks.example.com/"+alert.AlertName,
		)
	case domain.SeverityWarning:
		recommendations = append(recommendations,
			"Investigate within 1 hour",
			"Review recent deployments",
		)
	case domain.SeverityInfo:
		recommendations = append(recommendations,
			"Monitor for escalation",
		)
	}

	return recommendations
}

// ================================================================================
// Usage Example
// ================================================================================

func main() {
	// Create custom classifier
	classifier := NewMLClassifier("alert-classifier-v1", "1.0.0")

	// Example alert
	alert := &domain.Alert{
		Fingerprint: "abc123",
		AlertName:   "HighCPU",
		Status:      domain.StatusFiring,
		Labels: map[string]string{
			"alertname": "HighCPU",
			"severity":  "critical",
			"namespace": "production",
			"instance":  "server-01",
		},
		Annotations: map[string]string{
			"summary": "CPU usage above 90%",
		},
		StartsAt: time.Now(),
	}

	// Classify alert
	ctx := context.Background()
	result, err := classifier.Classify(ctx, alert)
	if err != nil {
		log.Fatal(err)
	}

	// Print results
	fmt.Printf("Alert Classification:\n")
	fmt.Printf("  Classifier: %s\n", result.ClassifierName)
	fmt.Printf("  Severity: %s\n", result.Severity)
	fmt.Printf("  Confidence: %.2f\n", result.Confidence)
	fmt.Printf("  Category: %s\n", result.Category)
	fmt.Printf("  Priority: %s\n", result.Priority)
	fmt.Printf("  Reasoning: %s\n", result.Reasoning)
	fmt.Printf("  Processing Time: %.3fs\n", result.ProcessingTime)
	fmt.Printf("  Recommendations:\n")
	for _, rec := range result.Recommendations {
		fmt.Printf("    - %s\n", rec)
	}

	fmt.Println("\n✅ Custom classifier works!")
}

// ================================================================================
// Integration with Alert History Service
// ================================================================================
//
// To integrate your custom classifier:
//
// 1. Implement interfaces.AlertClassifier interface (see above)
//
// 2. Register your classifier in main.go:
//
//    classifier := NewMLClassifier("my-model", "1.0.0")
//    registry.Register("ml-classifier", classifier)
//
// 3. Configure Alert History to use your classifier:
//
//    config.yml:
//      classification:
//        default_classifier: ml-classifier
//
// 4. Deploy and monitor:
//
//    - Monitor classification latency
//    - Track classification accuracy
//    - Monitor model health
//    - Set up alerts for classifier failures
//
// That's it! Your custom classifier is now integrated.
