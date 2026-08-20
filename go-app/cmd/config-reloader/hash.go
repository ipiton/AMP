package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
)

// hashFile returns the SHA256 of the file's contents, hex-encoded.
//
// Content hashing rather than mtime/size watching, deliberately: a Kubernetes
// ConfigMap update replaces the mounted symlink target, which changes mtime
// even when the content is identical (kubelet re-syncs periodically), and an
// mtime-triggered reload storm is exactly what this sidecar must not cause.
// Content is also what the application actually reads.
func hashFile(path string) (string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is an operator-supplied flag
	if err != nil {
		return "", fmt.Errorf("read config file: %w", err)
	}

	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// hashToFloat maps a hex hash onto a float64 for the config_hash gauge.
//
// Prometheus gauges are float64, so a 256-bit hash cannot be represented
// exactly. We take the leading 52 bits — the width float64 holds without loss —
// which is plenty to SEE that the config changed (the readable value lives on
// the config_info metric's `hash` label). Never use this for equality: it is a
// truncation, not the hash.
func hashToFloat(hash string) float64 {
	const bits = 13 // 13 hex chars = 52 bits
	if len(hash) > bits {
		hash = hash[:bits]
	}

	var value uint64
	for _, char := range hash {
		digit, ok := hexDigit(char)
		if !ok {
			return 0
		}
		value = value<<4 | uint64(digit)
	}
	return float64(value)
}

func hexDigit(char rune) (int, bool) {
	switch {
	case char >= '0' && char <= '9':
		return int(char - '0'), true
	case char >= 'a' && char <= 'f':
		return int(char-'a') + 10, true
	case char >= 'A' && char <= 'F':
		return int(char-'A') + 10, true
	default:
		return 0, false
	}
}

// shortHash trims a hash for logs and labels. Full SHA256 in a log line is
// noise; 12 hex chars is enough to correlate two lines.
func shortHash(hash string) string {
	const width = 12
	if len(hash) <= width {
		return hash
	}
	return hash[:width]
}
