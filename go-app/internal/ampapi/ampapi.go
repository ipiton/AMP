// Package ampapi serves AMP's own HTTP routes next to the upstream
// Alertmanager API (ADR-019 slice UPSTREAM-API-UI): the alert history and the
// latest investigation of an alert, looked up by the fingerprint api/v2
// reports (upstream's FNV hash, 16 hex) or by AMP's SHA-256 key (64 hex).
//
// The routes live under PathPrefix, outside /api/v2: cmd/amp-upstream sends
// only that prefix here and everything else to the upstream core unchanged.
package ampapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/core/alertconv"
)

// PathPrefix is the path, after the external URL's, under which every AMP
// route is served.
const PathPrefix = "/api/amp/"

// Fingerprint lengths: upstream's model.Fingerprint ("%016x") and AMP's
// alertconv.Fingerprint (hex SHA-256).
const (
	upstreamFingerprintLen = 16
	ampFingerprintLen      = 64
)

// AlertReader reads the alert history. Both methods return
// core.ErrAlertNotFound for a fingerprint without a row.
type AlertReader interface {
	AlertRecordByFingerprint(ctx context.Context, fingerprint string) (*core.AlertHistoryRecord, error)
	AlertRecordByUpstreamFingerprint(ctx context.Context, fingerprint string) (*core.AlertHistoryRecord, error)
}

// InvestigationReader reads investigations by AMP fingerprint; nil, nil when
// there is none.
type InvestigationReader interface {
	GetLatestByFingerprint(ctx context.Context, fingerprint string) (*core.Investigation, error)
}

// Options configures the handler. A nil reader answers its routes with 503:
// the history needs PostgreSQL.
type Options struct {
	// RoutePrefix is the external URL's path ("" or "/am", no trailing
	// slash).
	RoutePrefix    string
	Alerts         AlertReader
	Investigations InvestigationReader
	Logger         *slog.Logger
	Registerer     prometheus.Registerer
}

type handler struct {
	alerts         AlertReader
	investigations InvestigationReader
	logger         *slog.Logger
}

// New returns the handler of every route under RoutePrefix+PathPrefix.
func New(opts Options) http.Handler {
	h := &handler{
		alerts:         opts.Alerts,
		investigations: opts.Investigations,
		logger:         opts.Logger.With("component", "ampapi"),
	}
	duration := promauto.With(opts.Registerer).NewHistogramVec(prometheus.HistogramOpts{
		Name:    "amp_api_request_duration_seconds",
		Help:    "Duration of AMP API requests (/api/amp/) by handler, method and status code.",
		Buckets: prometheus.DefBuckets,
	}, []string{"handler", "method", "code"})
	instrument := func(name string, f http.HandlerFunc) http.Handler {
		return promhttp.InstrumentHandlerDuration(duration.MustCurryWith(prometheus.Labels{"handler": name}), f)
	}

	base := strings.TrimRight(opts.RoutePrefix, "/") + PathPrefix + "v1/alerts/{fingerprint}"
	mux := http.NewServeMux()
	mux.Handle("GET "+base, instrument("alert", h.alert))
	mux.Handle("GET "+base+"/investigation", instrument("investigation", h.investigation))
	return mux
}

// alertResponse is GET .../alerts/{fingerprint}.
type alertResponse struct {
	Fingerprint    string            `json:"fingerprint"`
	AMPFingerprint string            `json:"amp_fingerprint"`
	AlertName      string            `json:"alertname"`
	Status         string            `json:"status"`
	Labels         map[string]string `json:"labels"`
	Annotations    map[string]string `json:"annotations"`
	StartsAt       *time.Time        `json:"starts_at,omitempty"`
	EndsAt         *time.Time        `json:"ends_at,omitempty"`
	GeneratorURL   *string           `json:"generator_url,omitempty"`
	FirstSeenAt    time.Time         `json:"first_seen_at"`
	LastEventAt    *time.Time        `json:"last_event_at,omitempty"`
}

// investigationResponse is GET .../alerts/{fingerprint}/investigation: the
// shape of the removed GET /api/v1/alerts/{fingerprint}/investigation, with
// fingerprint now the upstream one and AMP's in amp_fingerprint.
type investigationResponse struct {
	ID              string                    `json:"id"`
	Fingerprint     string                    `json:"fingerprint,omitempty"`
	AMPFingerprint  string                    `json:"amp_fingerprint"`
	Status          string                    `json:"status"`
	RetryCount      int                       `json:"retry_count"`
	QueuedAt        time.Time                 `json:"queued_at"`
	CreatedAt       time.Time                 `json:"created_at"`
	UpdatedAt       time.Time                 `json:"updated_at"`
	StartedAt       *time.Time                `json:"started_at,omitempty"`
	CompletedAt     *time.Time                `json:"completed_at,omitempty"`
	ErrorMessage    *string                   `json:"error_message,omitempty"`
	ErrorType       *string                   `json:"error_type,omitempty"`
	Result          *core.InvestigationResult `json:"result,omitempty"`
	Steps           json.RawMessage           `json:"steps,omitempty"`
	IterationsCount int                       `json:"iterations_count,omitempty"`
	ToolCallsCount  int                       `json:"tool_calls_count,omitempty"`
}

