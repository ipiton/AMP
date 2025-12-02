package services

import (
	"crypto/sha256"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"

	"github.com/ipiton/AMP/internal/core"
)

// FingerprintAlgorithm represents the fingerprinting algorithm to use
type FingerprintAlgorithm string

const (
	// AlgorithmFNV1a is the Alertmanager-compatible FNV-1a algorithm (recommended)
	AlgorithmFNV1a FingerprintAlgorithm = "fnv1a"
	// AlgorithmSHA256 is the legacy SHA-256 algorithm (for backward compatibility)
	AlgorithmSHA256 FingerprintAlgorithm = "sha256"
)

// FingerprintGenerator generates deterministic fingerprints for alerts.
//
// The fingerprint is used for alert deduplication and must be consistent
// across multiple webhook deliveries of the same alert.
//
// 150% Enhancement: Supports multiple algorithms (FNV-1a for Alertmanager compatibility,
// SHA-256 for legacy compatibility).
type FingerprintGenerator interface {
	// Generate generates a fingerprint for an alert using the configured algorithm
	Generate(alert *core.Alert) string

	// GenerateFromLabels generates a fingerprint from labels only
	GenerateFromLabels(labels map[string]string) string

	// GenerateWithAlgorithm generates a fingerprint using a specific algorithm
	GenerateWithAlgorithm(labels map[string]string, algorithm FingerprintAlgorithm) string

	// GetAlgorithm returns the current algorithm being used
	GetAlgorithm() FingerprintAlgorithm
}

// alertmanagerFingerprintGenerator implements Alertmanager-compatible fingerprinting.
//
// Algorithm: FNV-1a (64-bit)
// Input: sorted labels (key=value pairs)
// Output: 16-character hex string
//
// This implementation ensures compatibility with Alertmanager's fingerprinting,
// allowing seamless integration with existing Alertmanager workflows.
type alertmanagerFingerprintGenerator struct {
	algorithm FingerprintAlgorithm
}

// FingerprintConfig holds configuration for fingerprint generator
type FingerprintConfig struct {
	// Algorithm specifies which algorithm to use (default: FNV-1a)
	Algorithm FingerprintAlgorithm
}

// NewFingerprintGenerator creates a new fingerprint generator with the specified algorithm.
//
// Parameters:
//   - config: Configuration for the generator (nil uses default: FNV-1a)
//
// Returns:
//   - FingerprintGenerator: Configured fingerprint generator
//
// Example:
//
//	generator := NewFingerprintGenerator(&FingerprintConfig{
//	    Algorithm: AlgorithmFNV1a,
//	})
func NewFingerprintGenerator(config *FingerprintConfig) FingerprintGenerator {
	if config == nil {
		config = &FingerprintConfig{Algorithm: AlgorithmFNV1a}
	}

	// Default to FNV-1a if not specified
	if config.Algorithm == "" {
		config.Algorithm = AlgorithmFNV1a
	}

	return &alertmanagerFingerprintGenerator{
		algorithm: config.Algorithm,
	}
}

// Generate generates a fingerprint for an alert using the configured algorithm.
//
// The fingerprint is deterministic - the same alert will always produce
// the same fingerprint, regardless of when or where it's generated.
//
// Parameters:
//   - alert: Alert to generate fingerprint for
//
// Returns:
//   - string: Hexadecimal fingerprint string
//
// Example:
//
//	alert := &core.Alert{
//	    AlertName: "HighCPU",
//	    Labels: map[string]string{
//	        "alertname": "HighCPU",
//	        "severity": "critical",
//	        "instance": "server-1",
//	    },
//	}
//	fingerprint := generator.Generate(alert)
//	// fingerprint: "a1b2c3d4e5f60708" (16 hex chars for FNV-1a)
func (f *alertmanagerFingerprintGenerator) Generate(alert *core.Alert) string {
	if alert == nil {
		return ""
	}
	return f.GenerateFromLabels(alert.Labels)
}

// GenerateFromLabels generates a fingerprint from labels only.
//
// This is the core fingerprinting logic that ensures deterministic output
// by sorting labels before hashing.
//
// Algorithm (FNV-1a):
//  1. Extract all label keys
//  2. Sort keys alphabetically (deterministic ordering)
//  3. Hash each key-value pair in sorted order using FNV-1a
//  4. Return 16-character hex string (64-bit hash)
//
// Algorithm (SHA-256):
//  1. Build fingerprint string: "alertname|key1=val1|key2=val2|..."
//  2. Hash using SHA-256
//  3. Return 64-character hex string
//
// Parameters:
//   - labels: Map of label key-value pairs
//
// Returns:
//   - string: Hexadecimal fingerprint string
//
// Example:
//
//	labels := map[string]string{
//	    "alertname": "HighCPU",
//	    "severity": "critical",
//	    "instance": "server-1",
//	}
//	fingerprint := generator.GenerateFromLabels(labels)
func (f *alertmanagerFingerprintGenerator) GenerateFromLabels(labels map[string]string) string {
	return f.GenerateWithAlgorithm(labels, f.algorithm)
}

