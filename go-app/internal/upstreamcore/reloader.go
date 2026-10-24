// This file is a port of
// github.com/prometheus/alertmanager@v0.34.1/app/reloader.go without the
// gossip peer (see doc.go):
//
//	Copyright The Prometheus Authors
//	Licensed under the Apache License, Version 2.0 (the "License");
//	you may not use this file except in compliance with the License.
//	You may obtain a copy of the License at
//
//	    http://www.apache.org/licenses/LICENSE-2.0
//
//	Unless required by applicable law or agreed to in writing, software
//	distributed under the License is distributed on an "AS IS" BASIS,
//	WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//	See the License for the specific language governing permissions and
//	limitations under the License.

package upstreamcore

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/prometheus/alertmanager/alert"
	"github.com/prometheus/alertmanager/api"
	"github.com/prometheus/alertmanager/config"
	"github.com/prometheus/alertmanager/config/receiver"
	"github.com/prometheus/alertmanager/dispatch"
	"github.com/prometheus/alertmanager/eventrecorder"
	"github.com/prometheus/alertmanager/inhibit"
	"github.com/prometheus/alertmanager/marker"
	"github.com/prometheus/alertmanager/notify"
	"github.com/prometheus/alertmanager/provider"
	"github.com/prometheus/alertmanager/silence"
	"github.com/prometheus/alertmanager/template"
	"github.com/prometheus/alertmanager/timeinterval"
	"github.com/prometheus/alertmanager/tracing"
	"github.com/prometheus/common/model"
)

// reloader owns the configuration-scoped subgraph: routing tree, receivers,
// notification pipeline, inhibitor and dispatcher. They are rebuilt and
// swapped on every config apply; the long-lived components they depend on
// are built once in New and only updated in place.
type reloader struct {
	// Long-lived components: created once during startup.
	alerts            provider.Alerts
	apih              *api.API
	dispatcherMetrics *dispatch.DispatcherMetrics
	eventRecorder     eventrecorder.Recorder
	groupMarker       marker.GroupMarker
	logger            *slog.Logger
	metrics           *metrics
	notificationLog   notify.NotificationLog
	pipelineBuilder   *notify.PipelineBuilder
	silencer          *silence.Silencer
	tracingMgr        *tracing.Manager

	// Short-lived components: atomically swapped on every reload.
	dispatcher atomic.Pointer[dispatch.Dispatcher]
	inhibitor  atomic.Pointer[inhibit.Inhibitor]

	// Functions and values used during reload.
	waitFunc    func() time.Duration
	timeoutFunc func(time.Duration) time.Duration
	externalURL *url.URL
	startTime   time.Time

	// Static configuration values.
	dispatchStartDelay          time.Duration
	dispatchMaintenanceInterval time.Duration
	retention                   time.Duration
}

// groups returns the alert groups from the currently active dispatcher. It
// is wired into the API as its GroupFunc.
func (r *reloader) groups(ctx context.Context, routeFilter func(*dispatch.Route) bool, alertFilter func(*alert.Alert, time.Time) bool) (dispatch.AlertGroups, map[model.Fingerprint][]string, error) {
	return r.dispatcher.Load().Groups(ctx, routeFilter, alertFilter)
}

