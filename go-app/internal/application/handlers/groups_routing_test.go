package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	appconfig "github.com/ipiton/AMP/internal/config"
	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/core/services"
	infraroute "github.com/ipiton/AMP/internal/infrastructure/routing"
	"github.com/ipiton/AMP/internal/infrastructure/storage/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Final review finding 17: /api/v2/alerts/groups reported `labels: {}` for every
// group unless the caller passed `?group_by=` (upstream has no such parameter —
// group labels come from the matched ROUTE's group_by), and hardcoded the FIRST
// configured receiver on every group regardless of where its alerts route.

// labelRouteEvaluator routes by a single label value, so a test can produce two
// groups with genuinely different receivers and group_by sets.
type labelRouteEvaluator struct {
	label   string
	byValue map[string]*services.RoutingDecision
	fallth  *services.RoutingDecision
}

func (e *labelRouteEvaluator) Evaluate(labels map[string]string) (*services.RoutingDecision, error) {
	if d, ok := e.byValue[labels[e.label]]; ok {
		return d, nil
	}
	return e.fallth, nil
}

func ingestGroupTestAlerts(t *testing.T) *memory.AlertStore {
	t.Helper()
	store := memory.NewAlertStore()
	now := time.Now().UTC()
	require.NoError(t, store.IngestBatch([]core.AlertIngestInput{
		{
			Labels:      map[string]string{"alertname": "DiskFull", "team": "sre", "cluster": "eu-1"},
			StartsAt:    now.Format(time.RFC3339),
			Fingerprint: "f-sre",
			Status:      "firing",
		},
		{
			Labels:      map[string]string{"alertname": "QueueLag", "team": "data", "cluster": "eu-1"},
			StartsAt:    now.Format(time.RFC3339),
			Fingerprint: "f-data",
			Status:      "firing",
		},
	}, now))
	return store
}

func getGroups(t *testing.T, registry RegistryProvider, url string) []core.APIGettableAlertGroup {
	t.Helper()
	rec := httptest.NewRecorder()
	AlertGroupsHandler(registry)(rec, httptest.NewRequest(http.MethodGet, url, nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var groups []core.APIGettableAlertGroup
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &groups))
	return groups
}

func TestAlertGroupsHandler_PerGroupReceiverAndLabelsFromRouteTree(t *testing.T) {
	registry := &extendedFakeRegistry{
		alertStore: ingestGroupTestAlerts(t),
		config:     &appconfig.Config{},
		routeEvaluator: &labelRouteEvaluator{
			label: "team",
			byValue: map[string]*services.RoutingDecision{
				"sre":  {Receiver: "sre-pager", GroupBy: []string{"alertname", "cluster"}},
				"data": {Receiver: "data-slack", GroupBy: []string{"team"}},
			},
			fallth: &services.RoutingDecision{Receiver: "default", GroupBy: []string{"alertname"}},
		},
	}

	groups := getGroups(t, registry, "/api/v2/alerts/groups")
	require.Len(t, groups, 2)

	byReceiver := map[string]core.APIGettableAlertGroup{}
	for _, g := range groups {
		byReceiver[g.Receiver.Name] = g
	}

	sre, ok := byReceiver["sre-pager"]
	require.True(t, ok, "each group must report the receiver its own alerts route to, got %v", byReceiver)
	assert.Equal(t, map[string]string{"alertname": "DiskFull", "cluster": "eu-1"}, sre.Labels,
		"group labels must come from the matched route's group_by")
	require.Len(t, sre.Alerts, 1)

	data, ok := byReceiver["data-slack"]
	require.True(t, ok)
	assert.Equal(t, map[string]string{"team": "data"}, data.Labels,
		"a route with a different group_by must produce different group labels")
	require.Len(t, data.Alerts, 1)
}

