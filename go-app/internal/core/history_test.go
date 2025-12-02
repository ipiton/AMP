package core

import (
	"testing"
)

// TestPagination tests the Pagination struct
func TestPagination(t *testing.T) {
	tests := []struct {
		name      string
		page      int
		perPage   int
		wantErr   bool
		wantOffset int
	}{
		{
			name:       "valid pagination",
			page:       1,
			perPage:    50,
			wantErr:    false,
			wantOffset: 0,
		},
		{
			name:       "page 2",
			page:       2,
			perPage:    50,
			wantErr:    false,
			wantOffset: 50,
		},
		{
			name:       "page 10 with 100 per page",
			page:       10,
			perPage:    100,
			wantErr:    false,
			wantOffset: 900,
		},
		{
			name:    "invalid page (0)",
			page:    0,
			perPage: 50,
			wantErr: true,
		},
		{
			name:    "invalid page (negative)",
			page:    -1,
			perPage: 50,
			wantErr: true,
		},
		{
			name:    "invalid per_page (0)",
			page:    1,
			perPage: 0,
			wantErr: true,
		},
		{
			name:    "invalid per_page (too large)",
			page:    1,
			perPage: 1001,
			wantErr: true,
		},
		{
			name:       "max per_page",
			page:       1,
			perPage:    1000,
			wantErr:    false,
			wantOffset: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Pagination{
				Page:    tt.page,
				PerPage: tt.perPage,
			}

			err := p.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Pagination.Validate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if got := p.Offset(); got != tt.wantOffset {
					t.Errorf("Pagination.Offset() = %v, want %v", got, tt.wantOffset)
				}
			}
		})
	}
}

// TestSorting tests the Sorting struct
func TestSorting(t *testing.T) {
	tests := []struct {
		name      string
		field     string
		order     SortOrder
		wantErr   bool
		wantSQL   string
	}{
		{
			name:    "valid sorting - created_at desc",
			field:   "created_at",
			order:   SortOrderDesc,
			wantErr: false,
			wantSQL: "created_at desc",
		},
		{
			name:    "valid sorting - starts_at asc",
			field:   "starts_at",
			order:   SortOrderAsc,
			wantErr: false,
			wantSQL: "starts_at asc",
		},
		{
			name:    "valid sorting - severity desc",
			field:   "severity",
			order:   SortOrderDesc,
			wantErr: false,
			wantSQL: "severity desc",
		},
		{
			name:    "invalid field",
			field:   "invalid_field",
			order:   SortOrderDesc,
			wantErr: true,
		},
		{
			name:    "empty field",
			field:   "",
			order:   SortOrderDesc,
			wantErr: true,
		},
		{
			name:    "invalid order",
			field:   "created_at",
			order:   SortOrder("invalid"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Sorting{
				Field: tt.field,
				Order: tt.order,
			}

			err := s.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Sorting.Validate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if got := s.ToSQL(); got != tt.wantSQL {
					t.Errorf("Sorting.ToSQL() = %v, want %v", got, tt.wantSQL)
				}
			}
		})
	}
}

// TestSorting_DefaultSQL tests the default SQL generation
func TestSorting_DefaultSQL(t *testing.T) {
	var s *Sorting
	got := s.ToSQL()
	want := "starts_at DESC"

	if got != want {
		t.Errorf("nil Sorting.ToSQL() = %v, want %v", got, want)
	}
}

// TestHistoryRequest_Validate tests the HistoryRequest validation
func TestHistoryRequest_Validate(t *testing.T) {
	tests := []struct {
		name    string
		req     *HistoryRequest
		wantErr bool
	}{
		{
			name: "valid request - minimal",
			req: &HistoryRequest{
				Pagination: &Pagination{
					Page:    1,
					PerPage: 50,
				},
			},
			wantErr: false,
		},
		{
			name: "valid request - with filters and sorting",
			req: &HistoryRequest{
				Filters: &AlertFilters{
					Status: func() *AlertStatus { s := StatusFiring; return &s }(),
					Limit:  100,
					Offset: 0,
				},
				Pagination: &Pagination{
					Page:    1,
					PerPage: 100,
				},
				Sorting: &Sorting{
					Field: "starts_at",
					Order: SortOrderDesc,
				},
			},
			wantErr: false,
		},
		{
			name: "invalid - nil pagination",
			req: &HistoryRequest{
				Pagination: nil,
			},
			wantErr: true,
		},
		{
			name: "invalid - bad pagination",
			req: &HistoryRequest{
				Pagination: &Pagination{
					Page:    0,
					PerPage: 50,
				},
			},
			wantErr: true,
		},
		{
			name: "invalid - bad filters",
			req: &HistoryRequest{
				Filters: &AlertFilters{
					Limit: 2000, // too large
				},
				Pagination: &Pagination{
					Page:    1,
					PerPage: 50,
				},
			},
			wantErr: true,
		},
		{
			name: "invalid - bad sorting",
			req: &HistoryRequest{
				Pagination: &Pagination{
					Page:    1,
					PerPage: 50,
				},
				Sorting: &Sorting{
					Field: "invalid_field",
					Order: SortOrderDesc,
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("HistoryRequest.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// BenchmarkPagination_Offset benchmarks the offset calculation
func BenchmarkPagination_Offset(b *testing.B) {
	p := &Pagination{
		Page:    100,
		PerPage: 50,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = p.Offset()
	}
}

// BenchmarkSorting_ToSQL benchmarks SQL generation
func BenchmarkSorting_ToSQL(b *testing.B) {
	s := &Sorting{
		Field: "created_at",
		Order: SortOrderDesc,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s.ToSQL()
	}
}

// BenchmarkHistoryRequest_Validate benchmarks request validation
func BenchmarkHistoryRequest_Validate(b *testing.B) {
	req := &HistoryRequest{
		Filters: &AlertFilters{
			Status: func() *AlertStatus { s := StatusFiring; return &s }(),
			Limit:  100,
		},
		Pagination: &Pagination{
			Page:    1,
			PerPage: 50,
		},
		Sorting: &Sorting{
			Field: "starts_at",
			Order: SortOrderDesc,
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = req.Validate()
	}
}
