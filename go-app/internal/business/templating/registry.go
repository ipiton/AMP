package templating

import (
	"sync/atomic"
)

// Registry holds the live *Template and swaps it atomically on config reload.
//
// It exists because parsing and executing a *Template have different
// thread-safety properties: execution is safe from any number of goroutines,
// but PARSING mutates the underlying text/html template instances. AMP renders
// notifications from the notify chain while an operator may be reloading the
// config, so a reload must never re-parse the template a sender is currently
// executing. Registry makes reload a pointer swap: Reload builds a COMPLETE new
// Template from the new globs, and only publishes it once it parsed cleanly.
//
// Failure posture: a reload whose globs are broken leaves the previous template
// in place and returns the error. That is deliberate and matches how
// ServiceRegistry treats the route tree — the operator sees a hard error, and
// meanwhile notifications keep rendering with the last-known-good library
// rather than silently reverting to bare defaults.
//
// The zero Registry is not usable; construct with NewRegistry.
type Registry struct {
	current atomic.Pointer[Template]
	opts    Options
}

// NewRegistry builds a Registry whose initial template is the embedded default
// library plus the given globs. It fails if any matched file is malformed —
// callers wire this into config load, so a bad `templates:` entry is a startup
// error rather than a surprise at first notification.
func NewRegistry(globs []string, opts Options) (*Registry, error) {
	tmpl, err := FromGlobs(globs, opts)
	if err != nil {
		return nil, err
	}

	r := &Registry{opts: opts}
	r.current.Store(tmpl)
	return r, nil
}

// Current returns the live template. Never nil for a Registry built by
// NewRegistry; nil only for a zero-value Registry, which callers must not
// construct.
func (r *Registry) Current() *Template {
	return r.current.Load()
}

// Reload rebuilds the template from globs and swaps it in atomically.
//
// The previous template stays live and untouched if the rebuild fails, so a
// caller can surface the error and keep delivering notifications. Renders
// already in flight continue against the old template — they hold their own
// pointer — which is exactly why the swap is safe.
func (r *Registry) Reload(globs []string) error {
	tmpl, err := FromGlobs(globs, r.opts)
	if err != nil {
		return err
	}
	r.current.Store(tmpl)
	return nil
}
