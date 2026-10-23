package publishing

import (
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/ipiton/AMP/internal/core"
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

// PUBLISH-SECRETS-IN-LOGS: a VALID target is logged at Debug too, and for
// Teams/Discord/Google Chat/Slack the token is part of the URL path.
func TestParseAndValidateSecrets_DoesNotLogValidTargetURLToken(t *testing.T) {
	var logBuf strings.Builder
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	manager := &DefaultTargetDiscoveryManager{
		namespace:     "default",
		labelSelector: "publishing-target=true",
		cache:         newTargetCache(),
		logger:        logger,
	}

	const token = "sup3rSecretToken"
	configJSON, err := json.Marshal(core.PublishingTarget{
		Name:    "teams",
		Type:    "webhook",
		URL:     "https://outlook.office.com/webhookb2/" + token,
		Format:  "webhook",
		Enabled: true,
	})
	require.NoError(t, err)
	secrets := []corev1.Secret{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "teams-target",
				Namespace: "default",
				Labels:    map[string]string{"publishing-target": "true"},
			},
			Data: map[string][]byte{
				"config": []byte(base64.StdEncoding.EncodeToString(configJSON)),
			},
		},
	}

	targets, invalid := manager.parseAndValidateSecrets(secrets)
	require.Len(t, targets, 1, "sanity: the secret must be accepted")
	require.Zero(t, invalid)

	logged := logBuf.String()
	assert.Contains(t, logged, "Parsed valid target")
	assert.Contains(t, logged, "https://outlook.office.com/***")
	assert.NotContains(t, logged, token)
}