func (h *handler) alert(w http.ResponseWriter, r *http.Request) {
	if h.alerts == nil {
		writeError(w, http.StatusServiceUnavailable, errHistoryUnavailable)
		return
	}
	fp, upstream, ok := parseFingerprint(r.PathValue("fingerprint"))
	if !ok {
		writeError(w, http.StatusBadRequest, errBadFingerprint)
		return
	}
	rec, err := h.record(r.Context(), fp, upstream)
	switch {
	case errors.Is(err, core.ErrAlertNotFound):
		writeError(w, http.StatusNotFound, "no alert history for this fingerprint")
		return
	case err != nil:
		h.internalError(w, "alert", err)
		return
	}
	a := rec.Alert
	writeJSON(w, http.StatusOK, alertResponse{
		Fingerprint:    alertconv.UpstreamFingerprint(a.Labels),
		AMPFingerprint: a.Fingerprint,
		AlertName:      a.AlertName,
		Status:         string(a.Status),
		Labels:         nonNil(a.Labels),
		Annotations:    nonNil(a.Annotations),
		StartsAt:       utcOrNil(&a.StartsAt),
		EndsAt:         utcOrNil(a.EndsAt),
		GeneratorURL:   a.GeneratorURL,
		FirstSeenAt:    rec.FirstSeenAt.UTC(),
		LastEventAt:    utcOrNil(a.Timestamp),
	})
}

func (h *handler) investigation(w http.ResponseWriter, r *http.Request) {
	if h.alerts == nil || h.investigations == nil {
		writeError(w, http.StatusServiceUnavailable, errHistoryUnavailable)
		return
	}
	fp, upstream, ok := parseFingerprint(r.PathValue("fingerprint"))
	if !ok {
		writeError(w, http.StatusBadRequest, errBadFingerprint)
		return
	}
	// The investigation is keyed by AMP's fingerprint: an upstream one is
	// resolved through its history row. An AMP fingerprint is looked up
	// even without a row; the response then has no upstream fingerprint.
	ampFP, upstreamFP := fp, ""
	rec, err := h.record(r.Context(), fp, upstream)
	switch {
	case err == nil:
		ampFP, upstreamFP = rec.Alert.Fingerprint, alertconv.UpstreamFingerprint(rec.Alert.Labels)
	case errors.Is(err, core.ErrAlertNotFound):
		if upstream {
			writeError(w, http.StatusNotFound, "no alert history for this fingerprint")
			return
		}
	default:
		h.internalError(w, "investigation", err)
		return
	}
	inv, err := h.investigations.GetLatestByFingerprint(r.Context(), ampFP)
	if err != nil {
		h.internalError(w, "investigation", err)
		return
	}
	if inv == nil {
		writeError(w, http.StatusNotFound, "no investigation for this fingerprint")
		return
	}
	resp := investigationResponse{
		ID:              inv.ID,
		Fingerprint:     upstreamFP,
		AMPFingerprint:  inv.Fingerprint,
		Status:          string(inv.Status),
		RetryCount:      inv.RetryCount,
		QueuedAt:        inv.QueuedAt.UTC(),
		CreatedAt:       inv.CreatedAt.UTC(),
		UpdatedAt:       inv.UpdatedAt.UTC(),
		StartedAt:       utcOrNil(inv.StartedAt),
		CompletedAt:     utcOrNil(inv.CompletedAt),
		ErrorMessage:    inv.ErrorMessage,
		Result:          inv.Result,
		Steps:           inv.Steps,
		IterationsCount: inv.IterationsCount,
		ToolCallsCount:  inv.ToolCallsCount,
	}
	if inv.ErrorType != nil {
		t := string(*inv.ErrorType)
		resp.ErrorType = &t
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *handler) record(ctx context.Context, fp string, upstream bool) (*core.AlertHistoryRecord, error) {
	if upstream {
		return h.alerts.AlertRecordByUpstreamFingerprint(ctx, fp)
	}
	return h.alerts.AlertRecordByFingerprint(ctx, fp)
}

// internalError logs the cause, which may quote the database, and answers
// without it.
func (h *handler) internalError(w http.ResponseWriter, route string, err error) {
	h.logger.Warn("AMP API request failed", "handler", route, "err", err)
	writeError(w, http.StatusInternalServerError, "failed to read the alert history")
}

const (
	errHistoryUnavailable = "alert history is unavailable: it needs PostgreSQL (--amp.config.file with profile standard and a reachable database)"
	errBadFingerprint     = "fingerprint must be 16 hex characters (as in api/v2) or 64 (AMP's SHA-256)"
)

// parseFingerprint lower-cases fp and reports whether it is an upstream
// (16 hex) or an AMP (64 hex) fingerprint.
func parseFingerprint(fp string) (normalized string, upstream, ok bool) {
	fp = strings.ToLower(fp)
	if len(fp) != upstreamFingerprintLen && len(fp) != ampFingerprintLen {
		return "", false, false
	}
	for i := 0; i < len(fp); i++ {
		c := fp[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return "", false, false
		}
	}
	return fp, len(fp) == upstreamFingerprintLen, true
}

// utcOrNil returns t in UTC, or nil for a nil or zero t (a NULL column).
func utcOrNil(t *time.Time) *time.Time {
	if t == nil || t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

func nonNil(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	// The status is sent; an encode error is a client gone mid-write.
	_ = json.NewEncoder(w).Encode(v)
}
