package handlers

import (
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/ipiton/AMP/internal/buildinfo"
	appconfig "github.com/ipiton/AMP/internal/config"
)

// StatusResponse represents the response for /api/v2/status, matching the
// upstream Alertmanager API v2 shape: a nested `config` object (not a flat
// "config.original" key), `versionInfo`, `cluster` and `uptime`.
type StatusResponse struct {
	Cluster     ClusterStatus `json:"cluster"`
	VersionInfo VersionInfo   `json:"versionInfo"`
	Config      StatusConfig  `json:"config"`
	Uptime      time.Time     `json:"uptime"`
}

// StatusConfig is the nested `config` object in /api/v2/status: `{"original": "..."}`.
type StatusConfig struct {
	Original string `json:"original"`
}

// ClusterStatus is the `cluster` field of /api/v2/status, matching
// upstream's own shape: {"status", "name", "peers": [{"name","address"}]}.
//
// Task 6.5 (alertmanager-parity, Phase 6): AMP's clustering is a Redis peer
// heartbeat (internal/infrastructure/cluster), not upstream's
// memberlist/gossip — but the wire shape is upstream-compatible. "disabled"
// (Name/Peers omitted) is reported for the lite profile, or a standard
// profile deployment without a live Redis cache backend (no heartbeat
// registry wired at all — see ServiceRegistry.initializeClusterHeartbeat).
// "ready" is reported once this replica's own heartbeat has successfully
// registered; there is no intermediate "settling" state because
// registration is synchronous (the first SET happens inside Start, before
// Initialize returns) — either it's registered by the time the HTTP server
// starts serving, or the whole heartbeat is absent (registration failure
// only logs a degraded reason, it does not fail startup).
type ClusterStatus struct {
	Status string        `json:"status"`
	Name   string        `json:"name,omitempty"`
	Peers  []ClusterPeer `json:"peers,omitempty"`
}

// ClusterPeer is one entry of ClusterStatus.Peers, matching upstream's
// {"name","address"} peer shape.
type ClusterPeer struct {
	Name    string `json:"name"`
	Address string `json:"address,omitempty"`
}

// VersionInfo represents the version information
type VersionInfo struct {
	Version   string `json:"version"`
	Revision  string `json:"revision"`
	Branch    string `json:"branch"`
	BuildUser string `json:"buildUser"`
	BuildDate string `json:"buildDate"`
	GoVersion string `json:"goVersion"`
}

func StatusAPIHandler(registry RegistryProvider) http.HandlerFunc {
	return getOnly(func(w http.ResponseWriter, r *http.Request) {
		configPath := os.Getenv("AMP_CONFIG_FILE")
		if configPath == "" {
			configPath = "config.yaml"
		}

		configContent, err := os.ReadFile(configPath)
		if err != nil {
			// Fallback if file not found
			configContent = []byte("# config file not found")
		}

		resp := StatusResponse{
			Cluster: registry.ClusterStatus(r.Context()),
			Config:  StatusConfig{Original: string(configContent)},
			VersionInfo: VersionInfo{
				Version:   buildinfo.Version,
				Revision:  buildinfo.Revision,
				Branch:    buildinfo.Branch,
				BuildUser: buildinfo.BuildUser,
				BuildDate: buildinfo.BuildDate,
				GoVersion: runtime.Version(),
			},
			Uptime: registry.StartTime(),
		}

		writeJSON(w, http.StatusOK, resp)
	})
}

func ReloadHandler(registry RegistryProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		if err := registry.ReloadConfig(r.Context()); err != nil {
			InternalErrorHandler(w, "failed to reload configuration: "+err.Error())
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	}
}

func ReceiversHandler(registry RegistryProvider) http.HandlerFunc {
	return getOnly(func(w http.ResponseWriter, r *http.Request) {
		receivers := registry.Config().Receivers
		if len(receivers) == 0 {
			// Fallback to default if empty
			receivers = []appconfig.ReceiverConfig{{Name: "default"}}
		}
		writeJSON(w, http.StatusOK, receivers)
	})
}
