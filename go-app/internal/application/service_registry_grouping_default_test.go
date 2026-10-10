package application

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appconfig "github.com/ipiton/AMP/internal/config"
	"github.com/ipiton/AMP/internal/core"
)

// groupingFallbackWarning is AlertProcessor's notice that grouping is on but
// an alert went out ungrouped.
const groupingFallbackWarning = "Grouping enabled but this alert has no usable routing decision/group manager"

// A copied alertmanager.yml has a route: tree and no AMP-specific grouping
// key. It must group (PROD-GROUPING-DEFAULT).
func TestGroupingDefault_RouteTreeWithoutGroupingKeyStartsGrouping(t *testing.T) {
	t.Setenv("ENVIRONMENT", "development")
	t.Setenv("PROFILE", "lite")
	t.Setenv("STORAGE_BACKEND", "filesystem")

	path := filepath.Join(t.TempDir(), "config.yaml")
	configYAML := `
server:
  port: 8080

route:
  receiver: team-x
  group_by: [alertname]

receivers:
  - name: team-x
    webhook_configs:
      - url: http://127.0.0.1:9/hook
`
	if err := os.WriteFile(path, []byte(configYAML), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := appconfig.LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if !cfg.HasRouteTree() {
		t.Fatalf("sanity: the route: tree must have been parsed")
	}

	r := newTestRegistryForGrouping(cfg)
	ctx := context.Background()
	if err := r.initializeGrouping(ctx); err != nil {
		t.Fatalf("initializeGrouping() error = %v", err)
	}
	if r.groupManager == nil || r.groupTimerManager == nil {
		t.Fatalf("grouping must start for a config with a route: tree and no grouping: section")
	}

	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := r.groupTimerManager.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("groupTimerManager.Shutdown() error = %v", err)
	}
}

// newRegistryForAlertProcessor builds the minimum initializeAlertProcessor
// needs, with the logger captured.
func newRegistryForAlertProcessor(cfg *appconfig.Config) (*ServiceRegistry, *bytes.Buffer) {
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	return &ServiceRegistry{
		config:       cfg,
		logger:       logger,
		filterEngine: &contractFilterEngine{},
		publisher:    NewMetricsOnlyPublisher("test", logger),
	}, logs
}

func processOneAlert(t *testing.T, r *ServiceRegistry) {
	t.Helper()
	if err := r.initializeAlertProcessor(context.Background()); err != nil {
		t.Fatalf("initializeAlertProcessor() error = %v", err)
	}
	alert := &core.Alert{
		Fingerprint: "fp-1",
		AlertName:   "HighCPU",
		Status:      core.StatusFiring,
		Labels:      map[string]string{"alertname": "HighCPU"},
		StartsAt:    time.Now(),
	}
	if err := r.alertProcessor.ProcessAlert(context.Background(), alert); err != nil {
		t.Fatalf("ProcessAlert() error = %v", err)
	}
}

// grouping.enabled is true by default, so a setup with no route: tree has the
// key on without ever asking for grouping. It must not be told on every alert
// that grouping fell back to direct publishing.
func TestEffectiveGrouping_EnabledWithoutRouteTreeDoesNotWarn(t *testing.T) {
	r, logs := newRegistryForAlertProcessor(&appconfig.Config{
		Profile:  appconfig.ProfileLite,
		Grouping: appconfig.GroupingConfig{Enabled: true},
	})

	processOneAlert(t, r)

	if strings.Contains(logs.String(), groupingFallbackWarning) {
		t.Fatalf("fallback warning logged without a route: tree; logs:\n%s", logs.String())
	}
}

// With a route: tree the key means what it says: if the subsystem did not come
// up, the alert is published ungrouped and the fallback stays loud.
func TestEffectiveGrouping_EnabledWithRouteTreeKeepsFallbackWarning(t *testing.T) {
	r, logs := newRegistryForAlertProcessor(&appconfig.Config{
		Profile:  appconfig.ProfileLite,
		Grouping: appconfig.GroupingConfig{Enabled: true},
		Routing:  minimalRouteTree(),
	})
	// r.groupManager stays nil: the grouping subsystem is not initialized.

	processOneAlert(t, r)

	if !strings.Contains(logs.String(), groupingFallbackWarning) {
		t.Fatalf("fallback warning missing with a route: tree and no group manager; logs:\n%s", logs.String())
	}
}

func TestEffectiveGrouping_DisabledWithRouteTreeDoesNotWarnPerAlert(t *testing.T) {
	r, logs := newRegistryForAlertProcessor(&appconfig.Config{
		Profile:  appconfig.ProfileLite,
		Grouping: appconfig.GroupingConfig{Enabled: false},
		Routing:  minimalRouteTree(),
	})

	processOneAlert(t, r)

	if strings.Contains(logs.String(), groupingFallbackWarning) {
		t.Fatalf("fallback warning logged although grouping is turned off; logs:\n%s", logs.String())
	}
}
