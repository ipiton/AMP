package config

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// ================================================================================
// Configuration Diff Calculator
// ================================================================================
// Calculates structured diff between two configurations (TN-150).
//
// Features:
// - Deep comparison of nested structures
// - Identifies added, modified, deleted fields
// - Sanitizes secrets in diff output
// - Identifies affected components
// - Detects critical changes
//
// Performance Target: < 20ms p95
// Quality Target: 150% (Grade A+ EXCEPTIONAL)
// Author: AI Assistant
// Date: 2025-11-22

// DefaultConfigComparator implements ConfigComparator interface
type DefaultConfigComparator struct {
	secretFields map[string]bool
}

// NewConfigComparator creates a new ConfigComparator instance
func NewConfigComparator() *DefaultConfigComparator {
	return &DefaultConfigComparator{
		secretFields: buildSecretFieldsMap(),
	}
}

// Compare implements ConfigComparator.Compare
//
// Calculates structured diff between old and new configurations
// Returns diff with added, modified, deleted fields
//
// Performance: < 20ms p95
func (cc *DefaultConfigComparator) Compare(oldCfg *Config, newCfg *Config, sections []string) (*ConfigDiff, error) {
	diff := NewConfigDiff()

	// Convert configs to maps for easier comparison
	oldMap, err := cc.configToMap(oldCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to convert old config to map: %w", err)
	}

	newMap, err := cc.configToMap(newCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to convert new config to map: %w", err)
	}

	// Filter by sections if specified
	if len(sections) > 0 {
		oldMap = cc.filterSections(oldMap, sections)
		newMap = cc.filterSections(newMap, sections)
	}

	// Calculate diff recursively
	cc.compareRecursive(oldMap, newMap, "", diff)

	// Identify affected components
	diff.Affected = cc.IdentifyAffectedComponents(diff)

	// Detect critical changes
	diff.IsCritical = cc.IsCriticalChange(diff)

	// Generate summary
	diff.Summary = diff.GenerateSummary()

	return diff, nil
}

// compareRecursive recursively compares two maps and populates diff
func (cc *DefaultConfigComparator) compareRecursive(oldMap, newMap map[string]interface{}, prefix string, diff *ConfigDiff) {
	// Find added and modified fields
	for key, newValue := range newMap {
		fieldPath := cc.buildFieldPath(prefix, key)
		oldValue, oldExists := oldMap[key]

		if !oldExists {
			// Field was added
			diff.Added[fieldPath] = cc.sanitizeFieldValue(fieldPath, newValue)
		} else {
			// Field exists in both, check if modified
			if cc.isModified(oldValue, newValue) {
				// Handle nested maps recursively
				oldMapVal, oldIsMap := oldValue.(map[string]interface{})
				newMapVal, newIsMap := newValue.(map[string]interface{})

				if oldIsMap && newIsMap {
					// Recurse into nested map
					cc.compareRecursive(oldMapVal, newMapVal, fieldPath, diff)
				} else {
					// Simple value modification
					diff.Modified[fieldPath] = DiffEntry{
						OldValue: cc.sanitizeFieldValue(fieldPath, oldValue),
						NewValue: cc.sanitizeFieldValue(fieldPath, newValue),
						Type:     cc.detectType(newValue),
					}
				}
			}
		}
	}

	// Find deleted fields
	for key := range oldMap {
		fieldPath := cc.buildFieldPath(prefix, key)
		if _, exists := newMap[key]; !exists {
			diff.Deleted = append(diff.Deleted, fieldPath)
		}
	}
}

// IdentifyAffectedComponents implements ConfigComparator.IdentifyAffectedComponents
//
// Returns list of component names that need hot reload based on diff
func (cc *DefaultConfigComparator) IdentifyAffectedComponents(diff *ConfigDiff) []string {
	affected := make(map[string]bool)

	// Check all changed fields
	allFields := make([]string, 0)
	for field := range diff.Added {
		allFields = append(allFields, field)
	}
	for field := range diff.Modified {
		allFields = append(allFields, field)
	}
	allFields = append(allFields, diff.Deleted...)

	// Map fields to components
	for _, field := range allFields {
		component := cc.fieldToComponent(field)
		if component != "" {
			affected[component] = true
		}
	}

	// Convert map to slice
	components := make([]string, 0, len(affected))
	for component := range affected {
		components = append(components, component)
	}

	return components
}

// IsCriticalChange implements ConfigComparator.IsCriticalChange
//
// Returns true if diff contains critical changes that require extra caution
func (cc *DefaultConfigComparator) IsCriticalChange(diff *ConfigDiff) bool {
	criticalFields := map[string]bool{
		"database.host":               true,
		"database.port":               true,
		"database.driver":             true,
		"redis.addr":                  true,
		"server.port":                 true,
		"webhook.authentication.enabled": true,
		"webhook.signature.enabled":   true,
	}

	// Check if any critical field was modified or deleted
	for field := range diff.Modified {
		if criticalFields[field] {
			return true
		}
	}

	for _, field := range diff.Deleted {
		if criticalFields[field] {
			return true
		}
	}

	return false
}

// ================================================================================
// Helper Functions
// ================================================================================

// configToMap converts Config struct to map[string]interface{}
func (cc *DefaultConfigComparator) configToMap(cfg *Config) (map[string]interface{}, error) {
	// Use JSON serialization for conversion
	configJSON, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal config: %w", err)
	}

	var configMap map[string]interface{}
	if err := json.Unmarshal(configJSON, &configMap); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config to map: %w", err)
	}

	return configMap, nil
}

