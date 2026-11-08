// Package upstreamtargets adds AMP's Kubernetes-Secret publishing targets to
// the upstream Alertmanager configuration as receiver integrations (ADR-019
// slice UPSTREAM-NON-UPSTREAM-RECEIVERS).
//
// Targets are discovered as the old core discovers them
// (business/publishing.TargetDiscoveryManager: label publishing-target=true,
// JSON in data.config, amp.receiver scoping) and held as a snapshot.
// Source.ExtendConfig is upstreamcore.Options.ExtendConfig: on every config
// apply it returns a copy of the file's configuration with each target
// appended, as a webhook_configs, slack_configs or pagerduty_configs entry,
// to the receivers it belongs to. Delivery, retries, the notification log and
// templates are upstream's. The file and /api/v2/status never see the
// Secrets' values. Source.Run polls for changes and reloads the core when the
// snapshot changes.
package upstreamtargets

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/alertmanager/config"
	"github.com/prometheus/alertmanager/config/receiver"
	"github.com/prometheus/alertmanager/template"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/ipiton/AMP/internal/business/publishing"
	"github.com/ipiton/AMP/internal/core"
)

// Source holds the latest snapshot of K8s targets.
type Source struct {
	discovery publishing.TargetDiscoveryManager
	logger    *slog.Logger
	targets   *prometheus.GaugeVec
	// checkTmpl only lets receiver.BuildReceiverIntegrations validate an
	// integration; the core builds the real ones with the config's
	// templates.
	checkTmpl *template.Template

	mu          sync.Mutex
	snapshot    []*core.PublishingTarget
	fingerprint [sha256.Size]byte
}

// New returns a Source with an empty snapshot; call Refresh to fill it.
func New(discovery publishing.TargetDiscoveryManager, logger *slog.Logger, reg prometheus.Registerer) (*Source, error) {
	if discovery == nil || logger == nil {
		return nil, errors.New("upstreamtargets: discovery and logger are required")
	}
	tmpl, err := template.FromGlobs(nil)
	if err != nil {
		return nil, fmt.Errorf("upstreamtargets: default templates: %w", err)
	}
	return &Source{
		discovery: discovery,
		logger:    logger.With("component", "k8s-targets"),
		targets: promauto.With(reg).NewGaugeVec(prometheus.GaugeOpts{
			Name: "amp_upstream_k8s_targets",
			Help: "K8s-Secret publishing targets in the applied configuration, by state (attached or skipped).",
		}, []string{"state"}),
		checkTmpl: tmpl,
	}, nil
}

// Refresh lists the targets and replaces the snapshot. changed reports
// whether the set of enabled targets differs from the previous snapshot. On
// a K8s API error the snapshot is kept and the error returned.
func (s *Source) Refresh(ctx context.Context) (changed bool, err error) {
	if err := s.discovery.DiscoverTargets(ctx); err != nil {
		return false, err
	}
	var enabled []*core.PublishingTarget
	for _, t := range s.discovery.ListTargets() {
		if t != nil && t.Enabled {
			enabled = append(enabled, t)
		}
	}
	slices.SortStableFunc(enabled, func(a, b *core.PublishingTarget) int { return strings.Compare(a.Name, b.Name) })

	// The JSON round trip is both the fingerprint and a deep copy, so the
	// snapshot shares no maps with the discovery cache.
	raw, err := json.Marshal(enabled)
	if err != nil {
		return false, fmt.Errorf("upstreamtargets: snapshot: %w", err)
	}
	var snapshot []*core.PublishingTarget
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return false, fmt.Errorf("upstreamtargets: snapshot: %w", err)
	}
	fp := sha256.Sum256(raw)

	s.mu.Lock()
	defer s.mu.Unlock()
	changed = fp != s.fingerprint
	s.snapshot = snapshot
	s.fingerprint = fp
	return changed, nil
}

// Run refreshes every interval and calls reload when the snapshot changed,
// until ctx is done.
func (s *Source) Run(ctx context.Context, interval time.Duration, reload func() error) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		changed, err := s.Refresh(ctx)
		if err != nil {
			s.logger.Warn("K8s target refresh failed, keeping the previous targets", "err", err)
			continue
		}
		if !changed {
			continue
		}
		s.logger.Info("K8s targets changed, reloading the configuration")
		if err := reload(); err != nil {
			s.logger.Error("configuration reload after a K8s target change failed", "err", err)
		}
	}
}

// ExtendConfig returns conf with the snapshot's targets appended to the
// receivers they belong to; conf itself is not modified. A target that cannot
// be converted or matches no receiver is skipped with a warning, so it never
// fails the reload: the error result is always nil.
func (s *Source) ExtendConfig(conf *config.Config) (*config.Config, error) {
	s.mu.Lock()
	snapshot := s.snapshot
	s.mu.Unlock()

	var attached, skipped int
	defer func() {
		s.targets.WithLabelValues("attached").Set(float64(attached))
		s.targets.WithLabelValues("skipped").Set(float64(skipped))
	}()
	if len(snapshot) == 0 {
		return conf, nil
	}

	// A shallow copy keeps the unexported original text that /api/v2/status
	// serves; only the receivers that gain an integration get new slices.
	out := *conf
	out.Receivers = slices.Clone(conf.Receivers)
	names := make(map[string]struct{}, len(out.Receivers))
	for _, rc := range out.Receivers {
		names[rc.Name] = struct{}{}
	}

	for _, t := range snapshot {
		in, err := s.build(t, conf.Global)
		if err != nil {
			s.logSkip(t, err)
			skipped++
			continue
		}
		for _, name := range t.Receivers {
			if _, ok := names[name]; !ok {
				s.logger.Warn("K8s target names a receiver that is not in the configuration",
					"target", t.Name, "receiver", name)
			}
		}
		matched := 0
		for i := range out.Receivers {
			if belongsTo(t, out.Receivers[i].Name) {
				in.appendTo(&out.Receivers[i])
				matched++
			}
		}
		if matched == 0 {
			s.logSkip(t, skip(reasonNoReceiver, errors.New("no receiver in the configuration matches the target")))
			skipped++
			continue
		}
		attached++
	}
	return &out, nil
}

// build converts t and checks that upstream can build a notifier from it, so
// a bad target is skipped here instead of failing the core's reload.
func (s *Source) build(t *core.PublishingTarget, global *config.GlobalConfig) (integration, error) {
	in, err := convert(t, global)
	if err != nil {
		return integration{}, err
	}
	check := config.Receiver{Name: t.Name}
	in.appendTo(&check)
	if _, err := receiver.BuildReceiverIntegrations(check, s.checkTmpl, s.logger); err != nil {
		return integration{}, skip(reasonInvalid, err)
	}
	return in, nil
}

// belongsTo is the old core's targetMatchesReceiver
// (infrastructure/publishing/coordinator.go): an unscoped target belongs to
// every receiver, a scoped one to those it names and to the receiver of its
// own name.
func belongsTo(t *core.PublishingTarget, receiverName string) bool {
	if len(t.Receivers) == 0 {
		return true
	}
	return slices.Contains(t.Receivers, receiverName) || t.Name == receiverName
}

func (s *Source) logSkip(t *core.PublishingTarget, err error) {
	reason := reasonInvalid
	var se *skipError
	if errors.As(err, &se) {
		reason = se.reason
		err = se.err
	}
	s.logger.Warn("K8s target skipped",
		"target", t.Name,
		"type", t.Type,
		"reason", reason,
		"err", redact(err.Error(), t))
}
