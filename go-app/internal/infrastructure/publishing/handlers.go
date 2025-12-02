package publishing

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/ipiton/AMP/internal/core"
)

// PublishingHandlers provides HTTP handlers for publishing management
type PublishingHandlers struct {
	discoveryManager TargetDiscoveryManager
	refreshManager   *RefreshManager
	queue            *PublishingQueue
	coordinator      *PublishingCoordinator
	modeManager      ModeManager
	logger           *slog.Logger
}

// NewPublishingHandlers creates new publishing HTTP handlers
func NewPublishingHandlers(
	discoveryManager TargetDiscoveryManager,
	refreshManager *RefreshManager,
	queue *PublishingQueue,
	coordinator *PublishingCoordinator,
	modeManager ModeManager,
	logger *slog.Logger,
) *PublishingHandlers {
	if logger == nil {
		logger = slog.Default()
	}

	return &PublishingHandlers{
		discoveryManager: discoveryManager,
		refreshManager:   refreshManager,
		queue:            queue,
		coordinator:      coordinator,
		modeManager:      modeManager,
		logger:           logger,
	}
}

// RegisterRoutes registers all publishing routes
func (h *PublishingHandlers) RegisterRoutes(router *mux.Router) {
	// Publishing management endpoints
	api := router.PathPrefix("/api/v1/publishing").Subrouter()

	// Target management
	api.HandleFunc("/targets", h.ListTargets).Methods("GET")
	api.HandleFunc("/targets/{name}", h.GetTarget).Methods("GET")
	api.HandleFunc("/targets/refresh", h.RefreshTargets).Methods("POST")
	api.HandleFunc("/targets/{name}/test", h.TestTarget).Methods("POST")

	// Statistics & status
	api.HandleFunc("/stats", h.GetStats).Methods("GET")
	api.HandleFunc("/queue", h.GetQueueStatus).Methods("GET")
	api.HandleFunc("/mode", h.GetPublishingMode).Methods("GET")

	// TN-056: Queue & Job management
	api.HandleFunc("/submit", h.SubmitAlert).Methods("POST")
	api.HandleFunc("/queue/stats", h.GetDetailedQueueStats).Methods("GET")
	api.HandleFunc("/jobs", h.ListJobs).Methods("GET")
	api.HandleFunc("/jobs/{id}", h.GetJob).Methods("GET")

	// TN-056: DLQ management
	api.HandleFunc("/dlq", h.ListDLQEntries).Methods("GET")
	api.HandleFunc("/dlq/{id}/replay", h.ReplayDLQEntry).Methods("POST")
	api.HandleFunc("/dlq/purge", h.PurgeDLQ).Methods("DELETE")
}

// TargetResponse represents a publishing target in API responses
type TargetResponse struct {
	Name    string            `json:"name"`
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Enabled bool              `json:"enabled"`
	Format  string            `json:"format"`
	Headers map[string]string `json:"headers,omitempty"`
}

// StatsResponse represents publishing statistics
type StatsResponse struct {
	TotalTargets   int                       `json:"total_targets"`
	EnabledTargets int                       `json:"enabled_targets"`
	TargetsByType  map[string]int            `json:"targets_by_type"`
	QueueSize      int                       `json:"queue_size"`
	QueueCapacity  int                       `json:"queue_capacity"`
	QueueUtilization float64                 `json:"queue_utilization_percent"`
}

// QueueStatusResponse represents queue status
type QueueStatusResponse struct {
	Size             int     `json:"size"`
	Capacity         int     `json:"capacity"`
	Utilization      float64 `json:"utilization_percent"`
	WorkersCount     int     `json:"workers_count"`
}

// PublishingModeResponse represents current publishing mode
type PublishingModeResponse struct {
	Mode                      string    `json:"mode"`
	TargetsAvailable          bool      `json:"targets_available"`
	EnabledTargets            int       `json:"enabled_targets"`
	MetricsOnlyActive         bool      `json:"metrics_only_active"`
	TransitionCount           int64     `json:"transition_count,omitempty"`           // TN-060: Number of mode transitions
	CurrentModeDurationSeconds float64   `json:"current_mode_duration_seconds,omitempty"` // TN-060: Duration in current mode
	LastTransitionTime        time.Time `json:"last_transition_time,omitempty"`      // TN-060: Last transition timestamp
	LastTransitionReason      string    `json:"last_transition_reason,omitempty"`     // TN-060: Reason for last transition
}

