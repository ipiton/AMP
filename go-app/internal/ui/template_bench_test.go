package ui

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// BenchmarkRenderDashboard benchmarks rendering dashboard template.
func BenchmarkRenderDashboard(b *testing.B) {
	// Create temporary template directory
	tmpDir := b.TempDir()

	// Create dashboard template
	dashboardHTML := `{{ define "dashboard" }}
<h1>{{ .Title }}</h1>
<div>Firing: {{ .Data.FiringAlerts }}</div>
<div>Resolved: {{ .Data.ResolvedAlerts }}</div>
{{ range .Data.RecentAlerts }}
  <div class="{{ statusClass .Status }}">
    {{ .AlertName }} - {{ timeAgo .StartsAt }}
  </div>
{{ end }}
{{ end }}`
	createTestTemplate(b, tmpDir, "dashboard.html", dashboardHTML)

	// Create engine
	opts := TemplateOptions{
		TemplateDir:   tmpDir,
		HotReload:     false,
		Cache:         true,
		EnableMetrics: false, // Disable metrics for pure render performance
	}
	engine, err := NewTemplateEngine(opts)
	if err != nil {
		b.Fatalf("Failed to create engine: %v", err)
	}

	// Prepare data
	data := &PageData{
		Title: "Dashboard",
		Data: map[string]interface{}{
			"FiringAlerts":   42,
			"ResolvedAlerts": 128,
			"RecentAlerts": []map[string]interface{}{
				{
					"AlertName": "HighCPUUsage",
					"Status":    "firing",
					"StartsAt":  time.Now().Add(-5 * time.Minute),
				},
				{
					"AlertName": "DiskSpaceLow",
					"Status":    "resolved",
					"StartsAt":  time.Now().Add(-30 * time.Minute),
				},
			},
		},
	}

	// Reset timer before benchmark loop
	b.ResetTimer()

	// Benchmark
	for i := 0; i < b.N; i++ {
		_, err := engine.RenderString("dashboard", data)
		if err != nil {
			b.Fatalf("Render failed: %v", err)
		}
	}
}

// BenchmarkRenderAlertList benchmarks rendering alert list (10 alerts).
func BenchmarkRenderAlertList(b *testing.B) {
	tmpDir := b.TempDir()

	// Create alert list template
	listHTML := `{{ define "alerts" }}
<div class="alert-list">
{{ range .Data.Alerts }}
  <div class="alert-card">
    <span class="{{ severity .Severity }}">{{ .Severity }}</span>
    <h3>{{ .AlertName }}</h3>
    <p>{{ truncate .Summary 100 }}</p>
    <span>{{ timeAgo .StartsAt }}</span>
  </div>
{{ end }}
</div>
{{ end }}`
	createTestTemplate(b, tmpDir, "alerts.html", listHTML)

	opts := TemplateOptions{
		TemplateDir:   tmpDir,
		HotReload:     false,
		Cache:         true,
		EnableMetrics: false,
	}
	engine, err := NewTemplateEngine(opts)
	if err != nil {
		b.Fatalf("Failed to create engine: %v", err)
	}

	// Create 10 alerts
	alerts := make([]map[string]interface{}, 10)
	for i := 0; i < 10; i++ {
		alerts[i] = map[string]interface{}{
			"AlertName": "TestAlert",
			"Severity":  "critical",
			"Summary":   "This is a test alert with a very long description that needs to be truncated to fit in the UI display area",
			"StartsAt":  time.Now().Add(-time.Duration(i) * time.Minute),
		}
	}

	data := &PageData{
		Title: "Alerts",
		Data: map[string]interface{}{
			"Alerts": alerts,
		},
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := engine.RenderString("alerts", data)
		if err != nil {
			b.Fatalf("Render failed: %v", err)
		}
	}
}

