//go:build futureparity
// +build futureparity

// Historical wide-surface upstream parity suite. Opt-in only until runtime restoration.

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestUpstreamParity_StatusRequiredShape(t *testing.T) {
	mux := newPhase0TestMux(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/status", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/status expected 200, got %d", rec.Code)
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("status response is not valid json: %v", err)
	}

	// PARITY-4.2: /api/v2/status now matches upstream's nested shape —
	// cluster{status}, versionInfo, config{original}, uptime.
	requiredTopLevel := []string{"cluster", "versionInfo", "config", "uptime"}
	for _, field := range requiredTopLevel {
		if _, ok := payload[field]; !ok {
			t.Fatalf("status response missing required field %q", field)
		}
	}

	versionInfo, ok := payload["versionInfo"].(map[string]any)
	if !ok {
		t.Fatalf("status versionInfo expected object, got %T", payload["versionInfo"])
	}
	for _, field := range []string{"version", "revision", "branch", "buildUser", "buildDate", "goVersion"} {
		value, ok := versionInfo[field].(string)
		if !ok || strings.TrimSpace(value) == "" {
			t.Fatalf("status versionInfo.%s expected non-empty string, got %v", field, versionInfo[field])
		}
	}

	configObj, ok := payload["config"].(map[string]any)
	if !ok {
		t.Fatalf("status config expected nested object, got %T", payload["config"])
	}
	if _, ok := configObj["original"].(string); !ok {
		t.Fatalf("status config.original expected string, got %T", configObj["original"])
	}

	clusterObj, ok := payload["cluster"].(map[string]any)
	if !ok {
		t.Fatalf("status cluster expected nested object, got %T", payload["cluster"])
	}
	// No clustering yet (separate parity phase): stub value is "disabled".
	if clusterObj["status"] != "disabled" {
		t.Fatalf("status cluster.status expected %q, got %v", "disabled", clusterObj["status"])
	}

	uptimeRaw, ok := payload["uptime"].(string)
	if !ok {
		t.Fatalf("status uptime expected string, got %T", payload["uptime"])
	}
	if _, err := time.Parse(time.RFC3339, uptimeRaw); err != nil {
		t.Fatalf("status uptime expected RFC3339, got %q: %v", uptimeRaw, err)
	}
}

