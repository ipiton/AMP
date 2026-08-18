package validators

import (
	"context"
	"testing"

	"github.com/ipiton/AMP/internal/alertmanager/config"
	"github.com/ipiton/AMP/pkg/configvalidator/types"
)

func runRouteValidator(cfg *config.AlertmanagerConfig) *types.Result {
	result := types.NewResult()
	v := NewRouteValidator(testOptions(), nil)
	v.Validate(context.Background(), cfg, result)
	return result
}

func TestRouteValidator(t *testing.T) {
	tests := []struct {
		name         string
		cfg          *config.AlertmanagerConfig
		wantErrCode  string
		wantWarnCode string
		wantNoIssues bool
	}{
		{
			name:         "valid minimal config",
			cfg:          minimalValidConfig(),
			wantNoIssues: true,
		},
		{
			name: "root route missing receiver",
			cfg: &config.AlertmanagerConfig{
				Route:     &config.Route{},
				Receivers: []*config.Receiver{{Name: "default", WebhookConfigs: []*config.WebhookConfig{{URL: "https://example.com"}}}},
			},
			wantErrCode: "E103",
		},
		{
			name: "child route unknown receiver",
			cfg: &config.AlertmanagerConfig{
				Route: &config.Route{
					Receiver: "default",
					Routes: []*config.Route{
						{Receiver: "does-not-exist"},
					},
				},
				Receivers: []*config.Receiver{{Name: "default", WebhookConfigs: []*config.WebhookConfig{{URL: "https://example.com"}}}},
			},
			wantErrCode: "E102",
		},
		{
			name: "valid matchers list syntax",
			cfg: &config.AlertmanagerConfig{
				Route: &config.Route{
					Receiver: "default",
					Routes: []*config.Route{
						{Receiver: "default", Matchers: []string{"severity=critical", "team!~^dev.*$"}},
					},
				},
				Receivers: []*config.Receiver{{Name: "default", WebhookConfigs: []*config.WebhookConfig{{URL: "https://example.com"}}}},
			},
			wantNoIssues: true,
		},
		{
			name: "bad matcher syntax (no operator)",
			cfg: &config.AlertmanagerConfig{
				Route: &config.Route{
					Receiver: "default",
					Routes: []*config.Route{
						{Receiver: "default", Matchers: []string{"severity-critical"}},
					},
				},
				Receivers: []*config.Receiver{{Name: "default", WebhookConfigs: []*config.WebhookConfig{{URL: "https://example.com"}}}},
			},
			wantErrCode: "E104",
		},
		{
			name: "bad matcher regex",
			cfg: &config.AlertmanagerConfig{
				Route: &config.Route{
					Receiver: "default",
					Routes: []*config.Route{
						{Receiver: "default", Matchers: []string{"severity=~("}},
					},
				},
				Receivers: []*config.Receiver{{Name: "default", WebhookConfigs: []*config.WebhookConfig{{URL: "https://example.com"}}}},
			},
			wantErrCode: "E105",
		},
		{
			name: "deprecated match field still validated",
			cfg: &config.AlertmanagerConfig{
				Route: &config.Route{
					Receiver: "default",
					Routes: []*config.Route{
						{Receiver: "default", Match: map[string]string{"severity": "critical"}},
					},
				},
				Receivers: []*config.Receiver{{Name: "default", WebhookConfigs: []*config.WebhookConfig{{URL: "https://example.com"}}}},
			},
			wantWarnCode: "W100",
		},
		{
			name: "negative group_wait",
			cfg: &config.AlertmanagerConfig{
				Route: &config.Route{
					Receiver:  "default",
					GroupWait: -1,
				},
				Receivers: []*config.Receiver{{Name: "default", WebhookConfigs: []*config.WebhookConfig{{URL: "https://example.com"}}}},
			},
			wantErrCode: "E026",
		},
		{
			name: "invalid group_by label name",
			cfg: &config.AlertmanagerConfig{
				Route: &config.Route{
					Receiver: "default",
					GroupBy:  []string{"not a label!"},
				},
				Receivers: []*config.Receiver{{Name: "default", WebhookConfigs: []*config.WebhookConfig{{URL: "https://example.com"}}}},
			},
			wantErrCode: "E106",
		},
		{
			name: "group_by ellipsis sentinel is valid",
			cfg: &config.AlertmanagerConfig{
				Route: &config.Route{
					Receiver: "default",
					GroupBy:  []string{"..."},
				},
				Receivers: []*config.Receiver{{Name: "default", WebhookConfigs: []*config.WebhookConfig{{URL: "https://example.com"}}}},
			},
			wantNoIssues: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := runRouteValidator(tt.cfg)

			if tt.wantNoIssues && (len(result.Errors) != 0 || len(result.Warnings) != 0) {
				t.Fatalf("expected no issues, got errors=%+v warnings=%+v", result.Errors, result.Warnings)
			}
			if tt.wantErrCode != "" && !hasErrorCode(result, tt.wantErrCode) {
				t.Fatalf("expected error code %s, got %+v", tt.wantErrCode, result.Errors)
			}
			if tt.wantWarnCode != "" && !hasWarningCode(result, tt.wantWarnCode) {
				t.Fatalf("expected warning code %s, got %+v", tt.wantWarnCode, result.Warnings)
			}
		})
	}
}

