// Package handlers provides HTTP handlers for the dashboard.
// TN-77: Modern Dashboard Page - Integration Tests (150% Quality Target)
// +build integration

package handlers

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ipiton/AMP/internal/ui"
)

// TestDashboardHandler_Integration tests full dashboard rendering with real templates.
// This test requires templates to be available in templates/ directory.
func TestDashboardHandler_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup
	logger := slog.Default()
	opts := ui.DefaultTemplateOptions()
	opts.HotReload = false
	templateEngine, err := ui.NewTemplateEngine(opts)
	if err != nil {
		t.Fatalf("Failed to create template engine: %v", err)
	}

	handler := NewSimpleDashboardHandler(templateEngine, logger)

	// Test request
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	w := httptest.NewRecorder()

	// Execute
	handler.ServeHTTP(w, req)

	// Assertions
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	body := w.Body.String()

	// Check for key dashboard elements
	checks := []struct {
		name     string
		contains string
	}{
		{"Dashboard title", "Dashboard"},
		{"Stats section", "stats-section"},
		{"Alerts section", "alerts-section"},
		{"Silences section", "silences-section"},
		{"Health section", "health-section"},
		{"Quick actions", "quick-actions"},
		{"Skip link", "Skip to main content"},
		{"ARIA live region", "dashboard-updates"},
		{"Keyboard shortcuts", "data-shortcut"},
	}

	for _, check := range checks {
		if !strings.Contains(body, check.contains) {
			t.Errorf("Expected body to contain %q (%s), but it didn't", check.contains, check.name)
		}
	}

	// Check Content-Type
	contentType := w.Header().Get("Content-Type")
	if contentType != "text/html; charset=utf-8" {
		t.Errorf("Expected Content-Type text/html; charset=utf-8, got %q", contentType)
	}

	// Check HTML structure
	if !strings.Contains(body, "<html") {
		t.Error("Expected HTML document structure")
	}
	if !strings.Contains(body, "<main") {
		t.Error("Expected main content area")
	}
	if !strings.Contains(body, "id=\"main-content\"") {
		t.Error("Expected main-content ID for skip link")
	}
}

// TestDashboardHandler_Accessibility tests accessibility features.
func TestDashboardHandler_Accessibility(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	logger := slog.Default()
	opts := ui.DefaultTemplateOptions()
	opts.HotReload = false
	templateEngine, err := ui.NewTemplateEngine(opts)
	if err != nil {
		t.Fatalf("Failed to create template engine: %v", err)
	}

	handler := NewSimpleDashboardHandler(templateEngine, logger)
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	body := w.Body.String()

	// WCAG 2.1 AA checks
	accessibilityChecks := []struct {
		name     string
		contains string
		desc     string
	}{
		{"Skip link", "skip-link", "WCAG 2.4.1 Bypass Blocks"},
		{"ARIA live region", "aria-live", "WCAG 3.2.1 Change on Request"},
		{"Main landmark", "role=\"main\"", "WCAG 1.3.1 Info and Relationships"},
		{"ARIA labels", "aria-labelledby", "WCAG 2.4.6 Headings and Labels"},
		{"Keyboard shortcuts", "data-shortcut", "WCAG 2.1.1 Keyboard Accessible"},
		{"Screen reader only", "sr-only", "WCAG 1.3.1 Info and Relationships"},
	}

	for _, check := range accessibilityChecks {
		if !strings.Contains(body, check.contains) {
			t.Errorf("Accessibility check failed: %s (%s) - expected %q", check.name, check.desc, check.contains)
		}
	}
}

// TestDashboardHandler_KeyboardShortcuts tests keyboard shortcut attributes.
func TestDashboardHandler_KeyboardShortcuts(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	logger := slog.Default()
	opts := ui.DefaultTemplateOptions()
	opts.HotReload = false
	templateEngine, err := ui.NewTemplateEngine(opts)
	if err != nil {
		t.Fatalf("Failed to create template engine: %v", err)
	}

	handler := NewSimpleDashboardHandler(templateEngine, logger)
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	body := w.Body.String()

	// Check for keyboard shortcuts
	shortcuts := []string{
		`data-shortcut="r"`,           // Refresh
		`data-shortcut="shift+s"`,     // Create Silence
		`data-shortcut="shift+a"`,     // Search Alerts
		`data-shortcut="shift+comma"`, // Settings
	}

	for _, shortcut := range shortcuts {
		if !strings.Contains(body, shortcut) {
			t.Errorf("Expected keyboard shortcut %q in HTML", shortcut)
		}
	}
}

// BenchmarkDashboardHandler_Integration benchmarks full dashboard rendering.
func BenchmarkDashboardHandler_Integration(b *testing.B) {
	logger := slog.Default()
	opts := ui.DefaultTemplateOptions()
	opts.HotReload = false
	templateEngine, err := ui.NewTemplateEngine(opts)
	if err != nil {
		b.Fatalf("Failed to create template engine: %v", err)
	}

	handler := NewSimpleDashboardHandler(templateEngine, logger)
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			b.Errorf("Expected status 200, got %d", w.Code)
		}
		// Verify body is not empty
		if w.Body.Len() == 0 {
			b.Error("Response body is empty")
		}
	}
}

// TestDashboardHandler_ResponseSize tests response size is reasonable.
func TestDashboardHandler_ResponseSize(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	logger := slog.Default()
	opts := ui.DefaultTemplateOptions()
	opts.HotReload = false
	templateEngine, err := ui.NewTemplateEngine(opts)
	if err != nil {
		t.Fatalf("Failed to create template engine: %v", err)
	}

	handler := NewSimpleDashboardHandler(templateEngine, logger)
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	bodySize := w.Body.Len()

	// Response should be reasonable size (not too small, not too large)
	if bodySize < 1000 {
		t.Errorf("Response body too small: %d bytes (expected >1KB)", bodySize)
	}
	if bodySize > 500000 {
		t.Errorf("Response body too large: %d bytes (expected <500KB)", bodySize)
	}

	t.Logf("Dashboard response size: %d bytes (~%.1f KB)", bodySize, float64(bodySize)/1024)
}

// TestDashboardHandler_HTMLValidation tests basic HTML structure validity.
func TestDashboardHandler_HTMLValidation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	logger := slog.Default()
	opts := ui.DefaultTemplateOptions()
	opts.HotReload = false
	templateEngine, err := ui.NewTemplateEngine(opts)
	if err != nil {
		t.Fatalf("Failed to create template engine: %v", err)
	}

	handler := NewSimpleDashboardHandler(templateEngine, logger)
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	body := w.Body.Bytes()

	// Basic HTML structure checks
	checks := []struct {
		name     string
		contains []byte
	}{
		{"DOCTYPE", []byte("<!DOCTYPE")},
		{"HTML tag", []byte("<html")},
		{"Head tag", []byte("<head")},
		{"Body tag", []byte("<body")},
		{"Main tag", []byte("<main")},
	}

	for _, check := range checks {
		if !bytes.Contains(body, check.contains) {
			t.Errorf("HTML validation failed: missing %s", check.name)
		}
	}
}
