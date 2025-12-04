// Package ui provides template engine for dashboard UI.
package ui

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

// TemplateEngine manages HTML templates for dashboard UI.
//
// Design:
//   - Load templates from disk on initialization
//   - Cache parsed templates in production
//   - Hot reload in development mode
//   - Custom functions for formatting
//   - XSS protection via html/template auto-escaping
//
// Thread Safety:
//
//	Safe for concurrent use (templates immutable after load).
//
// Example:
//
//	engine, _ := NewTemplateEngine(DefaultTemplateOptions())
//	engine.Render(w, "pages/dashboard", pageData)
type TemplateEngine struct {
	// templates is the parsed template tree
	templates *template.Template

	// funcs are custom template functions
	funcs template.FuncMap

	// opts controls engine behavior
	opts TemplateOptions

	// metrics tracks Prometheus metrics
	metrics *TemplateMetrics
}

// TemplateOptions controls TemplateEngine behavior.
type TemplateOptions struct {
	// TemplateDir is the root template directory
	TemplateDir string // default: "templates/"

	// HotReload enables template reloading on each request
	HotReload bool // default: false (dev: true, prod: false)

	// Cache enables template caching
	Cache bool // default: true (opposite of HotReload)

	// EnableMetrics enables Prometheus metrics
	EnableMetrics bool // default: true
}

// DefaultTemplateOptions returns default options.
//
// Defaults:
//   - TemplateDir: "templates/"
//   - HotReload: false (production mode)
//   - Cache: true
//   - EnableMetrics: true
func DefaultTemplateOptions() TemplateOptions {
	return TemplateOptions{
		TemplateDir:   "templates/",
		HotReload:     false,
		Cache:         true,
		EnableMetrics: true,
	}
}

// NewTemplateEngine creates a new template engine.
//
// Parameters:
//   - opts: Configuration options
//
// Returns:
//   - *TemplateEngine: A new engine instance
//   - error: If template loading fails
//
// The engine loads all templates from TemplateDir on initialization.
// Templates are cached if Cache=true (production).
//
// Example:
//
//	opts := DefaultTemplateOptions()
//	opts.HotReload = true // Enable for development
//	engine, err := NewTemplateEngine(opts)
func NewTemplateEngine(opts TemplateOptions) (*TemplateEngine, error) {
	e := &TemplateEngine{
		funcs: createTemplateFuncs(),
		opts:  opts,
	}

	// Initialize metrics if enabled
	if opts.EnableMetrics {
		e.metrics = NewTemplateMetrics()
	}

	// Load templates
	if err := e.LoadTemplates(); err != nil {
		return nil, err
	}

	slog.Info("template engine initialized",
		"template_dir", opts.TemplateDir,
		"hot_reload", opts.HotReload,
		"cache", opts.Cache)

	return e, nil
}

// LoadTemplates loads all templates from disk.
//
// Walks the template directory and parses all .html files.
// Templates are organized by directory:
//   - layouts/*.html
//   - pages/*.html
//   - partials/*.html
//   - errors/*.html
//
// Returns error if template parsing fails.
func (e *TemplateEngine) LoadTemplates() error {
	// Create new template with custom functions
	tmpl := template.New("").Funcs(e.funcs)

	// Walk template directory
	err := filepath.Walk(e.opts.TemplateDir, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		// Skip non-HTML files
		if !strings.HasSuffix(path, ".html") {
			return nil
		}

		// Parse template file
		_, err = tmpl.ParseFiles(path)
		if err != nil {
			return fmt.Errorf("failed to parse template %s: %w", path, err)
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("%w: %v", ErrTemplateLoad, err)
	}

	// Store parsed templates
	e.templates = tmpl

	slog.Debug("templates loaded",
		"count", len(tmpl.Templates()))

	return nil
}

// Render renders a template to http.ResponseWriter.
//
// Parameters:
//   - w: HTTP response writer
//   - templateName: Template name (e.g., "pages/dashboard")
//   - data: Data to pass to template
//
// Returns error if template not found or rendering fails.
//
// Example:
//
//	pageData := &PageData{
//	    Title: "Dashboard",
//	    Data:  dashboardData,
//	}
//	err := engine.Render(w, "pages/dashboard", pageData)
func (e *TemplateEngine) Render(
	w http.ResponseWriter,
	templateName string,
	data interface{},
) error {
	start := time.Now()

	// Hot reload if enabled
	if e.opts.HotReload {
		if err := e.LoadTemplates(); err != nil {
			slog.Error("hot reload failed", "error", err)
			return err
		}
	}

	// Find template
	tmpl := e.templates.Lookup(templateName)
	if tmpl == nil {
		if e.opts.EnableMetrics {
			e.metrics.RecordRender(templateName, time.Since(start), false)
		}
		return fmt.Errorf("%w: %s", ErrTemplateNotFound, templateName)
	}

	// Execute template to buffer (for error handling)
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		if e.opts.EnableMetrics {
			e.metrics.RecordRender(templateName, time.Since(start), false)
		}
		return fmt.Errorf("%w: %v", ErrTemplateRender, err)
	}

	// Write to response
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err := buf.WriteTo(w)

	// Record metrics
	duration := time.Since(start)
	if e.opts.EnableMetrics {
		e.metrics.RecordRender(templateName, duration, err == nil)
	}

	slog.Debug("template rendered",
		"template", templateName,
		"duration_ms", duration.Milliseconds())

	return err
}

// RenderString renders a template to string.
//
// Useful for testing or rendering partial templates.
//
// Returns:
//   - string: Rendered HTML
//   - error: If template not found or rendering fails
func (e *TemplateEngine) RenderString(
	templateName string,
	data interface{},
) (string, error) {
	// Hot reload if enabled
	if e.opts.HotReload {
		if err := e.LoadTemplates(); err != nil {
			return "", err
		}
	}

	// Find template
	tmpl := e.templates.Lookup(templateName)
	if tmpl == nil {
		return "", fmt.Errorf("%w: %s", ErrTemplateNotFound, templateName)
	}

	// Execute template
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("%w: %v", ErrTemplateRender, err)
	}

	return buf.String(), nil
}

// RenderWithFallback renders template with fallback to error template.
//
// If rendering fails, automatically renders errors/500.html with error details.
// This is the recommended method for HTTP handlers.
//
// Example:
//
//	func HandleDashboard(w http.ResponseWriter, r *http.Request) {
//	    engine.RenderWithFallback(w, "pages/dashboard", pageData)
//	}
func (e *TemplateEngine) RenderWithFallback(
	w http.ResponseWriter,
	templateName string,
	data interface{},
) {
	err := e.Render(w, templateName, data)
	if err != nil {
		// Log error
		slog.Error("template render failed",
			"template", templateName,
			"error", err)

		// Fallback to error template
		w.WriteHeader(http.StatusInternalServerError)
		_ = e.Render(w, "errors/500", map[string]interface{}{
			"Error": err.Error(),
		})
	}
}

// GetMetrics returns the engine's metrics instance.
//
// Returns nil if metrics are disabled (opts.EnableMetrics=false).
func (e *TemplateEngine) GetMetrics() *TemplateMetrics {
	return e.metrics
}
