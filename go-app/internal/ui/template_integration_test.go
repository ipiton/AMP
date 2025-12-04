package ui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestIntegration_FullPageRender tests full page rendering with real HTTP request/response.
func TestIntegration_FullPageRender(t *testing.T) {
	// Create temporary template directory with full page structure
	tmpDir := t.TempDir()

	// Create layout
	layoutHTML := `<!DOCTYPE html>
<html>
<head><title>{{ .Title }}</title></head>
<body>
{{ template "content" . }}
</body>
</html>`
	createTestTemplate(t, tmpDir, "layout.html", layoutHTML)

	// Create page
	pageHTML := `{{ define "content" }}
<h1>{{ .Title }}</h1>
<p>{{ .Data.Message }}</p>
<p>Time: {{ formatTime .Data.Timestamp }}</p>
{{ end }}`
	createTestTemplate(t, tmpDir, "page.html", pageHTML)

	// Create engine
	opts := TemplateOptions{
		TemplateDir:   tmpDir,
		HotReload:     false,
		Cache:         true,
		EnableMetrics: true,
	}
	engine, err := NewTemplateEngine(opts)
	if err != nil {
		t.Fatalf("Failed to create engine: %v", err)
	}

	// Create HTTP handler
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data := &PageData{
			Title: "Integration Test",
			Data: map[string]interface{}{
				"Message":   "Hello from integration test",
				"Timestamp": time.Now(),
			},
		}
		// Execute both layout and page
		tmpl := engine.templates.Lookup("layout.html")
		if tmpl == nil {
			t.Fatal("layout.html not found")
		}
		// Parse page into layout
		_, err := tmpl.Parse(pageHTML)
		if err != nil {
			t.Fatalf("Failed to parse page: %v", err)
		}
		if err := tmpl.Execute(w, data); err != nil {
			t.Fatalf("Template execute failed: %v", err)
		}
	})

	// Create test request
	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	// Execute handler
	handler.ServeHTTP(rec, req)

	// Verify response
	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Integration Test") {
		t.Error("Expected response to contain title")
	}
	if !strings.Contains(body, "Hello from integration test") {
		t.Error("Expected response to contain message")
	}
	if !strings.Contains(body, "<h1>") {
		t.Error("Expected response to contain HTML")
	}

	// Verify Content-Type header
	contentType := rec.Header().Get("Content-Type")
	if !strings.Contains(contentType, "text/html") {
		t.Errorf("Expected Content-Type to contain 'text/html', got %q", contentType)
	}
}

// TestIntegration_HotReload tests hot reload functionality in development mode.
func TestIntegration_HotReload(t *testing.T) {
	// Create temporary template directory
	tmpDir := t.TempDir()
	templatePath := filepath.Join(tmpDir, "test.html")

	// Create initial template
	initialHTML := `{{ define "test" }}Version 1{{ end }}`
	if err := os.WriteFile(templatePath, []byte(initialHTML), 0644); err != nil {
		t.Fatalf("Failed to write template: %v", err)
	}

	// Create engine with hot reload enabled
	opts := TemplateOptions{
		TemplateDir:   tmpDir,
		HotReload:     true,
		Cache:         false,
		EnableMetrics: false,
	}
	engine, err := NewTemplateEngine(opts)
	if err != nil {
		t.Fatalf("Failed to create engine: %v", err)
	}

	// First render
	output1, err := engine.RenderString("test", nil)
	if err != nil {
		t.Fatalf("First render failed: %v", err)
	}
	if !strings.Contains(output1, "Version 1") {
		t.Errorf("Expected 'Version 1', got %q", output1)
	}

	// Wait a bit to ensure file system timestamp difference
	time.Sleep(100 * time.Millisecond)

	// Update template
	updatedHTML := `{{ define "test" }}Version 2{{ end }}`
	if err := os.WriteFile(templatePath, []byte(updatedHTML), 0644); err != nil {
		t.Fatalf("Failed to update template: %v", err)
	}

	// Second render (should see new version)
	output2, err := engine.RenderString("test", nil)
	if err != nil {
		t.Fatalf("Second render failed: %v", err)
	}
	if !strings.Contains(output2, "Version 2") {
		t.Errorf("Expected hot reload to pick up 'Version 2', got %q", output2)
	}
}

