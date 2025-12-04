package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	tests := []struct {
		input    string
		expected slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"INFO", slog.LevelInfo},
		{"", slog.LevelInfo}, // default
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
		{"ERROR", slog.LevelError},
		{"invalid", slog.LevelInfo}, // fallback to default
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := ParseLevel(tt.input)
			if result != tt.expected {
				t.Errorf("ParseLevel(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestSetupWriter(t *testing.T) {
	tests := []struct {
		name   string
		config Config
		check  func(t *testing.T, writer interface{})
	}{
		{
			name: "stdout output",
			config: Config{
				Output: "stdout",
			},
			check: func(t *testing.T, writer interface{}) {
				if writer != os.Stdout {
					t.Error("Expected os.Stdout")
				}
			},
		},
		{
			name: "stderr output",
			config: Config{
				Output: "stderr",
			},
			check: func(t *testing.T, writer interface{}) {
				if writer != os.Stderr {
					t.Error("Expected os.Stderr")
				}
			},
		},
		{
			name: "default output",
			config: Config{
				Output: "",
			},
			check: func(t *testing.T, writer interface{}) {
				if writer != os.Stdout {
					t.Error("Expected os.Stdout as default")
				}
			},
		},
		{
			name: "file output without filename",
			config: Config{
				Output: "file",
			},
			check: func(t *testing.T, writer interface{}) {
				if writer != os.Stdout {
					t.Error("Expected os.Stdout when filename is empty")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writer := SetupWriter(tt.config)
			tt.check(t, writer)
		})
	}
}

func TestNewLogger(t *testing.T) {
	// Test JSON format
	cfg := Config{
		Level:  "info",
		Format: "json",
		Output: "stdout",
	}

	logger := NewLogger(cfg)
	if logger == nil {
		t.Fatal("NewLogger returned nil")
	}

	// Test that logger can log
	logger.Info("test message", "key", "value")
}

func TestGenerateRequestID(t *testing.T) {
	id1 := GenerateRequestID()
	id2 := GenerateRequestID()

	if id1 == id2 {
		t.Error("GenerateRequestID should generate unique IDs")
	}

	if !strings.HasPrefix(id1, "req_") {
		t.Errorf("Request ID should start with 'req_', got: %s", id1)
	}

	if len(id1) < 5 {
		t.Errorf("Request ID too short: %s", id1)
	}
}

func TestWithRequestID(t *testing.T) {
	ctx := context.Background()
	requestID := "test-request-id"

	newCtx := WithRequestID(ctx, requestID)

	retrievedID := GetRequestID(newCtx)
	if retrievedID != requestID {
		t.Errorf("Expected %s, got %s", requestID, retrievedID)
	}
}

func TestGetRequestIDEmpty(t *testing.T) {
	ctx := context.Background()

	requestID := GetRequestID(ctx)
	if requestID != "" {
		t.Errorf("Expected empty string, got %s", requestID)
	}
}

func TestLoggingMiddleware(t *testing.T) {
	var buf bytes.Buffer

	// Create logger that writes to buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// Create test handler
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check that request ID is in context
		requestID := GetRequestID(r.Context())
		if requestID == "" {
			t.Error("Request ID not found in context")
		}

		// Check that request ID is in response header
		responseID := w.Header().Get("X-Request-ID")
		if responseID == "" {
			t.Error("Request ID not found in response header")
		}

		if requestID != responseID {
			t.Error("Request ID mismatch between context and header")
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Wrap with logging middleware
	middleware := LoggingMiddleware(logger)
	handler := middleware(testHandler)

	// Create test request
	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()

	// Execute request
	handler.ServeHTTP(w, req)

	// Check response
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	// Check that log was written
	logOutput := buf.String()
	if logOutput == "" {
		t.Error("No log output generated")
	}

	// Parse JSON log
	var logEntry map[string]interface{}
	if err := json.Unmarshal([]byte(logOutput), &logEntry); err != nil {
		t.Fatalf("Failed to parse log JSON: %v", err)
	}

	// Check required fields
	requiredFields := []string{"method", "path", "status", "duration", "request_id"}
	for _, field := range requiredFields {
		if _, exists := logEntry[field]; !exists {
			t.Errorf("Missing required field in log: %s", field)
		}
	}

	// Check field values
	if logEntry["method"] != "GET" {
		t.Errorf("Expected method GET, got %v", logEntry["method"])
	}

	if logEntry["path"] != "/test" {
		t.Errorf("Expected path /test, got %v", logEntry["path"])
	}

	if logEntry["status"] != float64(200) {
		t.Errorf("Expected status 200, got %v", logEntry["status"])
	}
}

func TestLoggingMiddlewareWithExistingRequestID(t *testing.T) {
	var buf bytes.Buffer

	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	existingRequestID := "existing-request-id"

	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := GetRequestID(r.Context())
		if requestID != existingRequestID {
			t.Errorf("Expected existing request ID %s, got %s", existingRequestID, requestID)
		}
		w.WriteHeader(http.StatusOK)
	})

	middleware := LoggingMiddleware(logger)
	handler := middleware(testHandler)

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Request-ID", existingRequestID)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	// Check that existing request ID was used
	logOutput := buf.String()
	var logEntry map[string]interface{}
	if err := json.Unmarshal([]byte(logOutput), &logEntry); err != nil {
		t.Fatalf("Failed to parse log JSON: %v", err)
	}

	if logEntry["request_id"] != existingRequestID {
		t.Errorf("Expected request_id %s, got %v", existingRequestID, logEntry["request_id"])
	}
}

func TestFromContext(t *testing.T) {
	var buf bytes.Buffer

	baseLogger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// Test with request ID in context
	ctx := WithRequestID(context.Background(), "test-id")
	logger := FromContext(ctx, baseLogger)

	logger.Info("test message")

	logOutput := buf.String()
	var logEntry map[string]interface{}
	if err := json.Unmarshal([]byte(logOutput), &logEntry); err != nil {
		t.Fatalf("Failed to parse log JSON: %v", err)
	}

	if logEntry["request_id"] != "test-id" {
		t.Errorf("Expected request_id test-id, got %v", logEntry["request_id"])
	}

	// Test without request ID in context
	buf.Reset()
	ctx = context.Background()
	logger = FromContext(ctx, baseLogger)

	logger.Info("test message")

	logOutput = buf.String()
	var logEntry2 map[string]interface{}
	if err := json.Unmarshal([]byte(logOutput), &logEntry2); err != nil {
		t.Fatalf("Failed to parse log JSON: %v", err)
	}

	if _, exists := logEntry2["request_id"]; exists {
		t.Error("request_id should not be present when not in context")
	}
}

func TestResponseWriter(t *testing.T) {
	w := httptest.NewRecorder()
	rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

	// Test default status code
	if rw.statusCode != http.StatusOK {
		t.Errorf("Expected default status code 200, got %d", rw.statusCode)
	}

	// Test WriteHeader
	rw.WriteHeader(http.StatusNotFound)
	if rw.statusCode != http.StatusNotFound {
		t.Errorf("Expected status code 404, got %d", rw.statusCode)
	}

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected underlying writer status code 404, got %d", w.Code)
	}
}
