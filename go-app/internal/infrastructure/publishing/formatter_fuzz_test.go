package publishing

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

// FuzzAlertFormatter tests formatter with random inputs
func FuzzAlertFormatter(f *testing.F) {
	formatter := NewAlertFormatter()
	ctx := context.Background()

	// Seed corpus
	f.Add("TestAlert", "firing", "test-fingerprint", int64(1234567890))

	f.Fuzz(func(t *testing.T, alertName, status, fingerprint string, timestamp int64) {
		// Create alert from fuzz inputs
		alert := &core.EnrichedAlert{
			Alert: &core.Alert{
				Fingerprint: fingerprint,
				AlertName:   alertName,
				Status:      core.AlertStatus(status),
				Labels:      map[string]string{},
				Annotations: map[string]string{},
				StartsAt:    time.Unix(timestamp, 0),
			},
		}

		// Format should not panic
		_, _ = formatter.FormatAlert(ctx, alert, core.FormatAlertmanager)
		_, _ = formatter.FormatAlert(ctx, alert, core.FormatRootly)
		_, _ = formatter.FormatAlert(ctx, alert, core.FormatPagerDuty)
		_, _ = formatter.FormatAlert(ctx, alert, core.FormatSlack)
		_, _ = formatter.FormatAlert(ctx, alert, core.FormatWebhook)
	})
}

// TestFuzz_RandomAlerts generates 1M+ random alerts for stress testing
func TestFuzz_RandomAlerts(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping fuzzing in short mode")
	}

	formatter := NewAlertFormatter()
	ctx := context.Background()

	const numIterations = 1_000_000
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	formats := []core.PublishingFormat{
		core.FormatAlertmanager,
		core.FormatRootly,
		core.FormatPagerDuty,
		core.FormatSlack,
		core.FormatWebhook,
	}

	statuses := []core.AlertStatus{
		core.StatusFiring,
		core.StatusResolved,
	}

	severities := []core.AlertSeverity{
		core.SeverityCritical,
		core.SeverityWarning,
		core.SeverityInfo,
		core.SeverityNoise,
	}

	panics := 0
	errors := 0
	success := 0

	for i := 0; i < numIterations; i++ {
		// Generate random alert
		alert := generateRandomAlert(rng, statuses, severities)

		// Pick random format
		format := formats[rng.Intn(len(formats))]

		// Format alert (catch panics)
		func() {
			defer func() {
				if r := recover(); r != nil {
					panics++
					t.Logf("Panic at iteration %d: %v", i, r)
				}
			}()

			_, err := formatter.FormatAlert(ctx, alert, format)
			if err != nil {
				errors++
			} else {
				success++
			}
		}()

		// Log progress
		if (i+1)%100_000 == 0 {
			t.Logf("Progress: %d/%d (%.1f%%) - Success: %d, Errors: %d, Panics: %d",
				i+1, numIterations, float64(i+1)/float64(numIterations)*100,
				success, errors, panics)
		}
	}

	t.Logf("Fuzzing complete: %d iterations", numIterations)
	t.Logf("Success: %d (%.2f%%)", success, float64(success)/float64(numIterations)*100)
	t.Logf("Errors: %d (%.2f%%)", errors, float64(errors)/float64(numIterations)*100)
	t.Logf("Panics: %d (%.2f%%)", panics, float64(panics)/float64(numIterations)*100)

	// Assert no panics
	if panics > 0 {
		t.Errorf("Formatter panicked %d times", panics)
	}
}

