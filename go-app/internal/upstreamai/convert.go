package upstreamai

import (
	"github.com/prometheus/alertmanager/alert"
	"github.com/prometheus/common/model"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/core/alertconv"
)

// toCoreAlert converts an upstream alert for the classification service,
// the history and the investigation queue. The fingerprint is AMP's
// (alertconv.Fingerprint, SHA-256), not upstream's FNV one: it is the key of
// cmd/server's history rows, investigations and classification cache, so
// both binaries share them. Upstream's fingerprint is
// alertconv.UpstreamFingerprint of the same labels.
func toCoreAlert(a *alert.Alert) *core.Alert {
	labels := labelMap(a.Labels)
	status := core.StatusFiring
	if a.Resolved() {
		status = core.StatusResolved
	}
	ca := &core.Alert{
		Fingerprint: alertconv.Fingerprint(labels),
		AlertName:   labels[string(model.AlertNameLabel)],
		Status:      status,
		Labels:      labels,
		Annotations: labelMap(a.Annotations),
		StartsAt:    a.StartsAt,
	}
	if !a.EndsAt.IsZero() {
		endsAt := a.EndsAt
		ca.EndsAt = &endsAt
	}
	if a.GeneratorURL != "" {
		u := a.GeneratorURL
		ca.GeneratorURL = &u
	}
	return ca
}

func labelMap(ls model.LabelSet) map[string]string {
	m := make(map[string]string, len(ls))
	for k, v := range ls {
		m[string(k)] = string(v)
	}
	return m
}
