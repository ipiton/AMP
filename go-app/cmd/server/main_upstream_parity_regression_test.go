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
	if _, ok := cluster["peers"].([]any); !ok {
		t.Fatalf("status cluster.peers expected array, got %T", cluster["peers"])
	}
	if _, ok := cluster["name"].(string); !ok {
		t.Fatalf("status cluster.name expected string, got %T", cluster["name"])
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

func TestUpstreamParity_AlertGroupsShapeAndFilters(t *testing.T) {
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
