package upstreamai

import (
	"github.com/prometheus/alertmanager/alert"
	"github.com/prometheus/common/model"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/core/alertconv"
)

// toCoreAlert converts a firing upstream alert for the classification
// service. The fingerprint is AMP's (alertconv.Fingerprint), not upstream's
// FNV one, so the classification cache keys match cmd/server's.
func toCoreAlert(a *alert.Alert) *core.Alert {
	labels := labelMap(a.Labels)
	ca := &core.Alert{
		Fingerprint: alertconv.Fingerprint(labels),
		AlertName:   labels[string(model.AlertNameLabel)],
		Status:      core.StatusFiring,
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
