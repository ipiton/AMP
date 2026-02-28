package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
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

	requiredTopLevel := []string{"cluster", "versionInfo", "config", "uptime"}
	for _, field := range requiredTopLevel {
		if _, ok := payload[field]; !ok {
			t.Fatalf("status response missing required field %q", field)
		}
	}

	cluster, ok := payload["cluster"].(map[string]any)
	if !ok {
		t.Fatalf("status cluster expected object, got %T", payload["cluster"])
	}
	clusterStatus, _ := cluster["status"].(string)
	switch clusterStatus {
	case "ready", "settling", "disabled":
	default:
		t.Fatalf("status cluster.status unexpected value: %v", cluster["status"])
	}
	clusterPeersRaw, ok := cluster["peers"].([]any)
	if !ok {
		t.Fatalf("status cluster.peers expected array, got %T", cluster["peers"])
	}
	if clusterStatus == "disabled" {
		if len(clusterPeersRaw) != 0 {
			t.Fatalf("status cluster.peers expected empty for disabled mode, got %v", clusterPeersRaw)
		}
	} else {
		clusterName, ok := cluster["name"].(string)
		if !ok || strings.TrimSpace(clusterName) == "" {
			t.Fatalf("status cluster.name expected non-empty string in active cluster mode, got %v", cluster["name"])
		}
		if len(clusterPeersRaw) == 0 {
			t.Fatalf("status cluster.peers expected non-empty in active cluster mode")
		}
		for i, rawPeer := range clusterPeersRaw {
			peer, ok := rawPeer.(map[string]any)
			if !ok {
				t.Fatalf("status cluster.peers[%d] expected object, got %T", i, rawPeer)
			}
			peerName, _ := peer["name"].(string)
			peerAddress, _ := peer["address"].(string)
			if strings.TrimSpace(peerName) == "" || strings.TrimSpace(peerAddress) == "" {
				t.Fatalf("status cluster.peers[%d] expected non-empty name/address, got %v", i, peer)
			}
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
		t.Fatalf("status config expected object, got %T", payload["config"])
	}
	if _, ok := configObj["original"].(string); !ok {
		t.Fatalf("status config.original expected string, got %T", configObj["original"])
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

func TestUpstreamParity_StatusClusterDisabledWhenListenAddressEmpty(t *testing.T) {
	t.Setenv(runtimeClusterListenAddressEnv, "")

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
	cluster, ok := payload["cluster"].(map[string]any)
	if !ok {
		t.Fatalf("status cluster expected object, got %T", payload["cluster"])
	}

	status, _ := cluster["status"].(string)
	if status != "disabled" {
		t.Fatalf("status cluster.status expected disabled when listen address is empty, got %q", status)
	}
	peers, ok := cluster["peers"].([]any)
	if !ok {
		t.Fatalf("status cluster.peers expected array, got %T", cluster["peers"])
	}
	if len(peers) != 0 {
		t.Fatalf("status cluster.peers expected empty in disabled mode, got %v", peers)
	}
}

func TestUpstreamParity_StatusClusterEnabledShapeWhenListenAddressConfigured(t *testing.T) {
	t.Setenv(runtimeClusterListenAddressEnv, "0.0.0.0:9094")
	t.Setenv(runtimeClusterAdvertiseAddressEnv, "127.0.0.1:9094")
	t.Setenv(runtimeClusterNameEnv, "AMPSTATUSPARITYNODE")

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
	cluster, ok := payload["cluster"].(map[string]any)
	if !ok {
		t.Fatalf("status cluster expected object, got %T", payload["cluster"])
	}

	status, _ := cluster["status"].(string)
	if status != "ready" && status != "settling" {
		t.Fatalf("status cluster.status expected ready or settling with listen address configured, got %q", status)
	}

	name, _ := cluster["name"].(string)
	if name != "AMPSTATUSPARITYNODE" {
		t.Fatalf("status cluster.name expected configured node name, got %q", name)
	}

	peersRaw, ok := cluster["peers"].([]any)
	if !ok {
		t.Fatalf("status cluster.peers expected array, got %T", cluster["peers"])
	}
	if len(peersRaw) != 1 {
		t.Fatalf("status cluster.peers expected exactly one self peer, got %d", len(peersRaw))
	}
	peer, ok := peersRaw[0].(map[string]any)
	if !ok {
		t.Fatalf("status cluster.peers[0] expected object, got %T", peersRaw[0])
	}
	if peer["name"] != "AMPSTATUSPARITYNODE" {
		t.Fatalf("status peer name expected AMPSTATUSPARITYNODE, got %v", peer["name"])
	}
	if peer["address"] != "127.0.0.1:9094" {
		t.Fatalf("status peer address expected 127.0.0.1:9094, got %v", peer["address"])
	}
}

func TestUpstreamParity_StatusClusterGeneratedNameLooksULID(t *testing.T) {
	t.Setenv(runtimeClusterListenAddressEnv, "0.0.0.0:9094")
	t.Setenv(runtimeClusterNameEnv, "")

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
	cluster, ok := payload["cluster"].(map[string]any)
	if !ok {
		t.Fatalf("status cluster expected object, got %T", payload["cluster"])
	}

	clusterName, _ := cluster["name"].(string)
	if strings.TrimSpace(clusterName) == "" {
		t.Fatalf("status cluster.name expected non-empty generated value")
	}

	reULID := regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)
	if !reULID.MatchString(clusterName) {
		t.Fatalf("status cluster.name expected ULID-like format, got %q", clusterName)
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

var upstreamTimestampMillisPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`)

func requireUpstreamLikeTimestampMillis(t *testing.T, field, value string) {
	t.Helper()
	if !upstreamTimestampMillisPattern.MatchString(value) {
		t.Fatalf("%s expected upstream-like millisecond timestamp, got %q", field, value)
	}
}

func TestUpstreamParity_ReceiversConfiguredListOnly(t *testing.T) {
	configPath := writeTestConfigFile(t, `
route:
  receiver: "team-zeta"
  routes:
    - receiver: "team-db"
receivers:
  - name: "team-zeta"
  - name: "team-alpha"
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
	for _, receiver := range receivers {
		name, ok := receiver["name"].(string)
		if !ok || strings.TrimSpace(name) == "" {
			t.Fatalf("receiver.name expected non-empty string, got %v", receiver["name"])
		}
		names = append(names, name)
	}

	if names[0] != "team-zeta" || names[1] != "team-alpha" {
		t.Fatalf("unexpected receiver list order/content (must preserve config order): %v", names)
	}
}

func TestUpstreamParity_ReceiverFallbackUsesRouteReceiver(t *testing.T) {
	configPath := writeTestConfigFile(t, `
route:
  receiver: "team-ops"
receivers:
  - name: "team-ops"
`)
	t.Setenv(runtimeConfigFileEnv, configPath)

	mux := newPhase0TestMux(t)

	alertPayload := `[
		{
			"labels": {"alertname":"ReceiverFallbackParity"},
			"startsAt": "2026-02-27T00:00:00Z",
			"status": "firing"
		}
	]`
	postReq := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(alertPayload))
	postRec := httptest.NewRecorder()
	mux.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/alerts expected 200, got %d", postRec.Code)
	}

	alertsReq := httptest.NewRequest(http.MethodGet, "/api/v2/alerts?receiver=^team-ops$", nil)
	alertsRec := httptest.NewRecorder()
	mux.ServeHTTP(alertsRec, alertsReq)
	if alertsRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts expected 200, got %d", alertsRec.Code)
	}

	var alerts []map[string]any
	if err := json.Unmarshal(alertsRec.Body.Bytes(), &alerts); err != nil {
		t.Fatalf("failed to decode alerts response: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("expected exactly one alert with route.receiver fallback, got %d", len(alerts))
	}
	receivers, ok := alerts[0]["receivers"].([]any)
	if !ok || len(receivers) != 1 {
		t.Fatalf("alert receivers expected one item, got %v", alerts[0]["receivers"])
	}
	receiver, ok := receivers[0].(map[string]any)
	if !ok || receiver["name"] != "team-ops" {
		t.Fatalf("alert receiver fallback expected team-ops, got %v", receivers[0])
	}

	groupsReq := httptest.NewRequest(http.MethodGet, "/api/v2/alerts/groups?receiver=^team-ops$", nil)
	groupsRec := httptest.NewRecorder()
	mux.ServeHTTP(groupsRec, groupsReq)
	if groupsRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts/groups expected 200, got %d", groupsRec.Code)
	}

	var groups []map[string]any
	if err := json.Unmarshal(groupsRec.Body.Bytes(), &groups); err != nil {
		t.Fatalf("failed to decode alert groups response: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("expected one group with route.receiver fallback, got %d", len(groups))
	}
	groupReceiver, ok := groups[0]["receiver"].(map[string]any)
	if !ok || groupReceiver["name"] != "team-ops" {
		t.Fatalf("group receiver fallback expected team-ops, got %v", groups[0]["receiver"])
	}
}

