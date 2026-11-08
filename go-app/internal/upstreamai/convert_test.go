package upstreamai

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/core/alertconv"
)

// The history, investigations and classification cache are keyed by AMP's
// SHA-256 fingerprint; upstream's API shows its FNV one. Both come from the
// same labels, so one finds the other.
func TestFingerprintsOfOneAlertMatchBothBinaries(t *testing.T) {
	a := firing("DiskFull")
	labels := labelMap(a.Labels)

	assert.Equal(t, alertconv.Fingerprint(labels), toCoreAlert(a).Fingerprint)
	assert.Equal(t, a.Fingerprint().String(), alertconv.UpstreamFingerprint(labels))
}

func TestToCoreAlertStatus(t *testing.T) {
	assert.Equal(t, core.StatusFiring, toCoreAlert(firing("DiskFull")).Status)
	assert.Equal(t, core.StatusResolved, toCoreAlert(resolved("DiskFull")).Status)
}
