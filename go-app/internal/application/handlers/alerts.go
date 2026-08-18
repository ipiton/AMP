package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	appconfig "github.com/ipiton/AMP/internal/config"
	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/core/alertconv"
	"github.com/ipiton/AMP/internal/core/services"
	infrasilencing "github.com/ipiton/AMP/internal/infrastructure/silencing"
	"github.com/ipiton/AMP/internal/infrastructure/storage/memory"
	"github.com/ipiton/AMP/internal/infrastructure/webhook"
)

// RegistryProvider is an interface that provides access to the service registry.
// This allows us to inject a mock or the actual ServiceRegistry without circular imports.
type RegistryProvider interface {
	AlertStore() *memory.AlertStore
	SilenceStore() *memory.SilenceStore
	// SilenceRepository returns the persistent silence repository, or nil when
	// running without one (lite profile). With a nil repository the silence
	// handlers fall back to the legacy memory-only behavior.
	SilenceRepository() infrasilencing.SilenceRepository
	// SilenceEventPublisher returns the cross-replica silence cache
	// invalidation publisher (task 6.3), or nil when running without one
	// (lite profile, or a standard-profile deployment without a live Redis
	// cache backend). A nil publisher makes silence writes a no-op for
	// cross-replica sync — other replicas converge only on restart.
	SilenceEventPublisher() infrasilencing.SilenceEventPublisher
	AlertProcessor() *services.AlertProcessor
	Config() *appconfig.Config
	StartTime() time.Time
	ReloadConfig(ctx context.Context) error
	// ClusterStatus returns the `cluster` field for /api/v2/status (task
	// 6.5). See ClusterStatus's own doc comment (status_api.go) for the
	// disabled/ready contract.
	ClusterStatus(ctx context.Context) ClusterStatus
	// RouteEvaluator returns the live route-tree evaluator, or nil when
	// running without a `route:` section (lite/legacy single-receiver mode).
	// /api/v2/alerts/groups uses it for the per-alert receiver and group_by
	// (final review finding 17); a nil evaluator falls back to the configured
	// root route, then to the first legacy receiver.
	RouteEvaluator() services.RouteEvaluator
}

