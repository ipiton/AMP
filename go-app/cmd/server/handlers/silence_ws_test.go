package handlers

import (
	"net/http/httptest"
	"testing"
)

func TestNewOriginChecker(t *testing.T) {
	tests := []struct {
		name           string
		allowedOrigins string
		origin         string
		host           string
		want           bool
	}{
		{"no origin header always allowed", "", "", "amp.local", true},
		{"same-origin allowed with empty whitelist", "", "https://amp.local", "amp.local", true},
		{"cross-origin denied with empty whitelist", "", "https://evil.example.com", "amp.local", false},
		{"whitelisted origin allowed", "https://ui.example.com", "https://ui.example.com", "amp.local", true},
		{"whitelist is case-insensitive", "https://UI.example.com", "https://ui.EXAMPLE.com", "amp.local", true},
		{"whitelist trailing slash normalized", "https://ui.example.com/", "https://ui.example.com", "amp.local", true},
		{"non-whitelisted origin denied", "https://ui.example.com", "https://evil.example.com", "amp.local", false},
		{"multiple origins, second matches", "https://a.example.com, https://b.example.com", "https://b.example.com", "amp.local", true},
		{"wildcard allows any origin", "*", "https://evil.example.com", "amp.local", true},
		{"scheme must match whitelist entry", "https://ui.example.com", "http://ui.example.com", "amp.local", false},
		{"malformed origin denied", "https://ui.example.com", "://bad", "amp.local", false},
		{"same-origin fallback works alongside whitelist", "https://ui.example.com", "https://amp.local", "amp.local", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			check := newOriginChecker(tt.allowedOrigins)
			r := httptest.NewRequest("GET", "http://"+tt.host+"/ws/silences", nil)
			r.Host = tt.host
			if tt.origin != "" {
				r.Header.Set("Origin", tt.origin)
			}
			if got := check(r); got != tt.want {
				t.Errorf("newOriginChecker(%q) with Origin=%q Host=%q = %v, want %v",
					tt.allowedOrigins, tt.origin, tt.host, got, tt.want)
			}
		})
	}
}
