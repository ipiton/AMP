package silencing

import (
	"fmt"
	"regexp"
	"sync"
	"testing"
)

func TestNewRegexCache(t *testing.T) {
	tests := []struct {
		name    string
		maxSize int
	}{
		{"small cache", 10},
		{"medium cache", 100},
		{"large cache", 1000},
		{"very large cache", 10000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := NewRegexCache(tt.maxSize)

			if cache == nil {
				t.Fatal("NewRegexCache returned nil")
			}
			if cache.maxSize != tt.maxSize {
				t.Errorf("maxSize = %d, want %d", cache.maxSize, tt.maxSize)
			}
			if cache.Size() != 0 {
				t.Errorf("initial size = %d, want 0", cache.Size())
			}
			if cache.cache == nil {
				t.Error("cache map is nil")
			}
		})
	}
}

func TestRegexCache_Get_CompileAndCache(t *testing.T) {
	cache := NewRegexCache(100)

	tests := []struct {
		name    string
		pattern string
		wantErr bool
	}{
		{
			name:    "simple pattern",
			pattern: "test",
			wantErr: false,
		},
		{
			name:    "alternation",
			pattern: "(critical|warning)",
			wantErr: false,
		},
		{
			name:    "character class",
			pattern: "[a-z]+",
			wantErr: false,
		},
		{
			name:    "wildcard",
			pattern: ".*-prod-.*",
			wantErr: false,
		},
		{
			name:    "anchors",
			pattern: "^start.*end$",
			wantErr: false,
		},
		{
			name:    "quantifiers",
			pattern: "a{2,5}b+c*d?",
			wantErr: false,
		},
		{
			name:    "empty pattern",
			pattern: "",
			wantErr: false, // Empty pattern is valid regex
		},
		{
			name:    "invalid pattern - unclosed bracket",
			pattern: "[invalid",
			wantErr: true,
		},
		{
			name:    "invalid pattern - unclosed paren",
			pattern: "(unclosed",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			re, err := cache.Get(tt.pattern)

			if tt.wantErr {
				if err == nil {
					t.Errorf("Get(%q) expected error, got nil", tt.pattern)
				}
				if re != nil {
					t.Errorf("Get(%q) returned non-nil regexp on error", tt.pattern)
				}
				return
			}

			if err != nil {
				t.Fatalf("Get(%q) unexpected error: %v", tt.pattern, err)
			}
			if re == nil {
				t.Fatalf("Get(%q) returned nil regexp", tt.pattern)
			}

			// Verify the pattern works
			expected, _ := regexp.Compile(tt.pattern)
			testString := "test string critical warning prod"
			if re.MatchString(testString) != expected.MatchString(testString) {
				t.Errorf("compiled regex behaves differently than expected")
			}
		})
	}
}

func TestRegexCache_Get_CacheHit(t *testing.T) {
	cache := NewRegexCache(100)
	pattern := ".*-prod-.*"

	// First call: compile and cache
	re1, err := cache.Get(pattern)
	if err != nil {
		t.Fatalf("first Get() failed: %v", err)
	}
	if cache.Size() != 1 {
		t.Errorf("cache size after first Get() = %d, want 1", cache.Size())
	}

	// Second call: cache hit (should return same instance)
	re2, err := cache.Get(pattern)
	if err != nil {
		t.Fatalf("second Get() failed: %v", err)
	}
	if cache.Size() != 1 {
		t.Errorf("cache size after second Get() = %d, want 1", cache.Size())
	}

	// Verify same instance (pointer equality)
	if re1 != re2 {
		t.Error("cache did not return same instance on second call")
	}
}

func TestRegexCache_Get_MultiplePatternsache(t *testing.T) {
	cache := NewRegexCache(100)

	patterns := []string{
		"pattern1",
		"pattern2",
		"pattern3",
		".*-prod-.*",
		"(critical|warning)",
		"^start.*end$",
	}

	// Cache all patterns
	cached := make(map[string]*regexp.Regexp)
	for _, pattern := range patterns {
		re, err := cache.Get(pattern)
		if err != nil {
			t.Fatalf("Get(%q) failed: %v", pattern, err)
		}
		cached[pattern] = re
	}

	if cache.Size() != len(patterns) {
		t.Errorf("cache size = %d, want %d", cache.Size(), len(patterns))
	}

	// Verify all patterns can be retrieved from cache
	for _, pattern := range patterns {
		re, err := cache.Get(pattern)
		if err != nil {
			t.Fatalf("Get(%q) failed on retrieval: %v", pattern, err)
		}
		if re != cached[pattern] {
			t.Errorf("Get(%q) returned different instance on retrieval", pattern)
		}
	}

	// Cache size should not have grown
	if cache.Size() != len(patterns) {
		t.Errorf("cache size after retrieval = %d, want %d", cache.Size(), len(patterns))
	}
}

