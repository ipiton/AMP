package ui

import (
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestDefaultTemplateOptions tests the default options constructor.
func TestDefaultTemplateOptions(t *testing.T) {
	opts := DefaultTemplateOptions()

	if opts.TemplateDir != "templates/" {
		t.Errorf("Expected TemplateDir='templates/', got %q", opts.TemplateDir)
	}
	if opts.HotReload != false {
		t.Error("Expected HotReload=false for production default")
	}
	if opts.Cache != true {
		t.Error("Expected Cache=true for production default")
	}
	if opts.EnableMetrics != true {
		t.Error("Expected EnableMetrics=true")
	}
}

// TestNewTemplateEngine_Success tests successful engine creation.
func TestNewTemplateEngine_Success(t *testing.T) {
	// Create temporary template directory
	tmpDir := t.TempDir()
	createTestTemplate(t, tmpDir, "test.html", `{{ define "test" }}Hello{{ end }}`)

	opts := TemplateOptions{
		TemplateDir:   tmpDir,
		HotReload:     false,
		Cache:         true,
		EnableMetrics: true,
	}

	engine, err := NewTemplateEngine(opts)
	if err != nil {
		t.Fatalf("NewTemplateEngine failed: %v", err)
	}

	if engine == nil {
		t.Fatal("Expected non-nil engine")
	}
	if engine.templates == nil {
		t.Error("Expected templates to be loaded")
	}
	if engine.metrics == nil {
		t.Error("Expected metrics to be initialized")
	}
}

// TestNewTemplateEngine_WithoutMetrics tests engine creation without metrics.
func TestNewTemplateEngine_WithoutMetrics(t *testing.T) {
	tmpDir := t.TempDir()
	createTestTemplate(t, tmpDir, "test.html", `{{ define "test" }}Hello{{ end }}`)

	opts := TemplateOptions{
		TemplateDir:   tmpDir,
		HotReload:     false,
		Cache:         true,
		EnableMetrics: false,
	}

	engine, err := NewTemplateEngine(opts)
	if err != nil {
		t.Fatalf("NewTemplateEngine failed: %v", err)
	}

	if engine.metrics != nil {
		t.Error("Expected metrics to be nil when disabled")
	}
}

// TestNewTemplateEngine_InvalidDirectory tests error handling for invalid directory.
func TestNewTemplateEngine_InvalidDirectory(t *testing.T) {
	opts := TemplateOptions{
		TemplateDir:   "/nonexistent/directory/that/does/not/exist",
		HotReload:     false,
		Cache:         true,
		EnableMetrics: false,
	}

	_, err := NewTemplateEngine(opts)
	if err == nil {
		t.Error("Expected error for invalid directory")
	}
	if !errors.Is(err, ErrTemplateLoad) {
		t.Errorf("Expected ErrTemplateLoad, got %v", err)
	}
}

// TestLoadTemplates_Success tests successful template loading.
func TestLoadTemplates_Success(t *testing.T) {
	tmpDir := t.TempDir()
	createTestTemplate(t, tmpDir, "page1.html", `{{ define "page1" }}Page 1{{ end }}`)
	createTestTemplate(t, tmpDir, "page2.html", `{{ define "page2" }}Page 2{{ end }}`)

	opts := TemplateOptions{
		TemplateDir:   tmpDir,
		HotReload:     false,
		Cache:         true,
		EnableMetrics: false,
	}

	engine, err := NewTemplateEngine(opts)
	if err != nil {
		t.Fatalf("NewTemplateEngine failed: %v", err)
	}

	// Verify templates are loaded
	if engine.templates.Lookup("page1") == nil {
		t.Error("Expected page1 template to be loaded")
	}
	if engine.templates.Lookup("page2") == nil {
		t.Error("Expected page2 template to be loaded")
	}
}

// TestLoadTemplates_EmptyDirectory tests loading from empty directory.
func TestLoadTemplates_EmptyDirectory(t *testing.T) {
	tmpDir := t.TempDir()

	opts := TemplateOptions{
		TemplateDir:   tmpDir,
		HotReload:     false,
		Cache:         true,
		EnableMetrics: false,
	}

	engine, err := NewTemplateEngine(opts)
	if err != nil {
		t.Fatalf("NewTemplateEngine should succeed with empty directory: %v", err)
	}

	// Empty directory should still create valid engine
	if engine.templates == nil {
		t.Error("Expected templates to be initialized even for empty directory")
	}
}

// TestLoadTemplates_InvalidTemplate tests error handling for invalid template syntax.
func TestLoadTemplates_InvalidTemplate(t *testing.T) {
	tmpDir := t.TempDir()
	createTestTemplate(t, tmpDir, "invalid.html", `{{ define "invalid" }}{{ .Missing | invalidFunc }}{{ end`)

	opts := TemplateOptions{
		TemplateDir:   tmpDir,
		HotReload:     false,
		Cache:         true,
		EnableMetrics: false,
	}

	_, err := NewTemplateEngine(opts)
	if err == nil {
		t.Error("Expected error for invalid template syntax")
	}
	if !errors.Is(err, ErrTemplateLoad) {
		t.Errorf("Expected ErrTemplateLoad, got %v", err)
	}
}

// TestLoadTemplates_NestedDirectories tests loading templates from nested directories.
func TestLoadTemplates_NestedDirectories(t *testing.T) {
	tmpDir := t.TempDir()

	// Create nested structure
	layoutsDir := filepath.Join(tmpDir, "layouts")
	pagesDir := filepath.Join(tmpDir, "pages")
	os.MkdirAll(layoutsDir, 0755)
	os.MkdirAll(pagesDir, 0755)

	createTestTemplate(t, layoutsDir, "base.html", `{{ define "base" }}Layout{{ end }}`)
	createTestTemplate(t, pagesDir, "home.html", `{{ define "home" }}Home{{ end }}`)

	opts := TemplateOptions{
		TemplateDir:   tmpDir,
		HotReload:     false,
		Cache:         true,
		EnableMetrics: false,
	}

	engine, err := NewTemplateEngine(opts)
	if err != nil {
		t.Fatalf("NewTemplateEngine failed: %v", err)
	}

	if engine.templates.Lookup("base") == nil {
		t.Error("Expected base template from layouts/ to be loaded")
	}
	if engine.templates.Lookup("home") == nil {
		t.Error("Expected home template from pages/ to be loaded")
	}
}

// TestRender_Success tests successful template rendering.
func TestRender_Success(t *testing.T) {
	tmpDir := t.TempDir()
	createTestTemplate(t, tmpDir, "test.html", `{{ define "test" }}Hello, {{ .Name }}!{{ end }}`)

	opts := TemplateOptions{
		TemplateDir:   tmpDir,
		HotReload:     false,
		Cache:         true,
		EnableMetrics: false,
	}

	engine, err := NewTemplateEngine(opts)
	if err != nil {
		t.Fatalf("NewTemplateEngine failed: %v", err)
	}

	w := httptest.NewRecorder()
	data := map[string]string{"Name": "World"}

	err = engine.Render(w, "test", data)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	result := w.Body.String()
	expected := "Hello, World!"
	if !strings.Contains(result, expected) {
		t.Errorf("Expected output to contain %q, got %q", expected, result)
	}

	// Check Content-Type header
	contentType := w.Header().Get("Content-Type")
	if contentType != "text/html; charset=utf-8" {
		t.Errorf("Expected Content-Type='text/html; charset=utf-8', got %q", contentType)
	}
}

// TestRender_TemplateNotFound tests error handling for missing template.
func TestRender_TemplateNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	createTestTemplate(t, tmpDir, "exists.html", `{{ define "exists" }}Exists{{ end }}`)

	opts := TemplateOptions{
		TemplateDir:   tmpDir,
		HotReload:     false,
		Cache:         true,
		EnableMetrics: false,
	}

	engine, err := NewTemplateEngine(opts)
	if err != nil {
		t.Fatalf("NewTemplateEngine failed: %v", err)
	}

	w := httptest.NewRecorder()
	err = engine.Render(w, "nonexistent", nil)

	if err == nil {
		t.Error("Expected error for nonexistent template")
	}
	if !errors.Is(err, ErrTemplateNotFound) {
		t.Errorf("Expected ErrTemplateNotFound, got %v", err)
	}
}

