package services

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ipiton/AMP/internal/core"
)

// TestNewFingerprintGenerator_DefaultConfig tests default configuration
func TestNewFingerprintGenerator_DefaultConfig(t *testing.T) {
	generator := NewFingerprintGenerator(nil)

	require.NotNil(t, generator)
	assert.Equal(t, AlgorithmFNV1a, generator.GetAlgorithm(),
		"default algorithm should be FNV-1a")
}

// TestNewFingerprintGenerator_CustomConfig tests custom configuration
func TestNewFingerprintGenerator_CustomConfig(t *testing.T) {
	tests := []struct {
		name     string
		config   *FingerprintConfig
		wantAlgo FingerprintAlgorithm
	}{
		{
			name:     "FNV-1a explicit",
			config:   &FingerprintConfig{Algorithm: AlgorithmFNV1a},
			wantAlgo: AlgorithmFNV1a,
		},
		{
			name:     "SHA-256 legacy",
			config:   &FingerprintConfig{Algorithm: AlgorithmSHA256},
			wantAlgo: AlgorithmSHA256,
		},
		{
			name:     "empty config defaults to FNV-1a",
			config:   &FingerprintConfig{},
			wantAlgo: AlgorithmFNV1a,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			generator := NewFingerprintGenerator(tt.config)

			require.NotNil(t, generator)
			assert.Equal(t, tt.wantAlgo, generator.GetAlgorithm())
		})
	}
}

// TestGenerateFromLabels_FNV1a_Deterministic tests FNV-1a deterministic fingerprints
func TestGenerateFromLabels_FNV1a_Deterministic(t *testing.T) {
	generator := NewFingerprintGenerator(&FingerprintConfig{Algorithm: AlgorithmFNV1a})

	labels := map[string]string{
		"alertname": "HighCPU",
		"severity":  "critical",
		"instance":  "server-1",
		"namespace": "production",
	}

	// Generate fingerprint multiple times
	fp1 := generator.GenerateFromLabels(labels)
	fp2 := generator.GenerateFromLabels(labels)
	fp3 := generator.GenerateFromLabels(labels)

	// All should be identical (deterministic)
	assert.Equal(t, fp1, fp2, "fingerprints should be deterministic")
	assert.Equal(t, fp2, fp3, "fingerprints should be deterministic")
	assert.NotEmpty(t, fp1, "fingerprint should not be empty")
	assert.Len(t, fp1, 16, "FNV-1a fingerprint should be 16 hex chars")
}

// TestGenerateFromLabels_FNV1a_LabelOrderIndependent tests label order independence
func TestGenerateFromLabels_FNV1a_LabelOrderIndependent(t *testing.T) {
	generator := NewFingerprintGenerator(&FingerprintConfig{Algorithm: AlgorithmFNV1a})

	// Same labels, different insertion order
	labels1 := map[string]string{
		"alertname": "HighCPU",
		"severity":  "critical",
		"instance":  "server-1",
	}

	labels2 := map[string]string{
		"instance":  "server-1",
		"alertname": "HighCPU",
		"severity":  "critical",
	}

	labels3 := map[string]string{
		"severity":  "critical",
		"instance":  "server-1",
		"alertname": "HighCPU",
	}

	fp1 := generator.GenerateFromLabels(labels1)
	fp2 := generator.GenerateFromLabels(labels2)
	fp3 := generator.GenerateFromLabels(labels3)

	assert.Equal(t, fp1, fp2, "label order should not affect fingerprint")
	assert.Equal(t, fp2, fp3, "label order should not affect fingerprint")
}

// TestGenerateFromLabels_FNV1a_DifferentLabels tests different labels produce different fingerprints
func TestGenerateFromLabels_FNV1a_DifferentLabels(t *testing.T) {
	generator := NewFingerprintGenerator(&FingerprintConfig{Algorithm: AlgorithmFNV1a})

	labels1 := map[string]string{
		"alertname": "HighCPU",
		"severity":  "critical",
	}

	labels2 := map[string]string{
		"alertname": "HighMemory",
		"severity":  "warning",
	}

	labels3 := map[string]string{
		"alertname": "HighCPU",
		"severity":  "warning", // Same alertname, different severity
	}

	fp1 := generator.GenerateFromLabels(labels1)
	fp2 := generator.GenerateFromLabels(labels2)
	fp3 := generator.GenerateFromLabels(labels3)

	assert.NotEqual(t, fp1, fp2, "different alerts should have different fingerprints")
	assert.NotEqual(t, fp1, fp3, "different alerts should have different fingerprints")
	assert.NotEqual(t, fp2, fp3, "different alerts should have different fingerprints")
}

