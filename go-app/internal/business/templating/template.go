// The Template type and its parse/execute methods in this file are a port of
// github.com/prometheus/alertmanager@v0.34.0/template/template.go:
//
//	Copyright 2015 Prometheus Team
//	Licensed under the Apache License, Version 2.0 (the "License");
//	you may not use this file except in compliance with the License.
//	You may obtain a copy of the License at
//
//	    http://www.apache.org/licenses/LICENSE-2.0
//
//	Unless required by applicable law or agreed to in writing, software
//	distributed under the License is distributed on an "AS IS" BASIS,
//	WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//	See the License for the specific language governing permissions and
//	limitations under the License.
//
// See doc.go for why the notice is repeated per file.

package templating

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	tmplhtml "html/template"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	tmpltext "text/template"
	"time"
)

// defaultAssets carries the verbatim upstream default template library. See
// templates/NOTICE for provenance and checksums.
//
//go:embed templates/default.tmpl templates/email.tmpl
var defaultAssets embed.FS

// defaultAssetNames is the load order of the embedded library. It matches
// upstream's own `defaultTemplates := []string{"default.tmpl", "email.tmpl"}`:
// email.tmpl's definitions reference `__subject` from default.tmpl, and later
// definitions win, so the order is part of the contract.
var defaultAssetNames = []string{"templates/default.tmpl", "templates/email.tmpl"}

// Execution guard defaults. Upstream Alertmanager has NEITHER a timeout nor an
// output cap — a template is trusted config. AMP adds both (a divergence in
// AMP's favour): the notify chain is a long-lived hot path shared by every
// receiver, and a pathological template (accidental deep nesting over a
// thousand-alert group, a `range` over a huge dict) must degrade to "this one
// notification falls back to the fixed formatter", never to a wedged notify
// chain or an OOM.
//
// The numbers are chosen to be unreachable for legitimate notifications: the
// largest real payload any AMP integration accepts is ~1 MiB (PagerDuty), and
// Telegram caps a message at 4096 characters, so 4 MiB leaves an order of
// magnitude of headroom before the cap can produce a false positive. 5s is
// likewise ~3 orders of magnitude above the microseconds a default template
// takes.
const (
	DefaultTimeout        = 5 * time.Second
	DefaultMaxOutputBytes = 4 << 20 // 4 MiB
)

// Sentinel errors from guarded execution. Both are reported with
// errors.Is-compatible wrapping so a caller (slice 2's formatter fallback) can
// distinguish "the operator's template is broken/hostile" from "the template
// rendered fine but the sender rejected it".
var (
	// ErrOutputTooLarge means rendering exceeded Options.MaxOutputBytes.
	ErrOutputTooLarge = errors.New("template output exceeded the maximum allowed size")

	// ErrTimeout means rendering exceeded Options.Timeout.
	ErrTimeout = errors.New("template execution exceeded the maximum allowed duration")

	// ErrNotDefined means the named template definition does not exist in this
	// Template (neither in the embedded default library nor in any file loaded
	// from `templates:`).
	ErrNotDefined = errors.New("template is not defined")
)

// Options configures a Template's execution guards.
//
// The zero value is valid and means "use the defaults" — see
// Options.withDefaults.
type Options struct {
	// Timeout bounds a single ExecuteTextString/ExecuteHTMLString call.
	// Enforced on every write the template performs (see guardWriter), so it
	// aborts a runaway that produces output. A template that spins without
	// writing anything cannot be interrupted by ANY mechanism short of
	// abandoning a goroutine, which this package deliberately does not do —
	// leaking a goroutine per bad render is a worse failure than the render
	// taking long. text/template has no unbounded construct that produces no
	// output (`range` over a finite value, no recursion without a write), so
	// this is a theoretical rather than a reachable gap.
	Timeout time.Duration

	// MaxOutputBytes bounds the rendered output of a single execution.
	MaxOutputBytes int
}

