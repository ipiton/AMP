package ui

import (
	"strings"
	"testing"
	"time"
)

// TestFormatTime tests time formatting function.
func TestFormatTime(t *testing.T) {
	testTime := time.Date(2025, 11, 19, 14, 30, 45, 0, time.UTC)
	result := formatTime(testTime)
	expected := "2025-11-19 14:30:45"

	if result != expected {
		t.Errorf("formatTime() = %q, want %q", result, expected)
	}
}

// TestTimeAgo tests relative time formatting.
func TestTimeAgo(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name     string
		time     time.Time
		expected string
	}{
		{
			name:     "just now",
			time:     now.Add(-30 * time.Second),
			expected: "just now",
		},
		{
			name:     "minutes ago",
			time:     now.Add(-5 * time.Minute),
			expected: "5 minutes ago",
		},
		{
			name:     "1 minute ago (singular)",
			time:     now.Add(-1 * time.Minute),
			expected: "1 minute ago",
		},
		{
			name:     "hours ago",
			time:     now.Add(-3 * time.Hour),
			expected: "3 hours ago",
		},
		{
			name:     "1 hour ago (singular)",
			time:     now.Add(-1 * time.Hour),
			expected: "1 hour ago",
		},
		{
			name:     "days ago",
			time:     now.Add(-2 * 24 * time.Hour),
			expected: "2 days ago",
		},
		{
			name:     "1 day ago (singular)",
			time:     now.Add(-24 * time.Hour),
			expected: "1 day ago",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := timeAgo(tt.time)
			if result != tt.expected {
				t.Errorf("timeAgo() = %q, want %q", result, tt.expected)
			}
		})
	}
}