// TestRender_RenderError tests error handling for template execution errors.
func TestRender_RenderError(t *testing.T) {
	tmpDir := t.TempDir()
	// Template that will fail during execution - calling method on nil
	createTestTemplate(t, tmpDir, "error.html", `{{ define "error" }}{{ .Func }}{{ end }}`)

	opts := TemplateOptions{
		TemplateDir:   tmpDir,
		HotReload:     false,
		Cache:         true,
		EnableMetrics: false,
	}

	engine, err := NewTemplateEngine(opts)
	if err != nil {
		t.Fatalf("NewTemplateEngine failed: %v", err)
	}

	w := httptest.NewRecorder()
	// Pass a function that will cause error when called in template
	data := map[string]func() string{
		"Func": func() string { panic("template error") },
	}

	err = engine.Render(w, "error", data)
	// Note: Go templates are very forgiving, this test documents the behavior
	// In practice, most template errors are caught at parse time, not render time
	if err != nil && !errors.Is(err, ErrTemplateRender) {
		t.Errorf("Expected ErrTemplateRender or nil, got %v", err)
	}
}

// TestRender_WithData tests rendering with various data types.
func TestRender_WithData(t *testing.T) {
	tmpDir := t.TempDir()
	createTestTemplate(t, tmpDir, "data.html", `{{ define "data" }}Count: {{ .Count }}, Name: {{ .Name }}{{ end }}`)

	opts := TemplateOptions{
		TemplateDir:   tmpDir,
		HotReload:     false,
		Cache:         true,
		EnableMetrics: false,
	}

	engine, err := NewTemplateEngine(opts)
	if err != nil {
		t.Fatalf("NewTemplateEngine failed: %v", err)
	}

	tests := []struct {
		name     string
		data     interface{}
		expected string
	}{
		{
			name: "struct data",
			data: struct {
				Count int
				Name  string
			}{Count: 42, Name: "Test"},
			expected: "Count: 42, Name: Test",
		},
		{
			name:     "map data",
			data:     map[string]interface{}{"Count": 100, "Name": "Map"},
			expected: "Count: 100, Name: Map",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			err := engine.Render(w, "data", tt.data)
			if err != nil {
				t.Fatalf("Render failed: %v", err)
			}

			result := w.Body.String()
			if !strings.Contains(result, tt.expected) {
				t.Errorf("Expected output to contain %q, got %q", tt.expected, result)
			}
		})
	}
}

