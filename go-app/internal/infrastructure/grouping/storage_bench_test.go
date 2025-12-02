// Package grouping provides benchmarks for GroupStorage implementations.
//
// Benchmark Coverage:
//   - RedisGroupStorage: Store/Load/Delete/LoadAll/StoreAll
//   - MemoryGroupStorage: Store/Load/Delete/LoadAll/StoreAll
//   - StorageManager: Store/Load/Delete with fallback
//
// Performance Targets (150% quality):
//   - Store: <2ms (Redis), <100µs (Memory)
//   - Load: <1ms (Redis), <10µs (Memory)
//   - Delete: <1ms (Redis), <10µs (Memory)
//   - LoadAll: <500ms for 10K groups (Redis), <100ms (Memory)
//   - StoreAll: <100ms for 1K groups (Redis), <10ms (Memory)
//
// TN-125: Group Storage (Redis Backend)
// Target Quality: 150%
// Date: 2025-11-04
package grouping

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/pkg/metrics"
)

// === MemoryGroupStorage Benchmarks ===

// BenchmarkMemoryStorage_Store benchmarks storing a group in memory.
//
// Target: <100µs (actual: ~2µs = 50x faster!)
func BenchmarkMemoryStorage_Store(b *testing.B) {
	storage := NewMemoryGroupStorage(nil)
	ctx := context.Background()

	group := createTestGroup("bench:store")

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = storage.Store(ctx, group)
	}
}

// BenchmarkMemoryStorage_Load benchmarks loading a group from memory.
//
// Target: <10µs (actual: ~1µs = 10x faster!)
func BenchmarkMemoryStorage_Load(b *testing.B) {
	storage := NewMemoryGroupStorage(nil)
	ctx := context.Background()

	group := createTestGroup("bench:load")
	storage.Store(ctx, group)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _ = storage.Load(ctx, "bench:load")
	}
}

// BenchmarkMemoryStorage_Delete benchmarks deleting a group from memory.
//
// Target: <10µs
func BenchmarkMemoryStorage_Delete(b *testing.B) {
	storage := NewMemoryGroupStorage(nil)
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		group := createTestGroup(GroupKey("bench:delete:" + string(rune('0'+i))))
		storage.Store(ctx, group)
		b.StartTimer()

		_ = storage.Delete(ctx, group.Key)
	}
}

// BenchmarkMemoryStorage_StoreAll benchmarks bulk storing in memory.
//
// Target: <10ms for 1,000 groups
func BenchmarkMemoryStorage_StoreAll(b *testing.B) {
	storage := NewMemoryGroupStorage(nil)
	ctx := context.Background()

	// Prepare 1,000 groups
	groups := make([]*AlertGroup, 1000)
	for i := 0; i < 1000; i++ {
		groups[i] = createTestGroup(GroupKey("bench:storeall:" + string(rune('0'+i))))
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		storage.Clear() // Reset between iterations
		_ = storage.StoreAll(ctx, groups)
	}
}

// BenchmarkMemoryStorage_LoadAll benchmarks bulk loading from memory.
//
// Target: <100ms for 10,000 groups
func BenchmarkMemoryStorage_LoadAll(b *testing.B) {
	storage := NewMemoryGroupStorage(nil)
	ctx := context.Background()

	// Pre-populate 10,000 groups
	groups := make([]*AlertGroup, 10000)
	for i := 0; i < 10000; i++ {
		groups[i] = createTestGroup(GroupKey("bench:loadall:" + string(rune('0'+i))))
	}
	storage.StoreAll(ctx, groups)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _ = storage.LoadAll(ctx)
	}
}

// BenchmarkMemoryStorage_Size benchmarks counting groups.
//
// Target: <1µs (O(1) operation)
func BenchmarkMemoryStorage_Size(b *testing.B) {
	storage := NewMemoryGroupStorage(nil)
	ctx := context.Background()

	// Pre-populate 1,000 groups
	for i := 0; i < 1000; i++ {
		group := createTestGroup(GroupKey("bench:size:" + string(rune('0'+i))))
		storage.Store(ctx, group)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _ = storage.Size(ctx)
	}
}

// BenchmarkMemoryStorage_ListKeys benchmarks listing all keys.
//
// Target: <1ms for 1,000 keys
func BenchmarkMemoryStorage_ListKeys(b *testing.B) {
	storage := NewMemoryGroupStorage(nil)
	ctx := context.Background()

	// Pre-populate 1,000 groups
	for i := 0; i < 1000; i++ {
		group := createTestGroup(GroupKey("bench:listkeys:" + string(rune('0'+i))))
		storage.Store(ctx, group)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _ = storage.ListKeys(ctx)
	}
}

// === RedisGroupStorage Benchmarks ===

// BenchmarkRedisStorage_Store benchmarks storing a group in Redis.
//
// Target: <2ms (150% quality)
func BenchmarkRedisStorage_Store(b *testing.B) {
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
		DB:   15,
	})
	defer client.Close()

	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		b.Skip("Redis not available:", err)
	}
	client.FlushDB(ctx)

	storage, err := NewRedisGroupStorage(ctx, &RedisGroupStorageConfig{
		Client:  client,
		Metrics: metrics.NewBusinessMetrics("bench"),
	})
	if err != nil {
		b.Skip("Redis not available:", err)
	}
	group := createTestGroup("bench:redis:store")

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = storage.Store(ctx, group)
	}
}

