package validators

import (
	"context"
	"log/slog"

	"github.com/ipiton/AMP/internal/alertmanager/config"
	"github.com/ipiton/AMP/pkg/configvalidator/types"
)

// RouteValidator validates routing configurations
type RouteValidator struct {
	options types.Options
	logger  *slog.Logger
}

// NewRouteValidator creates a new RouteValidator
func NewRouteValidator(opts types.Options, logger *slog.Logger) *RouteValidator {
	return &RouteValidator{options: opts, logger: logger}
}

// Validate performs route validation - stub
func (v *RouteValidator) Validate(ctx context.Context, cfg *config.AlertmanagerConfig, result *types.Result) {
	v.logger.Debug("route validation - stub")
	// TODO: Implement full validation
}
