package services

import (
	"context"

	"github.com/ipiton/AMP/internal/core"
)

// classificationServiceAdapter adapts ClassificationService to core.AlertClassifier interface.
// This adapter bridges the naming difference: ClassifyAlert -> Classify.
type classificationServiceAdapter struct {
	svc ClassificationService
}

// NewAlertClassifierAdapter creates an adapter that implements core.AlertClassifier.
func NewAlertClassifierAdapter(svc ClassificationService) core.AlertClassifier {
	return &classificationServiceAdapter{svc: svc}
}

// Classify implements core.AlertClassifier interface by calling ClassifyAlert.
func (a *classificationServiceAdapter) Classify(ctx context.Context, alert *core.Alert) (*core.ClassificationResult, error) {
	return a.svc.ClassifyAlert(ctx, alert)
}
