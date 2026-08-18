package config

import (
	"crypto/sha256"
	"encoding/hex"

	infraroute "github.com/ipiton/AMP/internal/infrastructure/routing"
	"gopkg.in/yaml.v3"
)

// routingFingerprintNone is the fingerprint reported for a Config that carries
// no `route:` tree at all (legacy single-receiver config). It is a fixed
// sentinel rather than the empty string so that "gained a routing tree" and
// "lost a routing tree" both show up as a MODIFIED field in the config diff
// instead of an added/deleted one.
const routingFingerprintNone = "none"

// RoutingFingerprint returns a stable content hash of the Alertmanager-shaped
// routing section of a config: `route:`, `receivers:`, `inhibit_rules:`,
// `time_intervals:` (+ the deprecated `mute_time_intervals:` alias) and
// `global:`.
//
// WHY THIS EXISTS (final review finding 1 — silent notification loss):
// Config.Routing is tagged `json:"-" mapstructure:"-"` because RouteConfig
// carries fields encoding/json cannot marshal (compiled regexes keyed by
// *grouping.Route). DefaultConfigComparator diffs configs by round-tripping
// them through encoding/json (configToMap), so the entire routing section was
// invisible to the comparator: a config edit touching ONLY route:/receivers:/
// time_intervals: produced an empty diff, ReloadCoordinator.ReloadFromFile
// early-returned `Success: true` BEFORE the atomic currentConfig.Store, and
// ServiceRegistry.ReloadConfig then read back the OLD config pointer and
// rebuilt the routing tree from unchanged data. `POST /-/reload` reported
// 200 OK while silently discarding the change.
//
// Injecting this fingerprint into the diffed representation (see
// configToMap) makes any routing change surface as a modified
// `routing.fingerprint` field, which is enough to (a) get past the "no
// changes" short-circuit, (b) reach atomicApply/currentConfig.Store, and
// (c) be classified into the "routing" affected component by
// identifyAffectedComponents (it prefix-matches "route").
//
// Determinism: gopkg.in/yaml.v3 sorts map keys when encoding Go maps, so
// repeated marshals of the same tree produce byte-identical output (asserted
// by TestRoutingFingerprint_StableAcrossMarshals). The internal index/metadata
// fields of RouteConfig (ReceiverIndex, TimeIntervalIndex, CompiledRegex,
// Version, LoadedAt, SourceFile) are all tagged `yaml:"-"` and therefore do
// NOT contribute — which is exactly right: LoadedAt alone would otherwise make
// every reload look like a change.
func RoutingFingerprint(rc *infraroute.RouteConfig) string {
	if rc == nil {
		return routingFingerprintNone
	}

	data, err := yaml.Marshal(rc)
	if err != nil {
		// Marshal of a parsed RouteConfig should not fail; if it somehow
		// does, fall back to a value that differs from every real
		// fingerprint AND from routingFingerprintNone, so the reload errs
		// on the side of "something changed" rather than silently skipping.
		return "unmarshalable"
	}

	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