// BenchmarkCustomFunctions benchmarks individual custom functions.
func BenchmarkCustomFunctions(b *testing.B) {
	b.Run("formatTime", func(b *testing.B) {
		t := time.Now()
		for i := 0; i < b.N; i++ {
			_ = formatTime(t)
		}
	})

	b.Run("timeAgo", func(b *testing.B) {
		t := time.Now().Add(-5 * time.Minute)
		for i := 0; i < b.N; i++ {
			_ = timeAgo(t)
		}
	})

	b.Run("severity", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = severity("critical")
		}
	})

	b.Run("statusClass", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = statusClass("firing")
		}
	})

	b.Run("truncate", func(b *testing.B) {
		s := "This is a very long string that needs to be truncated to a reasonable length for display purposes"
		for i := 0; i < b.N; i++ {
			_ = truncate(s, 50)
		}
	})

	b.Run("jsonPretty", func(b *testing.B) {
		data := map[string]interface{}{
			"name":  "test",
			"value": 123,
			"tags":  []string{"tag1", "tag2"},
		}
		for i := 0; i < b.N; i++ {
			_ = jsonPretty(data)
		}
	})

	b.Run("defaultVal", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = defaultVal("default", "")
		}
	})

	b.Run("join", func(b *testing.B) {
		slice := []string{"tag1", "tag2", "tag3", "tag4", "tag5"}
		for i := 0; i < b.N; i++ {
			_ = join(slice, ", ")
		}
	})

	b.Run("contains", func(b *testing.B) {
		slice := []string{"admin", "editor", "viewer"}
		for i := 0; i < b.N; i++ {
			_ = contains(slice, "admin")
		}
	})

	b.Run("add", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = add(10, 5)
		}
	})

	b.Run("plural", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = plural(5)
		}
	})
}

// BenchmarkTemplateCache benchmarks template caching performance.
func BenchmarkTemplateCache(b *testing.B) {
	tmpDir := b.TempDir()

	// Create simple template
	simpleHTML := `{{ define "simple" }}Hello {{ .Data.Name }}{{ end }}`
	createTestTemplate(b, tmpDir, "simple.html", simpleHTML)

	opts := TemplateOptions{
		TemplateDir:   tmpDir,
		HotReload:     false,
		Cache:         true,
		EnableMetrics: false,
	}
	engine, err := NewTemplateEngine(opts)
	if err != nil {
		b.Fatalf("Failed to create engine: %v", err)
	}

	data := &PageData{
		Title: "Test",
		Data:  map[string]interface{}{"Name": "World"},
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := engine.RenderString("simple", data)
		if err != nil {
			b.Fatalf("Render failed: %v", err)
		}
	}
}

// BenchmarkConcurrentRender benchmarks concurrent template rendering.
func BenchmarkConcurrentRender(b *testing.B) {
	tmpDir := b.TempDir()

	// Create template
	templateHTML := `{{ define "concurrent" }}User: {{ .Data.UserID }}{{ end }}`
	createTestTemplate(b, tmpDir, "concurrent.html", templateHTML)

	opts := TemplateOptions{
		TemplateDir:   tmpDir,
		HotReload:     false,
		Cache:         true,
		EnableMetrics: false,
	}
	engine, err := NewTemplateEngine(opts)
	if err != nil {
		b.Fatalf("Failed to create engine: %v", err)
	}

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		data := &PageData{
			Title: "Concurrent Test",
			Data:  map[string]interface{}{"UserID": "user123"},
		}
		for pb.Next() {
			_, err := engine.RenderString("concurrent", data)
			if err != nil {
				b.Fatalf("Render failed: %v", err)
			}
		}
	})
}