func (o Options) withDefaults() Options {
	if o.Timeout <= 0 {
		o.Timeout = DefaultTimeout
	}
	if o.MaxOutputBytes <= 0 {
		o.MaxOutputBytes = DefaultMaxOutputBytes
	}
	return o
}

// Template bundles a text and an html template instance, exactly as upstream
// does: the SAME source text is parsed into both, so `email.default.html`
// renders with html/template's contextual auto-escaping while every other
// definition renders as plain text.
//
// A Template is immutable once built and safe for concurrent execution.
// PARSING is not concurrent-safe (it mutates the two instances) — that is why
// reload builds a brand-new Template and swaps it atomically rather than
// re-parsing in place; see Registry.
type Template struct {
	text *tmpltext.Template
	html *tmplhtml.Template

	opts Options
}

// Option is a generic modifier of the text and html templates used by a
// Template (upstream's own extension point, e.g. to register extra funcs).
// DefaultFuncs is applied AFTER all Options, so it always wins.
type Option func(text *tmpltext.Template, html *tmplhtml.Template)

// New returns a new Template with the DefaultFuncs added. The DefaultFuncs
// have precedence over any added custom functions. Options allow customization
// of the text and html templates in given order.
//
// The returned Template has NO definitions: it can execute inline expressions
// but no `{{ template "..." }}` reference. Use FromGlobs for the default
// library plus the operator's own files.
func New(opts Options, options ...Option) (*Template, error) {
	t := &Template{
		text: tmpltext.New("").Option("missingkey=zero"),
		html: tmplhtml.New("").Option("missingkey=zero"),
		opts: opts.withDefaults(),
	}

	for _, o := range options {
		o(t.text, t.html)
	}

	t.text.Funcs(tmpltext.FuncMap(DefaultFuncs))
	t.html.Funcs(tmplhtml.FuncMap(DefaultFuncs))

	return t, nil
}

// FromGlobs builds a Template from the embedded upstream default library plus
// every file matched by the given path globs, in order.
//
// Override semantics are upstream's, and they follow from text/template itself:
// a later `{{ define "x" }}` REPLACES an earlier one, so
//
//   - the embedded defaults are parsed first and are therefore the base layer;
//   - user files are parsed in glob order, and within a glob in the order
//     filepath.Glob returns (sorted), so a user definition of
//     `slack.default.title` wins over the default one;
//   - two user files defining the same name resolve to the LAST parsed.
//
// A glob matching nothing is NOT an error — upstream allows it explicitly
// ("we want to allow empty matches that may be populated later on"), e.g. a
// `/etc/amp/templates/*.tmpl` glob on a deployment where the ConfigMap is
// mounted later.
//
// A malformed template IS an error, and the returned message names the file and
// line: text/template's own parse errors carry `<file>:<line>` and this wraps
// them with the glob that pulled the file in.
func FromGlobs(paths []string, opts Options, options ...Option) (*Template, error) {
	t, err := New(opts, options...)
	if err != nil {
		return nil, err
	}

	for _, name := range defaultAssetNames {
		f, err := defaultAssets.Open(name)
		if err != nil {
			// Unreachable: the files are embedded at compile time.
			return nil, fmt.Errorf("open embedded default template %s: %w", name, err)
		}
		err = t.Parse(f)
		_ = f.Close()
		if err != nil {
			return nil, fmt.Errorf("parse embedded default template %s: %w", name, err)
		}
	}

	for _, tp := range paths {
		if err := t.FromGlob(tp); err != nil {
			return nil, err
		}
	}
	return t, nil
}

// Parse parses the given text into the template (both the text and the html
// instance, as upstream does).
func (t *Template) Parse(r io.Reader) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	if t.text, err = t.text.Parse(string(b)); err != nil {
		return err
	}
	if t.html, err = t.html.Parse(string(b)); err != nil {
		return err
	}
	return nil
}

