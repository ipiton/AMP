package validators

import (
	"context"
	"log/slog"

	"github.com/ipiton/AMP/internal/alertmanager/config"
	"github.com/ipiton/AMP/pkg/configvalidator/types"
)

// StructuralValidator validates structural aspects of configuration
type StructuralValidator struct {
	options types.Options
	logger  *slog.Logger
}

// NewStructuralValidator creates a new StructuralValidator
func NewStructuralValidator(opts types.Options, logger *slog.Logger) *StructuralValidator {
	return &StructuralValidator{options: opts, logger: logger}
}

// Validate performs structural validation - stub
func (v *StructuralValidator) Validate(ctx context.Context, cfg *config.AlertmanagerConfig, result *types.Result) {
	v.logger.Debug("structural validation - stub")
	// TODO: Implement full validation
}
