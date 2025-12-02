package main

import (
	"fmt"
	"log"

	"github.com/ipiton/AMP/pkg/configvalidator"
)

func main() {
	// Example 1: Validate a file
	fmt.Println("=== Example 1: Validate File ===")
	validateFile()

	// Example 2: Validate bytes (YAML)
	fmt.Println("\n=== Example 2: Validate YAML Bytes ===")
	validateYAML()

	// Example 3: Validation modes
	fmt.Println("\n=== Example 3: Validation Modes ===")
	demonstrateModes()

	// Example 4: Custom options
	fmt.Println("\n=== Example 4: Custom Options ===")
	customOptions()
}

func validateFile() {
	// Create validator with default options
	validator := configvalidator.New(configvalidator.DefaultOptions())

	// Validate configuration file
	result, err := validator.ValidateFile("alertmanager.yml")
	if err != nil {
		log.Fatalf("Failed to validate: %v", err)
	}

	// Print results
	printResult(result)
}

func validateYAML() {
	config := []byte(`
global:
  resolve_timeout: 5m

route:
  receiver: default
  group_by: ['alertname', 'cluster']

receivers:
  - name: default
    webhook_configs:
      - url: https://example.com/webhook
`)

	validator := configvalidator.New(configvalidator.DefaultOptions())
	result, err := validator.ValidateBytes(config)
	if err != nil {
		log.Fatalf("Failed to validate: %v", err)
	}

	printResult(result)
}

func demonstrateModes() {
	// Configuration with warnings (HTTP instead of HTTPS)
	config := []byte(`
route:
  receiver: default
receivers:
  - name: default
    webhook_configs:
      - url: http://example.com/webhook
`)

	modes := []configvalidator.ValidationMode{
		configvalidator.StrictMode,
		configvalidator.LenientMode,
		configvalidator.PermissiveMode,
	}

	for _, mode := range modes {
		fmt.Printf("\n--- %s Mode ---\n", mode)

		opts := configvalidator.DefaultOptions()
		opts.Mode = mode
		validator := configvalidator.New(opts)

		result, _ := validator.ValidateBytes(config)
		fmt.Printf("Valid: %v\n", result.Valid)
		fmt.Printf("Errors: %d, Warnings: %d\n", len(result.Errors), len(result.Warnings))
		fmt.Printf("Exit Code: %d\n", result.ExitCode(mode))
	}
}

func customOptions() {
	opts := configvalidator.Options{
		Mode:                  configvalidator.LenientMode,
		MaxFileSize:           5 * 1024 * 1024, // 5MB
		EnableSecurityChecks:  true,
		EnableBestPractices:   true,
		IncludeContextLines:   5,
		DefaultDocsURL:        "https://prometheus.io/docs/alerting/latest/configuration/",
	}

	validator := configvalidator.New(opts)

	config := []byte(`
route:
  receiver: default
receivers:
  - name: default
    slack_configs:
      - api_url: https://hooks.slack.com/services/XXX/YYY/ZZZ
        channel: alerts
`)

	result, _ := validator.ValidateBytes(config)
	printResult(result)
}

func printResult(result *configvalidator.Result) {
	fmt.Printf("Valid: %v\n", result.Valid)
	fmt.Printf("Duration: %dms\n", result.DurationMs)

	if len(result.Errors) > 0 {
		fmt.Printf("\nErrors (%d):\n", len(result.Errors))
		for _, e := range result.Errors {
			fmt.Printf("  [%s] %s\n", e.Code, e.Message)
			if e.Suggestion != "" {
				fmt.Printf("    → %s\n", e.Suggestion)
			}
		}
	}

	if len(result.Warnings) > 0 {
		fmt.Printf("\nWarnings (%d):\n", len(result.Warnings))
		for _, w := range result.Warnings {
			fmt.Printf("  [%s] %s\n", w.Code, w.Message)
			if w.Suggestion != "" {
				fmt.Printf("    → %s\n", w.Suggestion)
			}
		}
	}

	if len(result.Info) > 0 {
		fmt.Printf("\nInfo (%d):\n", len(result.Info))
		for _, i := range result.Info {
			fmt.Printf("  [%s] %s\n", i.Code, i.Message)
		}
	}

	if len(result.Suggestions) > 0 {
		fmt.Printf("\nSuggestions (%d):\n", len(result.Suggestions))
		for _, s := range result.Suggestions {
			fmt.Printf("  [%s] %s\n", s.Code, s.Message)
		}
	}

	fmt.Printf("\n%s\n", result.Summary())
}