func TestUpstreamParity_CoreEndpointMethodMatrix(t *testing.T) {
	mux := newPhase0TestMux(t)

	startsAt := time.Now().UTC().Add(2 * time.Minute).Format(time.RFC3339)
	endsAt := time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339)

	alertPayload := fmt.Sprintf(
		`[{"labels":{"alertname":"CoreEndpointMatrix","service":"core-matrix","severity":"critical"},"annotations":{"summary":"core endpoint matrix"},"startsAt":"%s","generatorURL":"http://example.local/alert"}]`,
		startsAt,
	)
	silencePayload := fmt.Sprintf(
		`{"matchers":[{"name":"service","value":"core-matrix","isRegex":false}],"startsAt":"%s","endsAt":"%s","createdBy":"parity-suite","comment":"core endpoint matrix"}`,
		startsAt,
		endsAt,
	)

	tests := []struct {
		name   string
		method string
		path   string
		body   string
		status int
	}{
		// NO-METHOD-ENFORCEMENT fixed: read-only endpoints (status, receivers,
		// groups, health probes) now reject non-GET/HEAD with 405.
		{name: "status get", method: http.MethodGet, path: "/api/v2/status", status: http.StatusOK},
		{name: "status post not allowed", method: http.MethodPost, path: "/api/v2/status", status: http.StatusMethodNotAllowed},

		{name: "receivers get", method: http.MethodGet, path: "/api/v2/receivers", status: http.StatusOK},
		{name: "receivers post not allowed", method: http.MethodPost, path: "/api/v2/receivers", status: http.StatusMethodNotAllowed},

		{name: "alerts get", method: http.MethodGet, path: "/api/v2/alerts", status: http.StatusOK},
		{name: "alerts post", method: http.MethodPost, path: "/api/v2/alerts", body: alertPayload, status: http.StatusOK},
		{name: "alerts put not allowed", method: http.MethodPut, path: "/api/v2/alerts", status: http.StatusMethodNotAllowed},

		{name: "alert groups get", method: http.MethodGet, path: "/api/v2/alerts/groups", status: http.StatusOK},
		{name: "alert groups post not allowed", method: http.MethodPost, path: "/api/v2/alerts/groups", status: http.StatusMethodNotAllowed},

		{name: "silences get", method: http.MethodGet, path: "/api/v2/silences", status: http.StatusOK},
		{name: "silences post", method: http.MethodPost, path: "/api/v2/silences", body: silencePayload, status: http.StatusOK},

		{name: "silence by id get", method: http.MethodGet, path: "/api/v2/silence/00000000-0000-4000-8000-000000000001", status: http.StatusNotFound},
		{name: "silence by id delete", method: http.MethodDelete, path: "/api/v2/silence/00000000-0000-4000-8000-000000000001", status: http.StatusNotFound},
		{name: "silence by id post not allowed", method: http.MethodPost, path: "/api/v2/silence/00000000-0000-4000-8000-000000000001", status: http.StatusMethodNotAllowed},

		{name: "healthy get", method: http.MethodGet, path: "/-/healthy", status: http.StatusOK},
		{name: "healthy head", method: http.MethodHead, path: "/-/healthy", status: http.StatusOK},
		{name: "healthy post not allowed", method: http.MethodPost, path: "/-/healthy", status: http.StatusMethodNotAllowed},

		{name: "ready get", method: http.MethodGet, path: "/-/ready", status: http.StatusOK},
		{name: "ready head", method: http.MethodHead, path: "/-/ready", status: http.StatusOK},
		{name: "ready post not allowed", method: http.MethodPost, path: "/-/ready", status: http.StatusMethodNotAllowed},

		{name: "reload post", method: http.MethodPost, path: "/-/reload", body: `{}`, status: http.StatusOK},
		{name: "reload get not allowed", method: http.MethodGet, path: "/-/reload", status: http.StatusMethodNotAllowed},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, bytes.NewBufferString(tt.body))
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != tt.status {
				t.Fatalf("%s %s expected %d, got %d body=%q", tt.method, tt.path, tt.status, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestUpstreamParity_StatusClusterSettlingWindow(t *testing.T) {
	now := time.Now().UTC()
	clusterCtx := &runtimeClusterContext{
		status:      "ready",
		name:        "AMPSTATUSPARITYNODE",
		settleUntil: now.Add(2 * time.Second),
		peers: []map[string]string{
			{
				"name":    "AMPSTATUSPARITYNODE",
				"address": "127.0.0.1:9094",
			},
		},
	}

	settlingPayload := buildRuntimeClusterStatusPayload(clusterCtx, now)
	if settlingPayload["status"] != "settling" {
		t.Fatalf("status during settling window expected settling, got %v", settlingPayload["status"])
	}

	readyPayload := buildRuntimeClusterStatusPayload(clusterCtx, now.Add(3*time.Second))
	if readyPayload["status"] != "ready" {
		t.Fatalf("status after settling window expected ready, got %v", readyPayload["status"])
	}
}

func TestUpstreamParity_ReceiversConfiguredListOnly(t *testing.T) {
	// task 1.3: route:/receivers: parse via infrastructure/routing.Parse(),
	// which requires every route.receiver to resolve in receivers: and
	// every receiver to define at least one notification config. The
	// pre-1.3 fixture's nested "team-db" child route referenced a receiver
	// absent from receivers: — that is now a load error rather than a
	// silent no-op, so it can no longer be used to prove /api/v2/receivers
	// sources from the flat receivers: list.
	//
	// "team-alpha" is deliberately NOT referenced by any route below. That
	// restores the real invariant from the receivers: side: the endpoint
	// must list every configured receiver regardless of route-tree
	// reachability, not just the ones a route happens to point at.
	configPath := writeTestConfigFile(t, `
route:
  receiver: "team-zeta"
receivers:
  - name: "team-zeta"
    webhook_configs:
      - url: https://example.com/webhook
  - name: "team-alpha"
    webhook_configs:
      - url: https://example.com/webhook
`)
	t.Setenv(runtimeConfigFileEnv, configPath)

	mux := newPhase0TestMux(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/receivers", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/receivers expected 200, got %d", rec.Code)
	}

	var receivers []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &receivers); err != nil {
		t.Fatalf("failed to decode receivers response: %v", err)
	}
	if len(receivers) != 2 {
		t.Fatalf("expected exactly two configured receivers, got %d", len(receivers))
	}

	names := []string{}
	nameSet := make(map[string]struct{}, len(receivers))
	for _, receiver := range receivers {
		// RECEIVERS-JSON-CASE fixed: config.ReceiverConfig now carries a json
		// tag, so the field matches the upstream Alertmanager schema ("name").
		name, ok := receiver["name"].(string)
		if !ok || strings.TrimSpace(name) == "" {
			t.Fatalf("receiver.Name expected non-empty string, got %v", receiver["name"])
		}
		names = append(names, name)
		nameSet[name] = struct{}{}
	}

	if names[0] != "team-zeta" || names[1] != "team-alpha" {
		t.Fatalf("unexpected receiver list order/content (must preserve config order): %v", names)
	}
	// "team-alpha" is unreferenced by any route (see fixture comment above):
	// its presence here proves the list is NOT filtered down to
	// route-reachable receivers only.
	if _, ok := nameSet["team-alpha"]; !ok {
		t.Fatalf("expected route-unreferenced receiver %q in /api/v2/receivers (list must source from receivers:, not route-tree reachability), got %v", "team-alpha", names)
	}
}

