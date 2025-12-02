package grouping

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGenerateKey_BasicGrouping tests basic grouping with single label
func TestGenerateKey_BasicGrouping(t *testing.T) {
	gen := NewGroupKeyGenerator()

	labels := map[string]string{
		"alertname": "HighCPU",
		"instance":  "server1",
		"cluster":   "prod",
	}
	groupBy := []string{"alertname"}

	key, err := gen.GenerateKey(labels, groupBy)
	require.NoError(t, err)
	assert.Equal(t, GroupKey("alertname=HighCPU"), key)
}

// TestGenerateKey_MultipleLabels tests grouping by multiple labels
func TestGenerateKey_MultipleLabels(t *testing.T) {
	gen := NewGroupKeyGenerator()

	labels := map[string]string{
		"alertname": "HighCPU",
		"cluster":   "prod",
		"instance":  "server1",
	}
	groupBy := []string{"alertname", "cluster"}

	key, err := gen.GenerateKey(labels, groupBy)
	require.NoError(t, err)
	// Labels should be sorted alphabetically
	assert.Equal(t, GroupKey("alertname=HighCPU,cluster=prod"), key)
}

// TestGenerateKey_LabelSorting tests that labels are sorted alphabetically
func TestGenerateKey_LabelSorting(t *testing.T) {
	gen := NewGroupKeyGenerator()

	labels := map[string]string{
		"instance":  "server1",
		"alertname": "HighCPU",
		"cluster":   "prod",
	}
	// groupBy in different order
	groupBy := []string{"cluster", "alertname", "instance"}

	key, err := gen.GenerateKey(labels, groupBy)
	require.NoError(t, err)
	// Should be sorted: alertname, cluster, instance
	assert.Equal(t, GroupKey("alertname=HighCPU,cluster=prod,instance=server1"), key)
}

// TestGenerateKey_SpecialGrouping tests special grouping with "..."
func TestGenerateKey_SpecialGrouping(t *testing.T) {
	gen := NewGroupKeyGenerator()

	labels := map[string]string{
		"alertname": "HighCPU",
		"cluster":   "prod",
		"instance":  "server1",
	}
	groupBy := []string{"..."}

	key, err := gen.GenerateKey(labels, groupBy)
	require.NoError(t, err)
	// Should include ALL labels, sorted
	assert.Equal(t, GroupKey("alertname=HighCPU,cluster=prod,instance=server1"), key)
}

// TestGenerateKey_GlobalGrouping tests global grouping with empty groupBy
func TestGenerateKey_GlobalGrouping(t *testing.T) {
	gen := NewGroupKeyGenerator()

	labels := map[string]string{
		"alertname": "HighCPU",
		"cluster":   "prod",
	}
	groupBy := []string{}

	key, err := gen.GenerateKey(labels, groupBy)
	require.NoError(t, err)
	assert.Equal(t, GlobalGroupKey, key)
}

// TestGenerateKey_MissingLabels tests handling of missing labels
func TestGenerateKey_MissingLabels(t *testing.T) {
	gen := NewGroupKeyGenerator()

	labels := map[string]string{
		"alertname": "HighCPU",
		// No 'cluster' label
	}
	groupBy := []string{"alertname", "cluster"}

	key, err := gen.GenerateKey(labels, groupBy)
	require.NoError(t, err)
	assert.Equal(t, GroupKey("alertname=HighCPU,cluster=<missing>"), key)
}

// TestGenerateKey_EmptyLabelValue tests handling of empty label values
func TestGenerateKey_EmptyLabelValue(t *testing.T) {
	gen := NewGroupKeyGenerator()

	labels := map[string]string{
		"alertname": "HighCPU",
		"cluster":   "", // Empty value
	}
	groupBy := []string{"alertname", "cluster"}

	key, err := gen.GenerateKey(labels, groupBy)
	require.NoError(t, err)
	assert.Equal(t, GroupKey("alertname=HighCPU,cluster="), key)
}

