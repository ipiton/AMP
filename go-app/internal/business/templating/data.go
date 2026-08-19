// The types and functions in this file are a port of the notification
// template data model from
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
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/prometheus/common/model"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/core/alertconv"
)

// ============================================================================
// Ported / skipped ledger (upstream template.go -> this package)
// ============================================================================
//
// PORTED VERBATIM (semantics, not just names):
//   Pair, Pairs, Pairs.Names/Values/String, Strings, Strings.Join,
//   KV, KV.SortedPairs/Remove/Names/Values/String,
//   Data (minus the two fields below), Alert, Alerts,
//   Alerts.Firing/Resolved, and the CommonLabels/CommonAnnotations
//   intersection performed by (*Template).Data — here BuildData.
//
// SKIPPED, with reasons:
//   Data.RouteLabels + routeLabelResolver + Template.RouteLabelRenderer +
//     MarkRouteLabelsRendered — upstream's route-label templating feature.
//     AMP's route config (internal/infrastructure/routing) has no route
//     labels at all, so there is nothing to populate the field from and a
//     resolver would only ever resolve the empty set. The `routeLabels`
//     template FUNCTION is kept (see funcs.go) as upstream's own no-op
//     placeholder so that a user template referencing it still parses instead
//     of failing the whole file load.
//   Data.NotificationReason — set by upstream's dispatcher from its notify
//     pipeline; AMP's notify chain has no equivalent value to supply, and
//     emitting a permanently-empty field would be a parity lie rather than
//     parity.
//   DeepCopyWithTemplate + normalizeYAMLValue — used by upstream only to
//     template arbitrary webhook/JSON payload trees. AMP's webhook v4 payload
//     is struct-marshaled and explicitly NOT templated (wave-2 batch
//     marshaling stays untouched), so nothing would call them.

// Pair is a key/value string pair.
type Pair struct {
	Name, Value string
}

// Pairs is a list of key/value string pairs.
type Pairs []Pair

// Names returns a list of names of the pairs.
func (ps Pairs) Names() Strings {
	ns := make([]string, 0, len(ps))
	for _, p := range ps {
		ns = append(ns, p.Name)
	}
	return Strings(ns)
}

// Values returns a list of values of the pairs.
func (ps Pairs) Values() Strings {
	vs := make([]string, 0, len(ps))
	for _, p := range ps {
		vs = append(vs, p.Value)
	}
	return Strings(vs)
}

func (ps Pairs) String() string {
	b := strings.Builder{}
	for i, p := range ps {
		b.WriteString(p.Name)
		b.WriteRune('=')
		b.WriteString(p.Value)
		if i < len(ps)-1 {
			b.WriteString(", ")
		}
	}
	return b.String()
}

// Strings is a list of strings exposed to templates.
type Strings []string

// Join makes strings.Join accessible from templates, e.g.
// {{ .GroupLabels.Values.Join ":" }}.
func (s Strings) Join(sep string) string {
	return strings.Join(s, sep)
}

// KV is a set of key/value string pairs.
type KV map[string]string

// SortedPairs returns a sorted list of key/value pairs.
//
// "alertname" is pinned FIRST and the remainder sorted lexically — that
// ordering is load-bearing for output parity: `__subject` renders
// `{{ .GroupLabels.SortedPairs.Values | join " " }}`, so a plain sort would
// silently reorder every notification title relative to upstream.
func (kv KV) SortedPairs() Pairs {
	var (
		pairs     = make([]Pair, 0, len(kv))
		keys      = make([]string, 0, len(kv))
		sortStart = 0
	)
	for k := range kv {
		if k == string(model.AlertNameLabel) {
			keys = append([]string{k}, keys...)
			sortStart = 1
		} else {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys[sortStart:])

	for _, k := range keys {
		pairs = append(pairs, Pair{k, kv[k]})
	}
	return pairs
}

// Remove returns a copy of the key/value set without the given keys.
func (kv KV) Remove(keys []string) KV {
	keySet := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		keySet[k] = struct{}{}
	}

	res := KV{}
	for k, v := range kv {
		if _, ok := keySet[k]; !ok {
			res[k] = v
		}
	}
	return res
}

// Names returns the names of the label names in the LabelSet.
func (kv KV) Names() Strings {
	return kv.SortedPairs().Names()
}

// Values returns a list of the values in the LabelSet.
func (kv KV) Values() Strings {
	return kv.SortedPairs().Values()
}

func (kv KV) String() string {
	return kv.SortedPairs().String()
}