// TestSeverity tests CSS class generation for severity.
func TestSeverity(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"critical", "badge-critical"},
		{"CRITICAL", "badge-critical"},
		{"warning", "badge-warning"},
		{"Warning", "badge-warning"},
		{"info", "badge-info"},
		{"INFO", "badge-info"},
		{"unknown", "badge-default"},
		{"", "badge-default"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := severity(tt.input)
			if result != tt.expected {
				t.Errorf("severity(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// TestStatusClass tests CSS class generation for status.
func TestStatusClass(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"firing", "status-firing"},
		{"FIRING", "status-firing"},
		{"resolved", "status-resolved"},
		{"Resolved", "status-resolved"},
		{"pending", "status-pending"},
		{"PENDING", "status-pending"},
		{"unknown", "status-unknown"},
		{"", "status-unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := statusClass(tt.input)
			if result != tt.expected {
				t.Errorf("statusClass(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// TestTruncate tests string truncation.
func TestTruncate(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxLen   int
		expected string
	}{
		{
			name:     "no truncation needed",
			input:    "short",
			maxLen:   10,
			expected: "short",
		},
		{
			name:     "truncate long string",
			input:    "This is a very long string that needs truncation",
			maxLen:   20,
			expected: "This is a very lo...",
		},
		{
			name:     "exact length",
			input:    "exactly",
			maxLen:   7,
			expected: "exactly",
		},
		{
			name:     "very short maxLen",
			input:    "test",
			maxLen:   2,
			expected: "te",
		},
		{
			name:     "empty string",
			input:    "",
			maxLen:   10,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncate(tt.input, tt.maxLen)
			if result != tt.expected {
				t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, result, tt.expected)
			}
		})
	}
}

// TestJSONPretty tests JSON pretty printing.
func TestJSONPretty(t *testing.T) {
	input := map[string]interface{}{
		"name":  "test",
		"count": 42,
	}

	result := jsonPretty(input)

	// Should contain formatted JSON
	if !strings.Contains(result, "\"name\"") {
		t.Error("Expected JSON to contain \"name\" field")
	}
	if !strings.Contains(result, "\"count\"") {
		t.Error("Expected JSON to contain \"count\" field")
	}
}

// TestUpper tests uppercase conversion.
func TestUpper(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "HELLO"},
		{"WORLD", "WORLD"},
		{"MiXeD", "MIXED"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := upper(tt.input)
			if result != tt.expected {
				t.Errorf("upper(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// TestLower tests lowercase conversion.
func TestLower(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"HELLO", "hello"},
		{"world", "world"},
		{"MiXeD", "mixed"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := lower(tt.input)
			if result != tt.expected {
				t.Errorf("lower(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// TestDefaultVal tests default value function.
func TestDefaultVal(t *testing.T) {
	tests := []struct {
		name     string
		def      interface{}
		val      interface{}
		expected interface{}
	}{
		{
			name:     "nil value",
			def:      "default",
			val:      nil,
			expected: "default",
		},
		{
			name:     "empty string",
			def:      "default",
			val:      "",
			expected: "default",
		},
		{
			name:     "zero int",
			def:      100,
			val:      0,
			expected: 100,
		},
		{
			name:     "non-empty string",
			def:      "default",
			val:      "value",
			expected: "value",
		},
		{
			name:     "non-zero int",
			def:      100,
			val:      42,
			expected: 42,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := defaultVal(tt.def, tt.val)
			if result != tt.expected {
				t.Errorf("defaultVal(%v, %v) = %v, want %v", tt.def, tt.val, result, tt.expected)
			}
		})
	}
}

// TestJoin tests slice joining.
func TestJoin(t *testing.T) {
	tests := []struct {
		name     string
		slice    []string
		sep      string
		expected string
	}{
		{
			name:     "comma separated",
			slice:    []string{"a", "b", "c"},
			sep:      ", ",
			expected: "a, b, c",
		},
		{
			name:     "dash separated",
			slice:    []string{"one", "two"},
			sep:      "-",
			expected: "one-two",
		},
		{
			name:     "empty slice",
			slice:    []string{},
			sep:      ", ",
			expected: "",
		},
		{
			name:     "single element",
			slice:    []string{"only"},
			sep:      ", ",
			expected: "only",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := join(tt.slice, tt.sep)
			if result != tt.expected {
				t.Errorf("join(%v, %q) = %q, want %q", tt.slice, tt.sep, result, tt.expected)
			}
		})
	}
}

// TestContains tests slice contains check.
func TestContains(t *testing.T) {
	slice := []string{"admin", "editor", "viewer"}

	tests := []struct {
		item     string
		expected bool
	}{
		{"admin", true},
		{"editor", true},
		{"viewer", true},
		{"superuser", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.item, func(t *testing.T) {
			result := contains(slice, tt.item)
			if result != tt.expected {
				t.Errorf("contains(%v, %q) = %v, want %v", slice, tt.item, result, tt.expected)
			}
		})
	}
}

// TestAdd tests addition function.
func TestAdd(t *testing.T) {
	tests := []struct {
		a, b     int
		expected int
	}{
		{1, 2, 3},
		{0, 0, 0},
		{-5, 10, 5},
		{100, -50, 50},
	}

	for _, tt := range tests {
		result := add(tt.a, tt.b)
		if result != tt.expected {
			t.Errorf("add(%d, %d) = %d, want %d", tt.a, tt.b, result, tt.expected)
		}
	}
}

// TestSub tests subtraction function.
func TestSub(t *testing.T) {
	tests := []struct {
		a, b     int
		expected int
	}{
		{5, 3, 2},
		{0, 0, 0},
		{10, 15, -5},
		{100, 50, 50},
	}

	for _, tt := range tests {
		result := sub(tt.a, tt.b)
		if result != tt.expected {
			t.Errorf("sub(%d, %d) = %d, want %d", tt.a, tt.b, result, tt.expected)
		}
	}
}

// TestMul tests multiplication function.
func TestMul(t *testing.T) {
	tests := []struct {
		a, b     int
		expected int
	}{
		{2, 3, 6},
		{0, 100, 0},
		{-5, 4, -20},
		{7, 7, 49},
	}

	for _, tt := range tests {
		result := mul(tt.a, tt.b)
		if result != tt.expected {
			t.Errorf("mul(%d, %d) = %d, want %d", tt.a, tt.b, result, tt.expected)
		}
	}
}

// TestDiv tests division function.
func TestDiv(t *testing.T) {
	tests := []struct {
		name     string
		a, b     int
		expected int
	}{
		{
			name:     "normal division",
			a:        10,
			b:        2,
			expected: 5,
		},
		{
			name:     "division by zero",
			a:        10,
			b:        0,
			expected: 0,
		},
		{
			name:     "zero dividend",
			a:        0,
			b:        5,
			expected: 0,
		},
		{
			name:     "negative division",
			a:        -10,
			b:        2,
			expected: -5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := div(tt.a, tt.b)
			if result != tt.expected {
				t.Errorf("div(%d, %d) = %d, want %d", tt.a, tt.b, result, tt.expected)
			}
		})
	}
}

// TestPlural tests pluralization helper.
func TestPlural(t *testing.T) {
	tests := []struct {
		count    int
		expected string
	}{
		{0, "s"},
		{1, ""},
		{2, "s"},
		{100, "s"},
		{-1, "s"},
	}

	for _, tt := range tests {
		result := plural(tt.count)
		if result != tt.expected {
			t.Errorf("plural(%d) = %q, want %q", tt.count, result, tt.expected)
		}
	}
}

// TestCreateTemplateFuncs tests that all functions are registered.
func TestCreateTemplateFuncs(t *testing.T) {
	funcs := createTemplateFuncs()

	expectedFuncs := []string{
		"formatTime", "timeAgo",
		"severity", "statusClass",
		"truncate", "jsonPretty", "upper", "lower",
		"defaultVal", "join", "contains",
		"add", "sub", "mul", "div",
		"plural",
	}

	for _, name := range expectedFuncs {
		if _, ok := funcs[name]; !ok {
			t.Errorf("Expected function %q to be registered", name)
		}
	}

	// Verify we have at least 15 functions
	if len(funcs) < 15 {
		t.Errorf("Expected at least 15 functions, got %d", len(funcs))
	}
}
