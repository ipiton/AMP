package routing

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidationError_Error(t *testing.T) {
	tests := []struct {
		name     string
		err      *ValidationError
		contains []string
	}{
		{
			name: "with suggestion",
			err: &ValidationError{
				Field:      "receivers[0].name",
				Message:    "required field missing",
				Suggestion: "Add a name field",
			},
			contains: []string{"receivers[0].name", "required field missing", "Add a name field"},
		},
		{
			name: "without suggestion",
			err: &ValidationError{
				Field:   "route.receiver",
				Message: "receiver not found",
			},
			contains: []string{"route.receiver", "receiver not found"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.err.Error()

			for _, expected := range tt.contains {
				assert.Contains(t, result, expected)
			}
		})
	}
}

func TestValidationErrors_Add(t *testing.T) {
	var errors ValidationErrors

	errors.Add("field1", "error1", "suggestion1")
	errors.Add("field2", "error2", "")

	assert.Len(t, errors, 2)
	assert.Equal(t, "field1", errors[0].Field)
	assert.Equal(t, "error1", errors[0].Message)
	assert.Equal(t, "suggestion1", errors[0].Suggestion)
	assert.Equal(t, "field2", errors[1].Field)
}

func TestValidationErrors_HasErrors(t *testing.T) {
	var emptyErrors ValidationErrors
	assert.False(t, emptyErrors.HasErrors())

	var withErrors ValidationErrors
	withErrors.Add("field", "error", "")
	assert.True(t, withErrors.HasErrors())
}

func TestValidationErrors_ErrType(t *testing.T) {
	var emptyErrors ValidationErrors
	assert.Nil(t, emptyErrors.ErrType())

	var withErrors ValidationErrors
	withErrors.Add("field", "error", "")
	assert.NotNil(t, withErrors.ErrType())
	assert.Error(t, withErrors.ErrType())
}

func TestValidationErrors_Error(t *testing.T) {
	var errors ValidationErrors
	errors.Add("field1", "error1", "")
	errors.Add("field2", "error2", "suggestion2")

	result := errors.Error()

	assert.Contains(t, result, "validation failed")
	assert.Contains(t, result, "field1")
	assert.Contains(t, result, "field2")
}