func AlertsHandler(registry RegistryProvider) http.HandlerFunc {
	externalURL := registry.Config().Server.ExternalURL
	return func(w http.ResponseWriter, r *http.Request) {
		alertStore := registry.AlertStore()
		silenceStore := registry.SilenceStore()

		switch r.Method {
		case http.MethodGet:
			handleAlertsGet(alertStore, silenceStore, w, r)
		case http.MethodPost:
			handleAlertsPost(registry.AlertProcessor(), alertStore, silenceStore, externalURL, w, r)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func handleAlertsGet(store *memory.AlertStore, silences *memory.SilenceStore, w http.ResponseWriter, r *http.Request) {
	gettableAlerts, errMsg := collectGettableAlerts(store, silences, r.URL.Query())
	if errMsg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": errMsg})
		return
	}

	writeJSON(w, http.StatusOK, gettableAlerts)
}

// collectGettableAlerts applies every GET /api/v2/alerts query-param filter
// (legacy status/resolved, filter=, and the upstream
// active/silenced/inhibited/unprocessed/receiver params) and returns the
// resulting v2 gettable alerts. Shared by the v2 GET handler above and the
// legacy v1 GET handler (handleV1AlertsGet) below, which reuses the same
// param names since upstream's v1 and v2 alert-listing filters overlap.
//
// A non-empty second return value is an error message the caller must
// surface as 400 Bad Request; the alerts slice is nil in that case.
func collectGettableAlerts(store *memory.AlertStore, silences *memory.SilenceStore, query url.Values) ([]core.APIGettableAlert, string) {
	// Legacy params (kept as aliases): status=firing|resolved, resolved=bool.
	// They filter at the store level, exactly as before this task.
	status := parseAlertsStatusQuery(query.Get("status"))
	includeResolved := parseBoolQueryLenient(query.Get("resolved"), false)
	if status == "resolved" {
		includeResolved = true
	}

	filters, err := ParseLabelMatchers(query["filter"])
	if err != nil {
		return nil, err.Error()
	}

	// Upstream params: active/silenced/inhibited/unprocessed/receiver.
	stateFilter, err := parseAlertStateFilters(query)
	if err != nil {
		return nil, err.Error()
	}
	receiverRe, err := parseReceiverFilter(query.Get("receiver"))
	if err != nil {
		return nil, err.Error()
	}

	alerts := store.List(status, includeResolved)

	now := time.Now().UTC()
	gettableAlerts := make([]core.APIGettableAlert, 0, len(alerts))
	for _, alert := range alerts {
		if !MatchesLabels(filters, alert.Labels) {
			continue
		}
		gettable := alertconv.ToGettableAlert(alert, silenceMatcher(silences), now)
		if !stateFilter.matches(gettable.Status) {
			continue
		}
		if receiverRe != nil && !matchesAnyReceiver(receiverRe, gettable.Receivers) {
			continue
		}
		gettableAlerts = append(gettableAlerts, gettable)
	}

	return gettableAlerts, ""
}

// handleV1AlertsGet implements the legacy GET /api/v1/alerts: a thin wrapper
// around the v2 listing (collectGettableAlerts) whose response is re-shaped
// into the v1 envelope ({"status":"success","data":[...]}) via
// alertconv.ToV1Alert. It accepts the same query params as v2 — see
// V1AlertsHandler's doc comment.
//
// Errors use the v1 envelope too ({"status":"error","errorType":"bad_data",
// "error":"..."}), NOT the bare {"error":"..."} shape v2 uses — every v1
// response, success or failure, carries "status".
func handleV1AlertsGet(store *memory.AlertStore, silences *memory.SilenceStore, w http.ResponseWriter, r *http.Request) {
	gettableAlerts, errMsg := collectGettableAlerts(store, silences, r.URL.Query())
	if errMsg != "" {
		writeJSON(w, http.StatusBadRequest, core.APIV1ErrorResponse{
			Status:    "error",
			ErrorType: core.APIV1ErrorTypeBadData,
			Error:     errMsg,
		})
		return
	}

	data := make([]core.APIV1Alert, 0, len(gettableAlerts))
	for _, alert := range gettableAlerts {
		data = append(data, alertconv.ToV1Alert(alert))
	}

	writeJSON(w, http.StatusOK, core.APIV1AlertsResponse{Status: "success", Data: data})
}

// alertStateFilter mirrors upstream Alertmanager's GET /api/v2/alerts state
// query params. Each flag defaults to true (include); setting one to false
// excludes alerts matching that specific criterion, independently of the
// others — an alert can be both silenced and inhibited at once, and
// active/unprocessed are mutually exclusive states computed by
// alertconv.ToGettableAlert.
//
// LIMITATION: the inhibition pipeline is a separate parity track (not yet
// wired into alertconv.ToGettableAlert), so Status.InhibitedBy is always
// empty today. The `inhibited` param is implemented structurally — it will
// start filtering real data once inhibition populates InhibitedBy — but is
// currently a no-op in practice.
type alertStateFilter struct {
	active      bool
	silenced    bool
	inhibited   bool
	unprocessed bool
}

func (f alertStateFilter) matches(status core.APIAlertStatus) bool {
	if !f.active && status.State == "active" {
		return false
	}
	if !f.silenced && len(status.SilencedBy) > 0 {
		return false
	}
	if !f.inhibited && len(status.InhibitedBy) > 0 {
		return false
	}
	if !f.unprocessed && status.State == "unprocessed" {
		return false
	}
	return true
}

func parseAlertStateFilters(query url.Values) (alertStateFilter, error) {
	var f alertStateFilter
	var err error
	if f.active, err = parseBoolQueryStrict(query, "active", true); err != nil {
		return f, err
	}
	if f.silenced, err = parseBoolQueryStrict(query, "silenced", true); err != nil {
		return f, err
	}
	if f.inhibited, err = parseBoolQueryStrict(query, "inhibited", true); err != nil {
		return f, err
	}
	if f.unprocessed, err = parseBoolQueryStrict(query, "unprocessed", true); err != nil {
		return f, err
	}
	return f, nil
}

// parseBoolQueryStrict parses a boolean query param, returning def when the
// param is absent/empty and an error when it is present but not a valid
// boolean (upstream returns 400 for malformed query params).
func parseBoolQueryStrict(query url.Values, key string, def bool) (bool, error) {
	raw := strings.TrimSpace(query.Get(key))
	if raw == "" {
		return def, nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return def, fmt.Errorf("invalid %s query parameter: %q", key, raw)
	}
	return v, nil
}

// parseReceiverFilter compiles the receiver query param into a regex, per
// upstream semantics (unanchored substring match against receiver name).
// An empty param returns a nil regex (no filtering).
func parseReceiverFilter(raw string) (*regexp.Regexp, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	re, err := regexp.Compile(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid receiver query parameter: %w", err)
	}
	return re, nil
}

func matchesAnyReceiver(re *regexp.Regexp, receivers []core.APIReceiver) bool {
	for _, r := range receivers {
		if re.MatchString(r.Name) {
			return true
		}
	}
	return false
}

// silenceMatcher converts a possibly-nil *memory.SilenceStore into the
// alertconv.SilenceMatcher interface without producing a typed-nil interface.
func silenceMatcher(s *memory.SilenceStore) alertconv.SilenceMatcher {
	if s == nil {
		return nil
	}
	return s
}

// V1AlertsHandler serves the legacy /api/v1/alerts endpoint (PARITY-4.3,
// amtool audit backlog item 3):
//
//   - POST aliases the v2 ingest pipeline. Upstream's v1 ingest payload is
//     the same alert JSON array as v2 (docs even for the retired v1 API
//     describe an identical body), so no conversion layer is needed — this
//     delegates straight into AlertsHandler's POST path.
//   - GET is a thin read-side wrapper: it reuses v2's alert listing/filtering
//     (collectGettableAlerts — same query param names: status, resolved,
//     filter, active, silenced, inhibited, unprocessed, receiver) and
//     re-shapes the result into the legacy v1 envelope
//     ({"status":"success","data":[...]}, receivers as bare name strings,
//     no mutedBy) via alertconv.ToV1Alert.
//
// Any other method returns 405.
func V1AlertsHandler(registry RegistryProvider) http.HandlerFunc {
	v2Handler := AlertsHandler(registry)
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			v2Handler(w, r)
		case http.MethodGet:
			handleV1AlertsGet(registry.AlertStore(), registry.SilenceStore(), w, r)
		default:
			w.Header().Set("Allow", "GET, POST")
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func AlertGroupsHandler(registry RegistryProvider) http.HandlerFunc {
	return getOnly(func(w http.ResponseWriter, r *http.Request) {
		queryParams := r.URL.Query()
		groupBy := queryParams["group_by"]

		stateFilter, err := parseAlertStateFilters(queryParams)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		receiverRe, err := parseReceiverFilter(queryParams.Get("receiver"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		resolver := alertGroupingResolver(registry, groupBy)
		groups := registry.AlertStore().GroupAlerts(resolver, silenceMatcher(registry.SilenceStore()))
		groups = filterAlertGroups(groups, stateFilter, receiverRe)
		writeJSON(w, http.StatusOK, groups)
	})
}

// alertGroupingResolver builds the per-alert grouping identity (receiver +
// group_by) that /api/v2/alerts/groups reports.
//
// Final review finding 17: every group used to report `labels: {}` unless the
// caller passed `?group_by=` (upstream has no such parameter — group labels come
// from the matched ROUTE's group_by), and every group reported the FIRST
// configured receiver regardless of where its alerts actually route.
//
// Resolution order, per alert:
//  1. The live route tree (registry.RouteEvaluator) — the authoritative answer,
//     and the only one that can differ per alert.
//  2. The root route's own receiver/group_by, when a `route:` tree exists but
//     evaluation fails (fail-open, matching AlertProcessor.evaluateRoute).
//  3. The first configured legacy receiver, then "default" — lite/legacy mode
//     with no route tree at all.
//
// queryGroupBy (the `?group_by=` parameter, an AMP extension kept for backwards
// compatibility) overrides the group_by from every source when present, so a
// caller can still ask "how would these alerts group by X".
func alertGroupingResolver(registry RegistryProvider, queryGroupBy []string) memory.AlertGroupingResolver {
	cfg := registry.Config()

	fallback := memory.AlertGrouping{Receiver: "default"}
	if cfg != nil {
		if cfg.Routing != nil && cfg.Routing.Route != nil {
			fallback.Receiver = cfg.Routing.Route.Receiver
			fallback.GroupBy = cfg.Routing.Route.GroupBy
		} else if len(cfg.Receivers) > 0 {
			fallback.Receiver = cfg.Receivers[0].Name
		}
	}
	if fallback.Receiver == "" {
		fallback.Receiver = "default"
	}

	evaluator := registry.RouteEvaluator()

	return func(labels map[string]string) memory.AlertGrouping {
		grouping := fallback

		if evaluator != nil {
			if decision, err := evaluator.Evaluate(labels); err == nil && decision != nil && decision.Receiver != "" {
				grouping = memory.AlertGrouping{Receiver: decision.Receiver, GroupBy: decision.GroupBy}
			}
		}

		if len(queryGroupBy) > 0 {
			grouping.GroupBy = queryGroupBy
		}
		return grouping
	}
}

// filterAlertGroups applies the active/silenced/inhibited/unprocessed state
// filter and the receiver regex filter to grouped alerts. A group whose
// receiver does not match is dropped entirely; a group whose alerts are all
// filtered out by the state filter is also dropped (an empty group carries
// no information a caller could act on).
func filterAlertGroups(groups []core.APIGettableAlertGroup, stateFilter alertStateFilter, receiverRe *regexp.Regexp) []core.APIGettableAlertGroup {
	out := make([]core.APIGettableAlertGroup, 0, len(groups))
	for _, g := range groups {
		if receiverRe != nil && !receiverRe.MatchString(g.Receiver.Name) {
			continue
		}
		filteredAlerts := make([]core.APIGettableAlert, 0, len(g.Alerts))
		for _, a := range g.Alerts {
			if stateFilter.matches(a.Status) {
				filteredAlerts = append(filteredAlerts, a)
			}
		}
		if len(filteredAlerts) == 0 {
			continue
		}
		g.Alerts = filteredAlerts
		out = append(out, g)
	}
	return out
}

func handleAlertsPost(processor *services.AlertProcessor, store *memory.AlertStore, silences *memory.SilenceStore, externalURL string, w http.ResponseWriter, r *http.Request) {
	defer func() { _ = r.Body.Close() }()

	if processor == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "alert processor is not available",
		})
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 10*1024*1024))
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{
			"error": "request payload too large",
		})
		return
	}

	now := time.Now().UTC()
	alerts, err := parseAlertsForProcessing(body, now, externalURL)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": err.Error(),
		})
		return
	}

	filteredAlerts := make([]*core.Alert, 0, len(alerts))
	for _, alert := range alerts {
		if alert.Status != core.StatusResolved && silences != nil && silences.HasActiveMatch(alert.Labels, now) {
			continue
		}
		filteredAlerts = append(filteredAlerts, alert)
	}

	successfulInputs := make([]core.AlertIngestInput, 0, len(filteredAlerts))
	failedCount := 0
	for _, alert := range filteredAlerts {
		if err := processor.ProcessAlert(r.Context(), alert); err != nil {
			failedCount++
			continue
		}
		successfulInputs = append(successfulInputs, toAlertIngestInput(alert))
	}

	if len(successfulInputs) > 0 {
		// These alerts are already committed to the database (dedup path inside
		// ProcessAlert). A memory-store failure here is an internal inconsistency,
		// not a client error — report 500, never 400.
		if err := store.IngestBatch(successfulInputs, now); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error": "alerts persisted but in-memory store rejected batch: " + err.Error(),
			})
			return
		}
	}

	if failedCount == 0 {
		w.WriteHeader(http.StatusOK)
		return
	}

	if len(successfulInputs) > 0 {
		writeJSON(w, http.StatusMultiStatus, map[string]int{
			"received":  len(alerts),
			"processed": len(successfulInputs),
			"failed":    failedCount,
		})
		return
	}

	writeJSON(w, http.StatusInternalServerError, map[string]any{
		"error":    "all alerts failed to process",
		"received": len(alerts),
		"failed":   failedCount,
	})
}