// FromGlob parses every file matched by path into the template.
//
// Unlike upstream (which delegates to ParseGlob), this reads and parses each
// matched file individually so a parse failure can name the offending FILE.
// ParseGlob's error only carries the template name of the file it happened to
// be reading, which for a glob spanning several directories is ambiguous —
// "parse errors must name file+line" is a hard requirement of this slice.
//
// Ordering is filepath.Glob's (lexical), made explicit with an extra sort so
// override precedence between two files in one glob is deterministic across
// platforms rather than dependent on Glob's guarantees.
func (t *Template) FromGlob(path string) error {
	// ParseGlob in the template packages errors if not at least one file is
	// matched. We want to allow empty matches that may be populated later on.
	matches, err := filepath.Glob(path)
	if err != nil {
		return fmt.Errorf("invalid template glob %q: %w", path, err)
	}
	sort.Strings(matches)

	for _, file := range matches {
		info, statErr := os.Stat(file)
		if statErr == nil && info.IsDir() {
			// A glob like `templates/*` can match a directory; upstream's
			// ParseGlob would fail reading it. Skipping is the friendlier and
			// equally safe behaviour.
			continue
		}
		if err := t.parseFile(path, file); err != nil {
			return err
		}
	}
	return nil
}

// parseFile parses one template file, wrapping any error with the glob that
// selected it and the file itself. text/template's parse errors already carry
// `<template name>:<line>: <detail>` and the template name IS the base file
// name, so the resulting message pins file and line together, e.g.
//
//	template glob "/etc/amp/tmpl/*.tmpl": file "/etc/amp/tmpl/slack.tmpl":
//	template: slack.tmpl:4: unexpected "}" in operand
func (t *Template) parseFile(glob, file string) error {
	f, err := os.Open(file) //nolint:gosec // path comes from the operator's own `templates:` glob, resolved by filepath.Glob.
	if err != nil {
		return fmt.Errorf("template glob %q: file %q: %w", glob, file, err)
	}
	defer func() { _ = f.Close() }()

	if err := t.Parse(f); err != nil {
		return fmt.Errorf("template glob %q: file %q: %w", glob, file, err)
	}
	return nil
}

// ExecuteTextString renders text (an arbitrary template expression, e.g. a
// per-integration `title:` field) against data using the text/template
// instance, so no HTML escaping is applied.
//
// An empty expression renders to the empty string without executing anything —
// upstream's own short-circuit, and the reason a config that leaves a
// presentation field unset costs nothing.
//
// Errors, never panics: a malformed expression, a function returning an error,
// a PANIC from a template function or a data method, the output cap and the
// timeout all come back as an error. That contract is what lets slice 2 fall
// back to AMP's fixed formatter instead of dropping the notification.
func (t *Template) ExecuteTextString(text string, data any) (string, error) {
	if text == "" {
		return "", nil
	}

	return t.guarded(func(w io.Writer) error {
		tmpl, err := t.text.Clone()
		if err != nil {
			return err
		}
		tmpl, err = tmpl.New("").Option("missingkey=zero").Parse(text)
		if err != nil {
			return err
		}
		return tmpl.Execute(w, data)
	})
}

// ExecuteHTMLString is ExecuteTextString for the html/template instance:
// contextual auto-escaping applies, which is what makes `email.default.html`
// safe to render with attacker-influenced label values in it.
func (t *Template) ExecuteHTMLString(html string, data any) (string, error) {
	if html == "" {
		return "", nil
	}

	return t.guarded(func(w io.Writer) error {
		tmpl, err := t.html.Clone()
		if err != nil {
			return err
		}
		tmpl, err = tmpl.New("").Option("missingkey=zero").Parse(html)
		if err != nil {
			return err
		}
		return tmpl.Execute(w, data)
	})
}

// ExecuteTextDefinition renders the named definition (e.g.
// "slack.default.title") with data as the dot.
//
// It is equivalent to ExecuteTextString(`{{ template "<name>" . }}`, data) —
// which is literally how upstream's integrations render their defaults — but
// reports a missing definition as ErrNotDefined instead of a generic
// text/template parse error, so a caller can distinguish "the operator removed
// this definition" from "the operator's template is broken".
func (t *Template) ExecuteTextDefinition(name string, data any) (string, error) {
	if !t.HasDefinition(name) {
		return "", fmt.Errorf("%w: %q", ErrNotDefined, name)
	}
	return t.ExecuteTextString(definitionRef(name), data)
}