// TestTargetRequest represents test request
type TestTargetRequest struct {
	AlertName string `json:"alert_name"`
}

// TestTargetResponse represents test result
type TestTargetResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Error   string `json:"error,omitempty"`
}

// ErrorResponse represents error response
type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// ===== TN-056: Queue & Job Management Types =====

// SubmitAlertRequest represents alert submission request
type SubmitAlertRequest struct {
	Alert      *core.Alert `json:"alert"`
	TargetName string      `json:"target_name,omitempty"` // Optional: specific target
}

// SubmitAlertResponse represents alert submission result
type SubmitAlertResponse struct {
	Success bool     `json:"success"`
	Message string   `json:"message"`
	JobIDs  []string `json:"job_ids,omitempty"` // List of created job IDs
	Mode    string   `json:"mode,omitempty"`    // TN-060: Current publishing mode
}

// DetailedQueueStatsResponse represents detailed queue statistics
type DetailedQueueStatsResponse struct {
	// Basic queue stats
	TotalSize    int `json:"total_size"`
	HighPriority int `json:"high_priority"`
	MedPriority  int `json:"med_priority"`
	LowPriority  int `json:"low_priority"`
	Capacity     int `json:"capacity"`

	// Worker stats
	WorkerCount  int `json:"worker_count"`
	ActiveJobs   int `json:"active_jobs"`

	// Metrics
	TotalSubmitted int64   `json:"total_submitted"`
	TotalCompleted int64   `json:"total_completed"`
	TotalFailed    int64   `json:"total_failed"`
	SuccessRate    float64 `json:"success_rate_percent"`

	// DLQ stats
	DLQSize int `json:"dlq_size"`
}

