package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRouting_ReloadPath_NoFork verifies that a Config carrying a populated
// Routing field (task 1.3) flows safely through the existing hot-reload
// machinery (ConfigValidator.Validate, ConfigComparator.Compare) without a
// separate parse path.
//
// Two hazards this guards against, both caused by Config.Routing embedding
// infrastructure/routing.RouteConfig directly:
//   - ConfigComparator.Compare() JSON-marshals the whole Config; RouteConfig
//     carries a `map[*grouping.Route]map[string]*regexp.Regexp` internal
//     field that encoding/json cannot serialize (unsupported map key type).
//     Config.Routing must be `json:"-"` to avoid this.
//   - ConfigValidator.Validate() runs go-playground/validator over the whole
//     Config via reflection; RouteConfig's own `validate:"alphanum_hyphen"`,
//     `validate:"https_production"`, etc. custom tags are only registered on
//     routing's own validator instance, not on the config package's. Without
//     `validate:"-"` on Config.Routing, this panics with "undefined
//     validation function".
func TestRouting_ReloadPath_NoFork(t *testing.T) {
	resetViper()
	unsetEnvKeys("SERVER_PORT")

	yaml := `
server:
  port: 8080

route:
  receiver: default
  group_by: [alertname]

receivers:
  - name: default
    webhook_configs:
      - url: https://example.com/webhook
`
	path := writeTempYAML(t, yaml)

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	require.True(t, cfg.HasRouteTree())

	// ConfigValidator.Validate must not panic and must not report spurious
	// errors against the already-validated Routing subtree.
	validator := NewConfigValidator()
	require.NotPanics(t, func() {
		errs := validator.Validate(cfg, nil)
		for _, e := range errs {
			assert.NotContains(t, e.Field, "Routing", "Routing must be excluded from structural validation (validate:\"-\")")
		}
	})

	// ConfigComparator.Compare must not error out on json.Marshal.
	comparator := NewConfigComparator()
	diff, err := comparator.Compare(cfg, cfg, nil)
	require.NoError(t, err)
	assert.NotNil(t, diff)
}