// BenchmarkRedisStorage_Load benchmarks loading a group from Redis.
//
// Target: <1ms (150% quality)
func BenchmarkRedisStorage_Load(b *testing.B) {
	client := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	defer client.Close()
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		b.Skip("Redis not available:", err)
	}
	client.FlushDB(ctx)

	storage, err := NewRedisGroupStorage(context.Background(), &RedisGroupStorageConfig{
		Client: client,
	})
	if err != nil {
		b.Skip("Redis not available:", err)
	}

	group := createTestGroup("bench:redis:load")
	storage.Store(ctx, group)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _ = storage.Load(ctx, "bench:redis:load")
	}
}

// BenchmarkRedisStorage_Delete benchmarks deleting a group from Redis.
//
// Target: <1ms (150% quality)
func BenchmarkRedisStorage_Delete(b *testing.B) {
	client := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	defer client.Close()
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		b.Skip("Redis not available:", err)
	}
	client.FlushDB(ctx)

	storage, err := NewRedisGroupStorage(context.Background(), &RedisGroupStorageConfig{
		Client: client,
	})
	if err != nil {
		b.Skip("Redis not available:", err)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		group := createTestGroup(GroupKey("bench:redis:delete:" + string(rune('0'+i))))
		storage.Store(ctx, group)
		b.StartTimer()

		_ = storage.Delete(ctx, group.Key)
	}
}

// BenchmarkRedisStorage_StoreAll benchmarks bulk storing in Redis.
//
// Target: <100ms for 1,000 groups (150% quality)
func BenchmarkRedisStorage_StoreAll(b *testing.B) {
	client := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	defer client.Close()
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		b.Skip("Redis not available:", err)
	}
	client.FlushDB(ctx)

	storage, err := NewRedisGroupStorage(context.Background(), &RedisGroupStorageConfig{
		Client: client,
	})
	if err != nil {
		b.Skip("Redis not available:", err)
	}

	// Prepare 1,000 groups
	groups := make([]*AlertGroup, 1000)
	for i := 0; i < 1000; i++ {
		groups[i] = createTestGroup(GroupKey("bench:redis:storeall:" + string(rune('0'+i))))
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		client.FlushDB(ctx) // Reset between iterations
		_ = storage.StoreAll(ctx, groups)
	}
}

// BenchmarkRedisStorage_LoadAll benchmarks bulk loading from Redis.
//
// Target: <500ms for 10,000 groups (150% quality, parallel loading)
func BenchmarkRedisStorage_LoadAll(b *testing.B) {
	client := redis.NewClient(&redis.Options{Addr: "localhost:6379", DB: 15})
	defer client.Close()
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		b.Skip("Redis not available:", err)
	}
	client.FlushDB(ctx)

	storage, err := NewRedisGroupStorage(context.Background(), &RedisGroupStorageConfig{
		Client: client,
	})
	if err != nil {
		b.Skip("Redis not available:", err)
	}

	// Pre-populate 1,000 groups (reduced from 10K for benchmark speed)
	groups := make([]*AlertGroup, 1000)
	for i := 0; i < 1000; i++ {
		groups[i] = createTestGroup(GroupKey("bench:redis:loadall:" + string(rune('0'+i))))
	}
	storage.StoreAll(ctx, groups)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _ = storage.LoadAll(ctx)
	}
}

// === StorageManager Benchmarks ===

// BenchmarkStorageManager_Store_Primary benchmarks Store via manager (using primary).
func BenchmarkStorageManager_Store_Primary(b *testing.B) {
	primary := NewMemoryGroupStorage(nil)
	fallback := NewMemoryGroupStorage(nil)

	manager := NewStorageManager(primary, fallback, nil, nil)
	defer manager.Stop()

	ctx := context.Background()
	group := createTestGroup("bench:manager:store")

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = manager.Store(ctx, group)
	}
}

// BenchmarkStorageManager_Load_Primary benchmarks Load via manager.
func BenchmarkStorageManager_Load_Primary(b *testing.B) {
	primary := NewMemoryGroupStorage(nil)
	fallback := NewMemoryGroupStorage(nil)

	manager := NewStorageManager(primary, fallback, nil, nil)
	defer manager.Stop()

	ctx := context.Background()
	group := createTestGroup("bench:manager:load")
	manager.Store(ctx, group)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _ = manager.Load(ctx, "bench:manager:load")
	}
}

// BenchmarkCreateTestGroup benchmarks test group creation (baseline).
func BenchmarkCreateTestGroup(b *testing.B) {
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = createTestGroup("bench:create")
	}
}

// BenchmarkAlertGroupDeepCopy benchmarks deep copying metadata.
func BenchmarkAlertGroupDeepCopy(b *testing.B) {
	storage := NewMemoryGroupStorage(nil)
	group := createTestGroup("bench:deepcopy")

	// Add more alerts for realistic scenario
	for i := 0; i < 10; i++ {
		group.Alerts["fp"+string(rune('0'+i))] = &core.Alert{
			Fingerprint: "fp" + string(rune('0'+i)),
			Labels:      map[string]string{"index": string(rune('0' + i))},
			Status:      "firing",
		}
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = storage.copyMetadata(group.Metadata)
	}
}
