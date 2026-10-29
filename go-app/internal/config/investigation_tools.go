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
	// AllowedNamespaces limits what the tool reads. Empty = only the alert's
	// `namespace` label; ["*"] = any namespace.
	AllowedNamespaces []string `mapstructure:"allowed_namespaces"`
}

// DatabaseToolConfig configures the database_query tool.
type DatabaseToolConfig struct {
	Enabled bool `mapstructure:"enabled"`
	// DSN of the database the tool inspects. Required: without it the tool
	// is not registered. It is never AMP's own database.
	DSN string `mapstructure:"dsn"`
}