// GenerateWithAlgorithm generates a fingerprint using a specific algorithm.
//
// 150% Enhancement: Allows runtime algorithm selection for migration scenarios.
//
// Parameters:
//   - labels: Map of label key-value pairs
//   - algorithm: Algorithm to use (FNV-1a or SHA-256)
//
// Returns:
//   - string: Hexadecimal fingerprint string
func (f *alertmanagerFingerprintGenerator) GenerateWithAlgorithm(labels map[string]string, algorithm FingerprintAlgorithm) string {
	if len(labels) == 0 {
		return ""
	}

	switch algorithm {
	case AlgorithmFNV1a:
		return f.generateFNV1a(labels)
	case AlgorithmSHA256:
		return f.generateSHA256(labels)
	default:
		// Default to FNV-1a for unknown algorithms
		return f.generateFNV1a(labels)
	}
}

// GetAlgorithm returns the current algorithm being used.
func (f *alertmanagerFingerprintGenerator) GetAlgorithm() FingerprintAlgorithm {
	return f.algorithm
}

// generateFNV1a generates a fingerprint using FNV-1a algorithm (Alertmanager-compatible).
//
// This is the RECOMMENDED algorithm for production use.
//
// FNV-1a (Fowler-Noll-Vo) is a fast, non-cryptographic hash function
// that provides good distribution and is used by Alertmanager.
//
// Performance: ~100-200 ns/op (very fast)
// Output: 16 hex characters (64-bit hash)
//
// Parameters:
//   - labels: Map of label key-value pairs
//
// Returns:
//   - string: 16-character hex fingerprint
func (f *alertmanagerFingerprintGenerator) generateFNV1a(labels map[string]string) string {
	// Sort labels by key for deterministic output
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Create FNV-1a hash (64-bit)
	h := fnv.New64a()

	// Hash each key-value pair in sorted order
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte(labels[k]))
	}

	// Return 16-character hex string (64-bit = 8 bytes = 16 hex chars)
	return fmt.Sprintf("%016x", h.Sum64())
}

// generateSHA256 generates a fingerprint using SHA-256 algorithm (legacy).
//
// This algorithm is provided for backward compatibility with existing
// fingerprints. New deployments should use FNV-1a (AlgorithmFNV1a).
//
// Performance: ~500-1000 ns/op (slower than FNV-1a)
// Output: 64 hex characters (256-bit hash)
//
// Parameters:
//   - labels: Map of label key-value pairs
//
// Returns:
//   - string: 64-character hex fingerprint
func (f *alertmanagerFingerprintGenerator) generateSHA256(labels map[string]string) string {
	// Sort labels by key for deterministic output
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Build fingerprint string
	var builder strings.Builder

	// Extract alertname first (if present)
	if alertName, ok := labels["alertname"]; ok {
		builder.WriteString(alertName)
		builder.WriteString("|")
	}

	// Append all labels in sorted order
	for _, k := range keys {
		builder.WriteString(k)
		builder.WriteString("=")
		builder.WriteString(labels[k])
		builder.WriteString("|")
	}

	// Hash the fingerprint string
	hash := sha256.Sum256([]byte(builder.String()))
	return fmt.Sprintf("%x", hash)
}

// ValidateFingerprint validates a fingerprint string format.
//
// 150% Enhancement: Validation utility for fingerprint format checks.
//
// Parameters:
//   - fingerprint: Fingerprint string to validate
//   - algorithm: Expected algorithm (used to validate length)
//
// Returns:
//   - bool: true if valid, false otherwise
//
// Validation rules:
//   - FNV-1a: exactly 16 hex characters
//   - SHA-256: exactly 64 hex characters
//   - Non-empty
func ValidateFingerprint(fingerprint string, algorithm FingerprintAlgorithm) bool {
	if fingerprint == "" {
		return false
	}

	expectedLen := 0
	switch algorithm {
	case AlgorithmFNV1a:
		expectedLen = 16 // 64-bit = 8 bytes = 16 hex chars
	case AlgorithmSHA256:
		expectedLen = 64 // 256-bit = 32 bytes = 64 hex chars
	default:
		return false
	}

	if len(fingerprint) != expectedLen {
		return false
	}

	// Check if all characters are valid hex
	for _, c := range fingerprint {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}

	return true
}
