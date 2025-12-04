package validators

import (
	"context"
	"log/slog"

	"github.com/ipiton/AMP/internal/alertmanager/config"
	"github.com/ipiton/AMP/pkg/configvalidator/types"
)

// InhibitionValidator validates inhibition rules
type InhibitionValidator struct {
	options types.Options
	logger  *slog.Logger
}

// NewInhibitionValidator creates a new InhibitionValidator
func NewInhibitionValidator(opts types.Options, logger *slog.Logger) *InhibitionValidator {
	return &InhibitionValidator{options: opts, logger: logger}
}

// Validate performs inhibition rules validation - stub
func (v *InhibitionValidator) Validate(ctx context.Context, cfg *config.AlertmanagerConfig, result *types.Result) {
	v.logger.Debug("inhibition validation - stub")
	// TODO: Implement full validation
}
