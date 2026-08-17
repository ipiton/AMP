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

// ClusterStatus is a stub for the `cluster` field. AMP has no clustering yet
// (that lands in a later phase); "disabled" is upstream's own value for a
// non-clustered Alertmanager instance.
type ClusterStatus struct {
	Status string `json:"status"`
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
			Cluster: ClusterStatus{Status: "disabled"},
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
