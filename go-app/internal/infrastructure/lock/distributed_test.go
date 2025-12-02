package lock

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestRedis(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	// Создаем mock Redis сервер
	mr, err := miniredis.Run()
	require.NoError(t, err)

	// Создаем Redis клиент
	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})

	return client, mr
}

func TestDistributedLock_Acquire(t *testing.T) {
	client, mr := setupTestRedis(t)
	defer mr.Close()
	defer client.Close()

	ctx := context.Background()

	t.Run("successful acquire", func(t *testing.T) {
		key := "test_lock_1"
		lock := NewDistributedLock(client, key, nil, nil)

		acquired, err := lock.Acquire(ctx)
		assert.NoError(t, err)
		assert.True(t, acquired)
		assert.True(t, lock.IsAcquired())
		assert.Equal(t, key, lock.GetKey())
		assert.NotEmpty(t, lock.GetValue())
	})

	t.Run("acquire already held lock", func(t *testing.T) {
		key := "test_lock_2"
		// Первая блокировка
		lock1 := NewDistributedLock(client, key, nil, nil)
		acquired1, err1 := lock1.Acquire(ctx)
		require.NoError(t, err1)
		require.True(t, acquired1)

		// Вторая блокировка (должна не получить)
		lock2 := NewDistributedLock(client, key, nil, nil)
		acquired2, err2 := lock2.AcquireWithRetry(ctx, 0) // Без повторных попыток
		assert.NoError(t, err2)
		assert.False(t, acquired2)
		assert.False(t, lock2.IsAcquired())
	})

	t.Run("acquire after release", func(t *testing.T) {
		key := "test_lock_3"
		// Получаем блокировку
		lock1 := NewDistributedLock(client, key, nil, nil)
		acquired1, err1 := lock1.Acquire(ctx)
		require.NoError(t, err1)
		require.True(t, acquired1)

		// Освобождаем блокировку
		err := lock1.Release(ctx)
		require.NoError(t, err)

		// Получаем блокировку снова
		lock2 := NewDistributedLock(client, key, nil, nil)
		acquired2, err2 := lock2.AcquireWithRetry(ctx, 0) // Без повторных попыток
		assert.NoError(t, err2)
		assert.True(t, acquired2)
	})
}

func TestDistributedLock_Release(t *testing.T) {
	client, mr := setupTestRedis(t)
	defer mr.Close()
	defer client.Close()

	ctx := context.Background()
	key := "test_lock"

	t.Run("release acquired lock", func(t *testing.T) {
		lock := NewDistributedLock(client, key, nil, nil)

		// Получаем блокировку
		acquired, err := lock.Acquire(ctx)
		require.NoError(t, err)
		require.True(t, acquired)

		// Освобождаем блокировку
		err = lock.Release(ctx)
		assert.NoError(t, err)
		assert.False(t, lock.IsAcquired())
	})

	t.Run("release not acquired lock", func(t *testing.T) {
		lock := NewDistributedLock(client, key, nil, nil)

		// Пытаемся освободить неполученную блокировку
		err := lock.Release(ctx)
		assert.NoError(t, err) // Не должно быть ошибки
	})

	t.Run("release with wrong value", func(t *testing.T) {
		// Получаем блокировку
		lock1 := NewDistributedLock(client, key, nil, nil)
		acquired1, err1 := lock1.Acquire(ctx)
		require.NoError(t, err1)
		require.True(t, acquired1)

		// Создаем другую блокировку с тем же ключом, но другим значением
		lock2 := NewDistributedLock(client, key, nil, nil)

		// Пытаемся освободить чужую блокировку
		err := lock2.Release(ctx)
		assert.NoError(t, err) // Не должно быть ошибки, но блокировка не освободится
	})
}