// TestGenerateKey_SpecialCharacters tests URL encoding of special characters
func TestGenerateKey_SpecialCharacters(t *testing.T) {
	gen := NewGroupKeyGenerator()

	tests := []struct {
		name     string
		value    string
		expected string
	}{
		{
			name:     "space",
			value:    "my team",
			expected: "alertname=my+team",
		},
		{
			name:     "comma",
			value:    "value,with,commas",
			expected: "alertname=value%2Cwith%2Ccommas",
		},
		{
			name:     "equals",
			value:    "key=value",
			expected: "alertname=key%3Dvalue",
		},
		{
			name:     "unicode",
			value:    "тест",
			expected: "alertname=%D1%82%D0%B5%D1%81%D1%82",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			labels := map[string]string{
				"alertname": tt.value,
			}
			groupBy := []string{"alertname"}

			key, err := gen.GenerateKey(labels, groupBy)
			require.NoError(t, err)
			assert.Equal(t, GroupKey(tt.expected), key)
		})
	}
}

// TestGenerateKey_NilLabels tests handling of nil labels
func TestGenerateKey_NilLabels(t *testing.T) {
	gen := NewGroupKeyGenerator()

	key, err := gen.GenerateKey(nil, []string{"alertname"})
	require.NoError(t, err)
	assert.Equal(t, EmptyGroupKey, key)
}

// TestGenerateKey_EmptyLabelsMap tests handling of empty labels map
func TestGenerateKey_EmptyLabelsMap(t *testing.T) {
	gen := NewGroupKeyGenerator()

	labels := map[string]string{}
	groupBy := []string{"alertname"}

	key, err := gen.GenerateKey(labels, groupBy)
	require.NoError(t, err)
	assert.Equal(t, EmptyGroupKey, key)
}

// TestGenerateKey_VeryLongValue tests handling of very long label values
func TestGenerateKey_VeryLongValue(t *testing.T) {
	gen := NewGroupKeyGenerator()

	longValue := strings.Repeat("a", 1000)
	labels := map[string]string{
		"alertname": longValue,
	}
	groupBy := []string{"alertname"}

	key, err := gen.GenerateKey(labels, groupBy)
	require.NoError(t, err)
	assert.Contains(t, string(key), longValue)
	assert.Greater(t, len(string(key)), 1000)
}

// TestGenerateKey_Determinism tests that same input produces same output
func TestGenerateKey_Determinism(t *testing.T) {
	gen := NewGroupKeyGenerator()

	labels := map[string]string{
		"alertname": "HighCPU",
		"cluster":   "prod",
		"instance":  "server1",
	}
	groupBy := []string{"alertname", "cluster"}

	// Generate key multiple times
	keys := make([]GroupKey, 100)
	for i := 0; i < 100; i++ {
		key, err := gen.GenerateKey(labels, groupBy)
		require.NoError(t, err)
		keys[i] = key
	}

	// All keys should be identical
	for i := 1; i < len(keys); i++ {
		assert.Equal(t, keys[0], keys[i], "Keys should be deterministic")
	}
}

// TestGenerateKey_LabelOrderIndependence tests that label order doesn't affect key
func TestGenerateKey_LabelOrderIndependence(t *testing.T) {
	gen := NewGroupKeyGenerator()

	// Same labels, different insertion order
	labels1 := map[string]string{
		"alertname": "HighCPU",
		"cluster":   "prod",
		"instance":  "server1",
	}

	labels2 := map[string]string{
		"instance":  "server1",
		"alertname": "HighCPU",
		"cluster":   "prod",
	}

	labels3 := map[string]string{
		"cluster":   "prod",
		"instance":  "server1",
		"alertname": "HighCPU",
	}

	groupBy := []string{"alertname", "cluster", "instance"}

	key1, err1 := gen.GenerateKey(labels1, groupBy)
	key2, err2 := gen.GenerateKey(labels2, groupBy)
	key3, err3 := gen.GenerateKey(labels3, groupBy)

	require.NoError(t, err1)
	require.NoError(t, err2)
	require.NoError(t, err3)

	assert.Equal(t, key1, key2, "Label order should not affect key")
	assert.Equal(t, key2, key3, "Label order should not affect key")
}

