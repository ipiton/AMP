package validators

import (
	"context"
	"log/slog"

	"github.com/ipiton/AMP/internal/alertmanager/config"
	"github.com/ipiton/AMP/pkg/configvalidator/types"
)

// ReceiverValidator validates receiver configurations
type ReceiverValidator struct {
	options types.Options
	logger  *slog.Logger
}

// NewReceiverValidator creates a new ReceiverValidator
func NewReceiverValidator(opts types.Options, logger *slog.Logger) *ReceiverValidator {
	return &ReceiverValidator{options: opts, logger: logger}
}

// Validate performs receiver validation - stub
func (v *ReceiverValidator) Validate(ctx context.Context, cfg *config.AlertmanagerConfig, result *types.Result) {
	v.logger.Debug("receiver validation - stub")
	// TODO: Implement full validation
}