func TestUpstreamParity_ReceiverRoutingUsesRouteMatchers(t *testing.T) {
	configPath := writeTestConfigFile(t, `
route:
  receiver: "team-default"
  routes:
    - receiver: "team-db"
      match:
        service: "db"
    - receiver: "team-api"
      match_re:
        service: "api-.+"
    - receiver: "team-critical"
      matchers:
        - severity="critical"
receivers:
  - name: "team-default"
  - name: "team-db"
  - name: "team-api"
  - name: "team-critical"
`)
	t.Setenv(runtimeConfigFileEnv, configPath)

	mux := newPhase0TestMux(t)

	runID := fmt.Sprintf("route-matchers-%d", time.Now().UnixNano())
	alertPayload := fmt.Sprintf(`[
		{
			"labels": {"alertname":"RouteMatcherDB","service":"db","runid":%q},
			"startsAt": "2026-02-27T00:00:00Z",
			"status": "firing"
		},
		{
			"labels": {"alertname":"RouteMatcherAPI","service":"api-prod","runid":%q},
			"startsAt": "2026-02-27T00:01:00Z",
			"status": "firing"
		},
		{
			"labels": {"alertname":"RouteMatcherCritical","severity":"critical","runid":%q},
			"startsAt": "2026-02-27T00:02:00Z",
			"status": "firing"
		},
		{
			"labels": {"alertname":"RouteMatcherDefault","runid":%q},
			"startsAt": "2026-02-27T00:03:00Z",
			"status": "firing"
		}
	]`, runID, runID, runID, runID)
	postReq := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(alertPayload))
	postRec := httptest.NewRecorder()
	mux.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/alerts expected 200, got %d", postRec.Code)
	}

	query := url.Values{}
	query.Add("filter", fmt.Sprintf(`runid="%s"`, runID))
	alertsReq := httptest.NewRequest(http.MethodGet, "/api/v2/alerts?"+query.Encode(), nil)
	alertsRec := httptest.NewRecorder()
	mux.ServeHTTP(alertsRec, alertsReq)
	if alertsRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts expected 200, got %d", alertsRec.Code)
	}

	var alerts []map[string]any
	if err := json.Unmarshal(alertsRec.Body.Bytes(), &alerts); err != nil {
		t.Fatalf("failed to decode alerts response: %v", err)
	}
	if len(alerts) != 4 {
		t.Fatalf("expected 4 routed alerts, got %d", len(alerts))
	}

	gotReceiversByAlert := make(map[string]string, len(alerts))
	for _, alert := range alerts {
		labels, _ := alert["labels"].(map[string]any)
		alertName, _ := labels["alertname"].(string)
		receivers, _ := alert["receivers"].([]any)
		if len(receivers) != 1 {
			t.Fatalf("alert %q expected one receiver, got %v", alertName, alert["receivers"])
		}
		receiverObj, _ := receivers[0].(map[string]any)
		receiverName, _ := receiverObj["name"].(string)
		gotReceiversByAlert[alertName] = receiverName
	}

	wantReceiversByAlert := map[string]string{
		"RouteMatcherDB":       "team-db",
		"RouteMatcherAPI":      "team-api",
		"RouteMatcherCritical": "team-critical",
		"RouteMatcherDefault":  "team-default",
	}
	for alertName, wantReceiver := range wantReceiversByAlert {
		if gotReceiversByAlert[alertName] != wantReceiver {
			t.Fatalf("alert %q expected receiver %q, got %q", alertName, wantReceiver, gotReceiversByAlert[alertName])
		}
	}

	dbFilter := url.Values{}
	dbFilter.Add("filter", fmt.Sprintf(`runid="%s"`, runID))
	dbFilter.Set("receiver", "^team-db$")
	dbReq := httptest.NewRequest(http.MethodGet, "/api/v2/alerts/groups?"+dbFilter.Encode(), nil)
	dbRec := httptest.NewRecorder()
	mux.ServeHTTP(dbRec, dbReq)
	if dbRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts/groups expected 200, got %d", dbRec.Code)
	}

	var groups []map[string]any
	if err := json.Unmarshal(dbRec.Body.Bytes(), &groups); err != nil {
		t.Fatalf("failed to decode groups response: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("expected one group for receiver team-db, got %d", len(groups))
	}
	groupReceiver, ok := groups[0]["receiver"].(map[string]any)
	if !ok || groupReceiver["name"] != "team-db" {
		t.Fatalf("group receiver expected team-db, got %v", groups[0]["receiver"])
	}
}

