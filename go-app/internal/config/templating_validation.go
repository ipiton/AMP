package config

import (
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/ipiton/AMP/internal/business/templating"
	infraroute "github.com/ipiton/AMP/internal/infrastructure/routing"
)

// resolveTemplateGlobs rewrites every RELATIVE `templates:` glob to be relative
// to the directory holding the CONFIG FILE, exactly as upstream Alertmanager
// does in resolveFilepaths (alertmanager@v0.34.0/config/config.go:168):
//
//	join := func(fp string) string {
//	    if len(fp) > 0 && !filepath.IsAbs(fp) {
//	        fp = filepath.Join(baseDir, fp)
//	    }
//	    return fp
//	}
//	for i, tf := range cfg.Templates {
//	    cfg.Templates[i] = join(tf)
//	}
//
// This is not a nicety. `templates: ['templates/*.tmpl']` is the canonical
// upstream idiom — it is what upstream's own docs and example configs use — and
// without this rewrite it resolves against whatever CWD the process happens to
// have ("/" in most containers), matches nothing, and loads CLEAN because an
// empty glob match is legal (correctly so: a ConfigMap may mount later). The
// operator gets zero errors, zero warnings, and every custom definition
// silently ignored — the exact "configs with custom templates lose their
// formatting silently" failure this epic exists to remove (slice-1 review C1).
//
// Rewriting at parse time fixes all three consumers at once, because they all
// read the same cfg.Routing.Templates: this file's load-time validation,
// application.ServiceRegistry.initializeTemplating, and its reloadTemplates.
//
// Absolute globs are left untouched, and so is the empty string (upstream's
// `len(fp) > 0` guard: filepath.Join(base, "") would silently turn "" into the
// base directory itself).
func resolveTemplateGlobs(rc *infraroute.RouteConfig, configPath string) {
	if rc == nil || len(rc.Templates) == 0 || configPath == "" {
		return
	}

	baseDir := filepath.Dir(configPath)
	for i, glob := range rc.Templates {
		if glob != "" && !filepath.IsAbs(glob) {
			rc.Templates[i] = filepath.Join(baseDir, glob)
		}
	}
}

// validateTemplateGlobs loads the `templates:` file globs through the real
// template engine so a malformed template file is a CONFIG LOAD error rather
// than a surprise at the first notification.
//
// It also WARNS about a configured glob that matched no files at all: an empty
// match stays legal (see resolveTemplateGlobs), but nothing else distinguishes
// "the ConfigMap mounts in ten seconds" from "your glob is wrong" (review I3).
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

	tmpl, err := templating.FromGlobs(rc.Templates, templating.Options{})
	if err != nil {
		return fmt.Errorf("invalid templates: configuration: %w", err)
	}

	// A configured glob that matched nothing is legal but suspicious. WARN with
	// the RESOLVED (absolute) pattern, which is the thing an operator can check
	// on disk — and which is what makes a wrong relative path visible instead of
	// silent. Deliberately not an error: see the doc comment.
	if unmatched := tmpl.UnmatchedGlobs(); len(unmatched) > 0 {
		slog.Warn("templates: glob matched no files; notifications will use the built-in defaults for anything it was meant to override",
			"globs", unmatched,
			"hint", "paths are resolved relative to the config file's directory; verify the files exist (a ConfigMap mounted after startup is the benign case)")
	}
	return nil
}
