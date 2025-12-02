package cache

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestRedis(t *testing.T) (*RedisCache, *miniredis.Miniredis) {
	// Создаем mock Redis сервер
	mr, err := miniredis.Run()
	require.NoError(t, err)

	// Создаем конфигурацию для тестового Redis
	config := &CacheConfig{
		Addr:       mr.Addr(),
		Password:   "",
		DB:         0,
		PoolSize:   5,
		DialTimeout: 1 * time.Second,
		ReadTimeout: 1 * time.Second,
	}

	// Создаем Redis cache
	cache, err := NewRedisCache(config, nil)
	require.NoError(t, err)

	return cache, mr
}

func TestRedisCache_Get(t *testing.T) {
	cache, mr := setupTestRedis(t)
	defer mr.Close()
	defer cache.Close()

	ctx := context.Background()
	key := "test_key"

	t.Run("get existing key", func(t *testing.T) {
		// Устанавливаем значение
		testValue := map[string]string{"name": "test", "value": "123"}
		err := cache.Set(ctx, key, testValue, time.Minute)
		require.NoError(t, err)

		// Получаем значение
		var result map[string]string
		err = cache.Get(ctx, key, &result)
		assert.NoError(t, err)
		assert.Equal(t, testValue, result)
	})

	t.Run("get non-existing key", func(t *testing.T) {
		var result map[string]string
		err := cache.Get(ctx, "non_existing_key", &result)
		assert.Error(t, err)
		assert.True(t, IsNotFound(err))
	})
}

func TestRedisCache_Set(t *testing.T) {
	cache, mr := setupTestRedis(t)
	defer mr.Close()
	defer cache.Close()

	ctx := context.Background()

	t.Run("set string value", func(t *testing.T) {
		err := cache.Set(ctx, "string_key", "test_value", time.Minute)
		assert.NoError(t, err)

		// Проверяем, что значение установлено
		var result string
		err = cache.Get(ctx, "string_key", &result)
		assert.NoError(t, err)
		assert.Equal(t, "test_value", result)
	})

	t.Run("set complex value", func(t *testing.T) {
		type TestStruct struct {
			Name  string `json:"name"`
			Value int    `json:"value"`
			Items []string `json:"items"`
		}

		testValue := TestStruct{
			Name:  "test",
			Value: 42,
			Items: []string{"item1", "item2"},
		}

		err := cache.Set(ctx, "complex_key", testValue, time.Minute)
		assert.NoError(t, err)

		// Получаем и проверяем
		var result TestStruct
		err = cache.Get(ctx, "complex_key", &result)
		assert.NoError(t, err)
		assert.Equal(t, testValue, result)
	})

	t.Run("set with TTL", func(t *testing.T) {
		key := "ttl_key"
		err := cache.Set(ctx, key, "ttl_value", time.Minute)
		assert.NoError(t, err)

		// Проверяем, что значение есть
		var result string
		err = cache.Get(ctx, key, &result)
		assert.NoError(t, err)
		assert.Equal(t, "ttl_value", result)

		// Проверяем TTL
		ttl, err := cache.TTL(ctx, key)
		assert.NoError(t, err)
		assert.True(t, ttl > 0)
		assert.True(t, ttl <= time.Minute)
	})
}

func TestRedisCache_Delete(t *testing.T) {
	cache, mr := setupTestRedis(t)
	defer mr.Close()
	defer cache.Close()

	ctx := context.Background()
	key := "delete_key"

	t.Run("delete existing key", func(t *testing.T) {
		// Устанавливаем значение
		err := cache.Set(ctx, key, "test_value", time.Minute)
		require.NoError(t, err)

		// Удаляем
		err = cache.Delete(ctx, key)
		assert.NoError(t, err)

		// Проверяем, что ключ удален
		var result string
		err = cache.Get(ctx, key, &result)
		assert.Error(t, err)
		assert.True(t, IsNotFound(err))
	})

	t.Run("delete non-existing key", func(t *testing.T) {
		err := cache.Delete(ctx, "non_existing_key")
		assert.Error(t, err)
		assert.True(t, IsNotFound(err))
	})
}

