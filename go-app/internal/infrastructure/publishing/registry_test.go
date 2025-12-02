package publishing

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ipiton/AMP/internal/core"
)

// TestNewDefaultFormatRegistry_BuiltinFormats verifies all 5 built-in formats are registered
func TestNewDefaultFormatRegistry_BuiltinFormats(t *testing.T) {
	registry := NewDefaultFormatRegistry()

	// Verify count
	assert.Equal(t, 5, registry.Count(), "Should have 5 built-in formats")

	// Verify each built-in format
	builtinFormats := []core.PublishingFormat{
		core.FormatAlertmanager,
		core.FormatRootly,
		core.FormatPagerDuty,
		core.FormatSlack,
		core.FormatWebhook,
	}

	for _, format := range builtinFormats {
		assert.True(t, registry.Supports(format), "Built-in format %s should be supported", format)

		fn, err := registry.Get(format)
		assert.NoError(t, err, "Should retrieve built-in format %s", format)
		assert.NotNil(t, fn, "Format function should not be nil for %s", format)
	}
}

// TestFormatRegistry_Register_Success tests successful format registration
func TestFormatRegistry_Register_Success(t *testing.T) {
	registry := NewDefaultFormatRegistry()

	// Define custom format
	customFormat := core.PublishingFormat("opsgenie")
	customFn := func(alert *core.EnrichedAlert) (map[string]any, error) {
		return map[string]any{"format": "opsgenie"}, nil
	}

	// Register custom format
	err := registry.Register(customFormat, customFn)
	require.NoError(t, err, "Registration should succeed")

	// Verify format is registered
	assert.True(t, registry.Supports(customFormat), "Custom format should be supported")
	assert.Equal(t, 6, registry.Count(), "Should have 6 formats (5 built-in + 1 custom)")

	// Verify format can be retrieved
	fn, err := registry.Get(customFormat)
	require.NoError(t, err, "Should retrieve custom format")
	require.NotNil(t, fn, "Format function should not be nil")

	// Verify function works
	result, err := fn(createTestEnrichedAlert())
	require.NoError(t, err, "Custom format function should execute")
	assert.Equal(t, "opsgenie", result["format"], "Should return correct format")
}

// TestFormatRegistry_Register_Overwrite tests overwriting existing format
func TestFormatRegistry_Register_Overwrite(t *testing.T) {
	registry := NewDefaultFormatRegistry()

	// Register custom format
	customFormat := core.PublishingFormat("custom")
	originalFn := func(alert *core.EnrichedAlert) (map[string]any, error) {
		return map[string]any{"version": "v1"}, nil
	}

	err := registry.Register(customFormat, originalFn)
	require.NoError(t, err)

	// Overwrite with new implementation
	newFn := func(alert *core.EnrichedAlert) (map[string]any, error) {
		return map[string]any{"version": "v2"}, nil
	}

	err = registry.Register(customFormat, newFn)
	require.NoError(t, err, "Overwrite should succeed")

	// Verify new implementation is used
	fn, _ := registry.Get(customFormat)
	result, _ := fn(createTestEnrichedAlert())
	assert.Equal(t, "v2", result["version"], "Should use new implementation")
}

// TestFormatRegistry_Register_ValidationErrors tests registration validation
func TestFormatRegistry_Register_ValidationErrors(t *testing.T) {
	registry := NewDefaultFormatRegistry()

	testCases := []struct {
		name          string
		format        core.PublishingFormat
		fn            formatFunc
		expectedError string
	}{
		{
			name:          "empty format",
			format:        core.PublishingFormat(""),
			fn:            func(*core.EnrichedAlert) (map[string]any, error) { return nil, nil },
			expectedError: "format cannot be empty",
		},
		{
			name:          "nil function",
			format:        core.PublishingFormat("valid"),
			fn:            nil,
			expectedError: "format function cannot be nil",
		},
		{
			name:          "invalid name (uppercase)",
			format:        core.PublishingFormat("Opsgenie"),
			fn:            func(*core.EnrichedAlert) (map[string]any, error) { return nil, nil },
			expectedError: "format name must match",
		},
		{
			name:          "invalid name (starts with digit)",
			format:        core.PublishingFormat("1format"),
			fn:            func(*core.EnrichedAlert) (map[string]any, error) { return nil, nil },
			expectedError: "format name must match",
		},
		{
			name:          "invalid name (special chars)",
			format:        core.PublishingFormat("format!"),
			fn:            func(*core.EnrichedAlert) (map[string]any, error) { return nil, nil },
			expectedError: "format name must match",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := registry.Register(tc.format, tc.fn)
			require.Error(t, err, "Should return error")
			assert.Contains(t, err.Error(), tc.expectedError, "Error message should match")

			// Verify format was not registered
			assert.False(t, registry.Supports(tc.format), "Invalid format should not be registered")
		})
	}
}

