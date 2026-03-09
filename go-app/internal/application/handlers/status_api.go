package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"runtime"
	"time"

	appconfig "github.com/ipiton/AMP/internal/config"
)

// StatusResponse represents the response for /api/v2/status
type StatusResponse struct {
	ConfigOriginal string      `json:"config.original"`
	VersionInfo    VersionInfo `json:"versionInfo"`
	Uptime         time.Time   `json:"uptime"`
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
	return func(w http.ResponseWriter, r *http.Request) {
		configPath := os.Getenv("AMP_CONFIG_FILE")
		if configPath == "" {
			configPath = "config.yaml"
		}

		configContent, err := os.ReadFile(configPath)
		if err != nil {
			// Fallback if file not found
			configContent = []byte("# config file not found")
		}

		// Hardcoded for now, should be injected or from build tags
		version := "0.0.1"
		revision := "unknown"
		branch := "main"
		buildUser := "ipiton"
		buildDate := time.Now().Format(time.RFC3339)

		resp := StatusResponse{
			ConfigOriginal: string(configContent),
			VersionInfo: VersionInfo{
				Version:   version,
				Revision:  revision,
				Branch:    branch,
				BuildUser: buildUser,
				BuildDate: buildDate,
				GoVersion: runtime.Version(),
			},
			Uptime: registry.StartTime(),
		}

		writeJSON(w, http.StatusOK, resp)
	}
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
	return func(w http.ResponseWriter, r *http.Request) {
		receivers := registry.Config().Receivers
		if len(receivers) == 0 {
			// Fallback to default if empty
			receivers = []appconfig.ReceiverConfig{{Name: "default"}}
		}
		writeJSON(w, http.StatusOK, receivers)
	}
}

func configSHA256(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}
