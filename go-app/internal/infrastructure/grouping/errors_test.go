package grouping

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseError_Error tests ParseError error message formatting
func TestParseError_Error(t *testing.T) {
	tests := []struct {
		name     string
		err      *ParseError
		contains []string
	}{
		{
			name: "full error",
			err: &ParseError{
				Field:  "group_wait",
				Value:  "invalid",
				Line:   10,
				Column: 5,
				Err:    errors.New("invalid duration"),
			},
			// Note: Line/Column are not included in Error() message, they're for metadata only
			contains: []string{"parse error", "group_wait", "invalid", "invalid duration"},
		},
		{
			name: "error with line only",
			err: &ParseError{
				Field: "receiver",
				Value: "",
				Line:  5,
				Err:   errors.New("missing receiver"),
			},
			// Note: Line is not included in Error() message, it's for metadata only
			contains: []string{"parse error", "receiver", "missing receiver"},
		},
		{
			name: "error without position",
			err: &ParseError{
				Field: "group_by",
				Value: "invalid-label",
				Err:   errors.New("invalid label name"),
			},
			contains: []string{"parse error", "group_by", "invalid-label", "invalid label name"},
		},
		{
			name: "minimal error",
			err: &ParseError{
				Err: errors.New("syntax error"),
			},
			contains: []string{"parse error", "syntax error"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := tt.err.Error()
			for _, substr := range tt.contains {
				assert.Contains(t, msg, substr)
			}
		})
	}
}

// TestParseError_Unwrap tests error unwrapping
func TestParseError_Unwrap(t *testing.T) {
	innerErr := errors.New("inner error")
	parseErr := &ParseError{
		Field: "test",
		Err:   innerErr,
	}

	unwrapped := parseErr.Unwrap()
	assert.Equal(t, innerErr, unwrapped)
}

// TestNewParseError tests ParseError constructor
func TestNewParseError(t *testing.T) {
	innerErr := errors.New("test error")
	parseErr := NewParseError("field1", "value1", innerErr)

	assert.NotNil(t, parseErr)
	assert.Equal(t, "field1", parseErr.Field)
	assert.Equal(t, "value1", parseErr.Value)
	assert.Equal(t, innerErr, parseErr.Err)
	assert.Equal(t, 0, parseErr.Line)
	assert.Equal(t, 0, parseErr.Column)
}

// TestValidationError_Error tests ValidationError error message
func TestValidationError_Error(t *testing.T) {
	tests := []struct {
		name     string
		err      ValidationError
		contains []string
	}{
		{
			name: "full validation error",
			err: ValidationError{
				Field:   "group_by",
				Value:   "invalid-label",
				Rule:    "labelname",
				Message: "invalid label name format",
			},
			// Note: Rule is not included in Error() message, it's for metadata only
			contains: []string{"validation error", "group_by", "invalid label name format", "invalid-label"},
		},
		{
			name: "required field error",
			err: ValidationError{
				Field:   "receiver",
				Value:   "",
				Rule:    "required",
				Message: "field is required",
			},
			contains: []string{"validation error", "receiver", "required", "field is required"},
		},
		{
			name: "range error",
			err: ValidationError{
				Field:   "group_wait",
				Value:   "2h",
				Rule:    "range",
				Message: "value out of range",
			},
			contains: []string{"validation error", "group_wait", "range", "value out of range", "2h"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := tt.err.Error()
			for _, substr := range tt.contains {
				assert.Contains(t, msg, substr)
			}
		})
	}
}

// TestNewValidationError tests ValidationError constructor
func TestNewValidationError(t *testing.T) {
	// NewValidationError signature: (field, message, value, ...rule)
	err := NewValidationError("field1", "message1", "value1", "rule1")

	assert.Equal(t, "field1", err.Field)
	assert.Equal(t, "value1", err.Value)
	assert.Equal(t, "rule1", err.Rule)
	assert.Equal(t, "message1", err.Message)
}

