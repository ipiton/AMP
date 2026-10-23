package core

import "testing"

func TestRedactURL(t *testing.T) {
	cases := map[string]struct{ in, want string }{
		"empty":            {"", ""},
		"host only":        {"https://hooks.example.com", "https://hooks.example.com"},
		"root path":        {"https://hooks.example.com/", "https://hooks.example.com"},
		"token in path":    {"https://outlook.office.com/webhookb2/sup3rSecretToken", "https://outlook.office.com/***"},
		"token in query":   {"https://chat.googleapis.com/v1?key=sup3rSecretToken", "https://chat.googleapis.com/***"},
		"userinfo":         {"https://user:sup3rSecretToken@hooks.example.com", "https://hooks.example.com/***"},
		"fragment":         {"https://hooks.example.com#sup3rSecretToken", "https://hooks.example.com/***"},
		"with port":        {"http://127.0.0.1:8080/hook", "http://127.0.0.1:8080/***"},
		"not absolute":     {"sup3rSecretToken", "***"},
		"unparseable":      {"https://[::1/sup3rSecretToken", "***"},
		"short token path": {"https://h.io/t", "https://h.io/***"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := RedactURL(tc.in); got != tc.want {
				t.Fatalf("RedactURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
