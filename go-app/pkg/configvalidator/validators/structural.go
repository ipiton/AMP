package validators

import (
	"context"
	"log/slog"

	"github.com/ipiton/AMP/internal/alertmanager/config"
	"github.com/ipiton/AMP/pkg/configvalidator/types"
)

// StructuralValidator checks that the top-level sections required for a
// usable Alertmanager configuration are present: a root route and at
// least one receiver. Per-route and per-receiver content is handled by
// RouteValidator and ReceiverValidator respectively; this validator only
// guards the overall shape.
type StructuralValidator struct {
	options types.Options
	logger  *slog.Logger
}

// NewStructuralValidator creates a new StructuralValidator.
func NewStructuralValidator(opts types.Options, logger *slog.Logger) *StructuralValidator {
	if logger == nil {
		logger = slog.Default()
	}
	return &StructuralValidator{options: opts, logger: logger}
}

// Validate performs structural validation of the top-level config shape.
func (v *StructuralValidator) Validate(_ context.Context, cfg *config.AlertmanagerConfig, result *types.Result) {
	if cfg == nil {
		result.AddError(newError(
			"E000", "root", "",
			"configuration is empty",
			"Provide a non-empty Alertmanager configuration",
		))
		return
	}

	// E100: a root route is mandatory - without it no alert can ever be routed.
	if cfg.Route == nil {
		result.AddError(newError(
			"E100", "route", "route",
			"root route is required",
			"Add a 'route' section to the configuration",
		))
	}

	// E021: at least one receiver must be defined, otherwise no route can
	// ever resolve to a real notification target.
	if len(cfg.Receivers) == 0 {
		result.AddError(newError(
			"E021", "receivers", "receivers",
			"no receivers defined",
			"Define at least one receiver in the 'receivers' section",
		))
	}

	v.noteFutureSections(cfg, result)
}

// noteFutureSections surfaces informational notes for sections that are
// accepted structurally (so parsing doesn't reject configs adopting them
// early) but whose contents are not yet validated.
//
// Decision (documented per task 5.1 brief): ACCEPT, don't warn or reject.
// time_intervals/mute_time_intervals ship in a later AMP phase; treating
// their mere presence as an error or warning would be a false positive
// for users who are simply ahead of this validator's coverage. We only
// emit a low-severity Info note (gated by IncludeInfo) so the gap stays
// visible without blocking strict-mode validation.
func (v *StructuralValidator) noteFutureSections(cfg *config.AlertmanagerConfig, result *types.Result) {
	if !v.options.IncludeInfo {
		return
	}
	if len(cfg.TimeIntervals) > 0 || len(cfg.MuteTimeIntervals) > 0 {
		result.AddInfo(types.Info{
			Type:     types.InfoTypeCompatibility,
			Code:     "I001",
			Message:  "time_intervals/mute_time_intervals are accepted but not yet semantically validated (planned for a later phase)",
			Location: types.Location{Section: "time_intervals"},
			DocsURL:  docsURL,
		})
	}
}
