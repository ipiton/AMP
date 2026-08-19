package config

import (
	"fmt"

	"github.com/ipiton/AMP/internal/business/templating"
	infraroute "github.com/ipiton/AMP/internal/infrastructure/routing"
)

// validateTemplateGlobs loads the `templates:` file globs through the real
// template engine so a malformed template file is a CONFIG LOAD error rather
// than a surprise at the first notification.
//
// Why load-time and not lazily: the alternative is discovering the typo when an
// alert fires at 03:00, with the notification degraded to a fixed-formatter
// fallback and only a log line to explain it. Upstream Alertmanager fails its
// own config load the same way (`amtool check-config` and the reload endpoint
// both parse `templates:`), so failing here is parity as well as good sense.
//
// The error message names the file and the line: templating.FromGlobs wraps
// text/template's own `<file>:<line>: <detail>` with the glob and the file path
// (see internal/business/templating.(*Template).parseFile).
//
// An empty/absent `templates:` list is not an error, and neither is a glob that
// matches nothing — a Kubernetes deployment routinely lists a glob over a
// ConfigMap mount that is populated after the process starts. See
// templating.FromGlobs for that (upstream) allowance.
//
// The built template is DISCARDED here on purpose: this function is a
// validation gate, and the live template is owned by
// application.ServiceRegistry (initializeTemplating), which rebuilds it from
// the same globs. Rebuilding costs a few hundred microseconds once per load and
// keeps internal/config free of runtime state that would then need its own
// concurrency story.
func validateTemplateGlobs(rc *infraroute.RouteConfig) error {
	if rc == nil || len(rc.Templates) == 0 {
		return nil
	}

	if _, err := templating.FromGlobs(rc.Templates, templating.Options{}); err != nil {
		return fmt.Errorf("invalid templates: configuration: %w", err)
	}
	return nil
}