func TestUpstreamParity_AlertsListOrderByFingerprint(t *testing.T) {
	mux := newPhase0TestMux(t)

	payload := `[
		{
			"labels": {"alertname":"newer"},
			"startsAt": "2026-02-27T00:00:00Z",
			"status": "firing"
		},
		{
			"labels": {"alertname":"a"},
			"startsAt": "2026-02-27T00:10:00Z",
			"status": "firing"
		}
	]`

	postReq := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(payload))
	postRec := httptest.NewRecorder()
	mux.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/alerts expected 200, got %d", postRec.Code)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v2/alerts", nil)
	getRec := httptest.NewRecorder()
	mux.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts expected 200, got %d", getRec.Code)
	}

	var alerts []map[string]any
	if err := json.Unmarshal(getRec.Body.Bytes(), &alerts); err != nil {
		t.Fatalf("failed to decode alerts response: %v", err)
	}
	if len(alerts) != 2 {
		t.Fatalf("expected 2 alerts, got %d", len(alerts))
	}

	firstLabels, ok := alerts[0]["labels"].(map[string]any)
	if !ok {
		t.Fatalf("alerts[0].labels expected object, got %T", alerts[0]["labels"])
	}
	secondLabels, ok := alerts[1]["labels"].(map[string]any)
	if !ok {
		t.Fatalf("alerts[1].labels expected object, got %T", alerts[1]["labels"])
	}
	firstFingerprint, _ := alerts[0]["fingerprint"].(string)
	secondFingerprint, _ := alerts[1]["fingerprint"].(string)
	if strings.TrimSpace(firstFingerprint) == "" || strings.TrimSpace(secondFingerprint) == "" {
		t.Fatalf("expected non-empty fingerprints, got first=%q second=%q", firstFingerprint, secondFingerprint)
	}
	if firstFingerprint >= secondFingerprint {
		t.Fatalf("expected fingerprint-ascending order, got first=%q second=%q", firstFingerprint, secondFingerprint)
	}
	if firstLabels["alertname"] == secondLabels["alertname"] {
		t.Fatalf("expected two distinct alerts, got duplicated alertname=%v", firstLabels["alertname"])
	}
}