// reload rebuilds the config-scoped subgraph from conf and swaps it in. It
// is the config coordinator's subscriber, so it runs for the initial config
// and on every reload. All fallible work happens before any live component
// is touched, so a failed reload leaves the active configuration intact.
func (r *reloader) reload(conf *config.Config) error {
	configLogger := r.logger.With("component", "configuration")

	tmpl, err := template.FromGlobs(conf.Templates)
	if err != nil {
		return fmt.Errorf("failed to parse templates: %w", err)
	}
	tmpl.ExternalURL = r.externalURL

	// Build the routing tree and record which receivers are used.
	routes := dispatch.NewRoute(conf.Route, nil)
	activeReceivers := make(map[string]struct{})
	routes.Walk(func(rt *dispatch.Route) {
		activeReceivers[rt.RouteOpts.Receiver] = struct{}{}
	})

	// Build the map of receiver to integrations.
	receivers := make(map[string][]notify.Integration, len(activeReceivers))
	var integrationsNum int
	for _, rcv := range conf.Receivers {
		if _, found := activeReceivers[rcv.Name]; !found {
			// No need to build a receiver if no route is using it.
			configLogger.Info("skipping creation of receiver not referenced by any route", "receiver", rcv.Name)
			continue
		}
		integrations, err := receiver.BuildReceiverIntegrations(rcv, tmpl, r.logger)
		if err != nil {
			return err
		}
		// rcv.Name is guaranteed to be unique across all receivers.
		receivers[rcv.Name] = integrations
		integrationsNum += len(integrations)
	}

	// Build the map of time interval names to time interval definitions.
	timeIntervals := make(map[string][]timeinterval.TimeInterval, len(conf.MuteTimeIntervals)+len(conf.TimeIntervals))
	for _, ti := range conf.MuteTimeIntervals {
		timeIntervals[ti.Name] = ti.TimeIntervals
	}
	for _, ti := range conf.TimeIntervals {
		timeIntervals[ti.Name] = ti.TimeIntervals
	}

	intervener := timeinterval.NewIntervener(timeIntervals)

	// Tracing is the last fallible step; applying it before stopping the
	// old components keeps them running if it errors.
	if err := r.tracingMgr.ApplyConfig(conf.TracingConfig); err != nil {
		return fmt.Errorf("failed to apply tracing config: %w", err)
	}

	// Reload event recorder outputs before stopping the old dispatcher so
	// events emitted while it shuts down go to the new outputs.
	r.eventRecorder.ApplyConfig(conf.EventRecorder)

	if old := r.inhibitor.Load(); old != nil {
		old.Stop()
	}
	if old := r.dispatcher.Load(); old != nil {
		old.Stop()
	}

	newInhibitor := inhibit.NewInhibitor(r.alerts, conf.InhibitRules, r.logger, r.eventRecorder)

	// No gossip cluster yet: notify detects that through a nil interface,
	// which an explicitly typed nil pointer would not be.
	pipeline := r.pipelineBuilder.New(
		receivers,
		r.waitFunc,
		newInhibitor,
		r.silencer,
		intervener,
		r.groupMarker,
		r.notificationLog,
		nil,
	)

	r.metrics.configuredReceivers.Set(float64(len(activeReceivers)))
	r.metrics.configuredIntegrations.Set(float64(integrationsNum))
	r.metrics.configuredInhibitionRules.Set(float64(len(conf.InhibitRules)))

	r.apih.Update(conf, func(ctx context.Context, labels model.LabelSet) {
		r.inhibitor.Load().Mutes(ctx, labels)
		r.silencer.Mutes(ctx, labels)
	})

	newDispatcher := dispatch.NewDispatcher(
		r.alerts,
		routes,
		pipeline,
		r.groupMarker,
		r.timeoutFunc,
		r.dispatchMaintenanceInterval,
		nil,
		r.logger,
		r.eventRecorder,
		r.dispatcherMetrics,
		tmpl,
	)
	routes.Walk(func(rt *dispatch.Route) {
		if rt.RouteOpts.RepeatInterval > r.retention {
			configLogger.Warn(
				"repeat_interval is greater than the data retention period. It can lead to notifications being repeated more often than expected.",
				"repeat_interval", rt.RouteOpts.RepeatInterval,
				"retention", r.retention,
				"route", rt.Key(),
			)
		}
		if rt.RouteOpts.RepeatInterval < rt.RouteOpts.GroupInterval {
			configLogger.Warn(
				"repeat_interval is less than group_interval. Notifications will not repeat until the next group_interval.",
				"repeat_interval", rt.RouteOpts.RepeatInterval,
				"group_interval", rt.RouteOpts.GroupInterval,
				"route", rt.Key(),
			)
		}
	})

	// Start the inhibitor first and publish it only once its cache is
	// loaded, so neither the dispatcher nor the API mute callback sees an
	// empty inhibition cache.
	go newInhibitor.Run()
	newInhibitor.WaitForLoading()
	r.inhibitor.Store(newInhibitor)

	// Then the dispatcher, published only after its aggregation groups are
	// loaded.
	go newDispatcher.Run(r.startTime.Add(r.dispatchStartDelay))
	newDispatcher.WaitForLoading()
	r.dispatcher.Store(newDispatcher)

	return nil
}

// stop tears down the active inhibitor and dispatcher. It is safe to call
// before any config has been applied (both pointers nil).
func (r *reloader) stop() error {
	if i := r.inhibitor.Load(); i != nil {
		i.Stop()
	}
	if d := r.dispatcher.Load(); d != nil {
		d.Stop()
	}
	return nil
}