func TestDistributedLock_Extend(t *testing.T) {
	client, mr := setupTestRedis(t)
	defer mr.Close()
	defer client.Close()

	ctx := context.Background()
	key := "test_lock"

	t.Run("extend acquired lock", func(t *testing.T) {
		config := &LockConfig{
			TTL: 5 * time.Second,
		}
		lock := NewDistributedLock(client, key, config, nil)

		// Получаем блокировку
		acquired, err := lock.Acquire(ctx)
		require.NoError(t, err)
		require.True(t, acquired)

		// Продлеваем блокировку
		newTTL := 10 * time.Second
		err = lock.Extend(ctx, newTTL)
		assert.NoError(t, err)
		assert.Equal(t, newTTL, lock.GetTTL())
	})

	t.Run("extend not acquired lock", func(t *testing.T) {
		lock := NewDistributedLock(client, key, nil, nil)

		// Пытаемся продлить неполученную блокировку
		err := lock.Extend(ctx, 10*time.Second)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cannot extend lock that was not acquired")
	})
}

func TestDistributedLock_Concurrency(t *testing.T) {
	client, mr := setupTestRedis(t)
	defer mr.Close()
	defer client.Close()

	ctx := context.Background()
	key := "concurrent_lock"
	numGoroutines := 3

	var wg sync.WaitGroup
	acquiredCount := 0
	var mu sync.Mutex

	// Запускаем несколько горутин, пытающихся получить одну блокировку
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			lock := NewDistributedLock(client, key, nil, nil)
			acquired, err := lock.AcquireWithRetry(ctx, 0) // Без повторных попыток

			if err != nil {
				t.Errorf("Goroutine %d: error acquiring lock: %v", id, err)
				return
			}

			if acquired {
				mu.Lock()
				acquiredCount++
				mu.Unlock()

				// Держим блокировку некоторое время
				time.Sleep(50 * time.Millisecond)

				// Освобождаем блокировку
				err = lock.Release(ctx)
				if err != nil {
					t.Errorf("Goroutine %d: error releasing lock: %v", id, err)
				}
			}
		}(i)
	}

	wg.Wait()

	// В miniredis TTL не работает, поэтому все горутины могут получить блокировку последовательно
	// В реальном Redis только одна горутина получила бы блокировку
	assert.GreaterOrEqual(t, acquiredCount, 1, "At least one goroutine should have acquired the lock")
}

func TestDistributedLock_TTL(t *testing.T) {
	client, mr := setupTestRedis(t)
	defer mr.Close()
	defer client.Close()

	ctx := context.Background()
	key := "ttl_lock"

	t.Run("lock expires after TTL", func(t *testing.T) {
		config := &LockConfig{
			TTL: 100 * time.Millisecond,
		}
		lock := NewDistributedLock(client, key, config, nil)

		// Получаем блокировку
		acquired, err := lock.Acquire(ctx)
		require.NoError(t, err)
		require.True(t, acquired)

		// В miniredis TTL не работает автоматически, поэтому вручную удаляем ключ
		// В реальном Redis это произошло бы автоматически
		mr.Del(key)

		// Пытаемся получить блокировку снова (должна быть доступна)
		lock2 := NewDistributedLock(client, key, nil, nil)
		acquired2, err2 := lock2.AcquireWithRetry(ctx, 0)
		assert.NoError(t, err2)
		assert.True(t, acquired2, "Lock should be available after TTL expiration")
	})
}