func TestUpstreamParity_ReloadReturns500OnInvalidConfig(t *testing.T) {
	configPath := writeTestConfigFile(t, "route: [\n")
	t.Setenv(runtimeConfigFileEnv, configPath)

	mux := newPhase0TestMux(t)

	req := httptest.NewRequest(http.MethodPost, "/-/reload", bytes.NewBufferString(`{}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("POST /-/reload expected 500 for invalid config, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "failed to reload config") {
		t.Fatalf("reload failure response expected failure prefix, got %q", rec.Body.String())
	}
}

func TestUpstreamParity_ReloadSuccessReturnsOKBody(t *testing.T) {
	// task 1.3: route:/receivers: parse via infrastructure/routing.Parse(),
	// which requires the root route's receiver to resolve in receivers: and
	// that receiver to define at least one notification config.
	configPath := writeTestConfigFile(t, `
route:
  receiver: "initial-receiver"
receivers:
  - name: "initial-receiver"
    webhook_configs:
      - url: https://example.com/webhook
`)
	t.Setenv(runtimeConfigFileEnv, configPath)

	mux := newPhase0TestMux(t)

	req := httptest.NewRequest(http.MethodPost, "/-/reload", bytes.NewBufferString(`{}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /-/reload expected 200 for valid config, got %d", rec.Code)
	}
	// ADR-002: the active ReloadHandler responds with a literal "OK" body
	// (upstream returned an empty body).
	if rec.Body.String() != "OK" {
		t.Fatalf("reload success expected body OK, got %q", rec.Body.String())
	}
}

func TestUpstreamParity_UpstreamStaticCompatibilityPaths(t *testing.T) {
	mux := newPhase0TestMux(t)

	// ADR-002: the active runtime dropped the upstream /script.js compatibility
	// asset (UI-PLACEHOLDER-REMOVAL); unknown root paths fall through the
	// catch-all dashboard handler and return 404.
	scriptReq := httptest.NewRequest(http.MethodGet, "/script.js", nil)
	scriptRec := httptest.NewRecorder()
	mux.ServeHTTP(scriptRec, scriptReq)
	if scriptRec.Code != http.StatusNotFound {
		t.Fatalf("GET /script.js expected 404, got %d", scriptRec.Code)
	}

	libReq := httptest.NewRequest(http.MethodGet, "/lib/nonexistent.js", nil)
	libRec := httptest.NewRecorder()
	mux.ServeHTTP(libRec, libReq)
	if libRec.Code != http.StatusNotFound {
		t.Fatalf("GET /lib/nonexistent.js expected 404 for missing asset, got %d", libRec.Code)
	}

	faviconReq := httptest.NewRequest(http.MethodGet, "/favicon.ico", nil)
	faviconRec := httptest.NewRecorder()
	mux.ServeHTTP(faviconRec, faviconReq)
	if faviconRec.Code != http.StatusNotFound {
		t.Fatalf("GET /favicon.ico expected 404 for missing asset, got %d", faviconRec.Code)
	}
}

func TestUpstreamParity_InvalidStatusAndResolvedAreIgnored(t *testing.T) {
	mux := newPhase0TestMux(t)

	payload := `[
		{
			"labels": {"alertname":"InvalidStatusParity","service":"api","namespace":"prod"},
			"startsAt": "2026-02-25T00:00:00Z",
			"status": "firing"
		}
	]`
	postReq := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(payload))
	postRec := httptest.NewRecorder()
	mux.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/alerts expected 200, got %d", postRec.Code)
	}

	statusReq := httptest.NewRequest(http.MethodGet, "/api/v2/alerts?status=broken", nil)
	statusRec := httptest.NewRecorder()
	mux.ServeHTTP(statusRec, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts with invalid status expected 200, got %d", statusRec.Code)
	}
	var statusFiltered []map[string]any
	if err := json.Unmarshal(statusRec.Body.Bytes(), &statusFiltered); err != nil {
		t.Fatalf("failed to decode status-filter response: %v", err)
	}
	if len(statusFiltered) != 1 {
		t.Fatalf("invalid status should be ignored, got %d alerts", len(statusFiltered))
	}

	resolvedReq := httptest.NewRequest(http.MethodGet, "/api/v2/alerts?resolved=not-bool", nil)
	resolvedRec := httptest.NewRecorder()
	mux.ServeHTTP(resolvedRec, resolvedReq)
	if resolvedRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts with invalid resolved expected 200, got %d", resolvedRec.Code)
	}
	var resolvedFiltered []map[string]any
	if err := json.Unmarshal(resolvedRec.Body.Bytes(), &resolvedFiltered); err != nil {
		t.Fatalf("failed to decode resolved-filter response: %v", err)
	}
	if len(resolvedFiltered) != 1 {
		t.Fatalf("invalid resolved should fallback to false and keep firing alerts, got %d", len(resolvedFiltered))
	}

	groupsReq := httptest.NewRequest(http.MethodGet, "/api/v2/alerts/groups?resolved=not-bool", nil)
	groupsRec := httptest.NewRecorder()
	mux.ServeHTTP(groupsRec, groupsReq)
	if groupsRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts/groups with invalid resolved expected 200, got %d", groupsRec.Code)
	}
	var groups []map[string]any
	if err := json.Unmarshal(groupsRec.Body.Bytes(), &groups); err != nil {
		t.Fatalf("failed to decode groups resolved-filter response: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("invalid resolved on groups should be ignored, got %d groups", len(groups))
	}
}