func TestRegexCache_Get_EvictionOnMaxSize(t *testing.T) {
	maxSize := 5
	cache := NewRegexCache(maxSize)

	// Fill cache to max size
	for i := 0; i < maxSize; i++ {
		pattern := string(rune('a' + i)) // "a", "b", "c", "d", "e"
		_, err := cache.Get(pattern)
		if err != nil {
			t.Fatalf("Get(%q) failed: %v", pattern, err)
		}
	}

	if cache.Size() != maxSize {
		t.Errorf("cache size after filling = %d, want %d", cache.Size(), maxSize)
	}

	// Add one more pattern - should trigger eviction (full cache clear)
	_, err := cache.Get("new-pattern")
	if err != nil {
		t.Fatalf("Get(new-pattern) failed: %v", err)
	}

	// After eviction, cache should only contain the new pattern
	if cache.Size() != 1 {
		t.Errorf("cache size after eviction = %d, want 1", cache.Size())
	}

	// Verify the new pattern is cached
	re, err := cache.Get("new-pattern")
	if err != nil {
		t.Fatalf("Get(new-pattern) after eviction failed: %v", err)
	}
	if re == nil {
		t.Error("new pattern not in cache after eviction")
	}
}

func TestRegexCache_ConcurrentAccess(t *testing.T) {
	cache := NewRegexCache(100)
	basePattern := ".*-prod-.*"

	// Pre-cache one pattern
	_, err := cache.Get(basePattern)
	if err != nil {
		t.Fatalf("initial Get() failed: %v", err)
	}

	const goroutines = 100
	const iterations = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)

	// Launch many goroutines that concurrently access the cache
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()

			for j := 0; j < iterations; j++ {
				// Mix of cache hits and misses with VALID patterns only
				var pattern string
				if j%2 == 0 {
					pattern = basePattern // Cache hit
				} else {
					// Create unique but VALID pattern
					// Use alphanumeric characters only (avoid regex special chars)
					suffix := fmt.Sprintf("pattern%d", id)
					pattern = suffix // Cache miss (unique per goroutine, valid regex)
				}

				re, err := cache.Get(pattern)
				if err != nil {
					t.Errorf("goroutine %d: Get(%q) failed: %v", id, pattern, err)
					return
				}
				if re == nil {
					t.Errorf("goroutine %d: Get(%q) returned nil", id, pattern)
					return
				}
			}
		}(i)
	}

	wg.Wait()

	// Cache should contain at least the original pattern
	if cache.Size() == 0 {
		t.Error("cache is empty after concurrent access")
	}
}

func TestRegexCache_Size(t *testing.T) {
	cache := NewRegexCache(100)

	if cache.Size() != 0 {
		t.Errorf("initial size = %d, want 0", cache.Size())
	}

	patterns := []string{"a", "b", "c", "d", "e"}
	for i, pattern := range patterns {
		cache.Get(pattern)
		expectedSize := i + 1
		if cache.Size() != expectedSize {
			t.Errorf("size after %d insertions = %d, want %d", expectedSize, cache.Size(), expectedSize)
		}
	}
}

func TestRegexCache_Clear(t *testing.T) {
	cache := NewRegexCache(100)

	// Add some patterns
	patterns := []string{"a", "b", "c", "d", "e"}
	for _, pattern := range patterns {
		_, err := cache.Get(pattern)
		if err != nil {
			t.Fatalf("Get(%q) failed: %v", pattern, err)
		}
	}

	if cache.Size() != len(patterns) {
		t.Fatalf("size before clear = %d, want %d", cache.Size(), len(patterns))
	}

	// Clear the cache
	cache.Clear()

	if cache.Size() != 0 {
		t.Errorf("size after clear = %d, want 0", cache.Size())
	}

	// Verify patterns need to be recompiled
	re1, _ := cache.Get("a")
	re2, _ := cache.Get("a")

	// Should be same instance (both from cache)
	if re1 != re2 {
		t.Error("pattern not properly re-cached after clear")
	}
}

// Benchmarks

func BenchmarkRegexCache_Get_CacheHit(b *testing.B) {
	cache := NewRegexCache(1000)
	pattern := ".*-prod-.*"

	// Pre-cache the pattern
	cache.Get(pattern)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := cache.Get(pattern)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRegexCache_Get_CacheMiss(b *testing.B) {
	cache := NewRegexCache(1000000) // Large size to avoid eviction

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		pattern := string(rune('a' + (i % 26))) + string(rune('0' + (i % 10)))
		_, err := cache.Get(pattern)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRegexCache_Get_ConcurrentReads(b *testing.B) {
	cache := NewRegexCache(1000)
	pattern := ".*-prod-.*"

	// Pre-cache the pattern
	cache.Get(pattern)

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, err := cache.Get(pattern)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkRegexCompile_NoCaching(b *testing.B) {
	// Baseline: raw regexp.Compile without caching
	pattern := ".*-prod-.*"

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := regexp.Compile(pattern)
		if err != nil {
			b.Fatal(err)
		}
	}
}