// TestAlertGroupsHandler_LabelsFromRootRouteWithoutQueryParam is the plain-
// request case that used to return `labels: {}`: no `?group_by=`, no per-alert
// evaluator, just a configured route tree.
func TestAlertGroupsHandler_LabelsFromRootRouteWithoutQueryParam(t *testing.T) {
	parsed, err := infraroute.NewRouteConfigParser().Parse([]byte(`
route:
  receiver: ops
  group_by: [alertname, cluster]
receivers:
  - name: ops
    webhook_configs:
      - url: https://example.com/webhook
`))
	require.NoError(t, err)

	registry := &extendedFakeRegistry{
		alertStore: ingestGroupTestAlerts(t),
		config:     &appconfig.Config{Routing: parsed},
	}

	groups := getGroups(t, registry, "/api/v2/alerts/groups")
	require.Len(t, groups, 2, "the root route's group_by must actually split the alerts")

	for _, g := range groups {
		assert.Equal(t, "ops", g.Receiver.Name, "the root route's receiver must be reported, not a hardcoded first receiver")
		assert.NotEmpty(t, g.Labels, "group labels must be populated from route.group_by even without ?group_by=")
		assert.Contains(t, g.Labels, "alertname")
		assert.Contains(t, g.Labels, "cluster")
	}
}

// TestAlertGroupsHandler_QueryGroupByStillOverrides keeps the AMP-specific
// `?group_by=` extension working (existing behaviour, existing callers).
func TestAlertGroupsHandler_QueryGroupByStillOverrides(t *testing.T) {
	registry := &extendedFakeRegistry{
		alertStore: ingestGroupTestAlerts(t),
		config:     &appconfig.Config{},
		routeEvaluator: &labelRouteEvaluator{
			label:  "team",
			fallth: &services.RoutingDecision{Receiver: "ops", GroupBy: []string{"alertname"}},
		},
	}

	groups := getGroups(t, registry, "/api/v2/alerts/groups?group_by=cluster")
	require.Len(t, groups, 1, "both alerts share cluster=eu-1, so ?group_by=cluster must collapse them")
	assert.Equal(t, map[string]string{"cluster": "eu-1"}, groups[0].Labels)
	assert.Equal(t, "ops", groups[0].Receiver.Name, "the receiver must still come from routing, not from the query")
}

// TestAlertGroupsHandler_LegacyReceiverFallback covers lite/legacy mode: no
// route tree and no evaluator, so the configured legacy receiver is the best
// answer available.
func TestAlertGroupsHandler_LegacyReceiverFallback(t *testing.T) {
	registry := &extendedFakeRegistry{
		alertStore: ingestGroupTestAlerts(t),
		config: &appconfig.Config{
			Receivers: []appconfig.ReceiverConfig{{Name: "legacy-webhook"}},
		},
	}

	groups := getGroups(t, registry, "/api/v2/alerts/groups")
	require.NotEmpty(t, groups)
	for _, g := range groups {
		assert.Equal(t, "legacy-webhook", g.Receiver.Name)
	}
}

// TestGroupAlerts_SameLabelsDifferentReceiversAreDistinctGroups pins the
// aggregation key change: the receiver is part of it, mirroring upstream's
// per-route aggrGroups. Without it, two routes sending the same label set to
// different receivers collapsed into one group reporting a single receiver.
func TestGroupAlerts_SameLabelsDifferentReceiversAreDistinctGroups(t *testing.T) {
	store := memory.NewAlertStore()
	now := time.Now().UTC()
	require.NoError(t, store.IngestBatch([]core.AlertIngestInput{
		{
			Labels:      map[string]string{"alertname": "Shared", "route": "a"},
			StartsAt:    now.Format(time.RFC3339),
			Fingerprint: "f-a",
			Status:      "firing",
		},
		{
			Labels:      map[string]string{"alertname": "Shared", "route": "b"},
			StartsAt:    now.Format(time.RFC3339),
			Fingerprint: "f-b",
			Status:      "firing",
		},
	}, now))

	// Identical group_by (so identical labels), different receivers.
	groups := store.GroupAlerts(func(labels map[string]string) memory.AlertGrouping {
		return memory.AlertGrouping{
			Receiver: "receiver-" + labels["route"],
			GroupBy:  []string{"alertname"},
		}
	}, nil)

	require.Len(t, groups, 2, "same labels + different receivers must stay two groups")
	assert.Equal(t, "receiver-a", groups[0].Receiver.Name, "ties must be broken deterministically by receiver name")
	assert.Equal(t, "receiver-b", groups[1].Receiver.Name)
	assert.Equal(t, groups[0].Labels, groups[1].Labels)
}