func TestUpstreamParity_AlertGroupsShapeAndFilters(t *testing.T) {
	mux := newPhase0TestMux(t)

	payload := `[
		{
			"labels": {"alertname":"GroupParityA","service":"api","namespace":"prod"},
			"annotations": {"summary":"a"},
			"startsAt": "2026-02-25T00:00:00Z",
			"status": "firing"
		},
		{
			"labels": {"alertname":"GroupParityB","service":"api","namespace":"prod"},
			"annotations": {"summary":"b"},
			"startsAt": "2026-02-25T00:01:00Z",
			"status": "firing"
		}
	]`

	postReq := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(payload))
	postRec := httptest.NewRecorder()
	mux.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/alerts expected 200, got %d", postRec.Code)
	}

	// No routing tree yet: groups carry the static "default" receiver (see
	// ADR-002 active-runtime scope). Verify the plain (unfiltered) shape
	// first, then verify PARITY-4.1's receiver regex filter against it.
	req := httptest.NewRequest(http.MethodGet, "/api/v2/alerts/groups", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts/groups expected 200, got %d", rec.Code)
	}

	var groups []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &groups); err != nil {
		t.Fatalf("failed to decode groups response: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}

	groupReceiver, ok := groups[0]["receiver"].(map[string]any)
	if !ok {
		t.Fatalf("group receiver expected object, got %T", groups[0]["receiver"])
	}
	if groupReceiver["name"] != "default" {
		t.Fatalf("group receiver.name expected default, got %v", groupReceiver["name"])
	}

	alerts, ok := groups[0]["alerts"].([]any)
	if !ok || len(alerts) != 2 {
		t.Fatalf("group alerts expected array with two entries, got %v", groups[0]["alerts"])
	}
	alert, ok := alerts[0].(map[string]any)
	if !ok {
		t.Fatalf("group alert expected object, got %T", alerts[0])
	}

	requiredNested := []string{"annotations", "receivers", "startsAt", "updatedAt", "endsAt", "fingerprint", "status"}
	for _, field := range requiredNested {
		if _, ok := alert[field]; !ok {
			t.Fatalf("nested alert missing required field %q", field)
		}
	}

	// PARITY-4.1: receiver is now a real regex filter — a pattern that does
	// not match the group's "default" receiver drops the group entirely.
	noMatchQuery := url.Values{}
	noMatchQuery.Set("receiver", "^team-ops$")
	noMatchReq := httptest.NewRequest(http.MethodGet, "/api/v2/alerts/groups?"+noMatchQuery.Encode(), nil)
	noMatchRec := httptest.NewRecorder()
	mux.ServeHTTP(noMatchRec, noMatchReq)
	if noMatchRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts/groups (no-match receiver) expected 200, got %d", noMatchRec.Code)
	}
	var noMatchGroups []map[string]any
	if err := json.Unmarshal(noMatchRec.Body.Bytes(), &noMatchGroups); err != nil {
		t.Fatalf("failed to decode groups response: %v", err)
	}
	if len(noMatchGroups) != 0 {
		t.Fatalf("expected 0 groups for a non-matching receiver regex, got %d", len(noMatchGroups))
	}

	// A pattern that does match the group's receiver keeps it.
	matchQuery := url.Values{}
	matchQuery.Set("receiver", "^default$")
	matchReq := httptest.NewRequest(http.MethodGet, "/api/v2/alerts/groups?"+matchQuery.Encode(), nil)
	matchRec := httptest.NewRecorder()
	mux.ServeHTTP(matchRec, matchReq)
	if matchRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts/groups (matching receiver) expected 200, got %d", matchRec.Code)
	}
	var matchGroups []map[string]any
	if err := json.Unmarshal(matchRec.Body.Bytes(), &matchGroups); err != nil {
		t.Fatalf("failed to decode groups response: %v", err)
	}
	if len(matchGroups) != 1 {
		t.Fatalf("expected 1 group for a matching receiver regex, got %d", len(matchGroups))
	}
}

func TestUpstreamParity_AlertGroupsWithoutGroupByHaveEmptyLabels(t *testing.T) {
	configPath := writeTestConfigFile(t, `
route:
  receiver: "default"
receivers:
  - name: "default"
`)
	t.Setenv(runtimeConfigFileEnv, configPath)

	mux := newPhase0TestMux(t)

	payload := `[
		{
			"labels": {"alertname":"GroupNoByA","service":"api","namespace":"prod"},
			"startsAt": "2026-02-25T00:00:00Z",
			"status": "firing"
		},
		{
			"labels": {"alertname":"GroupNoByB","service":"api","namespace":"prod"},
			"startsAt": "2026-02-25T00:01:00Z",
			"status": "firing"
		}
	]`

	postReq := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(payload))
	postRec := httptest.NewRecorder()
	mux.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/alerts expected 200, got %d", postRec.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v2/alerts/groups", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts/groups expected 200, got %d", rec.Code)
	}

	var groups []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &groups); err != nil {
		t.Fatalf("failed to decode groups response: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("expected one receiver-level group without route.group_by, got %d", len(groups))
	}

	labels, ok := groups[0]["labels"].(map[string]any)
	if !ok {
		t.Fatalf("group labels expected object, got %T", groups[0]["labels"])
	}
	if len(labels) != 0 {
		t.Fatalf("group labels expected empty object without route.group_by, got %v", labels)
	}

	alerts, ok := groups[0]["alerts"].([]any)
	if !ok {
		t.Fatalf("group alerts expected array, got %T", groups[0]["alerts"])
	}
	if len(alerts) != 2 {
		t.Fatalf("expected two alerts inside receiver-level group, got %d", len(alerts))
	}
}

