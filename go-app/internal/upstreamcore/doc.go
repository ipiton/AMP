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
// here, minus the UI and the gossip cluster (slice UPSTREAM-HA-MEMBERLIST).
//
// # Provenance and licence
//
// core.go and reloader.go are ports of
// github.com/prometheus/alertmanager@v0.34.1/app/{app.go,reloader.go,metrics.go,lifecycle.go}
// (lifecycle.go: the reload serialisation and the Stop ordering), and
// extURL in cmd/amp-upstream is a port of app/url.go. When bumping the
// upstream version, diff those files against the new release and carry the
// changes over. They are the work of The Prometheus Authors under the
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
// No gossip cluster (alertmanager_cluster_enabled stays 0), no UI, no event
// recorder (behind a feature flag upstream, off by default), no
// --silences.max-*, --alerts.per-alertname-limit, --dispatch.start-delay or
// --web.config.file; the HTTP server and signals belong to the caller.
package upstreamcore
