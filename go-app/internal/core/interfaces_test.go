package core

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestAlertFilters_Validate(t *testing.T) {
	tests := []struct {
		name        string
		filters     *AlertFilters
		expectError error
		description string
	}{
		{
			name: "valid filters with all fields",
			filters: &AlertFilters{
				Limit:  100,
				Offset: 0,
				Status: ptrAlertStatus(StatusFiring),
				Severity: ptrString("warning"),
				Namespace: ptrString("production"),
				Labels: map[string]string{
					"env": "prod",
				},
				TimeRange: &TimeRange{
					From: ptrTime(time.Now().Add(-24 * time.Hour)),
					To:   ptrTime(time.Now()),
				},
			},
			expectError: nil,
			description: "All valid filters should pass",
		},
		{
			name: "valid filters with minimal fields",
			filters: &AlertFilters{
				Limit:  10,
				Offset: 0,
			},
			expectError: nil,
			description: "Minimal filters should pass",
		},
		{
			name: "invalid limit negative",
			filters: &AlertFilters{
				Limit:  -1,
				Offset: 0,
			},
			expectError: ErrInvalidFilterLimit,
			description: "Negative limit should fail",
		},
		{
			name: "invalid limit too large",
			filters: &AlertFilters{
				Limit:  1001,
				Offset: 0,
			},
			expectError: ErrFilterLimitTooLarge,
			description: "Limit > 1000 should fail",
		},
		{
			name: "valid limit boundary 1000",
			filters: &AlertFilters{
				Limit:  1000,
				Offset: 0,
			},
			expectError: nil,
			description: "Limit = 1000 should pass",
		},
		{
			name: "invalid offset negative",
			filters: &AlertFilters{
				Limit:  10,
				Offset: -1,
			},
			expectError: ErrInvalidFilterOffset,
			description: "Negative offset should fail",
		},
		{
			name: "invalid status",
			filters: &AlertFilters{
				Limit:  10,
				Offset: 0,
				Status: ptrAlertStatus(AlertStatus("invalid")),
			},
			expectError: ErrInvalidFilterStatus,
			description: "Invalid status should fail",
		},
		{
			name: "valid status firing",
			filters: &AlertFilters{
				Limit:  10,
				Offset: 0,
				Status: ptrAlertStatus(StatusFiring),
			},
			expectError: nil,
			description: "Status firing should pass",
		},
		{
			name: "valid status resolved",
			filters: &AlertFilters{
				Limit:  10,
				Offset: 0,
				Status: ptrAlertStatus(StatusResolved),
			},
			expectError: nil,
			description: "Status resolved should pass",
		},
		{
			name: "invalid severity",
			filters: &AlertFilters{
				Limit:    10,
				Offset:   0,
				Severity: ptrString("invalid"),
			},
			expectError: ErrInvalidFilterSeverity,
			description: "Invalid severity should fail",
		},
		{
			name: "valid severity critical",
			filters: &AlertFilters{
				Limit:    10,
				Offset:   0,
				Severity: ptrString("critical"),
			},
			expectError: nil,
			description: "Severity critical should pass",
		},
		{
			name: "valid severity warning",
			filters: &AlertFilters{
				Limit:    10,
				Offset:   0,
				Severity: ptrString("warning"),
			},
			expectError: nil,
			description: "Severity warning should pass",
		},
		{
			name: "valid severity info",
			filters: &AlertFilters{
				Limit:    10,
				Offset:   0,
				Severity: ptrString("info"),
			},
			expectError: nil,
			description: "Severity info should pass",
		},
		{
			name: "valid severity noise",
			filters: &AlertFilters{
				Limit:    10,
				Offset:   0,
				Severity: ptrString("noise"),
			},
			expectError: nil,
			description: "Severity noise should pass",
		},
		{
			name: "invalid time range from after to",
			filters: &AlertFilters{
				Limit:  10,
				Offset: 0,
				TimeRange: &TimeRange{
					From: ptrTime(time.Now()),
					To:   ptrTime(time.Now().Add(-24 * time.Hour)),
				},
			},
			expectError: ErrInvalidTimeRange,
			description: "From after To should fail",
		},
		{
			name: "valid time range from before to",
			filters: &AlertFilters{
				Limit:  10,
				Offset: 0,
				TimeRange: &TimeRange{
					From: ptrTime(time.Now().Add(-24 * time.Hour)),
					To:   ptrTime(time.Now()),
				},
			},
			expectError: nil,
			description: "From before To should pass",
		},
		{
			name: "valid time range only from",
			filters: &AlertFilters{
				Limit:  10,
				Offset: 0,
				TimeRange: &TimeRange{
					From: ptrTime(time.Now().Add(-24 * time.Hour)),
				},
			},
			expectError: nil,
			description: "Only From should pass",
		},
		{
			name: "valid time range only to",
			filters: &AlertFilters{
				Limit:  10,
				Offset: 0,
				TimeRange: &TimeRange{
					To: ptrTime(time.Now()),
				},
			},
			expectError: nil,
			description: "Only To should pass",
		},
		{
			name: "too many labels",
			filters: &AlertFilters{
				Limit:  10,
				Offset: 0,
				Labels: map[string]string{
					"label1":  "value1",
					"label2":  "value2",
					"label3":  "value3",
					"label4":  "value4",
					"label5":  "value5",
					"label6":  "value6",
					"label7":  "value7",
					"label8":  "value8",
					"label9":  "value9",
					"label10": "value10",
					"label11": "value11",
					"label12": "value12",
					"label13": "value13",
					"label14": "value14",
					"label15": "value15",
					"label16": "value16",
					"label17": "value17",
					"label18": "value18",
					"label19": "value19",
					"label20": "value20",
					"label21": "value21", // 21 labels - exceeds limit
				},
			},
			expectError: ErrTooManyLabels,
			description: "More than 20 labels should fail",
		},
		{
			name: "exactly 20 labels",
			filters: &AlertFilters{
				Limit:  10,
				Offset: 0,
				Labels: map[string]string{
					"label1":  "value1",
					"label2":  "value2",
					"label3":  "value3",
					"label4":  "value4",
					"label5":  "value5",
					"label6":  "value6",
					"label7":  "value7",
					"label8":  "value8",
					"label9":  "value9",
					"label10": "value10",
					"label11": "value11",
					"label12": "value12",
					"label13": "value13",
					"label14": "value14",
					"label15": "value15",
					"label16": "value16",
					"label17": "value17",
					"label18": "value18",
					"label19": "value19",
					"label20": "value20",
				},
			},
			expectError: nil,
			description: "Exactly 20 labels should pass",
		},
		{
			name: "empty label key",
			filters: &AlertFilters{
				Limit:  10,
				Offset: 0,
				Labels: map[string]string{
					"": "value",
				},
			},
			expectError: ErrEmptyLabelKey,
			description: "Empty label key should fail",
		},
		{
			name: "label key too long",
			filters: &AlertFilters{
				Limit:  10,
				Offset: 0,
				Labels: map[string]string{
					string(make([]byte, 256)): "value", // 256 chars - exceeds limit
				},
			},
			expectError: ErrLabelKeyTooLong,
			description: "Label key > 255 chars should fail",
		},
		{
			name: "label value too long",
			filters: &AlertFilters{
				Limit:  10,
				Offset: 0,
				Labels: map[string]string{
					"key": string(make([]byte, 256)), // 256 chars - exceeds limit
				},
			},
			expectError: ErrLabelValueTooLong,
			description: "Label value > 255 chars should fail",
		},
		{
			name: "label key exactly 255 chars",
			filters: &AlertFilters{
				Limit:  10,
				Offset: 0,
				Labels: map[string]string{
					string(make([]byte, 255)): "value",
				},
			},
			expectError: nil,
			description: "Label key = 255 chars should pass",
		},
		{
			name: "label value exactly 255 chars",
			filters: &AlertFilters{
				Limit:  10,
				Offset: 0,
				Labels: map[string]string{
					"key": string(make([]byte, 255)),
				},
			},
			expectError: nil,
			description: "Label value = 255 chars should pass",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.filters.Validate()
			if tt.expectError != nil {
				assert.Error(t, err, tt.description)
				assert.Equal(t, tt.expectError, err, tt.description)
			} else {
				assert.NoError(t, err, tt.description)
			}
		})
	}
}

// Helper functions for creating pointers
func ptrString(s string) *string {
	return &s
}

func ptrAlertStatus(s AlertStatus) *AlertStatus {
	return &s
}

func ptrTime(t time.Time) *time.Time {
	return &t
}

// Benchmark for Validate method
func BenchmarkAlertFilters_Validate(b *testing.B) {
	filters := &AlertFilters{
		Limit:     100,
		Offset:    0,
		Status:    ptrAlertStatus(StatusFiring),
		Severity:  ptrString("warning"),
		Namespace: ptrString("production"),
		Labels: map[string]string{
			"env":     "prod",
			"cluster": "us-west-1",
		},
		TimeRange: &TimeRange{
			From: ptrTime(time.Now().Add(-24 * time.Hour)),
			To:   ptrTime(time.Now()),
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = filters.Validate()
	}
}
