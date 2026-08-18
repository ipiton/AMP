package publishing

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Final review finding 18: parseAndValidateSecrets logged valErr.Value at Debug.
// These targets come from Kubernetes Secrets, so the offending value is
// routinely a credential — a malformed webhook URL, an API key of the wrong
// length. Debug is not a safety boundary: it is on by default in staging and the
// line lands in whatever sink the cluster ships logs to.
func TestParseAndValidateSecrets_DoesNotLogOffendingValue(t *testing.T) {
	var logBuf strings.Builder
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	manager := &DefaultTargetDiscoveryManager{
		namespace:     "default",
		labelSelector: "publishing-target=true",
		cache:         newTargetCache(),
		logger:        logger,
	}

	// A URL that parses into a target but fails validation, carrying what would
	// be a credential in a real webhook URL.
	const secretValue = "not-a-url-sup3rSecretToken"
	secrets := []corev1.Secret{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "broken-target",
				Namespace: "default",
				Labels:    map[string]string{"publishing-target": "true"},
			},
			Data: map[string][]byte{
				"type": []byte("webhook"),
				"url":  []byte(secretValue),
			},
		},
	}

	targets, invalid := manager.parseAndValidateSecrets(secrets)
	require.Empty(t, targets, "sanity: the secret must have been rejected")
	require.Equal(t, 1, invalid)

	logged := logBuf.String()
	require.NotEmpty(t, logged, "sanity: the rejection must be logged at all")
	assert.Contains(t, logged, "broken-target", "the secret name is safe and needed for triage")
	assert.NotContains(t, logged, secretValue,
		"the offending value must NOT be logged — it is Secret material:\n%s", logged)
}