// TestValidationErrors_Error tests ValidationErrors error message
func TestValidationErrors_Error(t *testing.T) {
	tests := []struct {
		name     string
		errors   ValidationErrors
		contains []string
	}{
		{
			name:     "empty errors",
			errors:   ValidationErrors{},
			contains: []string{"no validation errors"},
		},
		{
			name: "single error",
			errors: ValidationErrors{
				{
					Field:   "receiver",
					Value:   "",
					Rule:    "required",
					Message: "receiver is required",
				},
			},
			// Single error returns ValidationError.Error() directly
			contains: []string{"validation error", "receiver", "receiver is required"},
		},
		{
			name: "multiple errors",
			errors: ValidationErrors{
				{
					Field:   "receiver",
					Value:   "",
					Rule:    "required",
					Message: "receiver is required",
				},
				{
					Field:   "group_by",
					Value:   "invalid-label",
					Rule:    "labelname",
					Message: "invalid label name",
				},
				{
					Field:   "group_wait",
					Value:   "-10s",
					Rule:    "range",
					Message: "must be non-negative",
				},
			},
			// Multiple errors returns "multiple validation errors (N): <first error>"
			contains: []string{
				"multiple validation errors (3)",
				"receiver is required", // First error only
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := tt.errors.Error()
			for _, substr := range tt.contains {
				assert.Contains(t, msg, substr)
			}
		})
	}
}

// TestValidationErrors_Add tests adding errors
func TestValidationErrors_Add(t *testing.T) {
	var errors ValidationErrors

	assert.Equal(t, 0, errors.Count())
	assert.False(t, errors.HasErrors())

	errors.Add("field1", "value1", "rule1", "message1")
	assert.Equal(t, 1, errors.Count())
	assert.True(t, errors.HasErrors())

	errors.Add("field2", "value2", "rule2", "message2")
	assert.Equal(t, 2, errors.Count())

	assert.Equal(t, "field1", errors[0].Field)
	assert.Equal(t, "field2", errors[1].Field)
}

// TestValidationErrors_AddError tests adding ValidationError
func TestValidationErrors_AddError(t *testing.T) {
	var errors ValidationErrors

	err1 := NewValidationError("field1", "value1", "rule1", "message1")
	err2 := NewValidationError("field2", "value2", "rule2", "message2")

	errors.AddError(err1)
	assert.Equal(t, 1, errors.Count())

	errors.AddError(err2)
	assert.Equal(t, 2, errors.Count())

	assert.Equal(t, err1, errors[0])
	assert.Equal(t, err2, errors[1])
}

// TestValidationErrors_HasErrors tests error detection
func TestValidationErrors_HasErrors(t *testing.T) {
	var errors ValidationErrors

	assert.False(t, errors.HasErrors())

	errors.Add("field", "value", "rule", "message")
	assert.True(t, errors.HasErrors())
}

// TestValidationErrors_Count tests error counting
func TestValidationErrors_Count(t *testing.T) {
	var errors ValidationErrors

	assert.Equal(t, 0, errors.Count())

	errors.Add("field1", "value1", "rule1", "message1")
	assert.Equal(t, 1, errors.Count())

	errors.Add("field2", "value2", "rule2", "message2")
	assert.Equal(t, 2, errors.Count())

	errors.Add("field3", "value3", "rule3", "message3")
	assert.Equal(t, 3, errors.Count())
}