// TestGenerateKey_DifferentLabels tests that different labels produce different keys
func TestGenerateKey_DifferentLabels(t *testing.T) {
	gen := NewGroupKeyGenerator()

	labels1 := map[string]string{
		"alertname": "HighCPU",
		"cluster":   "prod",
	}

	labels2 := map[string]string{
		"alertname": "HighCPU",
		"cluster":   "staging",
	}

	groupBy := []string{"alertname", "cluster"}

	key1, err1 := gen.GenerateKey(labels1, groupBy)
	key2, err2 := gen.GenerateKey(labels2, groupBy)

	require.NoError(t, err1)
	require.NoError(t, err2)

	assert.NotEqual(t, key1, key2, "Different labels should produce different keys")
}

// TestGenerateHash tests hash generation
func TestGenerateHash(t *testing.T) {
	gen := NewGroupKeyGenerator()

	labels := map[string]string{
		"alertname": "HighCPU",
		"cluster":   "prod",
	}
	groupBy := []string{"alertname", "cluster"}

	hash, err := gen.GenerateHash(labels, groupBy)
	require.NoError(t, err)
	assert.Len(t, hash, 16, "Hash should be 16 hex characters")
	assert.Regexp(t, "^[0-9a-f]{16}$", hash, "Hash should be hexadecimal")
}

// TestGenerateHash_Deterministic tests that hash is deterministic
func TestGenerateHash_Deterministic(t *testing.T) {
	gen := NewGroupKeyGenerator()

	labels := map[string]string{
		"alertname": "HighCPU",
		"cluster":   "prod",
	}
	groupBy := []string{"alertname", "cluster"}

	hash1, err1 := gen.GenerateHash(labels, groupBy)
	hash2, err2 := gen.GenerateHash(labels, groupBy)

	require.NoError(t, err1)
	require.NoError(t, err2)

	assert.Equal(t, hash1, hash2, "Hash should be deterministic")
}

// TestWithHashLongKeys tests automatic hashing of long keys
func TestWithHashLongKeys(t *testing.T) {
	gen := NewGroupKeyGenerator(
		WithHashLongKeys(true),
		WithMaxKeyLength(50),
	)

	// Create labels that will produce a long key
	labels := map[string]string{
		"alertname": "VeryLongAlertNameThatExceedsMaxLength",
		"cluster":   "production-us-east-1-cluster",
		"instance":  "server-with-very-long-name-12345",
	}
	groupBy := []string{"alertname", "cluster", "instance"}

	key, err := gen.GenerateKey(labels, groupBy)
	require.NoError(t, err)

	// Should be hashed
	assert.True(t, strings.HasPrefix(string(key), "{hash:"), "Long key should be hashed")
	assert.Len(t, string(key), 23, "Hashed key should be {hash:16hexchars}")
}