func parseAlertsForProcessing(body []byte, now time.Time, externalURL string) ([]*core.Alert, error) {
	if alerts, err := parsePrometheusAlerts(body, externalURL); err == nil {
		return alerts, nil
	}

	return parseLegacyAlerts(body, now)
}

func parsePrometheusAlerts(body []byte, externalURL string) ([]*core.Alert, error) {
	parser := webhook.NewPrometheusParser(externalURL)
	parsedWebhook, err := parser.Parse(body)
	if err != nil {
		return nil, err
	}

	validation := parser.Validate(parsedWebhook)
	if !validation.Valid {
		return nil, fmt.Errorf("invalid prometheus alert payload")
	}

	return parser.ConvertToDomain(parsedWebhook)
}

func parseLegacyAlerts(body []byte, now time.Time) ([]*core.Alert, error) {
	payload, err := parseAlertIngestPayload(body)
	if err != nil {
		return nil, err
	}

	alerts := make([]*core.Alert, 0, len(payload))
	for i, in := range payload {
		alert, err := convertIngestInputToAlert(in, now)
		if err != nil {
			return nil, fmt.Errorf("alert[%d]: %w", i, err)
		}
		alerts = append(alerts, alert)
	}

	return alerts, nil
}

func convertIngestInputToAlert(in core.AlertIngestInput, now time.Time) (*core.Alert, error) {
	startsAt, err := alertconv.ParseAlertTime(in.StartsAt)
	if err != nil {
		return nil, fmt.Errorf("invalid startsAt: %w", err)
	}
	if startsAt.IsZero() {
		startsAt = now
	}

	endsAt, err := alertconv.ParseOptionalAlertTime(in.EndsAt)
	if err != nil {
		return nil, fmt.Errorf("invalid endsAt: %w", err)
	}

	alertName := strings.TrimSpace(in.Labels["alertname"])
	if alertName == "" {
		return nil, fmt.Errorf("missing required label alertname")
	}

	status := alertconv.NormalizeStatus(in.Status, endsAt, now)
	fingerprint := strings.TrimSpace(in.Fingerprint)
	if fingerprint == "" {
		fingerprint = alertconv.Fingerprint(in.Labels)
	}

	var generatorURL *string
	if trimmed := strings.TrimSpace(in.GeneratorURL); trimmed != "" {
		generatorURL = &trimmed
	}

	return &core.Alert{
		Fingerprint:  fingerprint,
		AlertName:    alertName,
		Status:       core.AlertStatus(status),
		Labels:       alertconv.CloneStringMap(in.Labels),
		Annotations:  alertconv.CloneStringMap(in.Annotations),
		StartsAt:     startsAt,
		EndsAt:       endsAt,
		GeneratorURL: generatorURL,
		Timestamp:    &now,
	}, nil
}

