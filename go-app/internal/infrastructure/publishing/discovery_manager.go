package publishing

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/infrastructure/k8s"
	corev1 "k8s.io/api/core/v1"
)

// TargetDiscoveryManager manages discovery of publishing targets from K8s secrets
type TargetDiscoveryManager interface {
	// DiscoverTargets discovers all publishing targets from K8s secrets
	DiscoverTargets(ctx context.Context) error

	// GetTarget returns a specific target by name
	GetTarget(name string) (*core.PublishingTarget, error)

	// ListTargets returns all discovered targets
	ListTargets() []*core.PublishingTarget

	// GetTargetsByType returns targets filtered by type
	GetTargetsByType(targetType string) []*core.PublishingTarget

	// GetTargetCount returns the number of discovered targets
	GetTargetCount() int
}

// TargetDiscoveryConfig holds configuration for target discovery
type TargetDiscoveryConfig struct {
	// Namespace to search for secrets (default: "default")
	Namespace string

	// LabelSelector for filtering secrets (default: "publishing-target=true")
	LabelSelector string

	// Logger for structured logging
	Logger *slog.Logger
}

// DefaultTargetDiscoveryConfig returns config with sensible defaults
func DefaultTargetDiscoveryConfig() *TargetDiscoveryConfig {
	return &TargetDiscoveryConfig{
		Namespace:     "default",
		LabelSelector: "publishing-target=true",
		Logger:        slog.Default(),
	}
}

// DefaultTargetDiscoveryManager implements TargetDiscoveryManager
type DefaultTargetDiscoveryManager struct {
	k8sClient k8s.K8sClient
	config    *TargetDiscoveryConfig
	logger    *slog.Logger

	targets map[string]*core.PublishingTarget
	mu      sync.RWMutex
}

// NewTargetDiscoveryManager creates a new target discovery manager
func NewTargetDiscoveryManager(k8sClient k8s.K8sClient, config *TargetDiscoveryConfig) (TargetDiscoveryManager, error) {
	if k8sClient == nil {
		return nil, fmt.Errorf("k8s client is required")
	}

	if config == nil {
		config = DefaultTargetDiscoveryConfig()
	}

	return &DefaultTargetDiscoveryManager{
		k8sClient: k8sClient,
		config:    config,
		logger:    config.Logger,
		targets:   make(map[string]*core.PublishingTarget),
	}, nil
}

// DiscoverTargets discovers all publishing targets from K8s secrets
func (m *DefaultTargetDiscoveryManager) DiscoverTargets(ctx context.Context) error {
	m.logger.Info("Starting target discovery",
		"namespace", m.config.Namespace,
		"label_selector", m.config.LabelSelector,
	)

	// List secrets from K8s
	secrets, err := m.k8sClient.ListSecrets(ctx, m.config.Namespace, m.config.LabelSelector)
	if err != nil {
		m.logger.Error("Failed to list secrets", "error", err)
		return fmt.Errorf("failed to list secrets: %w", err)
	}

	m.logger.Debug("Found secrets", "count", len(secrets))

	// Parse secrets into targets
	newTargets := make(map[string]*core.PublishingTarget)
	for _, secret := range secrets {
		target, err := m.parseSecretToTarget(&secret)
		if err != nil {
			m.logger.Warn("Failed to parse secret",
				"secret_name", secret.Name,
				"namespace", secret.Namespace,
				"error", err,
			)
			continue
		}

		newTargets[target.Name] = target
		m.logger.Debug("Discovered target",
			"name", target.Name,
			"type", target.Type,
			"url", target.URL,
		)
	}

	// Update targets atomically
	m.mu.Lock()
	m.targets = newTargets
	m.mu.Unlock()

	m.logger.Info("Target discovery completed",
		"targets_count", len(newTargets),
	)

	return nil
}

// GetTarget returns a specific target by name
func (m *DefaultTargetDiscoveryManager) GetTarget(name string) (*core.PublishingTarget, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	target, exists := m.targets[name]
	if !exists {
		return nil, fmt.Errorf("target %s not found", name)
	}

	return target, nil
}

// ListTargets returns all discovered targets
func (m *DefaultTargetDiscoveryManager) ListTargets() []*core.PublishingTarget {
	m.mu.RLock()
	defer m.mu.RUnlock()

	targets := make([]*core.PublishingTarget, 0, len(m.targets))
	for _, target := range m.targets {
		targets = append(targets, target)
	}

	return targets
}

// GetTargetsByType returns targets filtered by type
func (m *DefaultTargetDiscoveryManager) GetTargetsByType(targetType string) []*core.PublishingTarget {
	m.mu.RLock()
	defer m.mu.RUnlock()

	targets := make([]*core.PublishingTarget, 0)
	for _, target := range m.targets {
		if target.Type == targetType {
			targets = append(targets, target)
		}
	}

	return targets
}

// GetTargetCount returns the number of discovered targets
func (m *DefaultTargetDiscoveryManager) GetTargetCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return len(m.targets)
}

// parseSecretToTarget converts a K8s secret to a PublishingTarget
func (m *DefaultTargetDiscoveryManager) parseSecretToTarget(secret *corev1.Secret) (*core.PublishingTarget, error) {
	// Helper to decode base64 data
	decodeData := func(key string) string {
		if data, ok := secret.Data[key]; ok {
			return string(data)
		}
		return ""
	}

	// Extract required fields
	name := decodeData("name")
	if name == "" {
		name = secret.Name // Fallback to secret name
	}

	targetType := decodeData("type")
	if targetType == "" {
		return nil, fmt.Errorf("missing required field: type")
	}

	url := decodeData("url")
	if url == "" {
		return nil, fmt.Errorf("missing required field: url")
	}

	// Extract optional fields
	enabled := true
	if enabledStr := decodeData("enabled"); enabledStr != "" {
		enabled = strings.ToLower(enabledStr) == "true"
	}

	format := decodeData("format")
	if format == "" {
		format = targetType // Default format = type
	}

	// Parse headers (format: key1=value1,key2=value2)
	headers := make(map[string]string)
	if headersStr := decodeData("headers"); headersStr != "" {
		pairs := strings.Split(headersStr, ",")
		for _, pair := range pairs {
			parts := strings.SplitN(pair, "=", 2)
			if len(parts) == 2 {
				headers[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
			}
		}
	}

	// Add auth headers if present
	if apiKey := decodeData("api_key"); apiKey != "" {
		headers["Authorization"] = "Bearer " + apiKey
	}
	if authToken := decodeData("auth_token"); authToken != "" {
		headers["Authorization"] = "Bearer " + authToken
	}

	target := &core.PublishingTarget{
		Name:    name,
		Type:    targetType,
		URL:     url,
		Enabled: enabled,
		Format:  core.PublishingFormat(format),
		Headers: headers,
	}

	return target, nil
}
