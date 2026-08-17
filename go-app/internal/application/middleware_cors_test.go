package application

import (
	"net/http"
	"net/http/httptest"
	"testing"

	appconfig "github.com/ipiton/AMP/internal/config"
)

func corsTestStack(t *testing.T, cors appconfig.CORSWebhookConfig) http.Handler {
	t.Helper()

	cfg := &appconfig.Config{}
	cfg.Server.CORS = cors

	stack := &MiddlewareStack{config: cfg}
	mw := stack.corsMiddleware()
	return mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
}

func TestCORSMiddleware_AllowedOrigin(t *testing.T) {
	handler := corsTestStack(t, appconfig.CORSWebhookConfig{
		Enabled:        true,
		AllowedOrigins: "https://ui.example.com",
		AllowedMethods: "GET, POST",
		AllowedHeaders: "Content-Type",
	})

	r := httptest.NewRequest(http.MethodGet, "/api/v2/alerts", nil)
	r.Header.Set("Origin", "https://ui.example.com")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://ui.example.com" {
		t.Fatalf("Allow-Origin = %q, want origin echoed", got)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestCORSMiddleware_DisallowedOriginGetsNoHeaders(t *testing.T) {
	handler := corsTestStack(t, appconfig.CORSWebhookConfig{
		Enabled:        true,
		AllowedOrigins: "https://ui.example.com",
	})

	r := httptest.NewRequest(http.MethodGet, "/api/v2/alerts", nil)
	r.Header.Set("Origin", "https://evil.example.com")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Allow-Origin = %q, want empty for disallowed origin", got)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("request itself must still be served, status = %d", w.Code)
	}
}

func TestCORSMiddleware_PreflightShortCircuits(t *testing.T) {
	handler := corsTestStack(t, appconfig.CORSWebhookConfig{
		Enabled:        true,
		AllowedOrigins: "*",
		AllowedMethods: "GET, POST, OPTIONS",
	})

	r := httptest.NewRequest(http.MethodOptions, "/api/v2/alerts", nil)
	r.Header.Set("Origin", "https://anything.example.com")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Fatal("preflight must carry Allow-Methods")
	}
}