func TestUpstreamParity_ReceiverRoutingContinueAddsAdditionalReceiverGroups(t *testing.T) {
	configPath := writeTestConfigFile(t, `
route:
  receiver: "team-default"
  routes:
    - receiver: "team-db"
      match:
        service: "db"
      continue: true
    - receiver: "team-critical"
      matchers:
        - severity="critical"
receivers:
  - name: "team-default"
  - name: "team-db"
  - name: "team-critical"
`)
	t.Setenv(runtimeConfigFileEnv, configPath)

	mux := newPhase0TestMux(t)
	runID := fmt.Sprintf("route-continue-%d", time.Now().UnixNano())
	alertPayload := fmt.Sprintf(`[
		{
			"labels": {"alertname":"RouteContinueParity","service":"db","severity":"critical","runid":%q},
			"startsAt": "2026-02-27T00:00:00Z",
			"status": "firing"
		}
	]`, runID)
	postReq := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(alertPayload))
	postRec := httptest.NewRecorder()
	mux.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/alerts expected 200, got %d", postRec.Code)
	}

	query := url.Values{}
	query.Add("filter", fmt.Sprintf(`runid="%s"`, runID))
	alertsReq := httptest.NewRequest(http.MethodGet, "/api/v2/alerts?"+query.Encode(), nil)
	alertsRec := httptest.NewRecorder()
	mux.ServeHTTP(alertsRec, alertsReq)
	if alertsRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts expected 200, got %d", alertsRec.Code)
	}

	var alerts []map[string]any
	if err := json.Unmarshal(alertsRec.Body.Bytes(), &alerts); err != nil {
		t.Fatalf("failed to decode alerts response: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("expected one routed alert, got %d", len(alerts))
	}
	receivers, ok := alerts[0]["receivers"].([]any)
	if !ok || len(receivers) != 2 {
		t.Fatalf("alert receivers expected exactly two entries with continue=true, got %v", alerts[0]["receivers"])
	}
	for idx, expected := range []string{"team-db", "team-critical"} {
		receiverObj, _ := receivers[idx].(map[string]any)
		receiverName, _ := receiverObj["name"].(string)
		if receiverName != expected {
			t.Fatalf("alert receivers expected order [team-db team-critical], got %v", alerts[0]["receivers"])
		}
	}

	alertsDBReq := httptest.NewRequest(http.MethodGet, "/api/v2/alerts?"+query.Encode()+"&receiver=%5Eteam-db%24", nil)
	alertsDBRec := httptest.NewRecorder()
	mux.ServeHTTP(alertsDBRec, alertsDBReq)
	if alertsDBRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts receiver team-db expected 200, got %d", alertsDBRec.Code)
	}
	var alertsDB []map[string]any
	if err := json.Unmarshal(alertsDBRec.Body.Bytes(), &alertsDB); err != nil {
		t.Fatalf("failed to decode team-db alerts response: %v", err)
	}
	if len(alertsDB) != 1 {
		t.Fatalf("expected alert to match receiver=team-db filter, got %d", len(alertsDB))
	}

	alertsCriticalReq := httptest.NewRequest(http.MethodGet, "/api/v2/alerts?"+query.Encode()+"&receiver=%5Eteam-critical%24", nil)
	alertsCriticalRec := httptest.NewRecorder()
	mux.ServeHTTP(alertsCriticalRec, alertsCriticalReq)
	if alertsCriticalRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts receiver team-critical expected 200, got %d", alertsCriticalRec.Code)
	}
	var alertsCritical []map[string]any
	if err := json.Unmarshal(alertsCriticalRec.Body.Bytes(), &alertsCritical); err != nil {
		t.Fatalf("failed to decode team-critical alerts response: %v", err)
	}
	if len(alertsCritical) != 1 {
		t.Fatalf("expected alert to match receiver=team-critical filter, got %d", len(alertsCritical))
	}

	groupsReq := httptest.NewRequest(http.MethodGet, "/api/v2/alerts/groups?"+query.Encode(), nil)
	groupsRec := httptest.NewRecorder()
	mux.ServeHTTP(groupsRec, groupsReq)
	if groupsRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts/groups expected 200, got %d", groupsRec.Code)
	}

	var groups []map[string]any
	if err := json.Unmarshal(groupsRec.Body.Bytes(), &groups); err != nil {
		t.Fatalf("failed to decode groups response: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("expected two receiver groups with continue=true, got %d", len(groups))
	}
	groupReceiverSet := make(map[string]struct{}, len(groups))
	for _, group := range groups {
		groupReceiver, _ := group["receiver"].(map[string]any)
		groupReceiverName, _ := groupReceiver["name"].(string)
		groupReceiverSet[groupReceiverName] = struct{}{}

		groupAlerts, _ := group["alerts"].([]any)
		if len(groupAlerts) != 1 {
			t.Fatalf("expected one alert per continue group, got %d", len(groupAlerts))
		}
		groupAlert, _ := groupAlerts[0].(map[string]any)
		groupAlertReceivers, _ := groupAlert["receivers"].([]any)
		if len(groupAlertReceivers) != 2 {
			t.Fatalf("nested alert receivers expected two entries, got %v", groupAlert["receivers"])
		}
		for idx, expected := range []string{"team-critical", "team-db"} {
			receiverObj, _ := groupAlertReceivers[idx].(map[string]any)
			receiverName, _ := receiverObj["name"].(string)
			if receiverName != expected {
				t.Fatalf("nested alert receivers expected order [team-critical team-db], got %v", groupAlert["receivers"])
			}
		}
	}
	for _, expected := range []string{"team-db", "team-critical"} {
		if _, ok := groupReceiverSet[expected]; !ok {
			t.Fatalf("expected groups to include receiver %q, got %v", expected, groupReceiverSet)
		}
	}
}

func TestUpstreamParity_ReceiverRoutingWithoutContinueStopsAtFirstRoute(t *testing.T) {
	configPath := writeTestConfigFile(t, `
route:
  receiver: "team-default"
  routes:
    - receiver: "team-db"
      match:
        service: "db"
    - receiver: "team-critical"
      matchers:
        - severity="critical"
receivers:
  - name: "team-default"
  - name: "team-db"
  - name: "team-critical"
`)
	t.Setenv(runtimeConfigFileEnv, configPath)

	mux := newPhase0TestMux(t)
	runID := fmt.Sprintf("route-no-continue-%d", time.Now().UnixNano())
	alertPayload := fmt.Sprintf(`[
		{
			"labels": {"alertname":"RouteNoContinueParity","service":"db","severity":"critical","runid":%q},
			"startsAt": "2026-02-27T00:00:00Z",
			"status": "firing"
		}
	]`, runID)
	postReq := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(alertPayload))
	postRec := httptest.NewRecorder()
	mux.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/alerts expected 200, got %d", postRec.Code)
	}

	query := url.Values{}
	query.Add("filter", fmt.Sprintf(`runid="%s"`, runID))
	alertsReq := httptest.NewRequest(http.MethodGet, "/api/v2/alerts?"+query.Encode(), nil)
	alertsRec := httptest.NewRecorder()
	mux.ServeHTTP(alertsRec, alertsReq)
	if alertsRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts expected 200, got %d", alertsRec.Code)
	}

	var alerts []map[string]any
	if err := json.Unmarshal(alertsRec.Body.Bytes(), &alerts); err != nil {
		t.Fatalf("failed to decode alerts response: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("expected one alert, got %d", len(alerts))
	}
	receivers, ok := alerts[0]["receivers"].([]any)
	if !ok || len(receivers) != 1 {
		t.Fatalf("alert receivers expected one entry without continue, got %v", alerts[0]["receivers"])
	}
	receiverObj, _ := receivers[0].(map[string]any)
	receiverName, _ := receiverObj["name"].(string)
	if receiverName != "team-db" {
		t.Fatalf("expected first matching route receiver team-db, got %q", receiverName)
	}

	alertsCriticalReq := httptest.NewRequest(http.MethodGet, "/api/v2/alerts?"+query.Encode()+"&receiver=%5Eteam-critical%24", nil)
	alertsCriticalRec := httptest.NewRecorder()
	mux.ServeHTTP(alertsCriticalRec, alertsCriticalReq)
	if alertsCriticalRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts receiver team-critical expected 200, got %d", alertsCriticalRec.Code)
	}
	var alertsCritical []map[string]any
	if err := json.Unmarshal(alertsCriticalRec.Body.Bytes(), &alertsCritical); err != nil {
		t.Fatalf("failed to decode team-critical alerts response: %v", err)
	}
	if len(alertsCritical) != 0 {
		t.Fatalf("expected no alerts for receiver=team-critical without continue, got %d", len(alertsCritical))
	}

	groupsReq := httptest.NewRequest(http.MethodGet, "/api/v2/alerts/groups?"+query.Encode(), nil)
	groupsRec := httptest.NewRecorder()
	mux.ServeHTTP(groupsRec, groupsReq)
	if groupsRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts/groups expected 200, got %d", groupsRec.Code)
	}

	var groups []map[string]any
	if err := json.Unmarshal(groupsRec.Body.Bytes(), &groups); err != nil {
		t.Fatalf("failed to decode groups response: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("expected one group without continue, got %d", len(groups))
	}
	groupReceiver, _ := groups[0]["receiver"].(map[string]any)
	groupReceiverName, _ := groupReceiver["name"].(string)
	if groupReceiverName != "team-db" {
		t.Fatalf("expected group receiver team-db without continue, got %q", groupReceiverName)
	}
}