// TestWithValidation tests label validation
func TestWithValidation(t *testing.T) {
	gen := NewGroupKeyGenerator(WithValidation(true))

	tests := []struct {
		name      string
		labels    map[string]string
		groupBy   []string
		shouldErr bool
	}{
		{
			name: "valid labels",
			labels: map[string]string{
				"alertname": "HighCPU",
				"cluster":   "prod",
			},
			groupBy:   []string{"alertname"},
			shouldErr: false,
		},
		{
			name: "invalid label with dash",
			labels: map[string]string{
				"alert-name": "HighCPU",
			},
			groupBy:   []string{"alert-name"},
			shouldErr: true,
		},
		{
			name: "invalid label starting with digit",
			labels: map[string]string{
				"1alert": "HighCPU",
			},
			groupBy:   []string{"1alert"},
			shouldErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := gen.GenerateKey(tt.labels, tt.groupBy)
			if tt.shouldErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestGroupKey_IsSpecial tests IsSpecial method
func TestGroupKey_IsSpecial(t *testing.T) {
	tests := []struct {
		key      GroupKey
		expected bool
	}{
		{GlobalGroupKey, true},
		{EmptyGroupKey, true},
		{GroupKey("{hash:a1b2c3d4e5f60708}"), true},
		{GroupKey("alertname=HighCPU"), false},
		{GroupKey("alertname=HighCPU,cluster=prod"), false},
	}

	for _, tt := range tests {
		t.Run(string(tt.key), func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.key.IsSpecial())
		})
	}
}

// TestGroupKey_Matches tests Matches method
func TestGroupKey_Matches(t *testing.T) {
	gen := NewGroupKeyGenerator()

	key := GroupKey("alertname=HighCPU,cluster=prod")

	tests := []struct {
		name     string
		labels   map[string]string
		groupBy  []string
		expected bool
	}{
		{
			name: "exact match",
			labels: map[string]string{
				"alertname": "HighCPU",
				"cluster":   "prod",
				"instance":  "server1",
			},
			groupBy:  []string{"alertname", "cluster"},
			expected: true,
		},
		{
			name: "different cluster",
			labels: map[string]string{
				"alertname": "HighCPU",
				"cluster":   "staging",
			},
			groupBy:  []string{"alertname", "cluster"},
			expected: false,
		},
		{
			name: "different alertname",
			labels: map[string]string{
				"alertname": "DiskFull",
				"cluster":   "prod",
			},
			groupBy:  []string{"alertname", "cluster"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matches := key.Matches(tt.labels, tt.groupBy, gen)
			assert.Equal(t, tt.expected, matches)
		})
	}
}

// TestGenerateKeyOrDefault tests graceful fallback
func TestGenerateKeyOrDefault(t *testing.T) {
	gen := NewGroupKeyGenerator(WithValidation(true))

	// Valid input
	labels := map[string]string{
		"alertname": "HighCPU",
	}
	groupBy := []string{"alertname"}

	key := gen.GenerateKeyOrDefault(labels, groupBy)
	assert.Equal(t, GroupKey("alertname=HighCPU"), key)

	// Invalid input (with validation enabled)
	invalidLabels := map[string]string{
		"alert-name": "HighCPU", // Invalid label name
	}

	key = gen.GenerateKeyOrDefault(invalidLabels, []string{"alert-name"})
	assert.Equal(t, GlobalGroupKey, key, "Should fallback to GlobalGroupKey on error")
}

// TestConcurrentAccess tests thread-safety
func TestConcurrentAccess(t *testing.T) {
	gen := NewGroupKeyGenerator()

	const (
		goroutines = 100
		iterations = 1000
	)

	var wg sync.WaitGroup
	errors := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				labels := map[string]string{
					"alertname": fmt.Sprintf("Alert%d", j),
					"instance":  fmt.Sprintf("server-%d", id),
				}
				groupBy := []string{"alertname"}

				_, err := gen.GenerateKey(labels, groupBy)
				if err != nil {
					errors <- err
					return
				}
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("Concurrent access error: %v", err)
	}
}

// TestHashFromKey tests HashFromKey convenience function
func TestHashFromKey(t *testing.T) {
	key := GroupKey("alertname=HighCPU,cluster=prod")
	hash := HashFromKey(key)

	assert.Len(t, hash, 16, "Hash should be 16 hex characters")
	assert.Regexp(t, "^[0-9a-f]{16}$", hash, "Hash should be hexadecimal")

	// Should be deterministic
	hash2 := HashFromKey(key)
	assert.Equal(t, hash, hash2, "Hash should be deterministic")
}

// TestGroupKey_String tests String method
func TestGroupKey_String(t *testing.T) {
	key := GroupKey("alertname=HighCPU")
	assert.Equal(t, "alertname=HighCPU", key.String())
}