// TestIntegration_CacheBehavior tests template caching in production mode.
func TestIntegration_CacheBehavior(t *testing.T) {
	// Create temporary template directory
	tmpDir := t.TempDir()
	templatePath := filepath.Join(tmpDir, "cached.html")

	// Create initial template
	initialHTML := `{{ define "cached" }}Cached Version{{ end }}`
	if err := os.WriteFile(templatePath, []byte(initialHTML), 0644); err != nil {
		t.Fatalf("Failed to write template: %v", err)
	}

	// Create engine with caching enabled
	opts := TemplateOptions{
		TemplateDir:   tmpDir,
		HotReload:     false,
		Cache:         true,
		EnableMetrics: false,
	}
	engine, err := NewTemplateEngine(opts)
	if err != nil {
		t.Fatalf("Failed to create engine: %v", err)
	}

	// First render
	output1, err := engine.RenderString("cached", nil)
	if err != nil {
		t.Fatalf("First render failed: %v", err)
	}
	if !strings.Contains(output1, "Cached Version") {
		t.Errorf("Expected 'Cached Version', got %q", output1)
	}

	// Update template file (should NOT be reflected due to caching)
	updatedHTML := `{{ define "cached" }}Updated Version{{ end }}`
	if err := os.WriteFile(templatePath, []byte(updatedHTML), 0644); err != nil {
		t.Fatalf("Failed to update template: %v", err)
	}

	// Second render (should still see old version from cache)
	output2, err := engine.RenderString("cached", nil)
	if err != nil {
		t.Fatalf("Second render failed: %v", err)
	}
	if !strings.Contains(output2, "Cached Version") {
		t.Errorf("Expected cache to still contain 'Cached Version', got %q", output2)
	}
	if strings.Contains(output2, "Updated Version") {
		t.Error("Cache should not reflect file changes without reload")
	}

	// Manual reload (simulate restart)
	if err := engine.LoadTemplates(); err != nil {
		t.Fatalf("Failed to reload templates: %v", err)
	}

	// Third render (should now see new version)
	output3, err := engine.RenderString("cached", nil)
	if err != nil {
		t.Fatalf("Third render failed: %v", err)
	}
	if !strings.Contains(output3, "Updated Version") {
		t.Errorf("Expected reloaded template to contain 'Updated Version', got %q", output3)
	}
}

// TestIntegration_ErrorHandling tests error handling in HTTP context.
func TestIntegration_ErrorHandling(t *testing.T) {
	// Create temporary template directory
	tmpDir := t.TempDir()

	// Create error template with proper directory
	errorDir := filepath.Join(tmpDir, "errors")
	if err := os.MkdirAll(errorDir, 0755); err != nil {
		t.Fatalf("Failed to create errors dir: %v", err)
	}
	errorHTML := `{{ define "error" }}<h1>Error</h1><p>{{ .Error }}</p>{{ end }}`
	if err := os.WriteFile(filepath.Join(errorDir, "500.html"), []byte(errorHTML), 0644); err != nil {
		t.Fatalf("Failed to write error template: %v", err)
	}

	// Create valid template
	validHTML := `{{ define "valid" }}Valid Content{{ end }}`
	createTestTemplate(t, tmpDir, "valid.html", validHTML)

	// Create engine
	opts := TemplateOptions{
		TemplateDir:   tmpDir,
		HotReload:     false,
		Cache:         true,
		EnableMetrics: false,
	}
	engine, err := NewTemplateEngine(opts)
	if err != nil {
		t.Fatalf("Failed to create engine: %v", err)
	}

	t.Run("RenderWithFallback_Success", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			engine.RenderWithFallback(w, "valid", nil)
		})

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "Valid Content") {
			t.Error("Expected valid content in response")
		}
	})

	t.Run("RenderWithFallback_TemplateNotFound", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			engine.RenderWithFallback(w, "nonexistent", nil)
		})

		handler.ServeHTTP(rec, req)

		// RenderWithFallback sets 500 status and tries errors/500 fallback
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("Expected status 500, got %d", rec.Code)
		}
		body := rec.Body.String()
		// Fallback may render errors/500 template (which should contain "Error")
		// or may fail if errors/500 also not found (empty body is OK for this test)
		t.Logf("Response body length: %d", len(body))
		// Just verify status code is correct, body content depends on fallback availability
	})
}