// Data is the data passed to notification templates.
//
// End-users should not be exposed to Go's type system, as this will confuse
// them and prevent simple things like simple equality checks to fail. Map
// everything to float64/string. (Upstream's own rationale, kept verbatim: it
// is why every field below is a string or a string map, never a typed label.)
//
// JSON tags match upstream's exactly. Nothing here marshals into AMP's
// webhook payload today (that stays struct-marshaled — see doc.go), but the
// tags are part of the ported contract: a user template can reach these via
// `toJson`, and slice 2 must not have to rename anything.
type Data struct {
	Receiver string `json:"receiver"`
	Status   string `json:"status"`
	Alerts   Alerts `json:"alerts"`

	GroupLabels       KV `json:"groupLabels"`
	CommonLabels      KV `json:"commonLabels"`
	CommonAnnotations KV `json:"commonAnnotations"`

	ExternalURL string `json:"externalURL"`
}

// Alert holds one alert for notification templates.
type Alert struct {
	Status       string    `json:"status"`
	Labels       KV        `json:"labels"`
	Annotations  KV        `json:"annotations"`
	StartsAt     time.Time `json:"startsAt"`
	EndsAt       time.Time `json:"endsAt"`
	GeneratorURL string    `json:"generatorURL"`
	Fingerprint  string    `json:"fingerprint"`
}

// Alerts is a list of Alert objects.
type Alerts []Alert

// Firing returns the subset of alerts that are firing.
func (as Alerts) Firing() []Alert {
	res := []Alert{}
	for _, a := range as {
		if a.Status == string(model.AlertFiring) {
			res = append(res, a)
		}
	}
	return res
}

// Resolved returns the subset of alerts that are resolved.
func (as Alerts) Resolved() []Alert {
	res := []Alert{}
	for _, a := range as {
		if a.Status == string(model.AlertResolved) {
			res = append(res, a)
		}
	}
	return res
}

// DataInput is everything BuildData needs from AMP's notify chain.
//
// It exists instead of a `(*Template).Data(group *grouping.AlertGroup, ...)`
// method so that this package never imports internal/infrastructure/grouping
// or internal/infrastructure/publishing — see doc.go, "Layering". Callers pass
// plain values they already have: grouping.groupLabelsFor's map, the
// publishing target's receiver name, and the alert slice the notify chain
// already filtered (inhibited/silenced alerts removed).
type DataInput struct {
	// Receiver is the receiver name as CONFIGURED. Callers holding a
	// publishing target name (`cfg:<receiver>/<kind><idx>`) should pass it
	// through ReceiverNameFromTarget first.
	Receiver string

	// GroupLabels are the group's group_by labels resolved to values —
	// exactly what grouping.groupLabelsFor returns. nil is fine (a route
	// without group_by groups everything, and upstream renders an empty
	// GroupLabels for it).
	GroupLabels map[string]string

	// Alerts is the group's alert set, in notification order. Empty/nil
	// yields a Data with Status "resolved" and empty Common* maps (upstream's
	// behaviour for a zero-alert notification, which should not happen but
	// must not panic).
	Alerts []*core.Alert

	// ExternalURL is the browser-facing base URL of this AMP instance,
	// rendered by `__alertmanagerURL` into every default template's link.
	// Slice 2 sources it from --web.external-url; an empty value renders
	// upstream's own "no external URL configured" shape (a relative
	// "/#/alerts?receiver=...").
	ExternalURL string
}

// BuildData assembles the template Data for one notification — the port of
// upstream's (*Template).Data.
//
// Faithfulness notes, each verified against upstream output by golden_test.go:
//
//   - Receiver goes through regexp.QuoteMeta, exactly as upstream does. This
//     is an upstream QUIRK, not a nicety: a receiver named `team.a` renders as
//     `team\.a` in every title and as `team%5C.a` in the alertmanager URL.
//     Reproducing it is the whole point of a parity port — a config moved from
//     Alertmanager to AMP must produce byte-identical notifications, warts
//     included. AMP's own `cfg:` target-name prefix is NOT part of this: strip
//     it with ReceiverNameFromTarget before calling, so what reaches templates
//     is the name the user wrote in `receivers:`.
//   - Status is the GROUP status: "firing" if any alert fires, else
//     "resolved" (upstream computes it via model.Alerts.Status()).
//   - EndsAt is exposed ONLY for resolved alerts; for a firing alert upstream
//     zeroes it ("if the end timestamp is not reached yet, do not expose it",
//     alert.Alerts()) — so a firing alert always renders the zero time.
//   - Fingerprint is the upstream 16-hex FNV-1a form via
//     alertconv.UpstreamFingerprint, NOT core.Alert.Fingerprint (AMP's
//     internal 64-hex SHA-256 dedup key). Templates and downstream consumers
//     compare against upstream's format.
//   - CommonLabels/CommonAnnotations are the INTERSECTION across the alert
//     set: seeded from the first alert, then any key whose value differs on a
//     later alert is dropped. A key missing from a later alert also drops
//     (upstream compares against the zero value of a missing label).
func BuildData(in DataInput) *Data {
	data := &Data{
		Receiver:          regexp.QuoteMeta(in.Receiver),
		Status:            string(model.AlertResolved),
		Alerts:            make(Alerts, 0, len(in.Alerts)),
		GroupLabels:       KV{},
		CommonLabels:      KV{},
		CommonAnnotations: KV{},
		ExternalURL:       in.ExternalURL,
	}

	for _, a := range in.Alerts {
		if a == nil {
			continue
		}
		if a.Status == core.StatusFiring {
			data.Status = string(model.AlertFiring)
		}
		data.Alerts = append(data.Alerts, buildAlert(a))
	}

	for k, v := range in.GroupLabels {
		data.GroupLabels[k] = v
	}

	commonLabels, commonAnnotations := commonPairs(in.Alerts)
	for k, v := range commonLabels {
		data.CommonLabels[k] = v
	}
	for k, v := range commonAnnotations {
		data.CommonAnnotations[k] = v
	}

	return data
}