// ExecuteHTMLDefinition is ExecuteTextDefinition against the html instance
// (used for `email.default.html`).
func (t *Template) ExecuteHTMLDefinition(name string, data any) (string, error) {
	if !t.HasDefinition(name) {
		return "", fmt.Errorf("%w: %q", ErrNotDefined, name)
	}
	return t.ExecuteHTMLString(definitionRef(name), data)
}

// definitionRef builds the `{{ template "name" . }}` expression upstream's
// integration defaults use. %q is deliberate: it quotes and escapes the name,
// so a name containing a quote cannot break out of the expression.
func definitionRef(name string) string {
	return fmt.Sprintf("{{ template %q . }}", name)
}

// HasDefinition reports whether a definition of that name exists.
func (t *Template) HasDefinition(name string) bool {
	return t.text.Lookup(name) != nil
}

// DefinitionNames lists every defined template name, sorted. Intended for
// diagnostics ("what did my `templates:` globs actually load?") and tests.
// The unnamed root template that Parse creates is excluded.
func (t *Template) DefinitionNames() []string {
	templates := t.text.Templates()
	names := make([]string, 0, len(templates))
	for _, tmpl := range templates {
		if tmpl.Name() == "" || tmpl.Tree == nil {
			continue
		}
		names = append(names, tmpl.Name())
	}
	sort.Strings(names)
	return names
}

// guarded runs exec with a writer that enforces Options.MaxOutputBytes and
// Options.Timeout, and converts a panic into an error.
//
// Why a guard WRITER rather than a goroutine plus context: text/template offers
// no cancellation, so the only ways to bound an execution are (a) run it in a
// goroutine and abandon it on timeout, or (b) fail the next write. (a) leaks a
// goroutine — and whatever it references — for every bad render, turning a
// transient config mistake into a slow resource leak in a process that renders
// on every notification. (b) has no leak, no extra scheduling, and terminates
// any runaway that produces output, which is every runaway text/template can
// actually express. The residual gap is documented on Options.Timeout.
//
// On error the partial output is discarded: half a rendered notification is
// worse than none, because none is what triggers the fixed-formatter fallback.
func (t *Template) guarded(exec func(io.Writer) error) (result string, err error) {
	w := &guardWriter{
		max:      t.opts.MaxOutputBytes,
		deadline: time.Now().Add(t.opts.Timeout),
		timeout:  t.opts.Timeout,
	}

	defer func() {
		if r := recover(); r != nil {
			// A panic here is a template function or a data method blowing up
			// (text/template recovers its OWN panics and returns them as
			// errors, but re-panics anything else). Never let it reach the
			// notify chain: the caller's contract is "errors, not panics".
			result = ""
			err = fmt.Errorf("template execution panicked: %v\n%s", r, debug.Stack())
		}
	}()

	if execErr := exec(w); execErr != nil {
		return "", execErr
	}
	return w.buf.String(), nil
}

// guardWriter caps total bytes written and enforces a wall-clock deadline,
// failing the write that crosses either limit. text/template surfaces a writer
// error by aborting the execution and returning that error unwrapped, so
// errors.Is(err, ErrOutputTooLarge) / errors.Is(err, ErrTimeout) work on what
// guarded returns.
type guardWriter struct {
	buf      bytes.Buffer
	max      int
	deadline time.Time
	timeout  time.Duration
}

func (w *guardWriter) Write(p []byte) (int, error) {
	if time.Now().After(w.deadline) {
		return 0, fmt.Errorf("%w (%s)", ErrTimeout, w.timeout)
	}
	if w.buf.Len()+len(p) > w.max {
		return 0, fmt.Errorf("%w (%d bytes)", ErrOutputTooLarge, w.max)
	}
	return w.buf.Write(p)
}
