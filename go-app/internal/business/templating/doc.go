// Package templating is AMP's port of upstream Alertmanager's notification
// template engine: the `template.Data` model handed to every notification
// template, the `DefaultFuncs` function map, the default template library
// (`slack.default.title`, `pagerduty.default.description`,
// `email.default.subject`, `telegram.default.message`, ...) and `templates:`
// glob loading with later-definition-wins override semantics.
//
// # Provenance and licence
//
// data.go, funcs.go and template.go are ports of
// github.com/prometheus/alertmanager@v0.34.0/template/template.go; the two
// files under templates/ are VERBATIM copies of that release's default.tmpl
// and email.tmpl (see templates/NOTICE for the checksums). All of it is the
// work of the Prometheus Team under the Apache License, Version 2.0:
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
// The per-file/per-symbol notices repeat this deliberately — Apache-2.0
// section 4(b)/4(d) asks the notice to be retained in derivative works, and
// the precedent in this repo is the ParseMatcher port in
// internal/business/routing/tree_builder.go.
//
// # Why this exists
//
// Upstream renders EVERY notification through text/template with this data
// model, and users override presentation with `templates:` globs plus
// per-integration fields (`title`, `text`, `message`, ...) that contain
// `{{ template "..." . }}` expressions. AMP historically rendered through
// fixed per-integration formatters (internal/infrastructure/publishing), so a
// config carrying custom templates lost its formatting silently. This package
// is the engine half of closing that gap; the wiring half (per-integration
// template fields -> formatter output -> the wire) is the next slice, which is
// why nothing here is called from the publishing path yet.
//
// # Layering
//
// The package deliberately depends on nothing but the standard library,
// prometheus/common/model (already a direct dependency, and the authority for
// "alertname"/"firing"/"resolved" and the 16-hex fingerprint format) and
// internal/core. In particular it does NOT import internal/infrastructure/
// grouping or internal/infrastructure/publishing: the publishing formatters
// will import THIS package in slice 2, and callers pass the group's labels in
// as a plain map (grouping.groupLabelsFor already computes exactly that), so
// there is no import cycle and no coupling to either package's types.
//
// # Divergences from upstream (all deliberate, all in AMP's favour)
//
//   - Execution is bounded: Options.Timeout and Options.MaxOutputBytes abort a
//     runaway template. Upstream has neither. See Template.ExecuteTextString.
//   - Execution never panics: a panic from a template function or a data
//     method is recovered and returned as an error, so the notify chain can
//     fall back to AMP's fixed formatter instead of taking the process down.
//   - Data.RouteLabels and the `routeLabels` data plumbing are NOT ported
//     (AMP has no route-label config); the `routeLabels` FUNCTION is present as
//     upstream's no-op placeholder so a config referencing it still parses.
//     Data.NotificationReason is not ported either — AMP has no source for it.
//     See data.go for the full ported-vs-skipped ledger.
package templating
