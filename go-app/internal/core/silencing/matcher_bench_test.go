package silencing

import (
	"context"
	"testing"
)

// ====================
// PHASE 7: Benchmarks (10 benchmarks)
// ====================

func BenchmarkMatcherEqual(b *testing.B) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"job":      "api-server",
		"severity": "critical",
	})

	silence := newTestSilence("s1", []Matcher{
		{Name: "job", Value: "api-server", Type: MatcherTypeEqual},
	})

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := matcher.Matches(ctx, alert, silence)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMatcherNotEqual(b *testing.B) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"env": "prod",
	})

	silence := newTestSilence("s1", []Matcher{
		{Name: "env", Value: "dev", Type: MatcherTypeNotEqual},
	})

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := matcher.Matches(ctx, alert, silence)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMatcherRegex_CacheHit(b *testing.B) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"instance": "server-prod-01",
	})

	silence := newTestSilence("s1", []Matcher{
		{Name: "instance", Value: ".*-prod-.*", Type: MatcherTypeRegex},
	})

	// Pre-warm cache
	matcher.Matches(ctx, alert, silence)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := matcher.Matches(ctx, alert, silence)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMatcherRegex_CacheMiss(b *testing.B) {
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"instance": "server-prod-01",
	})

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// Create new matcher each time to avoid cache
		matcher := NewSilenceMatcher()

		silence := newTestSilence("s1", []Matcher{
			{Name: "instance", Value: ".*-prod-.*", Type: MatcherTypeRegex},
		})

		_, err := matcher.Matches(ctx, alert, silence)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMatcherNotRegex(b *testing.B) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"instance": "server-prod-01",
	})

	silence := newTestSilence("s1", []Matcher{
		{Name: "instance", Value: ".*-dev-.*", Type: MatcherTypeNotRegex},
	})

	// Pre-warm cache
	matcher.Matches(ctx, alert, silence)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := matcher.Matches(ctx, alert, silence)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMultiMatcher_10Matchers(b *testing.B) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"l1": "v1", "l2": "v2", "l3": "v3", "l4": "v4", "l5": "v5",
		"l6": "v6", "l7": "v7", "l8": "v8", "l9": "v9", "l10": "v10",
	})

	matchers := make([]Matcher, 10)
	for i := 0; i < 10; i++ {
		matchers[i] = Matcher{
			Name:  string(rune('l')) + string(rune('1'+i)),
			Value: "v" + string(rune('1'+i)),
			Type:  MatcherTypeEqual,
		}
	}

	silence := newTestSilence("s1", matchers)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := matcher.Matches(ctx, alert, silence)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMatchesAny_10Silences(b *testing.B) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"job":      "api-server",
		"severity": "critical",
	})

	silences := make([]*Silence, 10)
	for i := 0; i < 10; i++ {
		silences[i] = newTestSilence(
			string(rune('s'))+string(rune('0'+i)),
			[]Matcher{
				{Name: "job", Value: "api-server", Type: MatcherTypeEqual},
			},
		)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := matcher.MatchesAny(ctx, alert, silences)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMatchesAny_100Silences(b *testing.B) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"job":      "api-server",
		"severity": "critical",
	})

	silences := make([]*Silence, 100)
	for i := 0; i < 100; i++ {
		silences[i] = newTestSilence(
			string(rune('s'))+string(rune('0'+i)),
			[]Matcher{
				{Name: "job", Value: "api-server", Type: MatcherTypeEqual},
				{Name: "severity", Value: "(critical|warning)", Type: MatcherTypeRegex},
			},
		)
	}

	// Pre-warm regex cache
	matcher.MatchesAny(ctx, alert, silences[:1])

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := matcher.MatchesAny(ctx, alert, silences)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMatchesAny_1000Silences(b *testing.B) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"job":      "api-server",
		"severity": "critical",
	})

	silences := make([]*Silence, 1000)
	for i := 0; i < 1000; i++ {
		silences[i] = newTestSilence(
			string(rune('s'))+string(rune('0'+i)),
			[]Matcher{
				{Name: "job", Value: "api-server", Type: MatcherTypeEqual},
				{Name: "severity", Value: "(critical|warning)", Type: MatcherTypeRegex},
			},
		)
	}

	// Pre-warm regex cache
	matcher.MatchesAny(ctx, alert, silences[:1])

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := matcher.MatchesAny(ctx, alert, silences)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMatchesAny_MixedOperators(b *testing.B) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"alertname": "HighCPU",
		"job":       "api-server",
		"env":       "prod",
		"severity":  "critical",
		"instance":  "server-prod-01",
	})

	silences := make([]*Silence, 100)
	for i := 0; i < 100; i++ {
		silences[i] = newTestSilence(
			string(rune('s'))+string(rune('0'+i)),
			[]Matcher{
				{Name: "alertname", Value: "HighCPU", Type: MatcherTypeEqual},           // =
				{Name: "env", Value: "dev", Type: MatcherTypeNotEqual},                  // !=
				{Name: "severity", Value: "(critical|warning)", Type: MatcherTypeRegex}, // =~
				{Name: "instance", Value: ".*-dev-.*", Type: MatcherTypeNotRegex},       // !~
			},
		)
	}

	// Pre-warm cache
	matcher.MatchesAny(ctx, alert, silences[:1])

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := matcher.MatchesAny(ctx, alert, silences)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRegexCache_ConcurrentAccess(b *testing.B) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"instance": "server-prod-01",
	})

	silence := newTestSilence("s1", []Matcher{
		{Name: "instance", Value: ".*-prod-.*", Type: MatcherTypeRegex},
	})

	// Pre-warm cache
	matcher.Matches(ctx, alert, silence)

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, err := matcher.Matches(ctx, alert, silence)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

// Comparison Benchmarks

func BenchmarkMatcherEqual_vs_Regex(b *testing.B) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"label": "value",
	})

	b.Run("Equal", func(b *testing.B) {
		silence := newTestSilence("s1", []Matcher{
			{Name: "label", Value: "value", Type: MatcherTypeEqual},
		})

		b.ResetTimer()
		b.ReportAllocs()

		for i := 0; i < b.N; i++ {
			matcher.Matches(ctx, alert, silence)
		}
	})

	b.Run("Regex", func(b *testing.B) {
		silence := newTestSilence("s1", []Matcher{
			{Name: "label", Value: "value", Type: MatcherTypeRegex},
		})

		// Pre-warm cache
		matcher.Matches(ctx, alert, silence)

		b.ResetTimer()
		b.ReportAllocs()

		for i := 0; i < b.N; i++ {
			matcher.Matches(ctx, alert, silence)
		}
	})
}

func BenchmarkEarlyExitOptimization(b *testing.B) {
	matcher := NewSilenceMatcher()
	ctx := context.Background()

	alert := newTestAlert(map[string]string{
		"label1": "wrong", // First matcher fails
		"label2": "value2",
		"label3": "value3",
	})

	silence := newTestSilence("s1", []Matcher{
		{Name: "label1", Value: "correct", Type: MatcherTypeEqual}, // Fails here
		{Name: "label2", Value: "value2", Type: MatcherTypeEqual},
		{Name: "label3", Value: "value3", Type: MatcherTypeEqual},
	})

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		matched, err := matcher.Matches(ctx, alert, silence)
		if err != nil {
			b.Fatal(err)
		}
		if matched {
			b.Fatal("expected no match")
		}
	}
}
