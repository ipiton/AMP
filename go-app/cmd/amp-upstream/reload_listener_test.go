package main

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestListenLoopbackRejectsOtherAddresses(t *testing.T) {
	for _, address := range []string{":0", "0.0.0.0:0", "[::]:0", "localhost:0", "192.0.2.1:0", "127.0.0.1"} {
		if l, err := listenLoopback(address); err == nil {
			_ = l.Close()
			t.Errorf("listenLoopback(%q) succeeded, want an error", address)
		}
	}
	l, err := listenLoopback("127.0.0.1:0")
	if err != nil {
		t.Fatalf("listenLoopback(127.0.0.1:0): %v", err)
	}
	_ = l.Close()
}

func TestReloadHandlerReloadsOnlyChangedConfig(t *testing.T) {
	file := filepath.Join(t.TempDir(), "alertmanager.yml")
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var (
		calls     int
		reloadErr error
	)
	handler := reloadHandler(file, func() error {
		calls++
		return reloadErr
	}, slog.New(slog.DiscardHandler))
	post := func(wantStatus, wantCalls int) {
		t.Helper()
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/-/reload", nil))
		if rec.Code != wantStatus || calls != wantCalls {
			t.Fatalf("status %d, reloads %d; want %d, %d", rec.Code, calls, wantStatus, wantCalls)
		}
	}

	write("a")
	post(http.StatusOK, 1) // nothing loaded here yet
	post(http.StatusOK, 1) // a webhook looping back to the listener stops here

	write("b")
	post(http.StatusOK, 2)

	write("broken")
	reloadErr = errors.New("parse error")
	post(http.StatusInternalServerError, 3)
	post(http.StatusInternalServerError, 4) // a failed load is not remembered

	reloadErr = nil
	post(http.StatusOK, 5)

	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	post(http.StatusInternalServerError, 5)
}

func TestReloadHandlerServesOnlyPostReload(t *testing.T) {
	file := filepath.Join(t.TempDir(), "alertmanager.yml")
	if err := os.WriteFile(file, []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := reloadHandler(file, func() error {
		t.Error("reload called")
		return nil
	}, slog.New(slog.DiscardHandler))
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/-/reload", http.StatusMethodNotAllowed},
		{http.MethodPost, "/api/v2/status", http.StatusNotFound},
		{http.MethodGet, "/metrics", http.StatusNotFound},
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if rec.Code != tc.want {
			t.Errorf("%s %s: status %d, want %d", tc.method, tc.path, rec.Code, tc.want)
		}
	}
}
