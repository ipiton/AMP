// The function map in this file is a port of DefaultFuncs from
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
	"encoding/base64"
	"encoding/json"
	"fmt"
	tmplhtml "html/template"
	"net/url"
	"regexp"
	"strings"
	"time"

	commonTemplates "github.com/prometheus/common/helpers/templates"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// FuncMap is the template function map type (upstream's own alias).
type FuncMap map[string]any

// DefaultFuncs is upstream's DefaultFuncs, ported entry for entry.
//
// Ported / skipped ledger — every name upstream defines is accounted for, and
// NONE is skipped:
//
//	toUpper toLower title trimSpace join match safeHtml safeUrl urlUnescape
//	reReplaceAll stringSlice routeLabels date tz now since humanizeDuration
//	toDate mustToDate toJson base64encode base64decode list append dict
//
// `title` and `humanizeDuration` are the two entries upstream implements via
// other modules rather than the standard library. Both are reused here rather
// than reimplemented, so their output is byte-identical to upstream's:
//
//   - title -> golang.org/x/text/cases.Title(language.AmericanEnglish)
//   - humanizeDuration -> prometheus/common/helpers/templates.HumanizeDuration
//
// Neither is a NEW external dependency: prometheus/common is already a direct
// dependency of this module, and golang.org/x/text is already in the build
// graph and go.sum as an indirect one (this import only promotes it to the
// direct require block — no new module, no new go.sum entry, no new
// supply-chain surface). Hand-rolled substitutes were written first and thrown
// away: both diverged from upstream on real input (x/text's Unicode
// special-casing; prometheus/common's sub-second unit thresholds), and a
// silently-divergent function is precisely what a parity port must not ship.
//
// Two entries that look odd, and why they are correct:
//
//   - routeLabels is upstream's own no-op placeholder. Upstream swaps in a real
//     resolver per execution; AMP does not port the route-label feature (see
//     data.go's ledger), so the placeholder is all there is. It stays in the
//     map so that a template referencing routeLabels still PARSES — without the
//     name registered, one such line fails the whole `templates:` file load and
//     takes every other definition in that file down with it.
//   - safeHtml/safeUrl return html/template types. They are meaningful only in
//     the HTML template variant (email.default.html); in the text variant they
//     degrade to rendering the value — exactly upstream's behaviour.
//
// DefaultFuncs is registered LAST in New, so it takes precedence over any
// caller-supplied Option funcs — again upstream's ordering.
var DefaultFuncs = FuncMap{
	"toUpper": strings.ToUpper,
	"toLower": strings.ToLower,
	"title": func(text string) string {
		// Casers should not be shared between goroutines, instead
		// create a new caser each time this function is called.
		return cases.Title(language.AmericanEnglish).String(text)
	},
	"trimSpace": strings.TrimSpace,
	// join is equal to strings.Join but inverts the argument order
	// for easier pipelining in templates.
	"join": func(sep string, s []string) string {
		return strings.Join(s, sep)
	},
	"match": regexp.MatchString,
	"safeHtml": func(text string) tmplhtml.HTML {
		return tmplhtml.HTML(text) //nolint:gosec // deliberate: upstream's escape hatch for pre-rendered HTML in email templates.
	},
	"safeUrl": func(text string) tmplhtml.URL {
		return tmplhtml.URL(text) //nolint:gosec // deliberate: see safeHtml.
	},
	"urlUnescape": url.QueryUnescape,
	"reReplaceAll": func(pattern, repl, text string) string {
		// MustCompile matches upstream. A bad pattern therefore panics — and
		// execText/execHTML's recover turns that panic into an error instead of
		// letting it take the notify chain down (AMP divergence, template.go).
		re := regexp.MustCompile(pattern)
		return re.ReplaceAllString(text, repl)
	},
	"stringSlice": func(s ...string) []string {
		return s
	},
	// routeLabels is a placeholder needed so templates referencing it parse
	// successfully. Upstream replaces it dynamically per execution; AMP does
	// not port the route-label feature, so it always renders empty.
	"routeLabels": func(_ string) (string, error) {
		return "", nil
	},
	// date returns the text representation of the time in the specified format.
	"date": func(layout string, t time.Time) string {
		return t.Format(layout)
	},
	// tz returns the time in the timezone.
	"tz": func(name string, t time.Time) (time.Time, error) {
		loc, err := time.LoadLocation(name)
		if err != nil {
			return time.Time{}, err
		}
		return t.In(loc), nil
	},
	"now":              time.Now,
	"since":            time.Since,
	"humanizeDuration": commonTemplates.HumanizeDuration,
	// toDate parses s into a time.Time using the given layout, returning zero time on failure.
	"toDate": func(layout, s string) time.Time {
		t, _ := time.ParseInLocation(layout, s, time.UTC)
		return t
	},
	// mustToDate parses s into a time.Time using the given layout, returning an error on failure.
	"mustToDate": func(layout, s string) (time.Time, error) {
		return time.ParseInLocation(layout, s, time.UTC)
	},
	"toJson": func(v any) (string, error) {
		b, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		return string(b), nil
	},
	// base64encode and base64decode use the URL-safe alphabet so the result
	// can be embedded directly in a URL query parameter, e.g. to build a
	// silence link for an external dashboard.
	"base64encode": func(text string) string {
		return base64.URLEncoding.EncodeToString([]byte(text))
	},
	"base64decode": func(text string) (string, error) {
		decoded, err := base64.URLEncoding.DecodeString(text)
		if err != nil {
			return "", err
		}
		return string(decoded), nil
	},
	"list": func(args ...any) ([]any, error) {
		if args == nil {
			return []any{}, nil
		}
		return args, nil
	},
	"append": func(slice []any, args ...any) []any {
		return append(slice, args...)
	},
	"dict": func(values ...any) (map[string]any, error) {
		if len(values)%2 != 0 {
			return nil, fmt.Errorf("dict requires an even number of arguments")
		}

		res := make(map[string]any, len(values)/2)
		for i := 0; i < len(values); i += 2 {
			key, ok := values[i].(string)
			if !ok {
				return nil, fmt.Errorf("dict keys must be strings")
			}
			res[key] = values[i+1]
		}

		return res, nil
	},
}