func TestUpstreamParity_AlertsAndGroupsInvalidQueryContract(t *testing.T) {
	mux := newPhase0TestMux(t)

	// Active-runtime contract:
	//   - PARITY-4.1: the upstream receiver query parameter is now validated
	//     as a regex on both /api/v2/alerts and /api/v2/alerts/groups (400 on
	//     a malformed pattern);
	//   - /api/v2/alerts validates filter matchers and returns 400 with an
	//     {"error": ...} object (upstream returned a bare JSON string);
	//   - /api/v2/alerts/groups does not parse the label `filter` parameter
	//     at all (that param is /api/v2/alerts-only, per upstream).
	okCases := []string{
		"/api/v2/alerts/groups?filter=broken-matcher",
	}
	for _, path := range okCases {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s expected 200 (param ignored by active runtime), got %d", path, rec.Code)
		}
	}

	badReceiverRegexCases := []string{
		"/api/v2/alerts?receiver=[",
		"/api/v2/alerts/groups?receiver=[",
	}
	for _, path := range badReceiverRegexCases {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("GET %s expected 400 (invalid receiver regex), got %d", path, rec.Code)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v2/alerts?filter=broken-matcher", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("GET /api/v2/alerts?filter=broken-matcher expected 400, got %d", rec.Code)
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("invalid filter expected JSON object body, got %q (%v)", rec.Body.String(), err)
	}
	message, _ := payload["error"].(string)
	if !strings.Contains(message, "invalid matcher syntax") {
		t.Fatalf("invalid filter expected invalid matcher syntax error, got %q", message)
	}
}

func TestUpstreamParity_PostAlertsErrorPayloadContracts(t *testing.T) {
	mux := newPhase0TestMux(t)

	// ADR-002: the active runtime returns 400 with an {"error": ...} JSON
	// object for malformed alert payloads (upstream used code/message envelopes
	// and 422 for validation failures).
	postAlertsExpectError := func(t *testing.T, body, wantSubstring string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(body))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("POST /api/v2/alerts %s expected 400, got %d", body, rec.Code)
		}
		var payload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("POST /api/v2/alerts %s expected JSON object body, got %q (%v)", body, rec.Body.String(), err)
		}
		message, _ := payload["error"].(string)
		if !strings.Contains(message, wantSubstring) {
			t.Fatalf("POST /api/v2/alerts %s expected error containing %q, got %q", body, wantSubstring, message)
		}
	}

	postAlertsExpectError(t, `{}`, "invalid alert payload")
	postAlertsExpectError(t, `[{"labels":{"alertname":"A"},"startsAt":"not-time"}]`, "invalid startsAt")
	postAlertsExpectError(t, `[{}]`, "missing required label alertname")
	postAlertsExpectError(t, `[{"labels":{}}]`, "missing required label alertname")

	// The active runtime does not validate generatorURL; the alert is accepted.
	invalidGeneratorReq := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(`[{"labels":{"alertname":"A"},"generatorURL":":bad"}]`))
	invalidGeneratorRec := httptest.NewRecorder()
	mux.ServeHTTP(invalidGeneratorRec, invalidGeneratorReq)
	if invalidGeneratorRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/alerts invalid generatorURL expected 200 (not validated), got %d", invalidGeneratorRec.Code)
	}
}

func TestUpstreamParity_PostAlertsDateOnlyTimestampsAccepted(t *testing.T) {
	mux := newPhase0TestMux(t)

	// DTO-FRAGMENTATION consolidation: the ingest edge and the memory store now
	// share one lenient time parser (internal/core/alertconv.ParseAlertTime:
	// RFC3339/RFC3339Nano + date-only YYYY-MM-DD, matching upstream
	// Alertmanager leniency). Date-only timestamps are therefore ACCEPTED on
	// every ingest path — the previous 400 expectation documented a divergence
	// between the handler and the store parsers that no longer exists.
	postReq := httptest.NewRequest(
		http.MethodPost,
		"/api/v2/alerts",
		bytes.NewBufferString(`[{"labels":{"alertname":"DateOnlyParity"},"startsAt":"2099-02-26","endsAt":"2099-03-01"}]`),
	)
	postRec := httptest.NewRecorder()
	mux.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/alerts with date-only timestamps expected 200 (lenient parser), got %d", postRec.Code)
	}
}

