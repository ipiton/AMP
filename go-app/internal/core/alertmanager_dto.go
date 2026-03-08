package core

import "time"

// Alertmanager API v2 DTOs for compatibility and internal state

// AlertIngestInput represents a single alert from a POST /api/v2/alerts request
type AlertIngestInput struct {
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     string            `json:"startsAt"`
	EndsAt       string            `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
	Fingerprint  string            `json:"fingerprint"`
	Status       string            `json:"status"`
}

// StoredAlertState represents the internal in-memory state of an alert
type StoredAlertState struct {
	DedupKey        string
	BaseFingerprint string
	Labels          map[string]string
	Annotations     map[string]string
	StartsAt        time.Time
	EndsAt          *time.Time
	GeneratorURL    string
	Status          string
	UpdatedAt       time.Time
}

// APIReceiver represents a receiver in Alertmanager API
type APIReceiver struct {
	Name string `json:"name"`
}

// APIAlertStatus represents the status of an alert in GET /api/v2/alerts
type APIAlertStatus struct {
	State       string   `json:"state"`
	SilencedBy  []string `json:"silencedBy"`
	InhibitedBy []string `json:"inhibitedBy"`
	MutedBy     []string `json:"mutedBy"`
}

// APIAlert represents a single alert in GET /api/v2/alerts response
type APIAlert struct {
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations,omitempty"`
	Receivers    []APIReceiver     `json:"receivers,omitempty"`
	StartsAt     string            `json:"startsAt"`
	UpdatedAt    string            `json:"updatedAt,omitempty"`
	EndsAt       *string           `json:"endsAt,omitempty"`
	GeneratorURL string            `json:"generatorURL,omitempty"`
	Fingerprint  string            `json:"fingerprint,omitempty"`
	Status       string            `json:"status"` // "firing" or "resolved" in simple DTO, or use Status field from APIAlertStatus
}

// APIGettableAlert is the full Alertmanager API v2 gettable alert
type APIGettableAlert struct {
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	Receivers    []APIReceiver     `json:"receivers"`
	StartsAt     string            `json:"startsAt"`
	UpdatedAt    string            `json:"updatedAt"`
	EndsAt       string            `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL,omitempty"`
	Fingerprint  string            `json:"fingerprint"`
	Status       APIAlertStatus    `json:"status"`
}

// APIAlertGroup represents a group of alerts in GET /api/v2/alerts/groups
type APIAlertGroup struct {
	Labels   map[string]string `json:"labels"`
	Receiver APIReceiver       `json:"receiver"`
	Alerts   []APIAlert        `json:"alerts"`
}

// APIGettableAlertGroup is the full Alertmanager API v2 alert group
type APIGettableAlertGroup struct {
	Labels   map[string]string  `json:"labels"`
	Receiver APIReceiver        `json:"receiver"`
	Alerts   []APIGettableAlert `json:"alerts"`
}

// Silence DTOs

// SilenceMatcherInput represents a label matcher in silence creation/update
type SilenceMatcherInput struct {
	Name    string `json:"name"`
	Value   string `json:"value"`
	IsRegex bool   `json:"isRegex,omitempty"`
	IsEqual *bool  `json:"isEqual,omitempty"`
}

// SilenceInput represents the payload for creating or updating a silence
type SilenceInput struct {
	ID        string                `json:"id,omitempty"`
	Matchers  []SilenceMatcherInput `json:"matchers"`
	StartsAt  string                `json:"startsAt"`
	EndsAt    string                `json:"endsAt"`
	CreatedBy string                `json:"createdBy"`
	Comment   string                `json:"comment"`
}

// StoredSilenceMatcher represents the internal state of a silence matcher
type StoredSilenceMatcher struct {
	Name    string
	Value   string
	IsRegex bool
	IsEqual bool
}

// StoredSilenceState represents the internal in-memory state of a silence
type StoredSilenceState struct {
	ID        string
	Matchers  []StoredSilenceMatcher
	StartsAt  time.Time
	EndsAt    time.Time
	CreatedBy string
	Comment   string
	UpdatedAt time.Time
}

// APISilenceMatcher represents a label matcher in a silence
type APISilenceMatcher struct {
	Name    string `json:"name"`
	Value   string `json:"value"`
	IsRegex bool   `json:"isRegex"`
	IsEqual bool   `json:"isEqual"`
}

// APISilenceStatus represents the status of a silence
type APISilenceStatus struct {
	State string `json:"state"`
}

// APISilence represents a silence in the Alertmanager API
type APISilence struct {
	ID        string              `json:"id"`
	Matchers  []APISilenceMatcher `json:"matchers"`
	StartsAt  string              `json:"startsAt"`
	EndsAt    string              `json:"endsAt"`
	UpdatedAt string              `json:"updatedAt"`
	CreatedBy string              `json:"createdBy"`
	Comment   string              `json:"comment"`
	Status    APISilenceStatus    `json:"status"`
}
