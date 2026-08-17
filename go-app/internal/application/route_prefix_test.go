package application

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNormalizeRoutePrefix(t *testing.T) {
	cases := map[string]string{
		"":          "",
		"/":         "",
		"amp":       "/amp",
		"/amp":      "/amp",
		"/amp/":     "/amp",
		"  /amp  ":  "/amp",
		"/amp/mon/": "/amp/mon",
	}
	for in, want := range cases {
		if got := NormalizeRoutePrefix(in); got != want {
			t.Errorf("NormalizeRoutePrefix(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveRoutePrefix(t *testing.T) {
	cases := []struct {
		name        string
		routePrefix string
		externalURL string
		want        string
	}{
		{
			name: "both unset -> no prefix",
			want: "",
		},
		{
			name:        "external_url with path, no route_prefix -> inherited",
			externalURL: "https://amp.example.com/monitoring",
			want:        "/monitoring",
		},
		{
			name:        "external_url without path -> no prefix",
			externalURL: "https://amp.example.com",
			want:        "",
		},
		{
			name:        "external_url root path -> no prefix",
			externalURL: "https://amp.example.com/",
			want:        "",
		},
		{
			name:        "explicit route_prefix wins over external_url path",
			routePrefix: "/custom",
			externalURL: "https://amp.example.com/monitoring",
			want:        "/custom",
		},
		{
			name:        "explicit route_prefix / opts out of inheritance",
			routePrefix: "/",
			externalURL: "https://amp.example.com/monitoring",
			want:        "",
		},
		{
			name:        "malformed external_url falls back to no prefix",
			externalURL: "://not a url",
			want:        "",
		},
		{
			name:        "route_prefix set without external_url",
			routePrefix: "/amp",
			want:        "/amp",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveRoutePrefix(tc.routePrefix, tc.externalURL)
			if got != tc.want {
				t.Errorf("ResolveRoutePrefix(%q, %q) = %q, want %q", tc.routePrefix, tc.externalURL, got, tc.want)
			}
		})
	}
}

func newRoutePrefixInner() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/status", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("status-ok"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("root-ok"))
	})
	return mux
}

func TestWithRoutePrefix_EmptyPrefixReturnsHandlerUnchanged(t *testing.T) {
	inner := newRoutePrefixInner()
	wrapped := WithRoutePrefix(inner, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v2/status", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || rec.Body.String() != "status-ok" {
		t.Fatalf("expected pass-through to inner handler, got status=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestWithRoutePrefix_ServesUnderPrefix(t *testing.T) {
	inner := newRoutePrefixInner()
	wrapped := WithRoutePrefix(inner, "/amp")

	req := httptest.NewRequest(http.MethodGet, "/amp/api/v2/status", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /amp/api/v2/status expected 200, got %d body=%q", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "status-ok" {
		t.Fatalf("expected prefix to be stripped before reaching inner handler, got body %q", rec.Body.String())
	}
}

func TestWithRoutePrefix_ExactPrefixRedirectsToTrailingSlash(t *testing.T) {
	inner := newRoutePrefixInner()
	wrapped := WithRoutePrefix(inner, "/amp")

	// net/http.ServeMux's built-in subtree redirect: a bare "/amp" (matching
	// the registered "/amp/" subtree without its trailing slash) redirects
	// to "/amp/" (method-preserving 307, per Go's current ServeMux).
	req := httptest.NewRequest(http.MethodGet, "/amp", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("GET /amp expected 307, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/amp/" {
		t.Fatalf("GET /amp Location header = %q, want %q", loc, "/amp/")
	}
}

func TestWithRoutePrefix_BareRootRedirectsToPrefix(t *testing.T) {
	inner := newRoutePrefixInner()
	wrapped := WithRoutePrefix(inner, "/amp")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("GET / expected 302, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/amp/" {
		t.Fatalf("GET / Location header = %q, want %q", loc, "/amp/")
	}
}

func TestWithRoutePrefix_UnrelatedPathNotFound(t *testing.T) {
	inner := newRoutePrefixInner()
	wrapped := WithRoutePrefix(inner, "/amp")

	req := httptest.NewRequest(http.MethodGet, "/api/v2/status", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/v2/status (outside prefix) expected 404, got %d", rec.Code)
	}
}

func TestWithRoutePrefix_TrailingSlashInConfigIsNormalized(t *testing.T) {
	inner := newRoutePrefixInner()
	wrapped := WithRoutePrefix(inner, "/amp/")

	req := httptest.NewRequest(http.MethodGet, "/amp/api/v2/status", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || rec.Body.String() != "status-ok" {
		t.Fatalf("expected trailing-slash prefix to normalize, got status=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestWithRoutePrefix_PostRequestsAlsoStripped(t *testing.T) {
	inner := http.NewServeMux()
	inner.HandleFunc("/api/v2/alerts", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	wrapped := WithRoutePrefix(inner, "/amp")

	req := httptest.NewRequest(http.MethodPost, "/amp/api/v2/alerts", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /amp/api/v2/alerts expected 200, got %d", rec.Code)
	}
}
