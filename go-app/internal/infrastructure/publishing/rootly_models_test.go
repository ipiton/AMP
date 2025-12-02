package publishing

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateIncidentRequest_Validation(t *testing.T) {
	tests := []struct {
		name      string
		req       *CreateIncidentRequest
		wantError bool
		errorMsg  string
	}{
		{
			name: "Valid request",
			req: &CreateIncidentRequest{
				Title:       "Test Incident",
				Description: "Test description with more than 10 characters",
				Severity:    "critical",
				StartedAt:   time.Now(),
			},
			wantError: false,
		},
		{
			name: "Missing title",
			req: &CreateIncidentRequest{
				Description: "Test description",
				Severity:    "critical",
				StartedAt:   time.Now(),
			},
			wantError: true,
			errorMsg:  "title is required",
		},
		{
			name: "Missing description",
			req: &CreateIncidentRequest{
				Title:     "Test Incident",
				Severity:  "critical",
				StartedAt: time.Now(),
			},
			wantError: true,
			errorMsg:  "description is required",
		},
		{
			name: "Invalid severity",
			req: &CreateIncidentRequest{
				Title:       "Test Incident",
				Description: "Test description",
				Severity:    "invalid",
				StartedAt:   time.Now(),
			},
			wantError: true,
			errorMsg:  "invalid severity",
		},
		{
			name: "All severities valid",
			req: &CreateIncidentRequest{
				Title:       "Test Incident",
				Description: "Test description with sufficient length",
				Severity:    "low",
				StartedAt:   time.Now(),
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			if tt.wantError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestUpdateIncidentRequest_Validation(t *testing.T) {
	tests := []struct {
		name      string
		req       *UpdateIncidentRequest
		wantError bool
		errorMsg  string
	}{
		{
			name: "Valid update - description only",
			req: &UpdateIncidentRequest{
				Description: "Updated description with sufficient length",
			},
			wantError: false,
		},
		{
			name: "Valid update - empty",
			req: &UpdateIncidentRequest{},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			if tt.wantError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestResolveIncidentRequest_Validation(t *testing.T) {
	tests := []struct {
		name      string
		req       *ResolveIncidentRequest
		wantError bool
		errorMsg  string
	}{
		{
			name: "Valid resolve request",
			req: &ResolveIncidentRequest{
				Summary: "Issue resolved",
			},
			wantError: false,
		},
		{
			name: "Empty summary (optional)",
			req: &ResolveIncidentRequest{
				Summary: "",
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			if tt.wantError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestIncidentResponse_JSONMarshaling(t *testing.T) {
	// Test unmarshaling from JSON
	jsonData := `{
		"data": {
			"id": "incident-123",
			"type": "incidents",
			"attributes": {
				"title": "Test Incident",
				"severity": "critical",
				"started_at": "2024-01-01T12:00:00Z",
				"status": "started",
				"created_at": "2024-01-01T12:00:00Z"
			}
		}
	}`

	var resp IncidentResponse
	err := json.Unmarshal([]byte(jsonData), &resp)
	require.NoError(t, err)

	assert.Equal(t, "incident-123", resp.Data.ID)
	assert.Equal(t, "incidents", resp.Data.Type)
	assert.Equal(t, "Test Incident", resp.Data.Attributes.Title)
	assert.Equal(t, "critical", resp.Data.Attributes.Severity)
	assert.Equal(t, "started", resp.Data.Attributes.Status)
}

func TestIncidentResponse_GetID(t *testing.T) {
	resp := &IncidentResponse{}
	resp.Data.ID = "test-id-123"

	assert.Equal(t, "test-id-123", resp.GetID())
}

func TestIncidentResponse_GetStatus(t *testing.T) {
	resp := &IncidentResponse{}
	resp.Data.Attributes.Status = "resolved"

	assert.Equal(t, "resolved", resp.GetStatus())
}

func TestIncidentResponse_IsResolved(t *testing.T) {
	tests := []struct {
		name     string
		status   string
		expected bool
	}{
		{"started", "started", false},
		{"resolved", "resolved", true},
		{"other", "other", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &IncidentResponse{}
			resp.Data.Attributes.Status = tt.status

			assert.Equal(t, tt.expected, resp.IsResolved())
		})
	}
}

func TestCreateIncidentRequest_ToJSON(t *testing.T) {
	now := time.Now()
	req := &CreateIncidentRequest{
		Title:       "Test Incident",
		Description: "Test description",
		Severity:    "critical",
		Tags:        []string{"alert", "production"},
		StartedAt:   now,
	}

	data, err := json.Marshal(req)
	require.NoError(t, err)

	var unmarshaled CreateIncidentRequest
	err = json.Unmarshal(data, &unmarshaled)
	require.NoError(t, err)

	assert.Equal(t, req.Title, unmarshaled.Title)
	assert.Equal(t, req.Description, unmarshaled.Description)
	assert.Equal(t, req.Severity, unmarshaled.Severity)
	assert.Equal(t, req.Tags, unmarshaled.Tags)
	assert.WithinDuration(t, req.StartedAt, unmarshaled.StartedAt, time.Second)
}

func BenchmarkCreateIncidentRequest_Validation(b *testing.B) {
	req := &CreateIncidentRequest{
		Title:       "Benchmark Incident",
		Description: "Benchmark test description",
		Severity:    "critical",
		StartedAt:   time.Now(),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = req.Validate()
	}
}

func BenchmarkIncidentResponse_JSONUnmarshal(b *testing.B) {
	jsonData := []byte(`{
		"data": {
			"id": "incident-123",
			"type": "incidents",
			"attributes": {
				"title": "Test Incident",
				"severity": "critical",
				"status": "started",
				"started_at": "2024-01-01T12:00:00Z",
				"created_at": "2024-01-01T12:00:00Z"
			}
		}
	}`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var resp IncidentResponse
		_ = json.Unmarshal(jsonData, &resp)
	}
}
