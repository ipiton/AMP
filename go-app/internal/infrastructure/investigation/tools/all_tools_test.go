package tools_test

import (
	"testing"

	"github.com/ipiton/AMP/internal/config"
	"github.com/ipiton/AMP/internal/core/investigation"
	"github.com/ipiton/AMP/internal/infrastructure/investigation/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/kubernetes/fake"
)

// TestAllToolsRegisterAndDefine is a smoke test verifying that all four built-in
// investigation tools can be registered in a real ToolRegistry and each returns
// a valid JSON Schema definition.
func TestAllToolsRegisterAndDefine(t *testing.T) {
	registry := investigation.NewToolRegistry()

	registry.Register(tools.NewPrometheusTool(&config.PrometheusToolConfig{
		Endpoint: "http://prometheus:9090",
	}))
	registry.Register(tools.NewLokiTool(&config.LokiToolConfig{
		Endpoint: "http://loki:3100",
	}))
	registry.Register(tools.NewKubernetesTool(fake.NewSimpleClientset()))
	registry.Register(tools.NewDatabaseTool(&fakeQuerier{}))

	defs := registry.Definitions()
	require.Len(t, defs, 4, "expected 4 tool definitions, got %d", len(defs))

	names := make(map[string]bool, 4)
	for _, d := range defs {
		names[d.Name] = true
		assert.NotEmpty(t, d.Description, "tool %q has empty description", d.Name)
		assert.Equal(t, "object", d.Parameters.Type, "tool %q parameters type must be 'object'", d.Name)
		assert.NotEmpty(t, d.Parameters.Properties, "tool %q has no parameter properties", d.Name)
	}

	assert.True(t, names["prometheus_query_range"], "prometheus_query_range not registered")
	assert.True(t, names["loki_query_range"], "loki_query_range not registered")
	assert.True(t, names["kubernetes_action"], "kubernetes_action not registered")
	assert.True(t, names["database_query"], "database_query not registered")
}