func TestUpstreamParity_SilencesFilterAndOrder(t *testing.T) {
	mux := newPhase0TestMux(t)
	now := time.Now().UTC()

	payloads := []string{
		fmt.Sprintf(`{
			"matchers": [{"name":"alertname","value":"PendingParity","isRegex":false}],
			"startsAt": %q,
			"endsAt": %q,
			"createdBy": "parity-suite",
			"comment": "pending-parity"
		}`, now.Add(20*time.Minute).Format(time.RFC3339), now.Add(40*time.Minute).Format(time.RFC3339)),
		fmt.Sprintf(`{
			"matchers": [{"name":"alertname","value":"ActiveLateParity","isRegex":false}],
			"startsAt": %q,
			"endsAt": %q,
			"createdBy": "parity-suite",
			"comment": "active-late-parity"
		}`, now.Add(-1*time.Minute).Format(time.RFC3339), now.Add(50*time.Minute).Format(time.RFC3339)),
		fmt.Sprintf(`{
			"matchers": [{"name":"alertname","value":"ActiveSoonParity","isRegex":false}],
			"startsAt": %q,
			"endsAt": %q,
			"createdBy": "parity-suite",
			"comment": "active-soon-parity"
		}`, now.Add(-1*time.Minute).Format(time.RFC3339), now.Add(10*time.Minute).Format(time.RFC3339)),
	}

	for i, payload := range payloads {
		req := httptest.NewRequest(http.MethodPost, "/api/v2/silences", bytes.NewBufferString(payload))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST /api/v2/silences payload #%d expected 200, got %d", i, rec.Code)
		}
	}

	filterQuery := url.Values{}
	filterQuery.Add("filter", `alertname="ActiveSoonParity"`)
	req := httptest.NewRequest(http.MethodGet, "/api/v2/silences?"+filterQuery.Encode(), nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/silences with filter expected 200, got %d", rec.Code)
	}

	var filtered []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &filtered); err != nil {
		t.Fatalf("failed to decode filtered silences response: %v", err)
	}
	// Upstream semantics: the filter matches the silence matcher VALUE, so
	// only the ActiveSoonParity silence passes.
	if len(filtered) != 1 {
		t.Fatalf("expected exactly one silence for alertname=ActiveSoonParity, got %d", len(filtered))
	}
	foundTarget := false
	for _, silence := range filtered {
		if silence["comment"] == "active-soon-parity" {
			foundTarget = true
		}
	}
	if !foundTarget {
		t.Fatalf("expected filtered set to include active-soon-parity, got %v", filtered)
	}
	filteredMatchers, ok := filtered[0]["matchers"].([]any)
	if !ok || len(filteredMatchers) == 0 {
		t.Fatalf("filtered silence expected non-empty matchers array, got %T", filtered[0]["matchers"])
	}
	firstFilteredMatcher, ok := filteredMatchers[0].(map[string]any)
	if !ok {
		t.Fatalf("filtered silence matcher expected object, got %T", filteredMatchers[0])
	}
	if _, ok := firstFilteredMatcher["isRegex"]; !ok {
		t.Fatalf("filtered silence matcher expected isRegex field to be present")
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v2/silences", nil)
	listRec := httptest.NewRecorder()
	mux.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/silences expected 200, got %d", listRec.Code)
	}

	var silences []map[string]any
	if err := json.Unmarshal(listRec.Body.Bytes(), &silences); err != nil {
		t.Fatalf("failed to decode silences list: %v", err)
	}
	if len(silences) != 3 {
		t.Fatalf("expected 3 silences, got %d", len(silences))
	}

	gotOrder := []string{
		fmt.Sprint(silences[0]["comment"]),
		fmt.Sprint(silences[1]["comment"]),
		fmt.Sprint(silences[2]["comment"]),
	}
	wantOrder := []string{"active-soon-parity", "active-late-parity", "pending-parity"}
	for i := range wantOrder {
		if gotOrder[i] != wantOrder[i] {
			t.Fatalf("unexpected silence order at %d: got=%v want=%v full=%v", i, gotOrder[i], wantOrder[i], gotOrder)
		}
	}
}

func TestUpstreamParity_SilencesInvalidFilterErrorPayloadIsJSONObject(t *testing.T) {
	mux := newPhase0TestMux(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/silences?filter=broken-matcher", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("GET /api/v2/silences invalid filter expected 400, got %d", rec.Code)
	}

	// ADR-002: the active runtime wraps errors in an {"error": ...} object
	// (upstream returned a bare JSON string).
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("invalid filter error expected JSON object body, got %q (%v)", rec.Body.String(), err)
	}
	message, _ := payload["error"].(string)
	if !strings.Contains(message, "invalid matcher syntax") {
		t.Fatalf("invalid filter error expected invalid matcher syntax message, got %q", message)
	}
}

func TestUpstreamParity_SilenceByIDInvalidUUIDReturnsNotFound(t *testing.T) {
	mux := newPhase0TestMux(t)

	// ADR-002: the active runtime does not validate the silence id as a UUID;
	// any unknown id (malformed or not) is a plain 404 (upstream returned 422
	// with a code/message envelope for malformed UUIDs).
	getReq := httptest.NewRequest(http.MethodGet, "/api/v2/silence/not-a-uuid", nil)
	getRec := httptest.NewRecorder()
	mux.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/v2/silence/{id} invalid uuid expected 404, got %d", getRec.Code)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v2/silence/not-a-uuid", nil)
	deleteRec := httptest.NewRecorder()
	mux.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusNotFound {
		t.Fatalf("DELETE /api/v2/silence/{id} invalid uuid expected 404, got %d", deleteRec.Code)
	}
}

