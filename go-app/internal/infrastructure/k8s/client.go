// Package k8s provides a Kubernetes client wrapper for the Publishing System.
//
// This package wraps k8s.io/client-go with a simplified interface for discovering
// and managing Kubernetes Secrets that contain publishing target configurations.
//
// Example usage:
//
//	config := DefaultK8sClientConfig()
//	client, err := NewK8sClient(config)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer client.Close()
//
//	secrets, err := client.ListSecrets(ctx, "default", "publishing-target=true")
//	if err != nil {
//	    log.Fatal(err)
//	}
//
// See TN-046 for detailed design documentation.
package k8s

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ipiton/AMP/pkg/retry"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// K8sClient defines the interface for Kubernetes operations needed by Publishing System.
// It provides a simplified API for working with Kubernetes Secrets.
type K8sClient interface {
	// ListSecrets returns secrets from namespace matching label selector.
	// Returns empty slice if no secrets match the selector.
	ListSecrets(ctx context.Context, namespace string, labelSelector string) ([]corev1.Secret, error)

	// GetSecret returns a specific secret by name.
	// Returns NotFoundError if secret doesn't exist.
	GetSecret(ctx context.Context, namespace, name string) (*corev1.Secret, error)

	// Health checks if K8s API is accessible.
	// Returns ConnectionError if API is unavailable.
	Health(ctx context.Context) error

	// Close cleans up resources.
	// Safe to call multiple times.
	Close() error
}

// K8sClientConfig holds configuration for K8s client.
type K8sClientConfig struct {
	// Timeout for K8s API requests (default 30s)
	Timeout time.Duration

	// MaxRetries for transient errors (default 3)
	MaxRetries int

	// RetryBackoff initial backoff duration (default 100ms)
	RetryBackoff time.Duration

	// MaxRetryBackoff maximum backoff duration (default 5s)
	MaxRetryBackoff time.Duration

	// Logger for structured logging
	Logger *slog.Logger
}

// DefaultK8sClientConfig returns configuration with sensible defaults.
func DefaultK8sClientConfig() *K8sClientConfig {
	return &K8sClientConfig{
		Timeout:         30 * time.Second,
		MaxRetries:      3,
		RetryBackoff:    100 * time.Millisecond,
		MaxRetryBackoff: 5 * time.Second,
		Logger:          slog.Default(),
	}
}

// listSecretsPageSize bounds one List response; ListSecrets follows the
// continue token until the last page.
const listSecretsPageSize = 500

// DefaultK8sClient implements K8sClient using k8s.io/client-go.
type DefaultK8sClient struct {
	clientset kubernetes.Interface
	config    *K8sClientConfig
	logger    *slog.Logger
}

// NewK8sClient creates a new K8s client with in-cluster configuration.
// Returns ConnectionError if in-cluster config is not available or if K8s API is unreachable.
func NewK8sClient(config *K8sClientConfig) (K8sClient, error) {
	if config == nil {
		config = DefaultK8sClientConfig()
	}

	// Load in-cluster config
	k8sConfig, err := rest.InClusterConfig()
	if err != nil {
		return nil, NewConnectionError("failed to load in-cluster config", err)
	}

	// Apply timeout from config
	k8sConfig.Timeout = config.Timeout

	// Create clientset
	clientset, err := kubernetes.NewForConfig(k8sConfig)
	if err != nil {
		return nil, NewConnectionError("failed to create K8s clientset", err)
	}

	client := &DefaultK8sClient{
		clientset: clientset,
		config:    config,
		logger:    config.Logger,
	}

	// Verify connection with initial health check
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := client.Health(ctx); err != nil {
		return nil, fmt.Errorf("K8s API health check failed: %w", err)
	}

	client.logger.Info("K8s client initialized successfully")

	return client, nil
}

// ListSecrets returns secrets from namespace matching label selector.
func (c *DefaultK8sClient) ListSecrets(ctx context.Context, namespace string, labelSelector string) ([]corev1.Secret, error) {
	c.logger.Debug("Listing K8s secrets",
		"namespace", namespace,
		"label_selector", labelSelector,
	)

	var secrets []corev1.Secret
	err := c.retryWithBackoff(ctx, func() error {
		// A retry restarts from the first page: a continue token can expire
		// (410 Gone) and a partial result must not be merged with a new one.
		secrets = nil
		listOptions := metav1.ListOptions{
			LabelSelector: labelSelector,
			Limit:         listSecretsPageSize,
		}
		for {
			secretList, err := c.clientset.CoreV1().Secrets(namespace).List(ctx, listOptions)
			if err != nil {
				return err
			}
			secrets = append(secrets, secretList.Items...)
			if secretList.Continue == "" {
				return nil
			}
			listOptions.Continue = secretList.Continue
		}
	})

	if err != nil {
		c.logger.Error("Failed to list secrets",
			"namespace", namespace,
			"error", err,
		)
		return nil, wrapK8sError("list secrets", err)
	}

	c.logger.Info("Successfully listed secrets",
		"namespace", namespace,
		"count", len(secrets),
	)

	return secrets, nil
}

