package publishing

import (
	"log/slog"
	"os"
	"testing"
	"time"
)

// slack_bench_test.go - Performance benchmarks for Slack publisher
// Targets: <50ns cache ops, <200ms p99 publish, <10µs buildMessage

// setupSlackBenchmark creates publisher for benchmarks
func setupSlackBenchmark() *EnhancedSlackPublisher {
	// Use mock client for benchmarks (no actual HTTP calls)
	client := &mockSlackWebhookClient{}
	cache := NewMessageCache()
	formatter := NewAlertFormatter()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	return NewEnhancedSlackPublisher(client, cache, sharedSlackMetrics, formatter, logger).(*EnhancedSlackPublisher)
}

// BenchmarkCache_Store benchmarks Store operation
// Target: <50ns per operation
func BenchmarkCache_Store(b *testing.B) {
	cache := NewMessageCache()
	entry := &MessageEntry{
		MessageTS: "1234567890.123456",
		ThreadTS:  "1234567890.123456",
		CreatedAt: time.Now(),
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		cache.Store("fp123", entry)
	}
}

// BenchmarkCache_Get benchmarks Get operation (cache hit)
// Target: <50ns per operation
func BenchmarkCache_Get(b *testing.B) {
	cache := NewMessageCache()
	entry := &MessageEntry{
		MessageTS: "1234567890.123456",
		ThreadTS:  "1234567890.123456",
		CreatedAt: time.Now(),
	}
	cache.Store("fp123", entry)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _ = cache.Get("fp123")
	}
}

// BenchmarkCache_Get_Miss benchmarks Get operation (cache miss)
// Target: <50ns per operation
func BenchmarkCache_Get_Miss(b *testing.B) {
	cache := NewMessageCache()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _ = cache.Get("nonexistent")
	}
}

// BenchmarkCache_Delete benchmarks Delete operation
// Target: <100ns per operation (sync.Map Delete is slightly slower)
func BenchmarkCache_Delete(b *testing.B) {
	cache := NewMessageCache()

	// Pre-populate cache
	for i := 0; i < b.N; i++ {
		cache.Store("fp"+string(rune(i)), &MessageEntry{
			MessageTS: "test",
			ThreadTS:  "test",
			CreatedAt: time.Now(),
		})
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		cache.Delete("fp" + string(rune(i)))
	}
}

// BenchmarkCache_Cleanup benchmarks Cleanup operation
// Target: <1ms per 100 entries
func BenchmarkCache_Cleanup(b *testing.B) {
	cache := NewMessageCache()

	// Add 100 entries (50 old, 50 recent)
	now := time.Now()
	for i := 0; i < 50; i++ {
		cache.Store("old"+string(rune(i)), &MessageEntry{
			MessageTS: "old",
			ThreadTS:  "old",
			CreatedAt: now.Add(-25 * time.Hour), // Expired
		})
	}
	for i := 0; i < 50; i++ {
		cache.Store("recent"+string(rune(i)), &MessageEntry{
			MessageTS: "recent",
			ThreadTS:  "recent",
			CreatedAt: now, // Not expired
		})
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		cache.Cleanup(24 * time.Hour)
	}
}

// BenchmarkBuildMessage benchmarks message conversion
// Target: <10µs per operation
func BenchmarkBuildMessage(b *testing.B) {
	publisher := setupSlackBenchmark()

	payload := map[string]any{
		"text": "Test alert",
		"blocks": []interface{}{
			map[string]interface{}{
				"type": "header",
				"text": map[string]interface{}{
					"type": "plain_text",
					"text": "Test Header",
				},
			},
			map[string]interface{}{
				"type": "section",
				"fields": []interface{}{
					map[string]interface{}{
						"type": "mrkdwn",
						"text": "*Field 1*",
					},
					map[string]interface{}{
						"type": "mrkdwn",
						"text": "*Field 2*",
					},
				},
			},
		},
		"attachments": []interface{}{
			map[string]interface{}{
				"color": "#FF0000",
				"text":  "Attachment text",
			},
		},
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = publisher.buildMessage(payload)
	}
}

// BenchmarkPublisher_Name benchmarks Name() method
// Target: <10ns per operation (trivial getter)
func BenchmarkPublisher_Name(b *testing.B) {
	publisher := setupSlackBenchmark()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = publisher.Name()
	}
}