func TestRedisCache_Exists(t *testing.T) {
	cache, mr := setupTestRedis(t)
	defer mr.Close()
	defer cache.Close()

	ctx := context.Background()
	key := "exists_key"

	t.Run("key exists", func(t *testing.T) {
		// Устанавливаем значение
		err := cache.Set(ctx, key, "test_value", time.Minute)
		require.NoError(t, err)

		// Проверяем существование
		exists, err := cache.Exists(ctx, key)
		assert.NoError(t, err)
		assert.True(t, exists)
	})

	t.Run("key does not exist", func(t *testing.T) {
		exists, err := cache.Exists(ctx, "non_existing_key")
		assert.NoError(t, err)
		assert.False(t, exists)
	})
}

func TestRedisCache_TTL(t *testing.T) {
	cache, mr := setupTestRedis(t)
	defer mr.Close()
	defer cache.Close()

	ctx := context.Background()
	key := "ttl_key"

	t.Run("get TTL", func(t *testing.T) {
		// Устанавливаем значение с TTL
		ttl := 2 * time.Second
		err := cache.Set(ctx, key, "test_value", ttl)
		require.NoError(t, err)

		// Получаем TTL
		actualTTL, err := cache.TTL(ctx, key)
		assert.NoError(t, err)
		assert.True(t, actualTTL > 0)
		assert.True(t, actualTTL <= ttl)
	})

	t.Run("TTL for non-existing key", func(t *testing.T) {
		ttl, err := cache.TTL(ctx, "non_existing_key")
		assert.NoError(t, err)
		assert.Equal(t, time.Duration(-2), ttl) // Redis возвращает -2 для несуществующих ключей
	})
}

func TestRedisCache_Expire(t *testing.T) {
	cache, mr := setupTestRedis(t)
	defer mr.Close()
	defer cache.Close()

	ctx := context.Background()
	key := "expire_key"

	t.Run("set TTL for existing key", func(t *testing.T) {
		// Устанавливаем значение без TTL
		err := cache.Set(ctx, key, "test_value", 0)
		require.NoError(t, err)

		// Устанавливаем TTL
		newTTL := 2 * time.Second
		err = cache.Expire(ctx, key, newTTL)
		assert.NoError(t, err)

		// Проверяем TTL
		actualTTL, err := cache.TTL(ctx, key)
		assert.NoError(t, err)
		assert.True(t, actualTTL > 0)
		assert.True(t, actualTTL <= newTTL)
	})

	t.Run("set TTL for non-existing key", func(t *testing.T) {
		err := cache.Expire(ctx, "non_existing_key", time.Second)
		assert.Error(t, err)
		assert.True(t, IsNotFound(err))
	})
}

func TestRedisCache_HealthCheck(t *testing.T) {
	cache, mr := setupTestRedis(t)
	defer mr.Close()
	defer cache.Close()

	ctx := context.Background()

	t.Run("healthy cache", func(t *testing.T) {
		err := cache.HealthCheck(ctx)
		assert.NoError(t, err)
	})

	t.Run("ping cache", func(t *testing.T) {
		err := cache.Ping(ctx)
		assert.NoError(t, err)
	})
}

func TestRedisCache_Flush(t *testing.T) {
	cache, mr := setupTestRedis(t)
	defer mr.Close()
	defer cache.Close()

	ctx := context.Background()

	// Устанавливаем несколько значений
	err := cache.Set(ctx, "key1", "value1", time.Minute)
	require.NoError(t, err)
	err = cache.Set(ctx, "key2", "value2", time.Minute)
	require.NoError(t, err)

	// Проверяем, что значения есть
	exists1, err := cache.Exists(ctx, "key1")
	require.NoError(t, err)
	assert.True(t, exists1)

	exists2, err := cache.Exists(ctx, "key2")
	require.NoError(t, err)
	assert.True(t, exists2)

	// Очищаем cache
	err = cache.Flush(ctx)
	assert.NoError(t, err)

	// Проверяем, что значения исчезли
	exists1, err = cache.Exists(ctx, "key1")
	require.NoError(t, err)
	assert.False(t, exists1)

	exists2, err = cache.Exists(ctx, "key2")
	require.NoError(t, err)
	assert.False(t, exists2)
}