// buildAlert maps one core.Alert onto the template Alert shape.
func buildAlert(a *core.Alert) Alert {
	status := string(model.AlertFiring)
	if a.Status != core.StatusFiring {
		status = string(model.AlertResolved)
	}

	alert := Alert{
		Status:      status,
		Labels:      make(KV, len(a.Labels)),
		Annotations: make(KV, len(a.Annotations)),
		StartsAt:    a.StartsAt,
		Fingerprint: alertconv.UpstreamFingerprint(a.Labels),
	}
	// Upstream exposes EndsAt only once the alert has actually resolved.
	if status == string(model.AlertResolved) && a.EndsAt != nil {
		alert.EndsAt = *a.EndsAt
	}
	if a.GeneratorURL != nil {
		alert.GeneratorURL = *a.GeneratorURL
	}
	for k, v := range a.Labels {
		alert.Labels[k] = v
	}
	for k, v := range a.Annotations {
		alert.Annotations[k] = v
	}
	return alert
}

// commonPairs computes the label/annotation intersection across alerts,
// mirroring upstream's loop (including its early exit once both sets are
// empty). Returns nil maps for an empty alert set.
func commonPairs(alerts []*core.Alert) (labels, annotations map[string]string) {
	first := -1
	for i, a := range alerts {
		if a != nil {
			first = i
			break
		}
	}
	if first < 0 {
		return nil, nil
	}

	labels = alertconv.CloneStringMap(alerts[first].Labels)
	annotations = alertconv.CloneStringMap(alerts[first].Annotations)

	for _, a := range alerts[first+1:] {
		if a == nil {
			continue
		}
		if len(labels) == 0 && len(annotations) == 0 {
			break
		}
		for ln, lv := range labels {
			if a.Labels[ln] != lv {
				delete(labels, ln)
			}
		}
		for an, av := range annotations {
			if a.Annotations[an] != av {
				delete(annotations, an)
			}
		}
	}
	return labels, annotations
}

// configTargetPrefix mirrors business/publishing.ConfigTargetPrefix.
//
// Duplicated as a literal ON PURPOSE rather than imported: slice 2 has the
// publishing formatters importing THIS package, and importing
// business/publishing back would close a cycle. The constant is a stable
// wire-visible naming convention (`cfg:<receiver>/<kind><idx>`), and
// TestReceiverNameFromTarget_MatchesPublishingPrefix pins the two together so
// a rename cannot drift silently.
const configTargetPrefix = "cfg:"

// ReceiverNameFromTarget recovers the CONFIGURED receiver name from an AMP
// publishing target name.
//
// AMP names config-provisioned targets `cfg:<receiver>/<kind><idx>` (see
// business/publishing.ConfigTargetName) so they cannot collide with
// Secret-provisioned, DNS-1123 target names. That encoding is an AMP internal
// detail: upstream templates render the receiver name the user configured, and
// `{{ .Receiver }}` leaking `cfg:team-x/slack0` into a Slack title (or into
// the alertmanager URL's `?receiver=` query) is exactly the mangling this
// strips.
//
// A name without the prefix is returned unchanged — Kubernetes-sourced targets
// and plain receiver names both pass through untouched.
func ReceiverNameFromTarget(target string) string {
	if !strings.HasPrefix(target, configTargetPrefix) {
		return target
	}
	name := strings.TrimPrefix(target, configTargetPrefix)
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		name = name[:idx]
	}
	return name
}
