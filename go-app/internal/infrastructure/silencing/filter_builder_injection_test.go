package silencing

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestBuildListQuery_OrderByInjection verifies that a malicious OrderBy value
// never reaches the SQL string (defense-in-depth vs skipping Validate).
func TestBuildListQuery_OrderByInjection(t *testing.T) {
	repo := &PostgresSilenceRepository{}

	filter := SilenceFilter{
		OrderBy: "created_at; SELECT pg_sleep(10); --",
	}
	query, _ := repo.buildListQuery(filter)

	assert.NotContains(t, query, "pg_sleep", "malicious OrderBy must not be interpolated")
	assert.Contains(t, query, "ORDER BY created_at DESC", "must fall back to default ordering")
}

// TestBuildListQuery_MatcherValueEscaped verifies JSONB filter values are escaped.
func TestBuildListQuery_MatcherValueEscaped(t *testing.T) {
	repo := &PostgresSilenceRepository{}

	filter := SilenceFilter{
		MatcherValue: `x","name":"injected`,
	}
	_, args := repo.buildListQuery(filter)

	if assert.Len(t, args, 3) { // matcher + limit + offset
		assert.Equal(t, `[{"value":"x\",\"name\":\"injected"}]`, args[0])
	}
}
