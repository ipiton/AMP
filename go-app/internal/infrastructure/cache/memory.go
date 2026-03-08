package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

type memoryCacheEntry struct {
	payload   []byte
	expiresAt time.Time
}

// MemoryCache is an in-process cache implementation used as fallback when Redis is unavailable.
type MemoryCache struct {
	mu     sync.RWMutex
	values map[string]memoryCacheEntry
	sets   map[string]map[string]struct{}
	logger *slog.Logger
}

// NewMemoryCache creates a new in-process cache.
func NewMemoryCache(logger *slog.Logger) *MemoryCache {
	if logger == nil {
		logger = slog.Default()
	}

	return &MemoryCache{
		values: make(map[string]memoryCacheEntry),
		sets:   make(map[string]map[string]struct{}),
		logger: logger,
	}
}

func (c *MemoryCache) Get(ctx context.Context, key string, dest interface{}) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	c.mu.RLock()
	entry, ok := c.values[key]
	c.mu.RUnlock()
	if !ok {
		return ErrNotFound
	}

	if c.isExpired(entry) {
		c.mu.Lock()
		delete(c.values, key)
		c.mu.Unlock()
		return ErrNotFound
	}

	if err := json.Unmarshal(entry.payload, dest); err != nil {
		return NewCacheError("failed to unmarshal cache value", "UNMARSHAL_ERROR").WithCause(err)
	}

	return nil
}

func (c *MemoryCache) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if key == "" {
		return NewCacheError("key cannot be empty", "INVALID_KEY")
	}

	data, err := json.Marshal(value)
	if err != nil {
		return NewCacheError("failed to marshal cache value", "MARSHAL_ERROR").WithCause(err)
	}

	entry := memoryCacheEntry{
		payload: data,
	}
	if ttl > 0 {
		entry.expiresAt = time.Now().Add(ttl)
	}

	c.mu.Lock()
	c.values[key] = entry
	c.mu.Unlock()
	return nil
}

func (c *MemoryCache) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	c.mu.Lock()
	delete(c.values, key)
	c.mu.Unlock()
	return nil
}

func (c *MemoryCache) Exists(ctx context.Context, key string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}

	c.mu.RLock()
	entry, ok := c.values[key]
	c.mu.RUnlock()
	if !ok {
		return false, nil
	}
	if c.isExpired(entry) {
		c.mu.Lock()
		delete(c.values, key)
		c.mu.Unlock()
		return false, nil
	}
	return true, nil
}

func (c *MemoryCache) TTL(ctx context.Context, key string) (time.Duration, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	c.mu.RLock()
	entry, ok := c.values[key]
	c.mu.RUnlock()
	if !ok {
		return 0, ErrNotFound
	}
	if c.isExpired(entry) {
		c.mu.Lock()
		delete(c.values, key)
		c.mu.Unlock()
		return 0, ErrNotFound
	}
	if entry.expiresAt.IsZero() {
		return 0, nil
	}
	return time.Until(entry.expiresAt), nil
}

func (c *MemoryCache) Expire(ctx context.Context, key string, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.values[key]
	if !ok {
		return ErrNotFound
	}
	if ttl <= 0 {
		entry.expiresAt = time.Time{}
	} else {
		entry.expiresAt = time.Now().Add(ttl)
	}
	c.values[key] = entry
	return nil
}

func (c *MemoryCache) HealthCheck(_ context.Context) error {
	return nil
}

func (c *MemoryCache) Ping(_ context.Context) error {
	return nil
}

func (c *MemoryCache) Flush(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	c.mu.Lock()
	c.values = make(map[string]memoryCacheEntry)
	c.sets = make(map[string]map[string]struct{})
	c.mu.Unlock()
	return nil
}

func (c *MemoryCache) SAdd(ctx context.Context, key string, members ...interface{}) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if key == "" {
		return NewCacheError("key cannot be empty", "INVALID_KEY")
	}
	if len(members) == 0 {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	set, ok := c.sets[key]
	if !ok {
		set = make(map[string]struct{})
		c.sets[key] = set
	}
	for _, member := range members {
		set[fmt.Sprint(member)] = struct{}{}
	}
	return nil
}

func (c *MemoryCache) SMembers(ctx context.Context, key string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	c.mu.RLock()
	set, ok := c.sets[key]
	c.mu.RUnlock()
	if !ok {
		return []string{}, nil
	}

	out := make([]string, 0, len(set))
	for member := range set {
		out = append(out, member)
	}
	return out, nil
}

func (c *MemoryCache) SRem(ctx context.Context, key string, members ...interface{}) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(members) == 0 {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	set, ok := c.sets[key]
	if !ok {
		return nil
	}
	for _, member := range members {
		delete(set, fmt.Sprint(member))
	}
	if len(set) == 0 {
		delete(c.sets, key)
	}
	return nil
}

func (c *MemoryCache) SCard(ctx context.Context, key string) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	return int64(len(c.sets[key])), nil
}

func (c *MemoryCache) isExpired(entry memoryCacheEntry) bool {
	return !entry.expiresAt.IsZero() && time.Now().After(entry.expiresAt)
}