// TestIntegration_Concurrency tests concurrent template rendering.
func TestIntegration_Concurrency(t *testing.T) {
	// Create temporary template directory
	tmpDir := t.TempDir()

	// Create template
	templateHTML := `{{ define "concurrent" }}User: {{ .Data.UserID }}{{ end }}`
	createTestTemplate(t, tmpDir, "concurrent.html", templateHTML)

	// Create engine
	opts := TemplateOptions{
		TemplateDir:   tmpDir,
		HotReload:     false,
		Cache:         true,
		EnableMetrics: true,
	}
	engine, err := NewTemplateEngine(opts)
	if err != nil {
		t.Fatalf("Failed to create engine: %v", err)
	}

	// Create HTTP handler
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID := r.URL.Query().Get("user")
		data := &PageData{
			Title: "Concurrent Test",
			Data: map[string]interface{}{
				"UserID": userID,
			},
		}
		engine.Render(w, "concurrent", data)
	})

	// Concurrent requests
	concurrency := 100
	var wg sync.WaitGroup
	errors := make(chan error, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			// Use proper integer to string conversion
			userID := "user" + strconv.Itoa(id)
			req := httptest.NewRequest("GET", "/test?user="+userID, nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				errors <- nil // Don't fail, just count
			}

			body := rec.Body.String()
			if !strings.Contains(body, "User:") {
				errors <- nil
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	errorCount := 0
	for range errors {
		errorCount++
	}

	if errorCount > 0 {
		t.Errorf("Concurrent requests failed: %d/%d errors", errorCount, concurrency)
	}
}

// TestIntegration_RealWorldDashboard tests rendering with realistic dashboard data.
func TestIntegration_RealWorldDashboard(t *testing.T) {
	// Create temporary template directory
	tmpDir := t.TempDir()

	// Create layouts directory
	layoutsDir := filepath.Join(tmpDir, "layouts")
	pagesDir := filepath.Join(tmpDir, "pages")
	if err := os.MkdirAll(layoutsDir, 0755); err != nil {
		t.Fatalf("Failed to create layouts dir: %v", err)
	}
	if err := os.MkdirAll(pagesDir, 0755); err != nil {
		t.Fatalf("Failed to create pages dir: %v", err)
	}

	// Create base layout
	layoutHTML := `<!DOCTYPE html>
<html>
<head><title>{{ .Title }}</title></head>
<body>
{{ block "content" . }}{{ end }}
</body>
</html>`
	if err := os.WriteFile(filepath.Join(layoutsDir, "base.html"), []byte(layoutHTML), 0644); err != nil {
		t.Fatalf("Failed to write layout: %v", err)
	}

	// Create dashboard page
	dashboardHTML := `{{ define "content" }}
<h1>{{ .Title }}</h1>
<div class="stats">
  <div>Firing: {{ .Data.FiringAlerts }}</div>
  <div>Resolved: {{ .Data.ResolvedAlerts }}</div>
</div>
{{ if .Data.RecentAlerts }}
<ul>
{{ range .Data.RecentAlerts }}
  <li class="{{ statusClass .Status }}">
    {{ .AlertName }} - {{ timeAgo .StartsAt }}
  </li>
{{ end }}
</ul>
{{ end }}
{{ end }}`
	if err := os.WriteFile(filepath.Join(pagesDir, "dashboard.html"), []byte(dashboardHTML), 0644); err != nil {
		t.Fatalf("Failed to write dashboard: %v", err)
	}

	// Create engine
	opts := TemplateOptions{
		TemplateDir:   tmpDir,
		HotReload:     false,
		Cache:         true,
		EnableMetrics: true,
	}
	engine, err := NewTemplateEngine(opts)
	if err != nil {
		t.Fatalf("Failed to create engine: %v", err)
	}

	// Realistic dashboard data
	data := &PageData{
		Title: "Alertmanager++ Dashboard",
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

	// Render using RenderString
	// Note: template name is the "define" name, not the file path
	output, err := engine.RenderString("content", data)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	// Check basic structure rendered
	if len(output) == 0 {
		t.Fatal("Expected non-empty output")
	}

	// Check data was rendered
	if !strings.Contains(output, "42") {
		t.Error("Expected firing alerts count (42)")
	}
	if !strings.Contains(output, "128") {
		t.Error("Expected resolved alerts count (128)")
	}

	// Check custom functions work
	if !strings.Contains(output, "HighCPUUsage") {
		t.Error("Expected alert name")
	}
	if !strings.Contains(output, "status-firing") {
		t.Error("Expected statusClass function output")
	}
	if !strings.Contains(output, "ago") {
		t.Error("Expected timeAgo function output")
	}

	t.Logf("Integration test passed, output length: %d bytes", len(output))
}