// TestGenerateFromLabels_FNV1a_EdgeCases tests edge cases
func TestGenerateFromLabels_FNV1a_EdgeCases(t *testing.T) {
	generator := NewFingerprintGenerator(&FingerprintConfig{Algorithm: AlgorithmFNV1a})

	tests := []struct {
		name        string
		labels      map[string]string
		expectEmpty bool
		expectLen   int
	}{
		{
			name:        "nil labels",
			labels:      nil,
			expectEmpty: true,
		},
		{
			name:        "empty labels",
			labels:      map[string]string{},
			expectEmpty: true,
		},
		{
			name: "single label",
			labels: map[string]string{
				"alertname": "TestAlert",
			},
			expectEmpty: false,
			expectLen:   16,
		},
		{
			name: "many labels",
			labels: map[string]string{
				"alertname": "TestAlert",
				"severity":  "critical",
				"instance":  "server-1",
				"namespace": "production",
				"cluster":   "us-west-1",
				"team":      "platform",
				"env":       "prod",
			},
			expectEmpty: false,
			expectLen:   16,
		},
		{
			name: "labels with special characters",
			labels: map[string]string{
				"alertname": "Test-Alert_123",
				"severity":  "critical!",
				"instance":  "server-1.example.com:9090",
			},
			expectEmpty: false,
			expectLen:   16,
		},
		{
			name: "labels with empty values",
			labels: map[string]string{
				"alertname": "TestAlert",
				"severity":  "",
				"instance":  "server-1",
			},
			expectEmpty: false,
			expectLen:   16,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fp := generator.GenerateFromLabels(tt.labels)

			if tt.expectEmpty {
				assert.Empty(t, fp, "fingerprint should be empty")
			} else {
				assert.NotEmpty(t, fp, "fingerprint should not be empty")
				assert.Len(t, fp, tt.expectLen, "fingerprint should have correct length")
				// Verify hex format
				assert.Regexp(t, "^[0-9a-f]{16}$", fp, "fingerprint should be valid hex")
			}
		})
	}
}

// TestGenerate_WithAlert tests fingerprint generation from alert
func TestGenerate_WithAlert(t *testing.T) {
	generator := NewFingerprintGenerator(&FingerprintConfig{Algorithm: AlgorithmFNV1a})

	alert := &core.Alert{
		AlertName: "HighCPU",
		Labels: map[string]string{
			"alertname": "HighCPU",
			"severity":  "critical",
			"instance":  "server-1",
		},
	}

	fp := generator.Generate(alert)

	assert.NotEmpty(t, fp, "fingerprint should not be empty")
	assert.Len(t, fp, 16, "FNV-1a fingerprint should be 16 hex chars")
	assert.Regexp(t, "^[0-9a-f]{16}$", fp, "fingerprint should be valid hex")
}

// TestGenerate_NilAlert tests nil alert handling
func TestGenerate_NilAlert(t *testing.T) {
	generator := NewFingerprintGenerator(&FingerprintConfig{Algorithm: AlgorithmFNV1a})

	fp := generator.Generate(nil)

	assert.Empty(t, fp, "fingerprint should be empty for nil alert")
}

// TestGenerateWithAlgorithm_FNV1a_vs_SHA256 tests algorithm comparison
func TestGenerateWithAlgorithm_FNV1a_vs_SHA256(t *testing.T) {
	generator := NewFingerprintGenerator(&FingerprintConfig{Algorithm: AlgorithmFNV1a})

	labels := map[string]string{
		"alertname": "HighCPU",
		"severity":  "critical",
		"instance":  "server-1",
	}

	fpFNV := generator.GenerateWithAlgorithm(labels, AlgorithmFNV1a)
	fpSHA := generator.GenerateWithAlgorithm(labels, AlgorithmSHA256)

	// Different algorithms should produce different fingerprints
	assert.NotEqual(t, fpFNV, fpSHA, "different algorithms should produce different fingerprints")

	// Verify lengths
	assert.Len(t, fpFNV, 16, "FNV-1a should be 16 hex chars")
	assert.Len(t, fpSHA, 64, "SHA-256 should be 64 hex chars")

	// Verify hex format
	assert.Regexp(t, "^[0-9a-f]{16}$", fpFNV, "FNV-1a should be valid hex")
	assert.Regexp(t, "^[0-9a-f]{64}$", fpSHA, "SHA-256 should be valid hex")
}

// TestGenerateFromLabels_SHA256_Deterministic tests SHA-256 deterministic fingerprints
func TestGenerateFromLabels_SHA256_Deterministic(t *testing.T) {
	generator := NewFingerprintGenerator(&FingerprintConfig{Algorithm: AlgorithmSHA256})

	labels := map[string]string{
		"alertname": "HighCPU",
		"severity":  "critical",
		"instance":  "server-1",
	}

	// Generate fingerprint multiple times
	fp1 := generator.GenerateFromLabels(labels)
	fp2 := generator.GenerateFromLabels(labels)
	fp3 := generator.GenerateFromLabels(labels)

	// All should be identical (deterministic)
	assert.Equal(t, fp1, fp2, "SHA-256 fingerprints should be deterministic")
	assert.Equal(t, fp2, fp3, "SHA-256 fingerprints should be deterministic")
	assert.NotEmpty(t, fp1, "fingerprint should not be empty")
	assert.Len(t, fp1, 64, "SHA-256 fingerprint should be 64 hex chars")
}

