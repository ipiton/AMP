package publishing

import "context"

// ============================================================================
// Group context for template rendering (TEMPLATES-EPIC slice 2)
// ============================================================================
//
// A notification's TEMPLATE DATA needs the group it belongs to: upstream's
// `__subject` renders `.GroupLabels` (the route's `group_by` values) and
// `.Receiver`, and its output is visibly different without them —
// `[FIRING:1] HighCPU (critical)` with them, `[FIRING:1]  (HighCPU critical)`
// without, since every label then falls into the parenthesised remainder.
//
// The queue HAS all of it (PublishingJob.GroupLabels/GroupKey/Receiver, set by
// PublishingCoordinator.PublishGroupToTargets) but the one-message-per-alert
// integrations reach the formatter through
// AlertPublisher.Publish(ctx, enrichedAlert, target) — no group argument
// anywhere on the path.
//
// So it travels on the CONTEXT, which every publisher already threads into
// FormatAlert unchanged. That is deliberately the smallest possible change:
// adding a group parameter would mean editing AlertPublisher, every publisher
// implementation, and both formatter middlewares — a wide refactor of contracts
// this epic promised not to touch — for data that is request-scoped by nature.
// Absence is handled everywhere (empty GroupLabels renders upstream's own
// no-group shape), so nothing breaks on a path that does not set it.

// groupContextKey is the unexported context key type, so no other package can
// collide with it or read the value by accident.
type groupContextKey struct{}

// GroupNotificationContext is the group metadata a notification belongs to.
type GroupNotificationContext struct {
	// GroupKey is the group's identity (diagnostics only today).
	GroupKey string

	// Receiver is the ROUTED receiver name — the name the operator wrote in
	// `receivers:`, which is what upstream renders in `{{ .Receiver }}`. It is a
	// better source than decoding the target name, and the reason
	// receiverNameOf consults it first.
	Receiver string

	// GroupLabels are the resolved `group_by` label values for this group.
	GroupLabels map[string]string
}

// withGroupNotificationContext attaches group metadata for downstream template
// rendering. Called by the queue when it dispatches a group's alerts.
func withGroupNotificationContext(ctx context.Context, group GroupNotificationContext) context.Context {
	return context.WithValue(ctx, groupContextKey{}, group)
}

// groupNotificationContextFrom reads the group metadata, returning the zero
// value when the notification did not come through the group path (a direct
// per-alert publish, a test, or the legacy single-receiver mode).
func groupNotificationContextFrom(ctx context.Context) GroupNotificationContext {
	if ctx == nil {
		return GroupNotificationContext{}
	}
	group, _ := ctx.Value(groupContextKey{}).(GroupNotificationContext)
	return group
}