// TestFormatRegistry_Unregister_Success tests successful unregistration
func TestFormatRegistry_Unregister_Success(t *testing.T) {
	registry := NewDefaultFormatRegistry()

	// Register custom format
	customFormat := core.PublishingFormat("temporary")
	customFn := func(alert *core.EnrichedAlert) (map[string]any, error) {
		return map[string]any{}, nil
	}

	err := registry.Register(customFormat, customFn)
	require.NoError(t, err)
	assert.Equal(t, 6, registry.Count())

	// Unregister format
	err = registry.Unregister(customFormat)
	require.NoError(t, err, "Unregistration should succeed")

	// Verify format is removed
	assert.False(t, registry.Supports(customFormat), "Format should no longer be supported")
	assert.Equal(t, 5, registry.Count(), "Count should decrease")

	// Verify Get returns error
	_, err = registry.Get(customFormat)
	require.Error(t, err, "Get should return error after unregistration")
	assert.IsType(t, &NotFoundError{}, err, "Should return NotFoundError")
}

// TestFormatRegistry_Unregister_NotFound tests unregistering non-existent format
func TestFormatRegistry_Unregister_NotFound(t *testing.T) {
	registry := NewDefaultFormatRegistry()

	// Try to unregister non-existent format
	nonExistent := core.PublishingFormat("nonexistent")
	err := registry.Unregister(nonExistent)

	require.Error(t, err, "Should return error")
	assert.IsType(t, &NotFoundError{}, err, "Should return NotFoundError")
	assert.Contains(t, err.Error(), "not found", "Error message should mention not found")
}

// TestFormatRegistry_Unregister_InUse tests unregistering format that's in use
func TestFormatRegistry_Unregister_InUse(t *testing.T) {
	registry := NewDefaultFormatRegistry()

	// Register custom format
	customFormat := core.PublishingFormat("custom")
	customFn := func(alert *core.EnrichedAlert) (map[string]any, error) {
		return map[string]any{}, nil
	}
	_ = registry.Register(customFormat, customFn)

	// Get format (increments reference count)
	fn, err := registry.Get(customFormat)
	require.NoError(t, err)

	// Try to unregister while in use
	err = registry.Unregister(customFormat)
	require.Error(t, err, "Should return error when format is in use")
	assert.IsType(t, &InUseError{}, err, "Should return InUseError")
	assert.Contains(t, err.Error(), "in use", "Error message should mention in use")

	// Execute function to decrement reference count
	_, _ = fn(createTestEnrichedAlert())

	// Now unregistration should succeed
	err = registry.Unregister(customFormat)
	require.NoError(t, err, "Unregistration should succeed after reference released")
}

// TestFormatRegistry_Get_NotFound tests retrieving non-existent format
func TestFormatRegistry_Get_NotFound(t *testing.T) {
	registry := NewDefaultFormatRegistry()

	// Try to get non-existent format
	nonExistent := core.PublishingFormat("nonexistent")
	fn, err := registry.Get(nonExistent)

	assert.Error(t, err, "Should return error")
	assert.Nil(t, fn, "Function should be nil")
	assert.IsType(t, &NotFoundError{}, err, "Should return NotFoundError")
}

// TestFormatRegistry_Get_ReferenceCounting tests reference counting mechanism
func TestFormatRegistry_Get_ReferenceCounting(t *testing.T) {
	registry := NewDefaultFormatRegistry()
	customFormat := core.PublishingFormat("reftest")

	// Register format
	_ = registry.Register(customFormat, func(alert *core.EnrichedAlert) (map[string]any, error) {
		return map[string]any{}, nil
	})

	// Get format multiple times
	fn1, err := registry.Get(customFormat)
	require.NoError(t, err)

	fn2, err := registry.Get(customFormat)
	require.NoError(t, err)

	// Should not be able to unregister (2 references)
	err = registry.Unregister(customFormat)
	assert.Error(t, err, "Should not unregister with active references")

	// Execute first function (decrements count to 1)
	_, _ = fn1(createTestEnrichedAlert())

	// Still should not be able to unregister (1 reference remaining)
	err = registry.Unregister(customFormat)
	assert.Error(t, err, "Should not unregister with 1 active reference")

	// Execute second function (decrements count to 0)
	_, _ = fn2(createTestEnrichedAlert())

	// Now should be able to unregister
	err = registry.Unregister(customFormat)
	assert.NoError(t, err, "Should unregister after all references released")
}

