package config

import "time"

// InvestigationToolsConfig holds configuration for all built-in investigation tools.
type InvestigationToolsConfig struct {
	Prometheus *PrometheusToolConfig `mapstructure:"prometheus" yaml:"prometheus,omitempty"`
	Loki       *LokiToolConfig       `mapstructure:"loki"       yaml:"loki,omitempty"`
	Kubernetes *KubernetesToolConfig `mapstructure:"kubernetes" yaml:"kubernetes,omitempty"`
	Database   *DatabaseToolConfig   `mapstructure:"database"   yaml:"database,omitempty"`
}

// PrometheusToolConfig configures the prometheus_query_range tool.
type PrometheusToolConfig struct {
	Endpoint string        `mapstructure:"endpoint"`
	Timeout  time.Duration `mapstructure:"timeout"`
	Username string        `mapstructure:"username,omitempty"`
	Password string        `mapstructure:"password,omitempty"`
}

// LokiToolConfig configures the loki_query_range tool.
type LokiToolConfig struct {
	Endpoint string        `mapstructure:"endpoint"`
	Timeout  time.Duration `mapstructure:"timeout"`
	Username string        `mapstructure:"username,omitempty"`
	Password string        `mapstructure:"password,omitempty"`
}

// KubernetesToolConfig configures the kubernetes_action tool.
type KubernetesToolConfig struct {
	Enabled    bool   `mapstructure:"enabled"`
	Kubeconfig string `mapstructure:"kubeconfig,omitempty"` // empty = in-cluster
}

// DatabaseToolConfig configures the database_query tool.
type DatabaseToolConfig struct {
	Enabled bool `mapstructure:"enabled"` // uses main PG connection pool
}
