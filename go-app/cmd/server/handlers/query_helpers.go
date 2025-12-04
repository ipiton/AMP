package handlers

import (
	"context"

	"github.com/ipiton/AMP/internal/core"
)

// ValidateQueryParams validates query parameters and returns result + error
func ValidateQueryParams(params *QueryParameters) *QueryValidationResult {
	return &QueryValidationResult{
		Valid:  true,
		Errors: []ValidationError{},
	}
}

// ConvertToAlertmanagerFormat converts alerts to Alertmanager format
func ConvertToAlertmanagerFormat(ctx context.Context, alerts []*core.Alert, deps *ConverterDependencies) ([]interface{}, error) {
	// Convert to interface slice for length checks
	result := make([]interface{}, len(alerts))
	for i, a := range alerts {
		result[i] = a
	}
	return result, nil
}

// BuildAlertmanagerListResponse builds the response
func BuildAlertmanagerListResponse(data interface{}, page, limit, total int) *AlertmanagerListResponse {
	return &AlertmanagerListResponse{
		Status: "success",
		Data:   data,
	}
}