// TestValidationErrors_ToError tests error conversion
func TestValidationErrors_ToError(t *testing.T) {
	tests := []struct {
		name    string
		errors  ValidationErrors
		wantNil bool
	}{
		{
			name:    "empty errors",
			errors:  ValidationErrors{},
			wantNil: true,
		},
		{
			name: "with errors",
			errors: ValidationErrors{
				{Field: "field1", Value: "value1", Rule: "rule1", Message: "message1"},
			},
			wantNil: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.errors.ToError()
			if tt.wantNil {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

// TestConfigError_Error tests ConfigError error message
func TestConfigError_Error(t *testing.T) {
	tests := []struct {
		name     string
		err      *ConfigError
		contains []string
	}{
		{
			name: "full error",
			err: &ConfigError{
				Message: "invalid configuration",
				Source:  "/path/to/config.yaml",
				Err:     errors.New("parse failed"),
			},
			// Note: ConfigError uses "config error" not "configuration error"
			contains: []string{"config error", "/path/to/config.yaml", "invalid configuration", "parse failed"},
		},
		{
			name: "error without source",
			err: &ConfigError{
				Message: "invalid configuration",
				Err:     errors.New("parse failed"),
			},
			contains: []string{"config error", "invalid configuration", "parse failed"},
		},
		{
			name: "error without inner error",
			err: &ConfigError{
				Message: "invalid configuration",
				Source:  "/path/to/config.yaml",
			},
			contains: []string{"config error", "/path/to/config.yaml", "invalid configuration"},
		},
		{
			name: "minimal error",
			err: &ConfigError{
				Message: "error",
			},
			contains: []string{"config error", "error"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := tt.err.Error()
			for _, substr := range tt.contains {
				assert.Contains(t, msg, substr)
			}
		})
	}
}

// TestConfigError_Unwrap tests ConfigError unwrapping
func TestConfigError_Unwrap(t *testing.T) {
	innerErr := errors.New("inner error")
	configErr := &ConfigError{
		Message: "config error",
		Err:     innerErr,
	}

	unwrapped := configErr.Unwrap()
	assert.Equal(t, innerErr, unwrapped)

	// Test with nil inner error
	configErr2 := &ConfigError{
		Message: "config error",
	}
	assert.Nil(t, configErr2.Unwrap())
}

// TestNewConfigError tests ConfigError constructor
func TestNewConfigError(t *testing.T) {
	innerErr := errors.New("test error")
	configErr := NewConfigError("message1", "source1", innerErr)

	assert.NotNil(t, configErr)
	assert.Equal(t, "message1", configErr.Message)
	assert.Equal(t, "source1", configErr.Source)
	assert.Equal(t, innerErr, configErr.Err)
}

// TestErrorChaining tests error chaining with errors.Is and errors.As
func TestErrorChaining(t *testing.T) {
	innerErr := errors.New("inner error")

	// Test ParseError chaining
	parseErr := &ParseError{
		Field: "test",
		Err:   innerErr,
	}
	assert.True(t, errors.Is(parseErr, innerErr))

	// Test ConfigError chaining
	configErr := &ConfigError{
		Message: "config error",
		Err:     innerErr,
	}
	assert.True(t, errors.Is(configErr, innerErr))

	// Test errors.As with ParseError
	var pe *ParseError
	assert.True(t, errors.As(parseErr, &pe))
	assert.Equal(t, "test", pe.Field)

	// Test errors.As with ConfigError
	var ce *ConfigError
	assert.True(t, errors.As(configErr, &ce))
	assert.Equal(t, "config error", ce.Message)
}

// TestValidationErrors_ComplexScenarios tests complex error scenarios
func TestValidationErrors_ComplexScenarios(t *testing.T) {
	var errors ValidationErrors

	// Add multiple errors
	errors.Add("receiver", "", "required", "receiver is required")
	errors.Add("group_by[0]", "invalid-label", "labelname", "invalid label name")
	errors.Add("group_wait", "-10s", "range", "must be non-negative")
	errors.Add("routes[0].receiver", "", "required", "nested receiver is required")
	errors.Add("routes[1].group_interval", "500ms", "range", "must be at least 1s")

	assert.Equal(t, 5, errors.Count())
	assert.True(t, errors.HasErrors())

	msg := errors.Error()
	// ValidationErrors.Error() returns "multiple validation errors (N): <first error>"
	assert.Contains(t, msg, "multiple validation errors (5)")
	assert.Contains(t, msg, "receiver is required") // First error message

	err := errors.ToError()
	require.Error(t, err)

	// Test that error is of type ValidationErrors
	ve, ok := err.(ValidationErrors)
	assert.True(t, ok)
	assert.Equal(t, 5, ve.Count())
}

// TestErrorTypes tests error type assertions
func TestErrorTypes(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		isParseErr   bool
		isValidationErr bool
		isConfigErr  bool
	}{
		{
			name:      "ParseError",
			err:       &ParseError{Field: "test"},
			isParseErr:   true,
			isValidationErr: false,
			isConfigErr:  false,
		},
		{
			name:      "ValidationErrors",
			err:       ValidationErrors{{Field: "test"}},
			isParseErr:   false,
			isValidationErr: true,
			isConfigErr:  false,
		},
		{
			name:      "ConfigError",
			err:       &ConfigError{Message: "test"},
			isParseErr:   false,
			isValidationErr: false,
			isConfigErr:  true,
		},
		{
			name:      "generic error",
			err:       errors.New("generic"),
			isParseErr:   false,
			isValidationErr: false,
			isConfigErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var pe *ParseError
			var ve ValidationErrors
			var ce *ConfigError

			assert.Equal(t, tt.isParseErr, errors.As(tt.err, &pe))
			assert.Equal(t, tt.isValidationErr, errors.As(tt.err, &ve))
			assert.Equal(t, tt.isConfigErr, errors.As(tt.err, &ce))
		})
	}
}