func TestRedisCache_GetStats(t *testing.T) {
	cache, mr := setupTestRedis(t)
	defer mr.Close()
	defer cache.Close()

	ctx := context.Background()

	stats, err := cache.GetStats(ctx)
	assert.NoError(t, err)
	assert.NotNil(t, stats)
	assert.Contains(t, stats, "pool_size")
	assert.Contains(t, stats, "healthy")
}

func TestRedisCache_Close(t *testing.T) {
	cache, mr := setupTestRedis(t)
	defer mr.Close()

	ctx := context.Background()

	// Проверяем, что cache работает
	err := cache.Set(ctx, "test_key", "test_value", time.Minute)
	assert.NoError(t, err)

	// Закрываем cache
	err = cache.Close()
	assert.NoError(t, err)

	// Проверяем, что после закрытия операции возвращают ошибку
	err = cache.Set(ctx, "test_key2", "test_value2", time.Minute)
	assert.Error(t, err)
	assert.True(t, IsConnectionError(err))
}

func TestRedisCache_Errors(t *testing.T) {
	t.Run("cache error with cause", func(t *testing.T) {
		originalErr := assert.AnError
		cacheErr := NewCacheError("test error", "TEST_ERROR").WithCause(originalErr)

		assert.Equal(t, "test error", cacheErr.Message)
		assert.Equal(t, "TEST_ERROR", cacheErr.Code)
		assert.Equal(t, originalErr, cacheErr.Cause)
		assert.Contains(t, cacheErr.Error(), "test error")
		assert.Contains(t, cacheErr.Error(), assert.AnError.Error())
	})

	t.Run("is not found error", func(t *testing.T) {
		assert.True(t, IsNotFound(ErrNotFound))
		assert.False(t, IsNotFound(ErrConnectionFailed))
	})

	t.Run("is connection error", func(t *testing.T) {
		assert.True(t, IsConnectionError(ErrConnectionFailed))
		assert.False(t, IsConnectionError(ErrNotFound))
	})
}

func TestCacheConfig_Validate(t *testing.T) {
	t.Run("valid config", func(t *testing.T) {
		config := &CacheConfig{
			Addr:        "localhost:6379",
			PoolSize:    10,
			DialTimeout: time.Second,
		}
		err := config.Validate()
		assert.NoError(t, err)
	})

	t.Run("empty address", func(t *testing.T) {
		config := &CacheConfig{
			Addr:        "",
			PoolSize:    10,
			DialTimeout: time.Second,
		}
		err := config.Validate()
		assert.Error(t, err)
		assert.Equal(t, ErrInvalidConfig, err)
	})

	t.Run("invalid pool size", func(t *testing.T) {
		config := &CacheConfig{
			Addr:        "localhost:6379",
			PoolSize:    0,
			DialTimeout: time.Second,
		}
		err := config.Validate()
		assert.Error(t, err)
		assert.Equal(t, ErrInvalidConfig, err)
	})

	t.Run("invalid dial timeout", func(t *testing.T) {
		config := &CacheConfig{
			Addr:        "localhost:6379",
			PoolSize:    10,
			DialTimeout: -1 * time.Second,
		}
		err := config.Validate()
		assert.Error(t, err)
		assert.Equal(t, ErrInvalidConfig, err)
	})
}

func BenchmarkRedisCache_Get(b *testing.B) {
	cache, mr := setupTestRedis(nil)
	defer mr.Close()
	defer cache.Close()

	ctx := context.Background()

	// Устанавливаем значение для бенчмарка
	err := cache.Set(ctx, "bench_key", "bench_value", time.Hour)
	require.NoError(nil, err)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		var result string
		err := cache.Get(ctx, "bench_key", &result)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRedisCache_Set(b *testing.B) {
	cache, mr := setupTestRedis(nil)
	defer mr.Close()
	defer cache.Close()

	ctx := context.Background()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("bench_key_%d", i)
		err := cache.Set(ctx, key, "bench_value", time.Hour)
		if err != nil {
			b.Fatal(err)
		}
	}
}
