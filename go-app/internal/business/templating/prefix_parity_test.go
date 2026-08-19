// This file is in the EXTERNAL test package (templating_test) on purpose.
//
// It imports internal/business/publishing, and slice 2 will have the publishing
// side importing internal/business/templating. An IN-PACKAGE test file
// (`package templating`) importing publishing would then be an import cycle —
// Go rejects a cycle through an in-package test, but exempts an external test
// package. Keeping the one cross-package assertion here means slice 2 can add
// its import without this test having to be deleted.
package templating_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/ipiton/AMP/internal/business/publishing"
	"github.com/ipiton/AMP/internal/business/templating"
)

// TestReceiverNameFromTarget_MatchesPublishingEncoding pins
// ReceiverNameFromTarget to the REAL target-name encoding rather than to
// templating's own copy of the prefix: if publishing ever changes
// ConfigTargetName's shape, this fails instead of silently leaking
// `cfg:team-x/slack0` into notification titles.
func TestReceiverNameFromTarget_MatchesPublishingEncoding(t *testing.T) {
	assert.Equal(t, "cfg:", publishing.ConfigTargetPrefix,
		"templating.configTargetPrefix duplicates this literal")

	for _, kind := range []string{"webhook", "slack", "pagerduty", "telegram", "email"} {
		for _, index := range []int{0, 3, 42} {
			target := publishing.ConfigTargetName("team-x", kind, index)
			assert.Equal(t, "team-x", templating.ReceiverNameFromTarget(target), "target %q", target)
		}
	}
}
