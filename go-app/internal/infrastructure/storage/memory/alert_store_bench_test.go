package memory

import (
	"fmt"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

// Benchmarks for GLOBAL-LOCK-CONTENTION: measure the single RWMutex under
// parallel load before deciding whether sharding is warranted.

func makeIngestInput(fingerprint string, now time.Time) []core.AlertIngestInput {
	return []core.AlertIngestInput{{
		Labels:      map[string]string{"alertname": "BenchAlert", "instance": fingerprint},
		Annotations: map[string]string{"summary": "bench"},
		StartsAt:    now.Format(time.RFC3339Nano),
		Fingerprint: fingerprint,
		Status:      "firing",
	}}
}

func BenchmarkAlertStore_IngestParallel(b *testing.B) {
	store := NewAlertStore()
	now := time.Now().UTC()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			i++
			if err := store.IngestBatch(makeIngestInput(fmt.Sprintf("bench-fp-%d", i%10000), now), now); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkAlertStore_MixedReadWrite(b *testing.B) {
	store := NewAlertStore()
	now := time.Now().UTC()
	for i := 0; i < 5000; i++ {
		_ = store.IngestBatch(makeIngestInput(fmt.Sprintf("seed-%d", i), now), now)
	}

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			i++
			if i%10 == 0 { // 10% writes, 90% reads
				_ = store.IngestBatch(makeIngestInput(fmt.Sprintf("mix-%d", i%1000), now), now)
			} else {
				_ = store.List("firing", false)
			}
		}
	})
}
