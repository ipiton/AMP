package handlers

// Stub types and functions for handlers

// Stub types for query handling
type AlertmanagerListResponse struct {
	Status string      `json:"status"`
	Data   interface{} `json:"data"`
}

type QueryValidationResult struct {
	Valid  bool              `json:"valid"`
	Errors []ValidationError `json:"errors,omitempty"`
}

type QueryParameters struct {
	Query     string     `json:"query"`
	Page      int        `json:"page"`
	Limit     int        `json:"limit"`
	Status    string     `json:"status,omitempty"`
	Severity  string     `json:"severity,omitempty"`
	StartTime *QueryTime `json:"start_time,omitempty"`
	EndTime   *QueryTime `json:"end_time,omitempty"`
	Filter    string     `json:"filter,omitempty"`
	SortBy    string     `json:"sort_by,omitempty"`
	SortOrder string     `json:"sort_order,omitempty"`
}

// QueryTime is a time wrapper
type QueryTime struct {
	value int64
}

func (t *QueryTime) IsZero() bool {
	return t == nil || t.value == 0
}

type LabelMatcher struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Operator string `json:"operator,omitempty"`
}

// PrometheusAlertsMetrics metrics
type PrometheusAlertsMetrics struct{}

func (m *PrometheusAlertsMetrics) IncrementConcurrent() {}
func (m *PrometheusAlertsMetrics) DecrementConcurrent() {}
func (m *PrometheusAlertsMetrics) RecordPayloadSize(size int) {}
func (m *PrometheusAlertsMetrics) RecordAlerts(format string, received, processed, failed int) {}
func (m *PrometheusAlertsMetrics) RecordProcessingError(errorType string) {}
func (m *PrometheusAlertsMetrics) RecordValidationError(errorType string) {}
func (m *PrometheusAlertsMetrics) RecordRequest(method string, duration float64) {}

type PrometheusQueryMetrics struct{}

func (m *PrometheusQueryMetrics) IncrementConcurrent() {}
func (m *PrometheusQueryMetrics) DecrementConcurrent() {}
func (m *PrometheusQueryMetrics) RecordRequest(status string, count int, duration interface{}) {}
func (m *PrometheusQueryMetrics) RecordValidationError(reason string) {}

type ConverterDependencies struct{
	Logger interface{}
}

func NewPrometheusAlertsMetrics() *PrometheusAlertsMetrics {
	return &PrometheusAlertsMetrics{}
}

func NewPrometheusQueryMetrics() *PrometheusQueryMetrics {
	return &PrometheusQueryMetrics{}
}

func ParseQueryParameters(query interface{}) (*QueryParameters, error) {
	return &QueryParameters{
		Query: "",
		Page:  1,
		Limit: 100,
	}, nil
}

// ValidateQueryParameters validates query parameters
func ValidateQueryParameters(params *QueryParameters) *QueryValidationResult {
	return &QueryValidationResult{
		Valid:  true,
		Errors: []ValidationError{},
	}
}

// ParseLabelMatchers parses label matchers from filter string
func ParseLabelMatchers(filter string) ([]LabelMatcher, error) {
	return []LabelMatcher{}, nil
}

// BuildErrorResponse builds an error response
func BuildErrorResponse(message string) interface{} {
	return map[string]interface{}{
		"status":  "error",
		"message": message,
	}
}

// Note: ValidationError already defined in prometheus_alerts.go