func TestUpstreamParity_ReceiverLabelDoesNotOverrideRouting(t *testing.T) {
	configPath := writeTestConfigFile(t, `
route:
  receiver: "team-default"
receivers:
  - name: "team-default"
  - name: "team-ops"
`)
	t.Setenv(runtimeConfigFileEnv, configPath)

	mux := newPhase0TestMux(t)
	runID := fmt.Sprintf("receiver-label-no-override-%d", time.Now().UnixNano())
	alertPayload := fmt.Sprintf(`[
		{
			"labels": {"alertname":"ReceiverLabelNoOverride","receiver":"team-ops","runid":%q},
			"startsAt": "2026-02-27T00:00:00Z",
			"status": "firing"
		}
	]`, runID)
	postReq := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(alertPayload))
	postRec := httptest.NewRecorder()
	mux.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/alerts expected 200, got %d", postRec.Code)
	}

	query := url.Values{}
	query.Add("filter", fmt.Sprintf(`runid="%s"`, runID))
	alertsReq := httptest.NewRequest(http.MethodGet, "/api/v2/alerts?"+query.Encode(), nil)
	alertsRec := httptest.NewRecorder()
	mux.ServeHTTP(alertsRec, alertsReq)
	if alertsRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts expected 200, got %d", alertsRec.Code)
	}

	var alerts []map[string]any
	if err := json.Unmarshal(alertsRec.Body.Bytes(), &alerts); err != nil {
		t.Fatalf("failed to decode alerts response: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("expected one alert, got %d", len(alerts))
	}
	receivers, ok := alerts[0]["receivers"].([]any)
	if !ok || len(receivers) != 1 {
		t.Fatalf("alert receivers expected one entry, got %v", alerts[0]["receivers"])
	}
	receiverObj, _ := receivers[0].(map[string]any)
	receiverName, _ := receiverObj["name"].(string)
	if receiverName != "team-default" {
		t.Fatalf("expected route-based receiver team-default (not labels.receiver), got %q", receiverName)
	}

	alertsOpsReq := httptest.NewRequest(http.MethodGet, "/api/v2/alerts?"+query.Encode()+"&receiver=%5Eteam-ops%24", nil)
	alertsOpsRec := httptest.NewRecorder()
	mux.ServeHTTP(alertsOpsRec, alertsOpsReq)
	if alertsOpsRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts receiver team-ops expected 200, got %d", alertsOpsRec.Code)
	}
	var alertsOps []map[string]any
	if err := json.Unmarshal(alertsOpsRec.Body.Bytes(), &alertsOps); err != nil {
		t.Fatalf("failed to decode team-ops filtered alerts response: %v", err)
	}
	if len(alertsOps) != 0 {
		t.Fatalf("expected no alerts for receiver=team-ops without routing rule, got %d", len(alertsOps))
	}
}

func TestUpstreamParity_TimestampsUseMillisecondPrecision(t *testing.T) {
	mux := newPhase0TestMux(t)

	alertPayload := `[
		{
			"labels": {"alertname":"TimestampMillisParity"},
			"startsAt": "2026-02-25T00:00:00Z",
			"endsAt": "2026-02-25T00:05:00Z",
			"status": "firing"
		}
	]`
	alertPostReq := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(alertPayload))
	alertPostRec := httptest.NewRecorder()
	mux.ServeHTTP(alertPostRec, alertPostReq)
	if alertPostRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/alerts expected 200, got %d", alertPostRec.Code)
	}

	alertQuery := url.Values{}
	alertQuery.Add("filter", `alertname="TimestampMillisParity"`)
	alertGetReq := httptest.NewRequest(http.MethodGet, "/api/v2/alerts?"+alertQuery.Encode(), nil)
	alertGetRec := httptest.NewRecorder()
	mux.ServeHTTP(alertGetRec, alertGetReq)
	if alertGetRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts expected 200, got %d", alertGetRec.Code)
	}

	var alerts []map[string]any
	if err := json.Unmarshal(alertGetRec.Body.Bytes(), &alerts); err != nil {
		t.Fatalf("failed to decode alerts payload: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("expected exactly one alert, got %d", len(alerts))
	}

	alertStartsAt, _ := alerts[0]["startsAt"].(string)
	alertEndsAt, _ := alerts[0]["endsAt"].(string)
	alertUpdatedAt, _ := alerts[0]["updatedAt"].(string)
	requireUpstreamLikeTimestampMillis(t, "alert startsAt", alertStartsAt)
	requireUpstreamLikeTimestampMillis(t, "alert endsAt", alertEndsAt)
	requireUpstreamLikeTimestampMillis(t, "alert updatedAt", alertUpdatedAt)

	now := time.Now().UTC()
	silencePayload := fmt.Sprintf(`{
		"matchers": [{"name":"alertname","value":"TimestampMillisParity","isRegex":false}],
		"startsAt": %q,
		"endsAt": %q,
		"createdBy": "parity-suite",
		"comment": "timestamp-millis-parity"
	}`, now.Add(-1*time.Minute).Format(time.RFC3339), now.Add(10*time.Minute).Format(time.RFC3339))
	silencePostReq := httptest.NewRequest(http.MethodPost, "/api/v2/silences", bytes.NewBufferString(silencePayload))
	silencePostRec := httptest.NewRecorder()
	mux.ServeHTTP(silencePostRec, silencePostReq)
	if silencePostRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/silences expected 200, got %d", silencePostRec.Code)
	}

	silenceFilter := url.Values{}
	silenceFilter.Add("filter", `alertname="TimestampMillisParity"`)
	silenceGetReq := httptest.NewRequest(http.MethodGet, "/api/v2/silences?"+silenceFilter.Encode(), nil)
	silenceGetRec := httptest.NewRecorder()
	mux.ServeHTTP(silenceGetRec, silenceGetReq)
	if silenceGetRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/silences expected 200, got %d", silenceGetRec.Code)
	}

	var silences []map[string]any
	if err := json.Unmarshal(silenceGetRec.Body.Bytes(), &silences); err != nil {
		t.Fatalf("failed to decode silences payload: %v", err)
	}
	if len(silences) != 1 {
		t.Fatalf("expected exactly one silence, got %d", len(silences))
	}

	silenceStartsAt, _ := silences[0]["startsAt"].(string)
	silenceEndsAt, _ := silences[0]["endsAt"].(string)
	silenceUpdatedAt, _ := silences[0]["updatedAt"].(string)
	requireUpstreamLikeTimestampMillis(t, "silence startsAt", silenceStartsAt)
	requireUpstreamLikeTimestampMillis(t, "silence endsAt", silenceEndsAt)
	requireUpstreamLikeTimestampMillis(t, "silence updatedAt", silenceUpdatedAt)

	statusReq := httptest.NewRequest(http.MethodGet, "/api/v2/status", nil)
	statusRec := httptest.NewRecorder()
	mux.ServeHTTP(statusRec, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/status expected 200, got %d", statusRec.Code)
	}

	var statusPayload map[string]any
	if err := json.Unmarshal(statusRec.Body.Bytes(), &statusPayload); err != nil {
		t.Fatalf("failed to decode status payload: %v", err)
	}
	statusUptime, _ := statusPayload["uptime"].(string)
	requireUpstreamLikeTimestampMillis(t, "status uptime", statusUptime)
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