// GetSecret returns a specific secret by name.
func (c *DefaultK8sClient) GetSecret(ctx context.Context, namespace, name string) (*corev1.Secret, error) {
	c.logger.Debug("Getting K8s secret",
		"namespace", namespace,
		"name", name,
	)

	var secret *corev1.Secret
	err := c.retryWithBackoff(ctx, func() error {
		s, err := c.clientset.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		secret = s
		return nil
	})

	if err != nil {
		// Check for NotFound error (no retry needed)
		if isNotFoundErr(err) {
			return nil, NewNotFoundError(fmt.Sprintf("secret %s/%s not found", namespace, name))
		}

		c.logger.Error("Failed to get secret",
			"namespace", namespace,
			"name", name,
			"error", err,
		)
		return nil, wrapK8sError("get secret", err)
	}

	c.logger.Debug("Successfully got secret",
		"namespace", namespace,
		"name", name,
	)

	return secret, nil
}

// Health checks if K8s API is accessible.
func (c *DefaultK8sClient) Health(ctx context.Context) error {
	// Short timeout for health checks
	healthCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// GET /version is the lightweight call ServerVersion makes, but bound to
	// healthCtx: ServerVersion takes no context, so a hung API server would
	// hold it for the whole client timeout. The fake clientset used in tests
	// has no REST client and keeps the context-free call.
	var err error
	if rc := c.clientset.Discovery().RESTClient(); rc != nil {
		err = rc.Get().AbsPath("/version").Do(healthCtx).Error()
	} else {
		_, err = c.clientset.Discovery().ServerVersion()
	}
	// A caller's cancellation is not a timeout of the API server.
	if err := ctx.Err(); err != nil {
		return err
	}
	if healthCtx.Err() != nil {
		return NewTimeoutError("health check timeout", healthCtx.Err())
	}
	if err != nil {
		c.logger.Warn("K8s health check failed", "error", err)
		return NewConnectionError("K8s API unavailable", err)
	}

	return nil
}

// Close cleans up resources. client-go holds nothing that needs closing, so
// it is a no-op: it used to nil the clientset under a mutex while every reader
// used it without the lock, racing a shutdown against an in-flight call.
func (c *DefaultK8sClient) Close() error {
	c.logger.Info("K8s client closed")
	return nil
}

// retryWithBackoff executes operation with exponential backoff retry logic.
//
// Migrated to use pkg/retry for unified retry strategy (TN-057).
func (c *DefaultK8sClient) retryWithBackoff(ctx context.Context, operation func() error) error {
	// Create retry strategy with K8s-specific settings
	strategy := retry.Strategy{
		MaxAttempts:     c.config.MaxRetries + 1, // +1 because pkg/retry counts attempts, not retries
		BaseDelay:       c.config.RetryBackoff,
		MaxDelay:        c.config.MaxRetryBackoff,
		Multiplier:      2.0,                   // Exponential backoff
		JitterRatio:     0.1,                   // 10% jitter
		ErrorClassifier: &k8sErrorClassifier{}, // K8s-specific error classifier
		Logger:          c.logger,
		OperationName:   "k8s_operation",
	}

	// Execute with unified retry logic
	return retry.DoSimple(ctx, strategy, operation)
}

// k8sErrorClassifier implements retry.ErrorClassifier for K8s errors.
type k8sErrorClassifier struct{}

func (c *k8sErrorClassifier) IsRetryable(err error) bool {
	return isRetryableError(err)
}

// isNotFoundErr checks if error is a NotFound error.
// Extracted for easier testing/mocking.
func isNotFoundErr(err error) bool {
	// Check both our custom NotFoundError and k8s NotFound
	var notFoundErr *NotFoundError
	if err != nil {
		// First check if it's already our NotFoundError
		if e, ok := err.(*NotFoundError); ok {
			return e != nil
		}
		// Check against our error type via unwrapping
		if notFoundErr != nil && fmt.Sprintf("%T", err) == fmt.Sprintf("%T", notFoundErr) {
			return true
		}
	}
	// Use k8s error checking as fallback
	return err != nil && (fmt.Sprintf("%v", err) == "not found" || fmt.Sprintf("%T", err) == "*errors.StatusError")
}
