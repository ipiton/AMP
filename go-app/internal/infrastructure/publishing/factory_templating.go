package publishing

import (
	"sync/atomic"

	"github.com/ipiton/AMP/internal/business/templating"
	"github.com/ipiton/AMP/internal/core"
)

// ============================================================================
// Template wiring for PublisherFactory (TEMPLATES-EPIC slice 2)
// ============================================================================
//
// Lives in its own file, and injects through a SETTER rather than through
// NewPublisherFactory's signature, for two reasons:
//
//  1. Nothing about the factory's construction changes, so a deployment that
//     never calls SetTemplateRegistry behaves exactly as before — the setter is
//     the on-switch for the whole feature.
//  2. publisher.go is being edited concurrently on another track (HTTP client
//     construction). Keeping this code out of that file limits the merge to the
//     one-line `f.formatter` -> `f.formatterFor(target)` substitutions.
//
// The registry is stored as an atomic pointer because SetTemplateRegistry is
// called from config reload while CreatePublisherForTarget runs on publish
// paths. The REGISTRY itself already handles template reloads internally (it
// swaps the parsed template atomically), so in practice the pointer is set once
// at startup — but "in practice" is not a concurrency argument.

// templateWiring holds the factory's template state. Embedded via a package-level
// map-free struct field added to PublisherFactory in publisher.go would have
// meant editing that file's struct; instead the state hangs off the factory
// through this small side struct, set atomically.
type templateWiring struct {
	registry *templating.Registry
	renderer *templateRenderer
}

// templateWiringStore is the factory's template state, keyed by nothing —
// there is exactly one wiring per factory, held in the factory's own atomic
// field below.
type templateWiringStore struct {
	value atomic.Pointer[templateWiring]
}

// SetTemplateRegistry enables template-rendered notification content for every
// publisher this factory creates from now on.
//
// registry == nil disables it again (used by tests, and by a deployment where
// template loading failed hard enough that ServiceRegistry chose not to wire
// it). Publishers already created keep the formatter they were built with —
// they are cheap, per-publish objects created by
// CreatePublisherForTarget/queue.publishJob, so the next notification picks up
// the change.
func (f *PublisherFactory) SetTemplateRegistry(registry *templating.Registry) {
	if registry == nil {
		f.templates.value.Store(nil)
		return
	}

	f.templates.value.Store(&templateWiring{
		registry: registry,
		renderer: newTemplateRenderer(registry, f.externalURL, f.metrics, f.logger),
	})
}

// TemplateRegistry returns the wired registry, or nil. Used by the email
// publisher path and by tests.
func (f *PublisherFactory) TemplateRegistry() *templating.Registry {
	if wiring := f.templates.value.Load(); wiring != nil {
		return wiring.registry
	}
	return nil
}

// formatterFor returns the formatter a publisher for target should use: the
// shared fixed formatter, wrapped in a per-target template decorator when
// templating is wired AND the target actually carries template fields.
//
// Returns the bare shared formatter otherwise — see newTemplateFormatter for
// why "absent" rather than "inert" matters for template-less deployments.
func (f *PublisherFactory) formatterFor(target *core.PublishingTarget) AlertFormatter {
	wiring := f.templates.value.Load()
	if wiring == nil {
		return f.formatter
	}
	return newTemplateFormatter(f.formatter, wiring.renderer, target)
}