func toAlertIngestInput(alert *core.Alert) core.AlertIngestInput {
	in := core.AlertIngestInput{
		Labels:      alertconv.CloneStringMap(alert.Labels),
		Annotations: alertconv.CloneStringMap(alert.Annotations),
		StartsAt:    alert.StartsAt.UTC().Format(time.RFC3339),
		Status:      string(alert.Status),
		Fingerprint: alert.Fingerprint,
	}
	if alert.EndsAt != nil {
		in.EndsAt = alert.EndsAt.UTC().Format(time.RFC3339)
	}
	if alert.GeneratorURL != nil {
		in.GeneratorURL = *alert.GeneratorURL
	}

	return in
}

func parseAlertsStatusQuery(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "firing":
		return "firing"
	case "resolved":
		return "resolved"
	default:
		return ""
	}
}

func parseBoolQueryLenient(raw string, def bool) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def
	}
	switch strings.ToLower(raw) {
	case "true", "1", "yes":
		return true
	case "false", "0", "no":
		return false
	default:
		return false
	}
}

func parseAlertIngestPayload(body []byte) ([]core.AlertIngestInput, error) {
	var alerts []core.AlertIngestInput
	if err := json.Unmarshal(body, &alerts); err == nil {
		if len(alerts) > 0 {
			return alerts, nil
		}
	}

	var envelope struct {
		Alerts []core.AlertIngestInput `json:"alerts"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil && len(envelope.Alerts) > 0 {
		return envelope.Alerts, nil
	}

	return nil, errors.New("invalid alert payload")
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if payload != nil {
		_ = json.NewEncoder(w).Encode(payload)
	}
}
