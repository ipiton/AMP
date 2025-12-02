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
	"sync"
	"time"

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

// DefaultK8sClient implements K8sClient using k8s.io/client-go.
type DefaultK8sClient struct {
	clientset kubernetes.Interface
	config    *K8sClientConfig
	logger    *slog.Logger
	mu        sync.RWMutex // For thread-safe configuration updates
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
		listOptions := metav1.ListOptions{
			LabelSelector: labelSelector,
			Limit:         1000, // Pagination support
		}

		secretList, err := c.clientset.CoreV1().Secrets(namespace).List(ctx, listOptions)
		if err != nil {
			return err
		}

		secrets = secretList.Items

		// Handle pagination if needed
		if secretList.Continue != "" {
			c.logger.Warn("Secrets list truncated, pagination not implemented",
				"namespace", namespace,
				"continue_token", secretList.Continue,
			)
		}

		return nil
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

	// Lightweight API call to check connectivity
	// Using Discovery().ServerVersion() is standard health check
	_, err := c.clientset.Discovery().ServerVersion()
	if err != nil {
		c.logger.Warn("K8s health check failed", "error", err)
		return NewConnectionError("K8s API unavailable", err)
	}

	// Check if context was cancelled during operation
	if healthCtx.Err() != nil {
		return NewTimeoutError("health check timeout", healthCtx.Err())
	}

	return nil
}

// Close cleans up resources.
func (c *DefaultK8sClient) Close() error {
	c.logger.Info("Closing K8s client")

	c.mu.Lock()
	defer c.mu.Unlock()

	// client-go's clientset doesn't have explicit Close()
	// But we can nil out references for GC
	c.clientset = nil

	c.logger.Info("K8s client closed")
	return nil
}

// retryWithBackoff executes operation with exponential backoff retry logic.
func (c *DefaultK8sClient) retryWithBackoff(ctx context.Context, operation func() error) error {
	backoff := c.config.RetryBackoff

	for attempt := 0; attempt <= c.config.MaxRetries; attempt++ {
		// Check context cancellation before attempt
		select {
		case <-ctx.Done():
			return NewTimeoutError("operation cancelled", ctx.Err())
		default:
		}

		err := operation()
		if err == nil {
			return nil // Success
		}

		// Check if error is retryable
		if !isRetryableError(err) {
			return err // Permanent error, no retry
		}

		// Last attempt - return error
		if attempt == c.config.MaxRetries {
			return err
		}

		// Log retry
		c.logger.Warn("Retrying K8s operation",
			"attempt", attempt+1,
			"max_retries", c.config.MaxRetries,
			"backoff", backoff,
			"error", err,
		)

		// Wait with backoff
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return NewTimeoutError("operation cancelled during backoff", ctx.Err())
		}

		// Exponential backoff
		backoff *= 2
		if backoff > c.config.MaxRetryBackoff {
			backoff = c.config.MaxRetryBackoff
		}
	}

	return fmt.Errorf("operation failed after %d retries", c.config.MaxRetries)
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