// filterSections filters map to include only specified sections
func (cc *DefaultConfigComparator) filterSections(configMap map[string]interface{}, sections []string) map[string]interface{} {
	filtered := make(map[string]interface{})

	for _, section := range sections {
		if value, exists := configMap[section]; exists {
			filtered[section] = value
		}
	}

	return filtered
}

// buildFieldPath builds field path from prefix and key
func (cc *DefaultConfigComparator) buildFieldPath(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

// isModified checks if two values are different
func (cc *DefaultConfigComparator) isModified(oldValue, newValue interface{}) bool {
	// Use reflect.DeepEqual for comprehensive comparison
	return !reflect.DeepEqual(oldValue, newValue)
}

// sanitizeFieldValue sanitizes value if it's a secret field
func (cc *DefaultConfigComparator) sanitizeFieldValue(fieldPath string, value interface{}) interface{} {
	if cc.secretFields[fieldPath] {
		return "***REDACTED***"
	}

	// Also check for common secret keywords in field path
	lowerPath := strings.ToLower(fieldPath)
	secretKeywords := []string{"password", "secret", "api_key", "apikey", "token", "jwt"}
	for _, keyword := range secretKeywords {
		if strings.Contains(lowerPath, keyword) {
			return "***REDACTED***"
		}
	}

	return value
}

// detectType detects value type for better formatting
func (cc *DefaultConfigComparator) detectType(value interface{}) string {
	switch value.(type) {
	case int, int32, int64, uint, uint32, uint64:
		return "integer"
	case float32, float64:
		return "float"
	case bool:
		return "boolean"
	case string:
		return "string"
	case map[string]interface{}:
		return "object"
	case []interface{}:
		return "array"
	default:
		return "unknown"
	}
}

// fieldToComponent maps field path to component name
func (cc *DefaultConfigComparator) fieldToComponent(field string) string {
	// Extract top-level section from field path
	parts := strings.Split(field, ".")
	if len(parts) == 0 {
		return ""
	}

	section := parts[0]

	// Map section to component name
	componentMap := map[string]string{
		"server":   "server",
		"database": "database",
		"redis":    "redis",
		"llm":      "llm",
		"log":      "logger",
		"cache":    "cache",
		"lock":     "lock",
		"app":      "app",
		"metrics":  "metrics",
		"webhook":  "webhook",
	}

	if component, exists := componentMap[section]; exists {
		return component
	}

	return section
}

// ================================================================================
// Utility Functions
// ================================================================================

// CalculateDiff is a convenience function to calculate diff between two configs
//
// Usage:
//
//	diff, err := CalculateDiff(oldConfig, newConfig, nil)
//	if err != nil {
//	    return err
//	}
//	fmt.Printf("Changes: %s\n", diff.Summary)
func CalculateDiff(oldCfg *Config, newCfg *Config, sections []string) (*ConfigDiff, error) {
	comparator := NewConfigComparator()
	return comparator.Compare(oldCfg, newCfg, sections)
}

// DiffToString converts diff to human-readable string
func DiffToString(diff *ConfigDiff) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Summary: %s\n", diff.Summary))

	if len(diff.Added) > 0 {
		sb.WriteString("\nAdded:\n")
		for field, value := range diff.Added {
			sb.WriteString(fmt.Sprintf("  + %s: %v\n", field, value))
		}
	}

	if len(diff.Modified) > 0 {
		sb.WriteString("\nModified:\n")
		for field, entry := range diff.Modified {
			sb.WriteString(fmt.Sprintf("  ~ %s: %v → %v\n", field, entry.OldValue, entry.NewValue))
		}
	}

	if len(diff.Deleted) > 0 {
		sb.WriteString("\nDeleted:\n")
		for _, field := range diff.Deleted {
			sb.WriteString(fmt.Sprintf("  - %s\n", field))
		}
	}

	if len(diff.Affected) > 0 {
		sb.WriteString(fmt.Sprintf("\nAffected Components: %s\n", strings.Join(diff.Affected, ", ")))
	}

	if diff.IsCritical {
		sb.WriteString("\n⚠️  WARNING: Contains critical changes!\n")
	}

	return sb.String()
}

// MergeDiffs merges multiple diffs into one
func MergeDiffs(diffs ...*ConfigDiff) *ConfigDiff {
	merged := NewConfigDiff()

	for _, diff := range diffs {
		// Merge added fields
		for field, value := range diff.Added {
			merged.Added[field] = value
		}

		// Merge modified fields
		for field, entry := range diff.Modified {
			merged.Modified[field] = entry
		}

		// Merge deleted fields
		merged.Deleted = append(merged.Deleted, diff.Deleted...)

		// Merge affected components (deduplicate)
		for _, component := range diff.Affected {
			if !contains(merged.Affected, component) {
				merged.Affected = append(merged.Affected, component)
			}
		}

		// Merge critical flag (OR logic)
		merged.IsCritical = merged.IsCritical || diff.IsCritical
	}

	// Regenerate summary
	merged.Summary = merged.GenerateSummary()

	return merged
}

// contains checks if slice contains string
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// ================================================================================
// Type Alias for Interface Implementation
// ================================================================================

// Ensure DefaultConfigComparator implements ConfigComparator interface
var _ ConfigComparator = (*DefaultConfigComparator)(nil)