func TestUpstreamParity_ReloadSuccessHasEmptyBody(t *testing.T) {
	configPath := writeTestConfigFile(t, `
route:
  receiver: "initial-receiver"
`)
	t.Setenv(runtimeConfigFileEnv, configPath)

	mux := newPhase0TestMux(t)

	req := httptest.NewRequest(http.MethodPost, "/-/reload", bytes.NewBufferString(`{}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /-/reload expected 200 for valid config, got %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("reload success expected empty body, got %q", rec.Body.String())
	}
}

func TestUpstreamParity_DebugPprofContract(t *testing.T) {
	mux := newPhase0TestMux(t)

	getReq := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
	getRec := httptest.NewRecorder()
	mux.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET /debug/pprof/ expected 200, got %d", getRec.Code)
	}
	if !strings.Contains(getRec.Body.String(), "Types of profiles available") {
		t.Fatalf("GET /debug/pprof/ expected pprof index body, got %q", getRec.Body.String())
	}

	postReq := httptest.NewRequest(http.MethodPost, "/debug/pprof/", bytes.NewBufferString(`{}`))
	postRec := httptest.NewRecorder()
	mux.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /debug/pprof/ expected 405, got %d", postRec.Code)
	}
}

func TestUpstreamParity_UpstreamStaticCompatibilityPaths(t *testing.T) {
	mux := newPhase0TestMux(t)

	scriptReq := httptest.NewRequest(http.MethodGet, "/script.js", nil)
	scriptRec := httptest.NewRecorder()
	mux.ServeHTTP(scriptRec, scriptReq)
	if scriptRec.Code != http.StatusOK {
		t.Fatalf("GET /script.js expected 200, got %d", scriptRec.Code)
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

func TestUpstreamParity_AlertsStateFiltersMatrix(t *testing.T) {
	mux := newPhase0TestMux(t)

	alertPayload := `[
		{
			"labels": {"alertname":"ActiveParity","service":"api"},
			"startsAt": "2026-02-25T00:00:00Z",
			"status": "firing"
		},
		{
			"labels": {"alertname":"SilencedParity","service":"api"},
			"startsAt": "2026-02-25T00:01:00Z",
			"status": "firing"
		},
		{
			"labels": {"alertname":"InhibitedParity","service":"api"},
			"annotations": {"inhibitedBy":"root-cause-fp"},
			"startsAt": "2026-02-25T00:02:00Z",
			"status": "firing"
		},
		{
			"labels": {"alertname":"ResolvedParity","service":"api"},
			"startsAt": "2026-02-25T00:03:00Z",
			"endsAt": "2026-02-25T00:04:00Z",
			"status": "resolved"
		}
	]`
	alertReq := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(alertPayload))
	alertRec := httptest.NewRecorder()
	mux.ServeHTTP(alertRec, alertReq)
	if alertRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/alerts expected 200, got %d", alertRec.Code)
	}

	now := time.Now().UTC()
	silencePayload := fmt.Sprintf(`{
		"matchers": [{"name":"alertname","value":"SilencedParity","isRegex":false}],
		"startsAt": %q,
		"endsAt": %q,
		"createdBy": "parity-suite",
		"comment": "silence for parity suite"
	}`, now.Add(-1*time.Minute).Format(time.RFC3339), now.Add(59*time.Minute).Format(time.RFC3339))
	silenceReq := httptest.NewRequest(http.MethodPost, "/api/v2/silences", bytes.NewBufferString(silencePayload))
	silenceRec := httptest.NewRecorder()
	mux.ServeHTTP(silenceRec, silenceReq)
	if silenceRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/silences expected 200, got %d", silenceRec.Code)
	}

	type stateFilterCase struct {
		name           string
		path           string
		expectedAlerts []string
	}

	cases := []stateFilterCase{
		{
			name:           "active-only",
			path:           "/api/v2/alerts?active=true&silenced=false&inhibited=false&unprocessed=false&resolved=true",
			expectedAlerts: []string{"ActiveParity"},
		},
		{
			name:           "silenced-only",
			path:           "/api/v2/alerts?active=false&silenced=true&inhibited=false&unprocessed=false&resolved=true",
			expectedAlerts: []string{"SilencedParity"},
		},
		{
			name:           "inhibited-only",
			path:           "/api/v2/alerts?active=false&silenced=false&inhibited=true&unprocessed=false&resolved=true",
			expectedAlerts: []string{"InhibitedParity"},
		},
		{
			name:           "unprocessed-only",
			path:           "/api/v2/alerts?active=false&silenced=false&inhibited=false&unprocessed=true&resolved=true",
			expectedAlerts: []string{"ResolvedParity"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s expected 200, got %d", tc.path, rec.Code)
			}

			var alerts []map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &alerts); err != nil {
				t.Fatalf("failed to decode alerts response: %v", err)
			}

			if len(alerts) != len(tc.expectedAlerts) {
				t.Fatalf("expected %d alerts, got %d", len(tc.expectedAlerts), len(alerts))
			}

			got := make(map[string]struct{}, len(alerts))
			for _, alert := range alerts {
				labels, _ := alert["labels"].(map[string]any)
				name, _ := labels["alertname"].(string)
				got[name] = struct{}{}
			}

			for _, expected := range tc.expectedAlerts {
				if _, ok := got[expected]; !ok {
					t.Fatalf("expected alert %q in result set, got %v", expected, got)
				}
			}
		})
	}
}

func TestUpstreamParity_InvalidStateFlagsAreIgnored(t *testing.T) {
	mux := newPhase0TestMux(t)

	payload := `[
		{
			"labels": {"alertname":"InvalidFlagsParity","service":"api","namespace":"prod"},
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

	alertsReq := httptest.NewRequest(http.MethodGet, "/api/v2/alerts?active=not-bool", nil)
	alertsRec := httptest.NewRecorder()
	mux.ServeHTTP(alertsRec, alertsReq)
	if alertsRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts with invalid active flag expected 200, got %d", alertsRec.Code)
	}
	var alerts []map[string]any
	if err := json.Unmarshal(alertsRec.Body.Bytes(), &alerts); err != nil {
		t.Fatalf("failed to decode alerts response: %v", err)
	}
	if len(alerts) != 0 {
		t.Fatalf("invalid active flag expected upstream-like false fallback, got %d alerts", len(alerts))
	}

	silencedReq := httptest.NewRequest(http.MethodGet, "/api/v2/alerts?silenced=not-bool", nil)
	silencedRec := httptest.NewRecorder()
	mux.ServeHTTP(silencedRec, silencedReq)
	if silencedRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts with invalid silenced flag expected 200, got %d", silencedRec.Code)
	}
	var silencedFiltered []map[string]any
	if err := json.Unmarshal(silencedRec.Body.Bytes(), &silencedFiltered); err != nil {
		t.Fatalf("failed to decode silenced-filter response: %v", err)
	}
	if len(silencedFiltered) != 1 {
		t.Fatalf("invalid silenced flag should not hide active alerts, got %d", len(silencedFiltered))
	}

	groupsReq := httptest.NewRequest(http.MethodGet, "/api/v2/alerts/groups?active=not-bool&silenced=not-bool&inhibited=not-bool&muted=not-bool", nil)
	groupsRec := httptest.NewRecorder()
	mux.ServeHTTP(groupsRec, groupsReq)
	if groupsRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts/groups with invalid state flags expected 200, got %d", groupsRec.Code)
	}
	var groups []map[string]any
	if err := json.Unmarshal(groupsRec.Body.Bytes(), &groups); err != nil {
		t.Fatalf("failed to decode groups response: %v", err)
	}
	if len(groups) != 0 {
		t.Fatalf("all invalid group state flags expected upstream-like false fallback, got %d groups", len(groups))
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
	configPath := writeTestConfigFile(t, `
route:
  receiver: "team-default"
  routes:
    - receiver: "team-ops"
      match:
        receiver: "team-ops"
    - receiver: "team-sre"
      match:
        receiver: "team-sre"
receivers:
  - name: "team-default"
  - name: "team-ops"
  - name: "team-sre"
`)
	t.Setenv(runtimeConfigFileEnv, configPath)

	mux := newPhase0TestMux(t)

	payload := `[
		{
			"labels": {"alertname":"GroupParityA","service":"api","namespace":"prod","receiver":"team-ops"},
			"annotations": {"summary":"a"},
			"startsAt": "2026-02-25T00:00:00Z",
			"status": "firing"
		},
		{
			"labels": {"alertname":"GroupParityB","service":"api","namespace":"prod","receiver":"team-sre"},
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

	filterQuery := url.Values{}
	filterQuery.Set("receiver", "^team-ops$")
	req := httptest.NewRequest(http.MethodGet, "/api/v2/alerts/groups?"+filterQuery.Encode(), nil)
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
		t.Fatalf("expected 1 filtered group, got %d", len(groups))
	}

	groupReceiver, ok := groups[0]["receiver"].(map[string]any)
	if !ok {
		t.Fatalf("group receiver expected object, got %T", groups[0]["receiver"])
	}
	if groupReceiver["name"] != "team-ops" {
		t.Fatalf("group receiver.name expected team-ops, got %v", groupReceiver["name"])
	}

	alerts, ok := groups[0]["alerts"].([]any)
	if !ok || len(alerts) != 1 {
		t.Fatalf("group alerts expected array with one entry, got %v", groups[0]["alerts"])
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
}

func TestUpstreamParity_ReceiverRegexUsesFullMatchSemantics(t *testing.T) {
	configPath := writeTestConfigFile(t, `
route:
  receiver: "team-default"
  routes:
    - receiver: "team-ops"
      match:
        receiver: "team-ops"
receivers:
  - name: "team-default"
  - name: "team-ops"
`)
	t.Setenv(runtimeConfigFileEnv, configPath)

	mux := newPhase0TestMux(t)

	payload := `[
		{
			"labels": {"alertname":"ReceiverRegexFullMatchParity","receiver":"team-ops"},
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

	testCases := []struct {
		name        string
		path        string
		wantEntries int
	}{
		{
			name:        "alerts exact full match",
			path:        "/api/v2/alerts?receiver=team-ops",
			wantEntries: 1,
		},
		{
			name:        "alerts partial does not match",
			path:        "/api/v2/alerts?receiver=ops",
			wantEntries: 0,
		},
		{
			name:        "alerts explicit substring regex matches",
			path:        "/api/v2/alerts?receiver=.*ops.*",
			wantEntries: 1,
		},
		{
			name:        "groups exact full match",
			path:        "/api/v2/alerts/groups?receiver=team-ops",
			wantEntries: 1,
		},
		{
			name:        "groups partial does not match",
			path:        "/api/v2/alerts/groups?receiver=ops",
			wantEntries: 0,
		},
		{
			name:        "groups explicit substring regex matches",
			path:        "/api/v2/alerts/groups?receiver=.*ops.*",
			wantEntries: 1,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s expected 200, got %d", tc.path, rec.Code)
			}

			var payload []map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
				t.Fatalf("failed to decode response for %s: %v", tc.path, err)
			}
			if len(payload) != tc.wantEntries {
				t.Fatalf("GET %s expected %d entries, got %d", tc.path, tc.wantEntries, len(payload))
			}
		})
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

func TestUpstreamParity_AlertGroupsWithGroupByAllUseFullLabelSet(t *testing.T) {
	configPath := writeTestConfigFile(t, `
route:
  receiver: "default"
  group_by: ["..."]
receivers:
  - name: "default"
`)
	t.Setenv(runtimeConfigFileEnv, configPath)

	mux := newPhase0TestMux(t)

	payload := `[
		{
			"labels": {"alertname":"GroupByAllParity","service":"api","namespace":"prod","instance":"api-1"},
			"startsAt": "2026-02-25T00:00:00Z",
			"status": "firing"
		},
		{
			"labels": {"alertname":"GroupByAllParity","service":"api","namespace":"prod","instance":"api-2"},
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
	if len(groups) != 2 {
		t.Fatalf("expected two groups with route.group_by=['...'], got %d", len(groups))
	}

	expectedInstances := map[string]struct{}{
		"api-1": {},
		"api-2": {},
	}
	for i, group := range groups {
		labels, ok := group["labels"].(map[string]any)
		if !ok {
			t.Fatalf("group[%d] labels expected object, got %T", i, group["labels"])
		}

		instance, _ := labels["instance"].(string)
		if _, ok := expectedInstances[instance]; !ok {
			t.Fatalf("group[%d] labels.instance expected one of api-1/api-2, got %q", i, instance)
		}
		delete(expectedInstances, instance)

		if labels["alertname"] != "GroupByAllParity" {
			t.Fatalf("group[%d] labels.alertname expected GroupByAllParity, got %v", i, labels["alertname"])
		}
		if labels["service"] != "api" {
			t.Fatalf("group[%d] labels.service expected api, got %v", i, labels["service"])
		}
		if labels["namespace"] != "prod" {
			t.Fatalf("group[%d] labels.namespace expected prod, got %v", i, labels["namespace"])
		}
		if len(labels) != 4 {
			t.Fatalf("group[%d] labels expected full alert label set size=4, got %v", i, labels)
		}

		alerts, ok := group["alerts"].([]any)
		if !ok || len(alerts) != 1 {
			t.Fatalf("group[%d] alerts expected array with one alert, got %v", i, group["alerts"])
		}
		alert, ok := alerts[0].(map[string]any)
		if !ok {
			t.Fatalf("group[%d] alert expected object, got %T", i, alerts[0])
		}
		alertLabels, ok := alert["labels"].(map[string]any)
		if !ok {
			t.Fatalf("group[%d] alert.labels expected object, got %T", i, alert["labels"])
		}
		if alertLabels["instance"] != instance {
			t.Fatalf("group[%d] alert.labels.instance expected %q, got %v", i, instance, alertLabels["instance"])
		}
		if len(alertLabels) != 4 {
			t.Fatalf("group[%d] alert.labels expected full label set size=4, got %v", i, alertLabels)
		}
	}

	if len(expectedInstances) != 0 {
		t.Fatalf("missing groups for instances: %v", expectedInstances)
	}
}

func TestUpstreamParity_AlertsAndGroupsInvalidQueryErrorPayloadIsJSONString(t *testing.T) {
	mux := newPhase0TestMux(t)

	cases := []struct {
		name    string
		path    string
		message string
	}{
		{
			name:    "alerts invalid receiver",
			path:    "/api/v2/alerts?receiver=[",
			message: "failed to parse receiver param: error parsing regexp: missing closing ]: `[)$`",
		},
		{
			name:    "alerts invalid filter",
			path:    "/api/v2/alerts?filter=broken-matcher",
			message: "bad matcher format: broken-matcher",
		},
		{
			name:    "groups invalid receiver",
			path:    "/api/v2/alerts/groups?receiver=[",
			message: "failed to parse receiver param: error parsing regexp: missing closing ]: `[)$`",
		},
		{
			name:    "groups invalid filter",
			path:    "/api/v2/alerts/groups?filter=broken-matcher",
			message: "bad matcher format: broken-matcher",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("GET %s expected 400, got %d", tc.path, rec.Code)
			}

			var payload string
			if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
				t.Fatalf("GET %s expected JSON string body, got %q (%v)", tc.path, rec.Body.String(), err)
			}
			if payload != tc.message {
				t.Fatalf("GET %s expected message %q, got %q", tc.path, tc.message, payload)
			}
		})
	}
}

func TestUpstreamParity_PostAlertsErrorPayloadContracts(t *testing.T) {
	mux := newPhase0TestMux(t)

	invalidJSONReq := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(`{}`))
	invalidJSONRec := httptest.NewRecorder()
	mux.ServeHTTP(invalidJSONRec, invalidJSONReq)
	if invalidJSONRec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/v2/alerts invalid JSON expected 400, got %d", invalidJSONRec.Code)
	}
	var invalidJSONPayload map[string]any
	if err := json.Unmarshal(invalidJSONRec.Body.Bytes(), &invalidJSONPayload); err != nil {
		t.Fatalf("invalid JSON expected object payload, got %q (%v)", invalidJSONRec.Body.String(), err)
	}
	if invalidJSONPayload["code"] != float64(http.StatusBadRequest) {
		t.Fatalf("invalid JSON expected code=400, got %v", invalidJSONPayload["code"])
	}
	const expectedInvalidJSONMessage = `parsing alerts body from "" failed, because json: cannot unmarshal object into Go value of type models.PostableAlerts`
	if invalidJSONPayload["message"] != expectedInvalidJSONMessage {
		t.Fatalf("invalid JSON expected message %q, got %v", expectedInvalidJSONMessage, invalidJSONPayload["message"])
	}

	invalidStartsAtReq := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(`[{"labels":{"alertname":"A"},"startsAt":"not-time"}]`))
	invalidStartsAtRec := httptest.NewRecorder()
	mux.ServeHTTP(invalidStartsAtRec, invalidStartsAtReq)
	if invalidStartsAtRec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/v2/alerts invalid startsAt expected 400, got %d", invalidStartsAtRec.Code)
	}
	var invalidStartsAtPayload map[string]any
	if err := json.Unmarshal(invalidStartsAtRec.Body.Bytes(), &invalidStartsAtPayload); err != nil {
		t.Fatalf("invalid startsAt expected object payload, got %q (%v)", invalidStartsAtRec.Body.String(), err)
	}
	if invalidStartsAtPayload["code"] != float64(http.StatusBadRequest) {
		t.Fatalf("invalid startsAt expected code=400, got %v", invalidStartsAtPayload["code"])
	}
	msg, _ := invalidStartsAtPayload["message"].(string)
	if !strings.Contains(msg, `as "2006-01-02"`) {
		t.Fatalf("invalid startsAt expected upstream-like date parse message, got %q", msg)
	}

	missingLabelsReq := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(`[{}]`))
	missingLabelsRec := httptest.NewRecorder()
	mux.ServeHTTP(missingLabelsRec, missingLabelsReq)
	if missingLabelsRec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST /api/v2/alerts missing labels expected 422, got %d", missingLabelsRec.Code)
	}
	var missingLabelsPayload map[string]any
	if err := json.Unmarshal(missingLabelsRec.Body.Bytes(), &missingLabelsPayload); err != nil {
		t.Fatalf("missing labels expected object payload, got %q (%v)", missingLabelsRec.Body.String(), err)
	}
	if missingLabelsPayload["code"] != float64(602) {
		t.Fatalf("missing labels expected code=602, got %v", missingLabelsPayload["code"])
	}

	emptyLabelsReq := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(`[{"labels":{}}]`))
	emptyLabelsRec := httptest.NewRecorder()
	mux.ServeHTTP(emptyLabelsRec, emptyLabelsReq)
	if emptyLabelsRec.Code != http.StatusBadRequest {
		t.Fatalf("POST /api/v2/alerts empty labels expected 400, got %d", emptyLabelsRec.Code)
	}
	var emptyLabelsPayload string
	if err := json.Unmarshal(emptyLabelsRec.Body.Bytes(), &emptyLabelsPayload); err != nil {
		t.Fatalf("empty labels expected JSON string payload, got %q (%v)", emptyLabelsRec.Body.String(), err)
	}
	if strings.TrimSpace(emptyLabelsPayload) == "" {
		t.Fatalf("empty labels expected non-empty message")
	}

	invalidGeneratorReq := httptest.NewRequest(http.MethodPost, "/api/v2/alerts", bytes.NewBufferString(`[{"labels":{"alertname":"A"},"generatorURL":":bad"}]`))
	invalidGeneratorRec := httptest.NewRecorder()
	mux.ServeHTTP(invalidGeneratorRec, invalidGeneratorReq)
	if invalidGeneratorRec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST /api/v2/alerts invalid generatorURL expected 422, got %d", invalidGeneratorRec.Code)
	}
	var invalidGeneratorPayload map[string]any
	if err := json.Unmarshal(invalidGeneratorRec.Body.Bytes(), &invalidGeneratorPayload); err != nil {
		t.Fatalf("invalid generatorURL expected object payload, got %q (%v)", invalidGeneratorRec.Body.String(), err)
	}
	if invalidGeneratorPayload["code"] != float64(601) {
		t.Fatalf("invalid generatorURL expected code=601, got %v", invalidGeneratorPayload["code"])
	}
}

func TestUpstreamParity_PostAlertsDateOnlyTimestampsAreAccepted(t *testing.T) {
	mux := newPhase0TestMux(t)

	postReq := httptest.NewRequest(
		http.MethodPost,
		"/api/v2/alerts",
		bytes.NewBufferString(`[{"labels":{"alertname":"DateOnlyParity"},"startsAt":"2099-02-26","endsAt":"2099-03-01"}]`),
	)
	postRec := httptest.NewRecorder()
	mux.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusOK {
		t.Fatalf("POST /api/v2/alerts with date-only timestamps expected 200, got %d", postRec.Code)
	}

	query := url.Values{}
	query.Add("filter", `alertname="DateOnlyParity"`)
	getReq := httptest.NewRequest(http.MethodGet, "/api/v2/alerts?"+query.Encode(), nil)
	getRec := httptest.NewRecorder()
	mux.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET /api/v2/alerts expected 200, got %d", getRec.Code)
	}

	var alerts []map[string]any
	if err := json.Unmarshal(getRec.Body.Bytes(), &alerts); err != nil {
		t.Fatalf("failed to decode alerts payload: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("expected exactly one alert, got %d", len(alerts))
	}

	startsAt, _ := alerts[0]["startsAt"].(string)
	endsAt, _ := alerts[0]["endsAt"].(string)
	if !strings.HasPrefix(startsAt, "2099-02-26T00:00:00") {
		t.Fatalf("expected normalized date-only startsAt, got %q", startsAt)
	}
	if !strings.HasPrefix(endsAt, "2099-03-01T00:00:00") {
		t.Fatalf("expected normalized date-only endsAt, got %q", endsAt)
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
	if len(filtered) != 1 {
		t.Fatalf("expected exactly one filtered silence, got %d", len(filtered))
	}
	if filtered[0]["comment"] != "active-soon-parity" {
		t.Fatalf("expected filtered silence comment active-soon-parity, got %v", filtered[0]["comment"])
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

func TestUpstreamParity_SilencesInvalidFilterErrorPayloadIsJSONString(t *testing.T) {
	mux := newPhase0TestMux(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/silences?filter=broken-matcher", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("GET /api/v2/silences invalid filter expected 400, got %d", rec.Code)
	}

	var payload string
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("invalid filter error expected JSON string body, got %q (%v)", rec.Body.String(), err)
	}
	const expected = "bad matcher format: broken-matcher"
	if payload != expected {
		t.Fatalf("invalid filter error expected message %q, got %q", expected, payload)
	}
}

func TestUpstreamParity_SilenceByIDInvalidUUIDReturns422WithCodeMessage(t *testing.T) {
	mux := newPhase0TestMux(t)

	getReq := httptest.NewRequest(http.MethodGet, "/api/v2/silence/not-a-uuid", nil)
	getRec := httptest.NewRecorder()
	mux.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("GET /api/v2/silence/{id} invalid uuid expected 422, got %d", getRec.Code)
	}
	var getPayload map[string]any
	if err := json.Unmarshal(getRec.Body.Bytes(), &getPayload); err != nil {
		t.Fatalf("GET invalid uuid expected JSON object payload, got %q (%v)", getRec.Body.String(), err)
	}
	if getPayload["code"] != float64(601) {
		t.Fatalf("GET invalid uuid expected code=601, got %v", getPayload["code"])
	}
	if message, _ := getPayload["message"].(string); !strings.Contains(message, "silenceID in path must be of type uuid") {
		t.Fatalf("GET invalid uuid expected upstream-like message, got %v", getPayload["message"])
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v2/silence/not-a-uuid", nil)
	deleteRec := httptest.NewRecorder()
	mux.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("DELETE /api/v2/silence/{id} invalid uuid expected 422, got %d", deleteRec.Code)
	}
	var deletePayload map[string]any
	if err := json.Unmarshal(deleteRec.Body.Bytes(), &deletePayload); err != nil {
		t.Fatalf("DELETE invalid uuid expected JSON object payload, got %q (%v)", deleteRec.Body.String(), err)
	}
	if deletePayload["code"] != float64(601) {
		t.Fatalf("DELETE invalid uuid expected code=601, got %v", deletePayload["code"])
	}
	if message, _ := deletePayload["message"].(string); !strings.Contains(message, "silenceID in path must be of type uuid") {
		t.Fatalf("DELETE invalid uuid expected upstream-like message, got %v", deletePayload["message"])
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

	invalidReq := httptest.NewRequest(http.MethodPost, "/api/v2/silences", bytes.NewBufferString(`{}`))
	invalidRec := httptest.NewRecorder()
	mux.ServeHTTP(invalidRec, invalidReq)
	if invalidRec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST /api/v2/silences invalid payload expected 422, got %d", invalidRec.Code)
	}
	var invalidPayload map[string]any
	if err := json.Unmarshal(invalidRec.Body.Bytes(), &invalidPayload); err != nil {
		t.Fatalf("invalid payload error expected JSON object body, got %q (%v)", invalidRec.Body.String(), err)
	}
	if invalidPayload["code"] != float64(602) {
		t.Fatalf("invalid payload error expected code=602, got %v", invalidPayload["code"])
	}

	noMatchersReq := httptest.NewRequest(http.MethodPost, "/api/v2/silences", bytes.NewBufferString(`{
		"matchers": [],
		"startsAt": "2099-01-01T00:00:00Z",
		"endsAt": "2099-01-01T01:00:00Z",
		"createdBy": "parity-suite",
		"comment": "no matchers"
	}`))
	noMatchersRec := httptest.NewRecorder()
	mux.ServeHTTP(noMatchersRec, noMatchersReq)
	if noMatchersRec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST /api/v2/silences empty matchers expected 422, got %d", noMatchersRec.Code)
	}
	var noMatchersPayload map[string]any
	if err := json.Unmarshal(noMatchersRec.Body.Bytes(), &noMatchersPayload); err != nil {
		t.Fatalf("empty matchers error expected JSON object body, got %q (%v)", noMatchersRec.Body.String(), err)
	}
	if noMatchersPayload["code"] != float64(612) {
		t.Fatalf("empty matchers error expected code=612, got %v", noMatchersPayload["code"])
	}

	unknownIDPayload := `{
		"id": "ffffffff-ffff-ffff-ffff-ffffffffffff",
		"matchers": [{"name":"alertname","value":"ParityUnknownID","isRegex":false}],
		"startsAt": "2099-01-01T00:00:00Z",
		"endsAt": "2099-01-01T01:00:00Z",
		"createdBy": "parity-suite",
		"comment": "unknown id update"
	}`
	notFoundReq := httptest.NewRequest(http.MethodPost, "/api/v2/silences", bytes.NewBufferString(unknownIDPayload))
	notFoundRec := httptest.NewRecorder()
	mux.ServeHTTP(notFoundRec, notFoundReq)
	if notFoundRec.Code != http.StatusNotFound {
		t.Fatalf("POST /api/v2/silences unknown id expected 404, got %d", notFoundRec.Code)
	}
	var notFoundPayload string
	if err := json.Unmarshal(notFoundRec.Body.Bytes(), &notFoundPayload); err != nil {
		t.Fatalf("unknown id error expected JSON string body, got %q (%v)", notFoundRec.Body.String(), err)
	}
	if strings.TrimSpace(notFoundPayload) == "" {
		t.Fatalf("unknown id error expected non-empty message")
	}

	invalidIDPayload := `{
		"id": "not-a-uuid",
		"matchers": [{"name":"alertname","value":"ParityInvalidID","isRegex":false}],
		"startsAt": "2099-01-01T00:00:00Z",
		"endsAt": "2099-01-01T01:00:00Z",
		"createdBy": "parity-suite",
		"comment": "invalid id update"
	}`
	invalidIDReq := httptest.NewRequest(http.MethodPost, "/api/v2/silences", bytes.NewBufferString(invalidIDPayload))
	invalidIDRec := httptest.NewRecorder()
	mux.ServeHTTP(invalidIDRec, invalidIDReq)
	if invalidIDRec.Code != http.StatusNotFound {
		t.Fatalf("POST /api/v2/silences invalid id expected 404, got %d", invalidIDRec.Code)
	}
	var invalidIDError string
	if err := json.Unmarshal(invalidIDRec.Body.Bytes(), &invalidIDError); err != nil {
		t.Fatalf("invalid id error expected JSON string body, got %q (%v)", invalidIDRec.Body.String(), err)
	}
	if strings.TrimSpace(invalidIDError) == "" {
		t.Fatalf("invalid id error expected non-empty message")
	}
}