// BenchmarkHotReload benchmarks hot reload performance.
func BenchmarkHotReload(b *testing.B) {
	tmpDir := b.TempDir()
	templatePath := filepath.Join(tmpDir, "reload.html")

	// Create initial template
	initialHTML := `{{ define "reload" }}Content{{ end }}`
	if err := os.WriteFile(templatePath, []byte(initialHTML), 0644); err != nil {
		b.Fatalf("Failed to write template: %v", err)
	}

	opts := TemplateOptions{
		TemplateDir:   tmpDir,
		HotReload:     true,
		Cache:         false,
		EnableMetrics: false,
	}
	engine, err := NewTemplateEngine(opts)
	if err != nil {
		b.Fatalf("Failed to create engine: %v", err)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := engine.RenderString("reload", nil)
		if err != nil {
			b.Fatalf("Render failed: %v", err)
		}
	}
}

// BenchmarkComplexTemplate benchmarks complex template with nested blocks.
func BenchmarkComplexTemplate(b *testing.B) {
	tmpDir := b.TempDir()

	// Create complex template with nested blocks
	complexHTML := `{{ define "complex" }}
<!DOCTYPE html>
<html>
<head>
    <title>{{ .Title }}</title>
</head>
<body>
    <header>
        <h1>{{ .Title }}</h1>
        <nav>
            {{ range .Data.MenuItems }}
            <a href="{{ .URL }}">{{ .Name }}</a>
            {{ end }}
        </nav>
    </header>
    <main>
        <div class="stats">
            {{ range .Data.Stats }}
            <div class="stat-card">
                <span class="{{ severity .Level }}">{{ .Level }}</span>
                <h3>{{ .Name }}</h3>
                <p>{{ .Value }}</p>
                <small>{{ timeAgo .UpdatedAt }}</small>
            </div>
            {{ end }}
        </div>
        <div class="content">
            {{ range .Data.Items }}
            <article>
                <h2>{{ .Title }}</h2>
                <p>{{ truncate .Description 200 }}</p>
                <footer>
                    <span>{{ formatTime .CreatedAt }}</span>
                    <span class="{{ statusClass .Status }}">{{ .Status }}</span>
                </footer>
            </article>
            {{ end }}
        </div>
    </main>
    <footer>
        <p>&copy; {{ .Data.Year }} Alertmanager++</p>
    </footer>
</body>
</html>
{{ end }}`
	createTestTemplate(b, tmpDir, "complex.html", complexHTML)

	opts := TemplateOptions{
		TemplateDir:   tmpDir,
		HotReload:     false,
		Cache:         true,
		EnableMetrics: false,
	}
	engine, err := NewTemplateEngine(opts)
	if err != nil {
		b.Fatalf("Failed to create engine: %v", err)
	}

	// Complex data
	data := &PageData{
		Title: "Complex Dashboard",
		Data: map[string]interface{}{
			"Year": 2025,
			"MenuItems": []map[string]interface{}{
				{"Name": "Dashboard", "URL": "/dashboard"},
				{"Name": "Alerts", "URL": "/alerts"},
				{"Name": "Silences", "URL": "/silences"},
			},
			"Stats": []map[string]interface{}{
				{
					"Name":      "Firing",
					"Value":     42,
					"Level":     "critical",
					"UpdatedAt": time.Now().Add(-1 * time.Minute),
				},
				{
					"Name":      "Resolved",
					"Value":     128,
					"Level":     "info",
					"UpdatedAt": time.Now().Add(-5 * time.Minute),
				},
			},
			"Items": []map[string]interface{}{
				{
					"Title":       "Critical Alert",
					"Description": "This is a critical alert that requires immediate attention from the on-call engineer to prevent potential service degradation or outage",
					"CreatedAt":   time.Now().Add(-10 * time.Minute),
					"Status":      "firing",
				},
				{
					"Title":       "Resolved Issue",
					"Description": "This issue has been resolved and all affected services are now operating normally without any remaining impact",
					"CreatedAt":   time.Now().Add(-30 * time.Minute),
					"Status":      "resolved",
				},
			},
		},
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := engine.RenderString("complex", data)
		if err != nil {
			b.Fatalf("Render failed: %v", err)
		}
	}
}