// TestRouteValidator_Cycle constructs a route graph with a genuine pointer
// cycle by hand (route.Routes[0] == route). This is not expressible via
// YAML unmarshalling (each node there is freshly allocated) but is
// expressible directly against the Go struct, which is what the cycle
// guard in validateNode defends against.
func TestRouteValidator_Cycle(t *testing.T) {
	root := &config.Route{Receiver: "default"}
	root.Routes = []*config.Route{root}

	cfg := &config.AlertmanagerConfig{
		Route:     root,
		Receivers: []*config.Receiver{{Name: "default", WebhookConfigs: []*config.WebhookConfig{{URL: "https://example.com"}}}},
	}

	result := runRouteValidator(cfg)
	if !hasErrorCode(result, "E160") {
		t.Fatalf("expected E160 cycle error, got %+v", result.Errors)
	}
}

func TestRouteValidator_DepthLimit(t *testing.T) {
	var root *config.Route
	var current *config.Route
	for i := 0; i <= MaxRouteDepth+1; i++ {
		node := &config.Route{Receiver: "default"}
		if root == nil {
			root = node
			current = node
			continue
		}
		current.Routes = []*config.Route{node}
		current = node
	}

	cfg := &config.AlertmanagerConfig{
		Route:     root,
		Receivers: []*config.Receiver{{Name: "default", WebhookConfigs: []*config.WebhookConfig{{URL: "https://example.com"}}}},
	}

	result := runRouteValidator(cfg)
	if !hasErrorCode(result, "E101") {
		t.Fatalf("expected E101 depth error, got %+v", result.Errors)
	}
}

// buildRouteChain builds a linear route tree (root -> child -> ... ) of
// exactly nodeCount nodes, each receiver-less except the root (children
// inherit), all referencing "default".
func buildRouteChain(nodeCount int) *config.Route {
	var root *config.Route
	var current *config.Route
	for i := 0; i < nodeCount; i++ {
		node := &config.Route{Receiver: "default"}
		if root == nil {
			root = node
			current = node
			continue
		}
		current.Routes = []*config.Route{node}
		current = node
	}
	return root
}

// TestRouteValidator_DepthBoundary is the explicit boundary regression for
// MaxRouteDepth (sourced from internal/infrastructure/routing.MaxRouteDepth,
// currently 10, see the const doc comment above): exactly at the limit
// must pass clean, one node past it must fail with E101. A previous
// version of this file hardcoded a depth limit of 100 against the real
// loader's 10 (Phase 5 review round 1) - this pins both edges of that
// fix so it cannot silently regress again.
func TestRouteValidator_DepthBoundary(t *testing.T) {
	t.Run("exactly at MaxRouteDepth passes", func(t *testing.T) {
		root := buildRouteChain(MaxRouteDepth)
		cfg := &config.AlertmanagerConfig{
			Route:     root,
			Receivers: []*config.Receiver{{Name: "default", WebhookConfigs: []*config.WebhookConfig{{URL: "https://example.com"}}}},
		}

		result := runRouteValidator(cfg)
		if hasErrorCode(result, "E101") {
			t.Fatalf("depth == MaxRouteDepth (%d) must not report E101, got %+v", MaxRouteDepth, result.Errors)
		}
	})

	t.Run("one past MaxRouteDepth fails", func(t *testing.T) {
		root := buildRouteChain(MaxRouteDepth + 1)
		cfg := &config.AlertmanagerConfig{
			Route:     root,
			Receivers: []*config.Receiver{{Name: "default", WebhookConfigs: []*config.WebhookConfig{{URL: "https://example.com"}}}},
		}

		result := runRouteValidator(cfg)
		if !hasErrorCode(result, "E101") {
			t.Fatalf("depth == MaxRouteDepth+1 (%d) must report E101, got %+v", MaxRouteDepth+1, result.Errors)
		}
	})
}

func TestRouteValidator_MuteTimeIntervalsAccepted(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Route.MuteTimeIntervals = []string{"weekends"}

	result := runRouteValidator(cfg)
	if len(result.Errors) != 0 {
		t.Fatalf("mute_time_intervals must not be rejected, got %+v", result.Errors)
	}
	if !hasInfoCode(result, "I001") {
		t.Fatalf("expected I001 info note, got %+v", result.Info)
	}
}