func TestUpstreamParity_SilenceByIDUnknownUUIDReturns404EmptyBody(t *testing.T) {
	mux := newPhase0TestMux(t)

	const unknownUUID = "00000000-0000-0000-0000-000000000001"

	getReq := httptest.NewRequest(http.MethodGet, "/api/v2/silence/"+unknownUUID, nil)
	getRec := httptest.NewRecorder()
	mux.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusNotFound {
		t.Fatalf("GET /api/v2/silence/{id} unknown uuid expected 404, got %d", getRec.Code)
	}
	if getRec.Body.Len() != 0 {
		t.Fatalf("GET /api/v2/silence/{id} unknown uuid expected empty body, got %q", getRec.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v2/silence/"+unknownUUID, nil)
	deleteRec := httptest.NewRecorder()
	mux.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusNotFound {
		t.Fatalf("DELETE /api/v2/silence/{id} unknown uuid expected 404, got %d", deleteRec.Code)
	}
	if deleteRec.Body.Len() != 0 {
		t.Fatalf("DELETE /api/v2/silence/{id} unknown uuid expected empty body, got %q", deleteRec.Body.String())
	}
}

func TestUpstreamParity_DeleteSilenceReturnsEmptyBody(t *testing.T) {
	mux := newPhase0TestMux(t)
	now := time.Now().UTC()

	payload := fmt.Sprintf(`{
		"matchers": [{"name":"alertname","value":"DeleteParity","isRegex":false}],
		"startsAt": %q,
		"endsAt": %q,
		"createdBy": "parity-suite",
		"comment": "delete-parity"
	}`, now.Add(-1*time.Minute).Format(time.RFC3339), now.Add(30*time.Minute).Format(time.RFC3339))

	createReq := httptest.NewRequest(http.MethodPost, "/api/v2/silences", bytes.NewBufferString(payload))
	createRec := httptest.NewRecorder()
	mux.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/silences expected 200, got %d", createRec.Code)
	}

	var createPayload map[string]any
	if err := json.Unmarshal(createRec.Body.Bytes(), &createPayload); err != nil {
		t.Fatalf("failed to decode silence create response: %v", err)
	}
	silenceID, _ := createPayload["silenceID"].(string)
	if strings.TrimSpace(silenceID) == "" {
		t.Fatalf("expected non-empty silenceID in create response")
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v2/silence/"+silenceID, nil)
	deleteRec := httptest.NewRecorder()
	mux.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("DELETE /api/v2/silence/{id} expected 200, got %d", deleteRec.Code)
	}
	if deleteRec.Body.Len() != 0 {
		t.Fatalf("DELETE /api/v2/silence/{id} expected empty body, got %q", deleteRec.Body.String())
	}
}

func TestUpstreamParity_PostSilenceErrorPayloadContracts(t *testing.T) {
	mux := newPhase0TestMux(t)

	// ADR-002: the active runtime answers every invalid silence POST with 400
	// and an {"error": ...} object (upstream used 422 code/message envelopes
	// and 404 for unknown-id updates).
	postSilenceExpect400 := func(t *testing.T, name, body string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/v2/silences", bytes.NewBufferString(body))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("POST /api/v2/silences %s expected 400, got %d", name, rec.Code)
		}
		var payload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("%s error expected JSON object body, got %q (%v)", name, rec.Body.String(), err)
		}
		message, _ := payload["error"].(string)
		if strings.TrimSpace(message) == "" {
			t.Fatalf("%s error expected non-empty error message, got %q", name, rec.Body.String())
		}
	}

	postSilenceExpect400(t, "invalid payload", `{}`)
	postSilenceExpect400(t, "empty matchers", `{
		"matchers": [],
		"startsAt": "2099-01-01T00:00:00Z",
		"endsAt": "2099-01-01T01:00:00Z",
		"createdBy": "parity-suite",
		"comment": "no matchers"
	}`)
	postSilenceExpect400(t, "unknown id update", `{
		"id": "ffffffff-ffff-ffff-ffff-ffffffffffff",
		"matchers": [{"name":"alertname","value":"ParityUnknownID","isRegex":false}],
		"startsAt": "2099-01-01T00:00:00Z",
		"endsAt": "2099-01-01T01:00:00Z",
		"createdBy": "parity-suite",
		"comment": "unknown id update"
	}`)
	postSilenceExpect400(t, "invalid id update", `{
		"id": "not-a-uuid",
		"matchers": [{"name":"alertname","value":"ParityInvalidID","isRegex":false}],
		"startsAt": "2099-01-01T00:00:00Z",
		"endsAt": "2099-01-01T01:00:00Z",
		"createdBy": "parity-suite",
		"comment": "invalid id update"
	}`)
}