// JobStatusResponse represents job status
type JobStatusResponse struct {
	ID              string    `json:"id"`
	Fingerprint     string    `json:"fingerprint"`
	TargetName      string    `json:"target_name"`
	TargetType      string    `json:"target_type"`
	Priority        string    `json:"priority"`
	State           string    `json:"state"`
	RetryCount      int       `json:"retry_count"`
	MaxRetries      int       `json:"max_retries"`
	LastError       string    `json:"last_error,omitempty"`
	ErrorType       string    `json:"error_type,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	ProcessingTime  string    `json:"processing_time,omitempty"`
}

// JobListResponse represents list of jobs
type JobListResponse struct {
	Jobs       []JobStatusResponse `json:"jobs"`
	TotalCount int                 `json:"total_count"`
}

// ===== TN-056: DLQ Management Types =====

// DLQEntryResponse represents a DLQ entry in API
type DLQEntryResponse struct {
	ID           string    `json:"id"`
	JobID        string    `json:"job_id"`
	Fingerprint  string    `json:"fingerprint"`
	TargetName   string    `json:"target_name"`
	TargetType   string    `json:"target_type"`
	ErrorMessage string    `json:"error_message"`
	ErrorType    string    `json:"error_type"`
	RetryCount   int       `json:"retry_count"`
	Priority     string    `json:"priority"`
	FailedAt     time.Time `json:"failed_at"`
	Replayed     bool      `json:"replayed"`
	ReplayedAt   *time.Time `json:"replayed_at,omitempty"`
}

// DLQListResponse represents list of DLQ entries
type DLQListResponse struct {
	Entries    []DLQEntryResponse `json:"entries"`
	TotalCount int                `json:"total_count"`
	Stats      *DLQStatsResponse  `json:"stats,omitempty"`
}

// DLQStatsResponse represents DLQ statistics
type DLQStatsResponse struct {
	TotalEntries       int            `json:"total_entries"`
	EntriesByErrorType map[string]int `json:"entries_by_error_type"`
	EntriesByTarget    map[string]int `json:"entries_by_target"`
	EntriesByPriority  map[string]int `json:"entries_by_priority"`
	ReplayedCount      int            `json:"replayed_count"`
}

// ReplayDLQResponse represents DLQ replay result
type ReplayDLQResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Error   string `json:"error,omitempty"`
}

// PurgeDLQRequest represents DLQ purge request
type PurgeDLQRequest struct {
	OlderThanHours int `json:"older_than_hours"` // Default: 168 (7 days)
}

// PurgeDLQResponse represents DLQ purge result
type PurgeDLQResponse struct {
	Success      bool   `json:"success"`
	Message      string `json:"message"`
	DeletedCount int64  `json:"deleted_count"`
}

// 1. ListTargets - GET /api/v1/publishing/targets
func (h *PublishingHandlers) ListTargets(w http.ResponseWriter, r *http.Request) {
	targets := h.discoveryManager.ListTargets()

	response := make([]TargetResponse, 0, len(targets))
	for _, t := range targets {
		response = append(response, TargetResponse{
			Name:    t.Name,
			Type:    t.Type,
			URL:     t.URL,
			Enabled: t.Enabled,
			Format:  string(t.Format),
			Headers: t.Headers,
		})
	}

	h.sendJSON(w, http.StatusOK, response)
}

// 2. GetTarget - GET /api/v1/publishing/targets/{name}
func (h *PublishingHandlers) GetTarget(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	target, err := h.discoveryManager.GetTarget(name)
	if err != nil {
		h.sendError(w, http.StatusNotFound, "Target not found", err.Error())
		return
	}

	response := TargetResponse{
		Name:    target.Name,
		Type:    target.Type,
		URL:     target.URL,
		Enabled: target.Enabled,
		Format:  string(target.Format),
		Headers: target.Headers,
	}

	h.sendJSON(w, http.StatusOK, response)
}

// 3. RefreshTargets - POST /api/v1/publishing/targets/refresh
func (h *PublishingHandlers) RefreshTargets(w http.ResponseWriter, r *http.Request) {
	h.logger.Info("Manual target refresh requested via API")

	err := h.refreshManager.RefreshNow(r.Context())
	if err != nil {
		h.logger.Error("Manual refresh failed", "error", err)
		h.sendError(w, http.StatusInternalServerError, "Refresh failed", err.Error())
		return
	}

	targetCount := h.discoveryManager.GetTargetCount()

	// Count enabled targets
	targets := h.discoveryManager.ListTargets()
	enabledCount := 0
	for _, t := range targets {
		if t.Enabled {
			enabledCount++
		}
	}

	response := map[string]interface{}{
		"success":         true,
		"message":         "Targets refreshed successfully",
		"total_targets":   targetCount,
		"enabled_targets": enabledCount,
	}

	h.sendJSON(w, http.StatusOK, response)
}

// 4. TestTarget - POST /api/v1/publishing/targets/{name}/test
func (h *PublishingHandlers) TestTarget(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	// Decode request body (optional)
	var req TestTargetRequest
	if r.Body != http.NoBody {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			h.sendError(w, http.StatusBadRequest, "Invalid request body", err.Error())
			return
		}
	}

	// Get target
	target, err := h.discoveryManager.GetTarget(name)
	if err != nil {
		h.sendError(w, http.StatusNotFound, "Target not found", err.Error())
		return
	}

	if !target.Enabled {
		h.sendJSON(w, http.StatusOK, TestTargetResponse{
			Success: false,
			Message: "Target is disabled",
		})
		return
	}

	// Create test alert
	testAlert := h.createTestAlert(req.AlertName)

	// Try to publish
	results, err := h.coordinator.PublishToTargets(r.Context(), testAlert, []string{name})
	if err != nil {
		h.sendError(w, http.StatusInternalServerError, "Test failed", err.Error())
		return
	}

	if len(results) == 0 {
		h.sendError(w, http.StatusInternalServerError, "No results", "Failed to publish test alert")
		return
	}

	result := results[0]
	response := TestTargetResponse{
		Success: result.Success,
		Message: "Test alert sent",
	}

	if result.Error != nil {
		response.Error = result.Error.Error()
	}

	h.sendJSON(w, http.StatusOK, response)
}

// 5. GetStats - GET /api/v1/publishing/stats
func (h *PublishingHandlers) GetStats(w http.ResponseWriter, r *http.Request) {
	targets := h.discoveryManager.ListTargets()

	// Count by type
	targetsByType := make(map[string]int)
	enabledCount := 0

	for _, t := range targets {
		targetsByType[t.Type]++
		if t.Enabled {
			enabledCount++
		}
	}

	queueSize := h.queue.GetQueueSize()
	queueCapacity := h.queue.GetQueueCapacity()
	utilization := 0.0
	if queueCapacity > 0 {
		utilization = float64(queueSize) / float64(queueCapacity) * 100
	}

	response := StatsResponse{
		TotalTargets:     len(targets),
		EnabledTargets:   enabledCount,
		TargetsByType:    targetsByType,
		QueueSize:        queueSize,
		QueueCapacity:    queueCapacity,
		QueueUtilization: utilization,
	}

	h.sendJSON(w, http.StatusOK, response)
}

// 6. GetQueueStatus - GET /api/v1/publishing/queue
func (h *PublishingHandlers) GetQueueStatus(w http.ResponseWriter, r *http.Request) {
	queueSize := h.queue.GetQueueSize()
	queueCapacity := h.queue.GetQueueCapacity()
	utilization := 0.0
	if queueCapacity > 0 {
		utilization = float64(queueSize) / float64(queueCapacity) * 100
	}

	response := QueueStatusResponse{
		Size:         queueSize,
		Capacity:     queueCapacity,
		Utilization:  utilization,
		WorkersCount: 10, // From config, hardcoded for now
	}

	h.sendJSON(w, http.StatusOK, response)
}

// 7. GetPublishingMode - GET /api/v1/publishing/mode
func (h *PublishingHandlers) GetPublishingMode(w http.ResponseWriter, r *http.Request) {
	// Use ModeManager if available (TN-060)
	if h.modeManager != nil {
		modeMetrics := h.modeManager.GetModeMetrics()
		currentMode := h.modeManager.GetCurrentMode()

		// Count enabled targets
		targets := h.discoveryManager.ListTargets()
		enabledCount := 0
		for _, t := range targets {
			if t.Enabled {
				enabledCount++
			}
		}
		targetsAvailable := enabledCount > 0

		response := PublishingModeResponse{
			Mode:                      currentMode.String(),
			TargetsAvailable:          targetsAvailable,
			EnabledTargets:           enabledCount,
			MetricsOnlyActive:         currentMode == ModeMetricsOnly,
			TransitionCount:           modeMetrics.TransitionCount,
			CurrentModeDurationSeconds: modeMetrics.CurrentModeDuration.Seconds(),
			LastTransitionTime:        modeMetrics.LastTransitionTime,
			LastTransitionReason:      modeMetrics.LastTransitionReason,
		}

		h.sendJSON(w, http.StatusOK, response)
		return
	}

	// Fallback to basic mode detection (backward compatibility)
	targets := h.discoveryManager.ListTargets()
	enabledCount := 0
	for _, t := range targets {
		if t.Enabled {
			enabledCount++
		}
	}
	targetsAvailable := enabledCount > 0

	mode := "normal"
	metricsOnly := false

	if !targetsAvailable {
		mode = "metrics-only"
		metricsOnly = true
	}

	response := PublishingModeResponse{
		Mode:              mode,
		TargetsAvailable:  targetsAvailable,
		EnabledTargets:    enabledCount,
		MetricsOnlyActive: metricsOnly,
	}

	h.sendJSON(w, http.StatusOK, response)
}

// ===== TN-056: Queue & Job Management Handlers =====

// SubmitAlert - POST /api/v1/publishing/submit
func (h *PublishingHandlers) SubmitAlert(w http.ResponseWriter, r *http.Request) {
	var req SubmitAlertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.sendError(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}

	if req.Alert == nil {
		h.sendError(w, http.StatusBadRequest, "Missing alert", "alert field is required")
		return
	}

	// Check mode before submitting (TN-060: Metrics-only mode fallback)
	if h.modeManager != nil && h.modeManager.IsMetricsOnly() {
		h.logger.Info("Alert submission rejected (metrics-only mode)",
			"alert_fingerprint", req.Alert.Fingerprint,
		)

		// Record metric (if metrics available via queue)
		// Note: ModeManager metrics are recorded in mode_manager.go

		// Return informative response
		h.sendJSON(w, http.StatusOK, SubmitAlertResponse{
			Success: false,
			Message: "Alert not submitted: system is in metrics-only mode",
			Mode:    "metrics-only",
		})
		return
	}

	// Create enriched alert (minimal enrichment for API submission)
	enrichedAlert := &core.EnrichedAlert{
		Alert: req.Alert,
		Classification: &core.ClassificationResult{
			Severity:   core.SeverityInfo,
			Confidence: 0.5,
			Reasoning:  "Submitted via API",
		},
	}

	// Submit to specific target or all targets
	var jobIDs []string
	var err error

	if req.TargetName != "" {
		// Submit to specific target
		target, err := h.discoveryManager.GetTarget(req.TargetName)
		if err != nil {
			h.sendError(w, http.StatusNotFound, "Target not found", err.Error())
			return
		}

		err = h.queue.Submit(enrichedAlert, target)
		if err != nil {
			h.sendError(w, http.StatusInternalServerError, "Failed to submit job", err.Error())
			return
		}
		jobIDs = []string{enrichedAlert.Alert.Fingerprint + ":" + target.Name}
	} else {
		// Submit to all enabled targets
		targets := h.discoveryManager.ListTargets()
		for _, target := range targets {
			if !target.Enabled {
				continue
			}

			err = h.queue.Submit(enrichedAlert, target)
			if err != nil {
				h.logger.Warn("Failed to submit to target", "target", target.Name, "error", err)
				continue
			}
			jobIDs = append(jobIDs, enrichedAlert.Alert.Fingerprint+":"+target.Name)
		}
	}

	if len(jobIDs) == 0 {
		h.sendError(w, http.StatusInternalServerError, "No jobs created", "Failed to submit alert to any target")
		return
	}

	h.sendJSON(w, http.StatusAccepted, SubmitAlertResponse{
		Success: true,
		Message: fmt.Sprintf("Alert submitted to %d target(s)", len(jobIDs)),
		JobIDs:  jobIDs,
	})
}

// GetDetailedQueueStats - GET /api/v1/publishing/queue/stats
func (h *PublishingHandlers) GetDetailedQueueStats(w http.ResponseWriter, r *http.Request) {
	stats := h.queue.GetStats()

	// Calculate success rate
	successRate := 0.0
	if stats.TotalSubmitted > 0 {
		successRate = float64(stats.TotalCompleted) / float64(stats.TotalSubmitted) * 100
	}

	// Get DLQ stats
	dlqStats, err := h.queue.dlqRepository.GetStats(r.Context())
	dlqSize := 0
	if err == nil && dlqStats != nil {
		dlqSize = dlqStats.TotalEntries
	}

	response := DetailedQueueStatsResponse{
		TotalSize:      stats.TotalSize,
		HighPriority:   stats.HighPriority,
		MedPriority:    stats.MedPriority,
		LowPriority:    stats.LowPriority,
		Capacity:       stats.Capacity,
		WorkerCount:    stats.WorkerCount,
		ActiveJobs:     stats.ActiveJobs,
		TotalSubmitted: stats.TotalSubmitted,
		TotalCompleted: stats.TotalCompleted,
		TotalFailed:    stats.TotalFailed,
		SuccessRate:    successRate,
		DLQSize:        dlqSize,
	}

	h.sendJSON(w, http.StatusOK, response)
}

// GetJob - GET /api/v1/publishing/jobs/{id}
func (h *PublishingHandlers) GetJob(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	jobID := vars["id"]

	job := h.queue.jobTrackingStore.Get(jobID)
	if job == nil {
		h.sendError(w, http.StatusNotFound, "Job not found", fmt.Sprintf("Job %s does not exist", jobID))
		return
	}

	response := jobSnapshotToResponse(job)
	h.sendJSON(w, http.StatusOK, response)
}

// ListJobs - GET /api/v1/publishing/jobs
func (h *PublishingHandlers) ListJobs(w http.ResponseWriter, r *http.Request) {
	// Get query parameters for filtering
	query := r.URL.Query()

	filters := JobFilters{
		State:      query.Get("state"),       // pending, processing, completed, failed, retrying
		TargetName: query.Get("target"),      // target name
		Priority:   query.Get("priority"),    // high, medium, low
	}

	// Get jobs from tracking store
	jobs := h.queue.jobTrackingStore.List(filters)

	// Convert to response format
	responses := make([]JobStatusResponse, 0, len(jobs))
	for _, job := range jobs {
		responses = append(responses, jobSnapshotToResponse(job))
	}

	h.sendJSON(w, http.StatusOK, JobListResponse{
		Jobs:       responses,
		TotalCount: len(responses),
	})
}

// ===== TN-056: DLQ Management Handlers =====

// ListDLQEntries - GET /api/v1/publishing/dlq
func (h *PublishingHandlers) ListDLQEntries(w http.ResponseWriter, r *http.Request) {
	// Get query parameters
	query := r.URL.Query()

	filters := DLQFilters{
		TargetName: query.Get("target"),
		ErrorType:  query.Get("error_type"),
		Priority:   query.Get("priority"),
	}

	// Parse replayed filter
	if replayedStr := query.Get("replayed"); replayedStr != "" {
		replayed := replayedStr == "true"
		filters.Replayed = &replayed
	}

	// Parse limit (default 100, max 1000)
	limit := 100
	if limitStr := query.Get("limit"); limitStr != "" {
		if parsedLimit, err := strconv.Atoi(limitStr); err == nil {
			if parsedLimit > 0 && parsedLimit <= 1000 {
				limit = parsedLimit
			}
		}
	}
	filters.Limit = limit

	// Parse offset (default 0)
	offset := 0
	if offsetStr := query.Get("offset"); offsetStr != "" {
		if parsedOffset, err := strconv.Atoi(offsetStr); err == nil && parsedOffset >= 0 {
			offset = parsedOffset
		}
	}
	filters.Offset = offset

	// Fetch entries
	entries, err := h.queue.dlqRepository.Read(r.Context(), filters)
	if err != nil {
		h.sendError(w, http.StatusInternalServerError, "Failed to read DLQ", err.Error())
		return
	}

	// Convert to response format
	responses := make([]DLQEntryResponse, 0, len(entries))
	for _, entry := range entries {
		responses = append(responses, DLQEntryResponse{
			ID:           entry.ID.String(),
			JobID:        entry.JobID.String(),
			Fingerprint:  entry.Fingerprint,
			TargetName:   entry.TargetName,
			TargetType:   entry.TargetType,
			ErrorMessage: entry.ErrorMessage,
			ErrorType:    entry.ErrorType,
			RetryCount:   entry.RetryCount,
			Priority:     entry.Priority,
			FailedAt:     entry.FailedAt,
			Replayed:     entry.Replayed,
			ReplayedAt:   entry.ReplayedAt,
		})
	}

	// Get DLQ statistics if requested
	var stats *DLQStatsResponse
	if query.Get("include_stats") == "true" {
		dlqStats, err := h.queue.dlqRepository.GetStats(r.Context())
		if err == nil {
			stats = &DLQStatsResponse{
				TotalEntries:       dlqStats.TotalEntries,
				EntriesByErrorType: dlqStats.EntriesByErrorType,
				EntriesByTarget:    dlqStats.EntriesByTarget,
				EntriesByPriority:  dlqStats.EntriesByPriority,
				ReplayedCount:      dlqStats.ReplayedCount,
			}
		}
	}

	h.sendJSON(w, http.StatusOK, DLQListResponse{
		Entries:    responses,
		TotalCount: len(responses),
		Stats:      stats,
	})
}

// ReplayDLQEntry - POST /api/v1/publishing/dlq/{id}/replay
func (h *PublishingHandlers) ReplayDLQEntry(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	idStr := vars["id"]

	// Parse UUID
	id, err := uuid.Parse(idStr)
	if err != nil {
		h.sendError(w, http.StatusBadRequest, "Invalid DLQ entry ID", err.Error())
		return
	}

	// Replay entry
	err = h.queue.dlqRepository.Replay(r.Context(), id)
	if err != nil {
		h.sendJSON(w, http.StatusOK, ReplayDLQResponse{
			Success: false,
			Message: "Replay failed",
			Error:   err.Error(),
		})
		return
	}

	h.sendJSON(w, http.StatusOK, ReplayDLQResponse{
		Success: true,
		Message: "DLQ entry replayed successfully",
	})
}

// PurgeDLQ - DELETE /api/v1/publishing/dlq/purge
func (h *PublishingHandlers) PurgeDLQ(w http.ResponseWriter, r *http.Request) {
	// Parse request body (optional)
	var req PurgeDLQRequest
	req.OlderThanHours = 168 // Default: 7 days

	if r.Body != http.NoBody {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			// Ignore decode errors, use default
			h.logger.Debug("Failed to decode purge request, using defaults", "error", err)
		}
	}

	// Validate
	if req.OlderThanHours <= 0 {
		req.OlderThanHours = 168
	}

	// Purge
	olderThan := time.Duration(req.OlderThanHours) * time.Hour
	deletedCount, err := h.queue.dlqRepository.Purge(r.Context(), olderThan)
	if err != nil {
		h.sendError(w, http.StatusInternalServerError, "Purge failed", err.Error())
		return
	}

	h.sendJSON(w, http.StatusOK, PurgeDLQResponse{
		Success:      true,
		Message:      fmt.Sprintf("Purged %d DLQ entries older than %d hours", deletedCount, req.OlderThanHours),
		DeletedCount: deletedCount,
	})
}

// Helper methods

// jobSnapshotToResponse converts JobSnapshot to JobStatusResponse
func jobSnapshotToResponse(job *JobSnapshot) JobStatusResponse {
	response := JobStatusResponse{
		ID:          job.ID,
		Fingerprint: job.Fingerprint,
		TargetName:  job.TargetName,
		Priority:    job.Priority,
		State:       job.State,
		RetryCount:  job.RetryCount,
		MaxRetries:  0, // Not tracked in snapshot
		CreatedAt:   time.Unix(job.SubmittedAt, 0),
	}

	if job.ErrorType != "" {
		response.ErrorType = job.ErrorType
		// LastError is not available in snapshot
	}

	if job.CompletedAt != nil {
		completedTime := time.Unix(*job.CompletedAt, 0)
		response.CompletedAt = &completedTime
		processingTime := completedTime.Sub(time.Unix(job.SubmittedAt, 0))
		response.ProcessingTime = processingTime.String()
	}

	return response
}

func (h *PublishingHandlers) sendJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		h.logger.Error("Failed to encode JSON response", "error", err)
	}
}

func (h *PublishingHandlers) sendError(w http.ResponseWriter, status int, message string, details string) {
	h.logger.Warn("API error", "status", status, "message", message, "details", details)
	h.sendJSON(w, status, ErrorResponse{
		Error:   message,
		Message: details,
	})
}

func (h *PublishingHandlers) createTestAlert(alertName string) *core.EnrichedAlert {
	if alertName == "" {
		alertName = "TestAlert"
	}

	now := time.Now()
	generatorURL := "http://test/alert"

	return &core.EnrichedAlert{
		Alert: &core.Alert{
			Fingerprint: "test-" + alertName,
			AlertName:   alertName,
			Status:      core.StatusFiring,
			Labels: map[string]string{
				"alertname": alertName,
				"severity":  "info",
				"test":      "true",
			},
			Annotations: map[string]string{
				"summary":     "Test alert for target validation",
				"description": "This is a test alert sent via API",
			},
			StartsAt:     now,
			GeneratorURL: &generatorURL,
		},
		Classification: &core.ClassificationResult{
			Severity:   core.SeverityInfo,
			Confidence: 1.0,
			Reasoning:  "Test alert",
			Recommendations: []string{
				"This is a test - no action required",
			},
		},
	}
}
