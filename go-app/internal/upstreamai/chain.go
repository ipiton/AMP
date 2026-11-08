package upstreamai

import (
	"github.com/prometheus/alertmanager/alert"
	"github.com/prometheus/alertmanager/provider/mem"
	"github.com/prometheus/common/model"
)

// Chain runs callbacks in order, for upstreamcore.Options.NewAlertCallback,
// which takes one. PreStore stops at the first error, which rejects the
// alert as it would on its own.
func Chain(callbacks ...mem.AlertStoreCallback) mem.AlertStoreCallback {
	return chain(callbacks)
}

type chain []mem.AlertStoreCallback

func (c chain) PreStore(a *alert.Alert, existing bool) error {
	for _, cb := range c {
		if err := cb.PreStore(a, existing); err != nil {
			return err
		}
	}
	return nil
}

func (c chain) PostStore(a *alert.Alert, existing bool) {
	for _, cb := range c {
		cb.PostStore(a, existing)
	}
}

func (c chain) PostDelete(a *alert.Alert) {
	for _, cb := range c {
		cb.PostDelete(a)
	}
}

func (c chain) PostGC(fps model.Fingerprints) {
	for _, cb := range c {
		cb.PostGC(fps)
	}
}