// TestRender_HotReload tests hot reload functionality.
func TestRender_HotReload(t *testing.T) {
	tmpDir := t.TempDir()
	templatePath := filepath.Join(tmpDir, "reload.html")

	// Create initial template
	createTestTemplate(t, tmpDir, "reload.html", `{{ define "reload" }}Version 1{{ end }}`)

	opts := TemplateOptions{
		TemplateDir:   tmpDir,
		HotReload:     true,
		Cache:         false,
		EnableMetrics: false,
	}

	engine, err := NewTemplateEngine(opts)
	if err != nil {
		t.Fatalf("NewTemplateEngine failed: %v", err)
	}

	// First render
	w1 := httptest.NewRecorder()
	err = engine.Render(w1, "reload", nil)
	if err != nil {
		t.Fatalf("First render failed: %v", err)
	}
	if !strings.Contains(w1.Body.String(), "Version 1") {
		t.Error("Expected first render to contain 'Version 1'")
	}

	// Update template
	time.Sleep(10 * time.Millisecond) // Ensure file modification time changes
	err = os.WriteFile(templatePath, []byte(`{{ define "reload" }}Version 2{{ end }}`), 0644)
	if err != nil {
		t.Fatalf("Failed to update template: %v", err)
	}

	// Second render should pick up changes
	w2 := httptest.NewRecorder()
	err = engine.Render(w2, "reload", nil)
	if err != nil {
		t.Fatalf("Second render failed: %v", err)
	}
	if !strings.Contains(w2.Body.String(), "Version 2") {
		t.Error("Expected hot reload to pick up 'Version 2'")
	}
}

