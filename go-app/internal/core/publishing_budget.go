package core

import "time"

// MaxDeliveryConfirmationTimeout is the largest value
// publishing.queue.delivery_confirmation_timeout may be set to (task rec fix
// round 2, review finding R9).
//
// Lives here rather than in infrastructure/publishing so internal/config can
// enforce this ceiling without importing an infrastructure package for one
// constant (wave-3 review finding M-f: importing infrastructure/publishing
// from config coupled the config package to an infra package and
// pre-committed that publishing can never import config). core is a leaf
// domain package already depended on by both config and
// infrastructure/publishing, and neither of those may depend on config, so
// hosting the constant here preserves config -> {core, grouping} -> ...
// without a back-edge into infrastructure/publishing.
// infrastructure/publishing.MaxDeliveryConfirmationTimeout re-exports this
// value for callers that expect it at that import path.
//
// The knob is not merely a timeout: the notify chain holds the group's publish
// lock and its cross-replica claim for the whole wait, and grouping derives the
// timer-callback deadline and the orphan-adoption grace period from it (see
// grouping/notify_budget.go). Two minutes already implies a ~2m45s adoption
// grace, which is the point where those derived values start crowding a typical
// group_interval and the timer record's own TTL grace. Enforced in
// config.validatePublishing so a too-large value fails at load time with an
// explanation instead of quietly degrading dispatch.
const MaxDeliveryConfirmationTimeout = 2 * time.Minute
