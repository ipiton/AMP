package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	appconfig "github.com/ipiton/AMP/internal/config"
)

func toolNames(t *testing.T, cfg appconfig.InvestigationToolsConfig) []string {
	t.Helper()
	registry, closeTools := newToolRegistry(discardLogger(), cfg)
	t.Cleanup(closeTools)
	var names []string
	for _, d := range registry.Definitions() {
		names = append(names, d.Name)
	}
	return names
}

// llm.agent_mode registers exactly the tools configured under
// investigation.tools.
func TestNewToolRegistryFollowsConfig(t *testing.T) {
	assert.Empty(t, toolNames(t, appconfig.InvestigationToolsConfig{}), "nothing configured")

	assert.Equal(t, []string{"database_query", "loki_query_range", "prometheus_query_range"}, toolNames(t, appconfig.InvestigationToolsConfig{
		Prometheus: &appconfig.PrometheusToolConfig{Endpoint: "http://prometheus:9090"},
		Loki:       &appconfig.LokiToolConfig{Endpoint: "http://loki:3100"},
		Database:   &appconfig.DatabaseToolConfig{Enabled: true, DSN: "postgres://ro@db/app"},
	}))
}

func TestNewToolRegistrySkipsIncompleteTools(t *testing.T) {
	assert.Empty(t, toolNames(t, appconfig.InvestigationToolsConfig{
		Prometheus: &appconfig.PrometheusToolConfig{},
		Loki:       &appconfig.LokiToolConfig{},
		// Never AMP's own database: no DSN, no tool.
		Database:   &appconfig.DatabaseToolConfig{Enabled: true},
		Kubernetes: &appconfig.KubernetesToolConfig{Enabled: false},
	}))
	assert.Empty(t, toolNames(t, appconfig.InvestigationToolsConfig{
		Database: &appconfig.DatabaseToolConfig{Enabled: false, DSN: "postgres://ro@db/app"},
	}), "disabled database tool")
}
