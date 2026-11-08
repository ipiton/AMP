// Package upstreamcore wires the upstream Alertmanager delivery core —
// config, provider/mem, silence, nflog, inhibit, dispatch, notify and the
// api/v2 handlers — from its packages into an AMP binary (ADR-019, slice
// UPSTREAM-CORE-POC-WIRING).
//
// # Why a port and not app.New
//
// Upstream v0.34 already moved this wiring into its app package, but that
// package cannot be built from the Go module: it imports ui, whose
// `//go:embed app/dist` points at the built frontend, which the module zip
// does not ship. app.Options is also closed for injection — no
// AlertStoreCallback, no extra notify stages, no extra routes — which are
// exactly the seams the later ADR-019 slices need. So the construction lives
// here, minus the UI.
//
// # Provenance and licence
//
// core.go and reloader.go are ports of
// github.com/prometheus/alertmanager@v0.34.1/app/{app.go,reloader.go,cluster.go,metrics.go,lifecycle.go}
// (lifecycle.go: the reload serialisation and the Stop ordering), and
// extURL in cmd/amp-upstream is a port of app/url.go. When bumping the
// upstream version, diff those files against the new release and carry the
// changes over (scripts/upstream-bump.sh, docs/UPSTREAM_UPGRADE.md) and
// update PortedFrom. They are the work of The Prometheus Authors under the
// Apache License, Version 2.0:
//
//	Copyright The Prometheus Authors
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
// The per-file notices repeat this deliberately, following
// internal/business/templating/doc.go. Upstream's NOTICE file, retained per
// Apache-2.0 section 4(d) (its Bootstrap entry concerns the UI, which is not
// ported):
//
//	Prometheus Alertmanager
//	Copyright 2013-2015 The Prometheus Authors
//
//	This product includes software developed at
//	SoundCloud Ltd. (http://soundcloud.com/).
//
// # Known differences from upstream
//
// No UI, no event recorder (behind a feature flag upstream, off by default;
// so no SetClusterPeer either), no --silences.max-*,
// --alerts.per-alertname-limit, --dispatch.start-delay or --web.config.file;
// the HTTP server and signals belong to the caller. Stop waits for a reload
// in progress without a deadline, where upstream abandons it after its
// shutdown timeout, and logs a failed gossip Leave instead of returning it.
//
// The gossip cluster (cluster.Peer, slice UPSTREAM-HA-MEMBERLIST) is wired
// as upstream wires it: silences and the notification log are gossiped,
// notifications wait for the cluster to settle and for one PeerTimeout per
// peer ahead in position. An empty Options.ClusterBindAddr keeps it off, as
// upstream's app.DefaultOptions does; the amp-upstream binary defaults the
// flag to 0.0.0.0:9094, as upstream's binary does. Options are not
// validated as upstream's app.Options.validate: a zero duration falls back
// to its default (SettleTimeout excepted: zero settles at once, as
// upstream); the binary rejects non-positive --cluster.* durations itself.
//
// # AMP extensions
//
// Optional seams that upstream's app.Options lacks; unset, the port behaves
// as upstream. ADR-019 slice UPSTREAM-AI-CLASSIFY: Options.NewAlertCallback
// composes an alert-store callback after the silencer (chainCallback in
// core.go) and hands it a mutes function — the silencer, then the active
// inhibitor (reloader.inhibits; slice UPSTREAM-AI-HISTORY-INVESTIGATION),
// which is why r is declared ahead of the memory provider in New — and
// Options.NotifyStage is prepended to every receiver's pipeline on each
// reload (reloader.go). Slice UPSTREAM-NON-UPSTREAM-RECEIVERS:
// Options.ExtendConfig replaces the configuration at the top of every apply
// (reloader.go), which is how K8s-Secret receivers join the file's. A bump
// must keep all of them in place; upstream-bump.sh diffs only the upstream
// files.
package upstreamcore

// PortedFrom is the upstream release the port above was taken from.
// TestPortMatchesUpstreamVersion fails while it differs from the
// github.com/prometheus/alertmanager version in go.mod, so a bump cannot
// leave the port behind silently.
const PortedFrom = "v0.34.1"