// generateRandomAlert creates random alert for fuzzing
func generateRandomAlert(rng *rand.Rand, statuses []core.AlertStatus, severities []core.AlertSeverity) *core.EnrichedAlert {
	alert := &core.EnrichedAlert{
		Alert: &core.Alert{
			Fingerprint: randomString(rng, 16, 64),
			AlertName:   randomString(rng, 1, 100),
			Status:      statuses[rng.Intn(len(statuses))],
			Labels:      randomMap(rng, 0, 20),
			Annotations: randomMap(rng, 0, 10),
			StartsAt:    randomTime(rng),
		},
	}

	// Randomly add classification
	if rng.Float64() > 0.5 {
		alert.Classification = &core.ClassificationResult{
			Severity:        severities[rng.Intn(len(severities))],
			Confidence:      rng.Float64(),
			Reasoning:       randomString(rng, 0, 500),
			Recommendations: randomStringSlice(rng, 0, 10, 0, 200),
		}
	}

	// Randomly add EndsAt
	if rng.Float64() > 0.5 {
		endsAt := alert.Alert.StartsAt.Add(time.Duration(rng.Int63n(int64(24 * time.Hour))))
		alert.Alert.EndsAt = &endsAt
	}

	// Randomly add GeneratorURL
	if rng.Float64() > 0.7 {
		url := "http://" + randomString(rng, 5, 20) + "/path"
		alert.Alert.GeneratorURL = &url
	}

	return alert
}

// randomString generates random string
func randomString(rng *rand.Rand, minLen, maxLen int) string {
	length := minLen + rng.Intn(maxLen-minLen+1)
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"

	b := make([]byte, length)
	for i := range b {
		b[i] = charset[rng.Intn(len(charset))]
	}
	return string(b)
}

// randomMap generates random string map
func randomMap(rng *rand.Rand, minSize, maxSize int) map[string]string {
	size := minSize + rng.Intn(maxSize-minSize+1)
	m := make(map[string]string, size)

	for i := 0; i < size; i++ {
		key := randomString(rng, 1, 20)
		value := randomString(rng, 0, 100)
		m[key] = value
	}

	return m
}

// randomStringSlice generates random string slice
func randomStringSlice(rng *rand.Rand, minSize, maxSize, minStrLen, maxStrLen int) []string {
	size := minSize + rng.Intn(maxSize-minSize+1)
	slice := make([]string, size)

	for i := 0; i < size; i++ {
		slice[i] = randomString(rng, minStrLen, maxStrLen)
	}

	return slice
}

// randomTime generates random time
func randomTime(rng *rand.Rand) time.Time {
	// Random time between 2020-01-01 and 2025-12-31
	minTimestamp := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	maxTimestamp := time.Date(2025, 12, 31, 23, 59, 59, 0, time.UTC).Unix()

	timestamp := minTimestamp + rng.Int63n(maxTimestamp-minTimestamp)
	return time.Unix(timestamp, 0)
}

// BenchmarkFuzz_Alertmanager benchmarks fuzzing performance
func BenchmarkFuzz_Alertmanager(b *testing.B) {
	formatter := NewAlertFormatter()
	ctx := context.Background()
	rng := rand.New(rand.NewSource(42))

	statuses := []core.AlertStatus{core.StatusFiring, core.StatusResolved}
	severities := []core.AlertSeverity{core.SeverityCritical, core.SeverityWarning}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		alert := generateRandomAlert(rng, statuses, severities)
		_, _ = formatter.FormatAlert(ctx, alert, core.FormatAlertmanager)
	}
}

// BenchmarkFuzz_AllFormats benchmarks all formats
func BenchmarkFuzz_AllFormats(b *testing.B) {
	formatter := NewAlertFormatter()
	ctx := context.Background()
	rng := rand.New(rand.NewSource(42))

	statuses := []core.AlertStatus{core.StatusFiring, core.StatusResolved}
	severities := []core.AlertSeverity{core.SeverityCritical, core.SeverityWarning}

	formats := []core.PublishingFormat{
		core.FormatAlertmanager,
		core.FormatRootly,
		core.FormatPagerDuty,
		core.FormatSlack,
		core.FormatWebhook,
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		alert := generateRandomAlert(rng, statuses, severities)
		format := formats[i%len(formats)]
		_, _ = formatter.FormatAlert(ctx, alert, format)
	}
}