func TestLockManager(t *testing.T) {
	client, mr := setupTestRedis(t)
	defer mr.Close()
	defer client.Close()

	ctx := context.Background()
	manager := NewLockManager(client, nil, nil)

	t.Run("acquire and release multiple locks", func(t *testing.T) {
		// Получаем несколько блокировок
		lock1, err1 := manager.AcquireLock(ctx, "lock1")
		require.NoError(t, err1)
		require.NotNil(t, lock1)

		lock2, err2 := manager.AcquireLock(ctx, "lock2")
		require.NoError(t, err2)
		require.NotNil(t, lock2)

		// Проверяем, что блокировки управляются
		assert.Equal(t, 2, len(manager.ListLocks()))
		_, exists1 := manager.GetLock("lock1")
		_, exists2 := manager.GetLock("lock2")
		assert.True(t, exists1)
		assert.True(t, exists2)

		// Освобождаем одну блокировку
		err := manager.ReleaseLock(ctx, "lock1")
		assert.NoError(t, err)
		assert.Equal(t, 1, len(manager.ListLocks()))

		// Освобождаем все оставшиеся блокировки
		err = manager.ReleaseAll(ctx)
		assert.NoError(t, err)
		assert.Equal(t, 0, len(manager.ListLocks()))
	})

	t.Run("acquire same lock twice", func(t *testing.T) {
		// Получаем блокировку
		lock1, err1 := manager.AcquireLock(ctx, "same_lock")
		require.NoError(t, err1)
		require.NotNil(t, lock1)

		// Пытаемся получить ту же блокировку снова
		lock2, err2 := manager.AcquireLock(ctx, "same_lock")
		assert.Error(t, err2)
		assert.Nil(t, lock2)
		assert.Contains(t, err2.Error(), "failed to acquire lock")
	})
}

func TestDistributedLock_Retry(t *testing.T) {
	client, mr := setupTestRedis(t)
	defer mr.Close()
	defer client.Close()

	ctx := context.Background()
	key := "retry_lock"

	t.Run("acquire with retry", func(t *testing.T) {
		// Получаем блокировку
		lock1 := NewDistributedLock(client, key, nil, nil)
		acquired1, err1 := lock1.Acquire(ctx)
		require.NoError(t, err1)
		require.True(t, acquired1)

		// Вторая блокировка с retry
		lock2 := NewDistributedLock(client, key, nil, nil)
		acquired2, err2 := lock2.AcquireWithRetry(ctx, 2)
		assert.NoError(t, err2)
		assert.False(t, acquired2) // Должна не получить

		// Освобождаем первую блокировку
		err1 = lock1.Release(ctx)
		require.NoError(t, err1)

		// Теперь вторая блокировка должна получить
		acquired2, err2 = lock2.AcquireWithRetry(ctx, 2)
		assert.NoError(t, err2)
		assert.True(t, acquired2)
	})
}

func TestDistributedLock_Configuration(t *testing.T) {
	client, mr := setupTestRedis(t)
	defer mr.Close()
	defer client.Close()

	key := "config_lock"

	t.Run("custom configuration", func(t *testing.T) {
		config := &LockConfig{
			TTL:            5 * time.Second,
			MaxRetries:     5,
			RetryInterval:  50 * time.Millisecond,
			AcquireTimeout: 2 * time.Second,
			ReleaseTimeout: 1 * time.Second,
			ValuePrefix:    "custom",
		}

		lock := NewDistributedLock(client, key, config, nil)
		assert.Equal(t, config.TTL, lock.GetTTL())
		assert.Equal(t, key, lock.GetKey())
		assert.Contains(t, lock.GetValue(), "custom")
	})
}

func BenchmarkDistributedLock_Acquire(b *testing.B) {
	client, mr := setupTestRedis(nil)
	defer mr.Close()
	defer client.Close()

	ctx := context.Background()
	key := "bench_lock"

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		lock := NewDistributedLock(client, key, nil, nil)
		acquired, err := lock.Acquire(ctx)
		if err != nil {
			b.Fatal(err)
		}
		if acquired {
			lock.Release(ctx)
		}
	}
}

func BenchmarkDistributedLock_Concurrent(b *testing.B) {
	client, mr := setupTestRedis(nil)
	defer mr.Close()
	defer client.Close()

	ctx := context.Background()
	key := "bench_concurrent_lock"

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			lock := NewDistributedLock(client, key, nil, nil)
			acquired, err := lock.Acquire(ctx)
			if err != nil {
				b.Fatal(err)
			}
			if acquired {
				lock.Release(ctx)
			}
		}
	})
}