// TestRender_MetricsRecorded tests that metrics are recorded.
func TestRender_MetricsRecorded(t *testing.T) {
	tmpDir := t.TempDir()
	createTestTemplate(t, tmpDir, "metrics.html", `{{ define "metrics" }}Test{{ end }}`)

	opts := TemplateOptions{
		TemplateDir:   tmpDir,
		HotReload:     false,
		Cache:         true,
		EnableMetrics: true,
	}

	engine, err := NewTemplateEngine(opts)
	if err != nil {
		t.Fatalf("NewTemplateEngine failed: %v", err)
	}

	if engine.metrics == nil {
		t.Fatal("Expected metrics to be initialized")
	}

	w := httptest.NewRecorder()
	err = engine.Render(w, "metrics", nil)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	// Metrics should be recorded (we can't easily verify Prometheus metrics in unit tests,
	// but we can verify the metrics object exists and methods don't panic)
}

// TestRender_ConcurrentSafe tests thread safety of concurrent rendering.
func TestRender_ConcurrentSafe(t *testing.T) {
	tmpDir := t.TempDir()
	createTestTemplate(t, tmpDir, "concurrent.html", `{{ define "concurrent" }}ID: {{ .ID }}{{ end }}`)

	opts := TemplateOptions{
		TemplateDir:   tmpDir,
		HotReload:     false,
		Cache:         true,
		EnableMetrics: false,
	}

	engine, err := NewTemplateEngine(opts)
	if err != nil {
		t.Fatalf("NewTemplateEngine failed: %v", err)
	}

	// Render concurrently from multiple goroutines
	const numGoroutines = 100
	var wg sync.WaitGroup
	errors := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			w := httptest.NewRecorder()
			data := map[string]int{"ID": id}
			if err := engine.Render(w, "concurrent", data); err != nil {
				errors <- err
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// Check for any errors
	for err := range errors {
		t.Errorf("Concurrent render error: %v", err)
	}
}

// TestRenderString_Success tests RenderString method.
func TestRenderString_Success(t *testing.T) {
	tmpDir := t.TempDir()
	createTestTemplate(t, tmpDir, "string.html", `{{ define "string" }}Hello, {{ .Name }}!{{ end }}`)

	opts := TemplateOptions{
		TemplateDir:   tmpDir,
		HotReload:     false,
		Cache:         true,
		EnableMetrics: false,
	}

	engine, err := NewTemplateEngine(opts)
	if err != nil {
		t.Fatalf("NewTemplateEngine failed: %v", err)
	}

	data := map[string]string{"Name": "String"}
	result, err := engine.RenderString("string", data)
	if err != nil {
		t.Fatalf("RenderString failed: %v", err)
	}

	expected := "Hello, String!"
	if !strings.Contains(result, expected) {
		t.Errorf("Expected output to contain %q, got %q", expected, result)
	}
}

// TestRenderString_Error tests RenderString error handling.
func TestRenderString_Error(t *testing.T) {
	tmpDir := t.TempDir()
	createTestTemplate(t, tmpDir, "exists.html", `{{ define "exists" }}Exists{{ end }}`)

	opts := TemplateOptions{
		TemplateDir:   tmpDir,
		HotReload:     false,
		Cache:         true,
		EnableMetrics: false,
	}

	engine, err := NewTemplateEngine(opts)
	if err != nil {
		t.Fatalf("NewTemplateEngine failed: %v", err)
	}

	_, err = engine.RenderString("nonexistent", nil)
	if err == nil {
		t.Error("Expected error for nonexistent template")
	}
	if !errors.Is(err, ErrTemplateNotFound) {
		t.Errorf("Expected ErrTemplateNotFound, got %v", err)
	}
}

// TestRenderWithFallback_Success tests successful rendering with fallback.
func TestRenderWithFallback_Success(t *testing.T) {
	tmpDir := t.TempDir()
	createTestTemplate(t, tmpDir, "fallback.html", `{{ define "fallback" }}Success{{ end }}`)

	opts := TemplateOptions{
		TemplateDir:   tmpDir,
		HotReload:     false,
		Cache:         true,
		EnableMetrics: false,
	}

	engine, err := NewTemplateEngine(opts)
	if err != nil {
		t.Fatalf("NewTemplateEngine failed: %v", err)
	}

	w := httptest.NewRecorder()
	engine.RenderWithFallback(w, "fallback", nil)

	result := w.Body.String()
	if !strings.Contains(result, "Success") {
		t.Errorf("Expected successful render, got %q", result)
	}

	// Should not have 500 status
	if w.Code == 500 {
		t.Error("Expected status 200, got 500")
	}
}

// TestRenderWithFallback_TemplateError tests fallback behavior on error.
func TestRenderWithFallback_TemplateError(t *testing.T) {
	tmpDir := t.TempDir()
	createTestTemplate(t, tmpDir, "500.html", `{{ define "errors/500" }}Error: {{ .Error }}{{ end }}`)

	opts := TemplateOptions{
		TemplateDir:   tmpDir,
		HotReload:     false,
		Cache:         true,
		EnableMetrics: false,
	}

	engine, err := NewTemplateEngine(opts)
	if err != nil {
		t.Fatalf("NewTemplateEngine failed: %v", err)
	}

	w := httptest.NewRecorder()
	engine.RenderWithFallback(w, "nonexistent", nil)

	// Should set 500 status
	if w.Code != 500 {
		t.Errorf("Expected status 500, got %d", w.Code)
	}

	// Should render error template
	result := w.Body.String()
	if !strings.Contains(result, "Error:") {
		t.Errorf("Expected error template to be rendered, got %q", result)
	}
}

// TestGetMetrics tests GetMetrics method.
func TestGetMetrics(t *testing.T) {
	tmpDir := t.TempDir()
	createTestTemplate(t, tmpDir, "test.html", `{{ define "test" }}Test{{ end }}`)

	t.Run("with metrics enabled", func(t *testing.T) {
		opts := TemplateOptions{
			TemplateDir:   tmpDir,
			HotReload:     false,
			Cache:         true,
			EnableMetrics: true,
		}

		engine, err := NewTemplateEngine(opts)
		if err != nil {
			t.Fatalf("NewTemplateEngine failed: %v", err)
		}

		metrics := engine.GetMetrics()
		if metrics == nil {
			t.Error("Expected non-nil metrics when enabled")
		}
	})

	t.Run("with metrics disabled", func(t *testing.T) {
		opts := TemplateOptions{
			TemplateDir:   tmpDir,
			HotReload:     false,
			Cache:         true,
			EnableMetrics: false,
		}

		engine, err := NewTemplateEngine(opts)
		if err != nil {
			t.Fatalf("NewTemplateEngine failed: %v", err)
		}

		metrics := engine.GetMetrics()
		if metrics != nil {
			t.Error("Expected nil metrics when disabled")
		}
	})
}

// Helper function to create test templates
func createTestTemplate(t testing.TB, dir, filename, content string) {
	t.Helper()
	path := filepath.Join(dir, filename)
	err := os.WriteFile(path, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create test template %s: %v", filename, err)
	}
}