// TestFormatRegistry_Supports tests format support checks
func TestFormatRegistry_Supports(t *testing.T) {
	registry := NewDefaultFormatRegistry()

	// Test built-in formats
	assert.True(t, registry.Supports(core.FormatAlertmanager))
	assert.True(t, registry.Supports(core.FormatRootly))

	// Test non-existent format
	assert.False(t, registry.Supports(core.PublishingFormat("nonexistent")))

	// Register custom format
	customFormat := core.PublishingFormat("custom")
	_ = registry.Register(customFormat, func(*core.EnrichedAlert) (map[string]any, error) { return nil, nil })

	// Test custom format
	assert.True(t, registry.Supports(customFormat))

	// Unregister and test again
	_ = registry.Unregister(customFormat)
	assert.False(t, registry.Supports(customFormat))
}

// TestFormatRegistry_List tests listing registered formats
func TestFormatRegistry_List(t *testing.T) {
	registry := NewDefaultFormatRegistry()

	// Get list of built-in formats
	formats := registry.List()
	assert.Len(t, formats, 5, "Should have 5 built-in formats")

	// Verify sorting (alphabetical)
	assert.Equal(t, core.FormatAlertmanager, formats[0], "First should be alertmanager")

	// Register custom format
	customFormat := core.PublishingFormat("aaa-custom")
	_ = registry.Register(customFormat, func(*core.EnrichedAlert) (map[string]any, error) { return nil, nil })

	// Get updated list
	formats = registry.List()
	assert.Len(t, formats, 6, "Should have 6 formats")
	assert.Equal(t, customFormat, formats[0], "Custom format should be first (alphabetically)")

	// Verify list is a copy (not live view)
	formats[0] = core.PublishingFormat("modified")
	newList := registry.List()
	assert.NotEqual(t, formats[0], newList[0], "List should be a copy, not live view")
}

// TestFormatRegistry_Count tests format count
func TestFormatRegistry_Count(t *testing.T) {
	registry := NewDefaultFormatRegistry()

	// Initial count
	assert.Equal(t, 5, registry.Count(), "Should start with 5 built-in formats")

	// Register custom formats
	for i := 1; i <= 3; i++ {
		format := core.PublishingFormat("custom-" + string(rune('a'+i-1)))
		_ = registry.Register(format, func(*core.EnrichedAlert) (map[string]any, error) { return nil, nil })
	}

	assert.Equal(t, 8, registry.Count(), "Should have 8 formats after registering 3")

	// Unregister one format
	_ = registry.Unregister(core.PublishingFormat("custom-a"))
	assert.Equal(t, 7, registry.Count(), "Should have 7 formats after unregistering 1")
}

// TestFormatRegistry_ThreadSafety tests concurrent access
func TestFormatRegistry_ThreadSafety(t *testing.T) {
	registry := NewDefaultFormatRegistry()

	// Number of goroutines
	const numGoroutines = 10
	const numOperations = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines * 3) // Register, Get, Unregister

	// Concurrent Register operations
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				format := core.PublishingFormat("concurrent-register")
				_ = registry.Register(format, func(*core.EnrichedAlert) (map[string]any, error) {
					return map[string]any{}, nil
				})
			}
		}(i)
	}

	// Concurrent Get operations
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				fn, err := registry.Get(core.FormatAlertmanager)
				if err == nil && fn != nil {
					_, _ = fn(createTestEnrichedAlert())
				}
			}
		}(i)
	}

	// Concurrent Supports/List operations
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				_ = registry.Supports(core.FormatAlertmanager)
				_ = registry.List()
				_ = registry.Count()
			}
		}(i)
	}

	// Wait for all goroutines to complete
	wg.Wait()

	// Verify registry is still functional
	assert.True(t, registry.Supports(core.FormatAlertmanager), "Registry should still be functional")
	assert.GreaterOrEqual(t, registry.Count(), 5, "Should have at least 5 formats")
}

// TestIsValidFormatName tests format name validation
func TestIsValidFormatName(t *testing.T) {
	validNames := []string{
		"alertmanager",
		"rootly",
		"pagerduty",
		"slack",
		"webhook",
		"opsgenie",
		"custom-format",
		"my_format",
		"format123",
		"a",
	}

	invalidNames := []string{
		"",               // empty
		"Alertmanager",   // uppercase first letter
		"1format",        // starts with digit
		"format!",        // special character
		"Format_Name",    // uppercase
		"format space",   // space
		"format.name",    // dot
		"-format",        // starts with hyphen
		"_format",        // starts with underscore
		"format/name",    // slash
	}

	for _, name := range validNames {
		assert.True(t, isValidFormatName(name), "Should be valid: %s", name)
	}

	for _, name := range invalidNames {
		assert.False(t, isValidFormatName(name), "Should be invalid: %s", name)
	}
}