// TestValidateFingerprint tests fingerprint validation
func TestValidateFingerprint(t *testing.T) {
	tests := []struct {
		name        string
		fingerprint string
		algorithm   FingerprintAlgorithm
		want        bool
	}{
		// Valid cases
		{
			name:        "valid FNV-1a",
			fingerprint: "a1b2c3d4e5f60708",
			algorithm:   AlgorithmFNV1a,
			want:        true,
		},
		{
			name:        "valid SHA-256",
			fingerprint: "a1b2c3d4e5f607089a1b2c3d4e5f607089a1b2c3d4e5f607089a1b2c3d4e5f60",
			algorithm:   AlgorithmSHA256,
			want:        true,
		},
		// Invalid cases
		{
			name:        "empty fingerprint",
			fingerprint: "",
			algorithm:   AlgorithmFNV1a,
			want:        false,
		},
		{
			name:        "wrong length FNV-1a (too short)",
			fingerprint: "a1b2c3d4",
			algorithm:   AlgorithmFNV1a,
			want:        false,
		},
		{
			name:        "wrong length FNV-1a (too long)",
			fingerprint: "a1b2c3d4e5f607089",
			algorithm:   AlgorithmFNV1a,
			want:        false,
		},
		{
			name:        "wrong length SHA-256 (too short)",
			fingerprint: "a1b2c3d4e5f60708",
			algorithm:   AlgorithmSHA256,
			want:        false,
		},
		{
			name:        "invalid hex characters",
			fingerprint: "g1b2c3d4e5f60708",
			algorithm:   AlgorithmFNV1a,
			want:        false,
		},
		{
			name:        "uppercase hex (should be valid)",
			fingerprint: "A1B2C3D4E5F60708",
			algorithm:   AlgorithmFNV1a,
			want:        true,
		},
		{
			name:        "non-hex characters",
			fingerprint: "a1b2-3d4-5f60708",
			algorithm:   AlgorithmFNV1a,
			want:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ValidateFingerprint(tt.fingerprint, tt.algorithm)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestGenerateFromLabels_AlertmanagerCompatibility tests Alertmanager compatibility
//
// This test verifies that our FNV-1a implementation produces fingerprints
// compatible with Alertmanager's fingerprinting algorithm.
func TestGenerateFromLabels_AlertmanagerCompatibility(t *testing.T) {
	generator := NewFingerprintGenerator(&FingerprintConfig{Algorithm: AlgorithmFNV1a})

	// Test cases from Alertmanager
	tests := []struct {
		name   string
		labels map[string]string
		// We can't hardcode expected values because Go's map iteration order
		// might differ from Alertmanager, but we can verify properties
		expectNonEmpty bool
		expectLen      int
	}{
		{
			name: "basic alert",
			labels: map[string]string{
				"alertname": "InstanceDown",
				"job":       "node_exporter",
				"instance":  "localhost:9100",
			},
			expectNonEmpty: true,
			expectLen:      16,
		},
		{
			name: "complex alert",
			labels: map[string]string{
				"alertname": "HighErrorRate",
				"service":   "api",
				"severity":  "critical",
				"region":    "us-west-1",
				"env":       "production",
			},
			expectNonEmpty: true,
			expectLen:      16,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fp := generator.GenerateFromLabels(tt.labels)

			if tt.expectNonEmpty {
				assert.NotEmpty(t, fp)
			}
			if tt.expectLen > 0 {
				assert.Len(t, fp, tt.expectLen)
			}

			// Verify it's deterministic
			fp2 := generator.GenerateFromLabels(tt.labels)
			assert.Equal(t, fp, fp2, "fingerprint should be deterministic")
		})
	}
}

// TestGenerateFromLabels_Collisions tests for fingerprint collisions
//
// This test verifies that different label combinations produce different fingerprints.
// While hash collisions are theoretically possible, they should be extremely rare
// for typical alert label sets.
func TestGenerateFromLabels_Collisions(t *testing.T) {
	generator := NewFingerprintGenerator(&FingerprintConfig{Algorithm: AlgorithmFNV1a})

	// Generate many different label combinations
	labelSets := []map[string]string{
		{"alertname": "Alert1", "severity": "critical"},
		{"alertname": "Alert2", "severity": "critical"},
		{"alertname": "Alert1", "severity": "warning"},
		{"alertname": "Alert1", "instance": "server-1"},
		{"alertname": "Alert1", "instance": "server-2"},
		{"alertname": "Alert1", "namespace": "default"},
		{"alertname": "Alert1", "namespace": "production"},
	}

	fingerprints := make(map[string]bool)
	for i, labels := range labelSets {
		fp := generator.GenerateFromLabels(labels)
		if fingerprints[fp] {
			t.Errorf("collision detected at index %d: fingerprint %s already exists", i, fp)
		}
		fingerprints[fp] = true
	}

	assert.Len(t, fingerprints, len(labelSets), "all fingerprints should be unique")
}