// BenchmarkClassifySlackError benchmarks error classification
// Target: <100ns per operation
func BenchmarkClassifySlackError(b *testing.B) {
	err := &SlackAPIError{StatusCode: 429, ErrorMessage: "rate_limited"}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = classifySlackError(err)
	}
}

// BenchmarkCache_Concurrent benchmarks concurrent Store/Get operations
// Target: <100ns per operation under concurrent load
func BenchmarkCache_Concurrent(b *testing.B) {
	cache := NewMessageCache()

	// Pre-populate
	for i := 0; i < 100; i++ {
		cache.Store("fp"+string(rune(i)), &MessageEntry{
			MessageTS: "test",
			ThreadTS:  "test",
			CreatedAt: time.Now(),
		})
	}

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := "fp" + string(rune(i%100))

			// Mix of reads and writes (80% read, 20% write)
			if i%5 == 0 {
				cache.Store(key, &MessageEntry{
					MessageTS: "test",
					ThreadTS:  "test",
					CreatedAt: time.Now(),
				})
			} else {
				_, _ = cache.Get(key)
			}

			i++
		}
	})
}

// BenchmarkCache_Size benchmarks Size operation
// Target: <1µs per operation (requires Range over sync.Map)
func BenchmarkCache_Size(b *testing.B) {
	cache := NewMessageCache()

	// Add 100 entries
	for i := 0; i < 100; i++ {
		cache.Store("fp"+string(rune(i)), &MessageEntry{
			MessageTS: "test",
			ThreadTS:  "test",
			CreatedAt: time.Now(),
		})
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = cache.Size()
	}
}

// BenchmarkPublisher_Lifecycle benchmarks full publisher lifecycle
// Target: <1µs for setup/teardown
func BenchmarkPublisher_Lifecycle(b *testing.B) {
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		publisher := setupSlackBenchmark()

		// Simulate teardown
		_ = publisher.Name()
	}
}

// BenchmarkBuildBlock benchmarks single block conversion
// Target: <1µs per block
func BenchmarkBuildBlock(b *testing.B) {
	publisher := setupSlackBenchmark()

	blockMap := map[string]interface{}{
		"type": "section",
		"text": map[string]interface{}{
			"type": "mrkdwn",
			"text": "*Test text*",
		},
		"fields": []interface{}{
			map[string]interface{}{
				"type": "mrkdwn",
				"text": "*Field 1*",
			},
			map[string]interface{}{
				"type": "mrkdwn",
				"text": "*Field 2*",
			},
		},
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = publisher.buildBlock(blockMap)
	}
}

// BenchmarkBuildAttachment benchmarks attachment conversion
// Target: <500ns per attachment
func BenchmarkBuildAttachment(b *testing.B) {
	publisher := setupSlackBenchmark()

	attachMap := map[string]interface{}{
		"color": "#FF0000",
		"text":  "Attachment text with some content here",
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = publisher.buildAttachment(attachMap)
	}
}

// BenchmarkCache_StoreAndGet benchmarks combined Store+Get workflow
// Target: <100ns per operation
func BenchmarkCache_StoreAndGet(b *testing.B) {
	cache := NewMessageCache()
	entry := &MessageEntry{
		MessageTS: "1234567890.123456",
		ThreadTS:  "1234567890.123456",
		CreatedAt: time.Now(),
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		key := "fp" + string(rune(i%100))
		cache.Store(key, entry)
		_, _ = cache.Get(key)
	}
}

// BenchmarkMessageEntry_Creation benchmarks MessageEntry allocation
// Target: <100ns per entry (allocation overhead)
func BenchmarkMessageEntry_Creation(b *testing.B) {
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = &MessageEntry{
			MessageTS: "1234567890.123456",
			ThreadTS:  "1234567890.123456",
			CreatedAt: time.Now(),
		}
	}
}

// BenchmarkSlackMessage_Creation benchmarks SlackMessage allocation
// Target: <500ns per message (complex structure)
func BenchmarkSlackMessage_Creation(b *testing.B) {
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = &SlackMessage{
			Text: "Test message",
			Blocks: []Block{
				{
					Type: "header",
					Text: &Text{
						Type: "plain_text",
						Text: "Test Header",
					},
				},
			},
			Attachments: []Attachment{
				{
					Color: "#FF0000",
					Text:  "Test attachment",
				},
			},
		}
	}
}

// setupSlackPublisher is reused from slack_publisher_test.go
// (already defined, no need to duplicate)
