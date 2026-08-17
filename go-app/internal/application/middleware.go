package application

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	appconfig "github.com/ipiton/AMP/internal/config"
)

// MiddlewareStack manages HTTP middleware.
//
// Middleware is applied in order:
//  1. Recovery (panic handler) - outermost
//  2. Request logging
//  3. Metrics collection
//  4. CORS
//  5. Authentication
//  6. Rate limiting
//  7. Compression - innermost
//
// The stack wraps the HTTP handler with all middleware layers.
type MiddlewareStack struct {
	config   *appconfig.Config
	services *ServiceRegistry
	logger   *slog.Logger

	// Middleware functions
	middlewares []Middleware
}

// Middleware is a function that wraps an HTTP handler.
type Middleware func(http.Handler) http.Handler

// NewMiddlewareStack creates a new middleware stack.
func NewMiddlewareStack(
	config *appconfig.Config,
	services *ServiceRegistry,
	logger *slog.Logger,
) (*MiddlewareStack, error) {
	if config == nil {
		return nil, fmt.Errorf("config is nil")
	}
	if services == nil {
		return nil, fmt.Errorf("services is nil")
	}
	if logger == nil {
		logger = slog.Default()
	}

	stack := &MiddlewareStack{
		config:      config,
		services:    services,
		logger:      logger,
		middlewares: make([]Middleware, 0, 10),
	}

	// Build middleware stack
	stack.buildStack()

	return stack, nil
}

// buildStack builds the middleware stack in order.
func (s *MiddlewareStack) buildStack() {
	s.logger.Info("Building middleware stack...")

	// 1. Recovery (panic handler)
	s.middlewares = append(s.middlewares, s.recoveryMiddleware())

	// 2. Request logging
	s.middlewares = append(s.middlewares, s.loggingMiddleware())

	// 3. Metrics collection
	s.middlewares = append(s.middlewares, s.metricsMiddleware())

	// 4. CORS (if enabled via server.cors.*)
	if s.config.Server.CORS.Enabled {
		s.middlewares = append(s.middlewares, s.corsMiddleware())
	}

	// TODO: Add more middleware
	// - Authentication
	// - Rate limiting
	// - Compression

	s.logger.Info("✅ Middleware stack built", "count", len(s.middlewares))
}

// Wrap wraps an HTTP handler with all middleware.
func (s *MiddlewareStack) Wrap(handler http.Handler) http.Handler {
	// Apply middleware in order (outermost first)
	for i := len(s.middlewares) - 1; i >= 0; i-- {
		handler = s.middlewares[i](handler)
	}
	return handler
}

// recoveryMiddleware handles panics and returns 500 Internal Server Error.
func (s *MiddlewareStack) recoveryMiddleware() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					s.logger.Error("Panic recovered",
						"error", err,
						"path", r.URL.Path,
						"method", r.Method)

					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte("Internal Server Error"))
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}

// loggingMiddleware logs all HTTP requests.
func (s *MiddlewareStack) loggingMiddleware() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s.logger.Debug("HTTP request",
				"method", r.Method,
				"path", r.URL.Path,
				"remote_addr", r.RemoteAddr)

			next.ServeHTTP(w, r)
		})
	}
}

// corsMiddleware sets cross-origin headers per server.cors.* config and
// answers OPTIONS preflight requests. Origins are matched exactly against
// the comma-separated whitelist; "*" allows any origin.
func (s *MiddlewareStack) corsMiddleware() Middleware {
	cfg := s.config.Server.CORS

	allowAll := false
	allowed := make(map[string]bool)
	for _, o := range strings.Split(cfg.AllowedOrigins, ",") {
		o = strings.TrimSpace(strings.ToLower(strings.TrimSuffix(o, "/")))
		switch o {
		case "":
		case "*":
			allowAll = true
		default:
			allowed[o] = true
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" && (allowAll || allowed[strings.ToLower(strings.TrimSuffix(origin, "/"))]) {
				h := w.Header()
				h.Set("Access-Control-Allow-Origin", origin)
				h.Add("Vary", "Origin")
				h.Set("Access-Control-Allow-Methods", cfg.AllowedMethods)
				h.Set("Access-Control-Allow-Headers", cfg.AllowedHeaders)

				if r.Method == http.MethodOptions {
					w.WriteHeader(http.StatusNoContent)
					return
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

// metricsMiddleware collects metrics for all HTTP requests.
func (s *MiddlewareStack) metricsMiddleware() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// TODO: Collect metrics (duration, status code, etc.)
			next.ServeHTTP(w, r)
		})
	}
}
