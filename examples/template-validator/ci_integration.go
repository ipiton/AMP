package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
)

// ================================================================================
// TN-156: Template Validator - CI/CD Integration Example
// ================================================================================
// Demonstrates CI/CD integration with JSON output and exit codes.

// CIResult represents CI-friendly validation result
type CIResult struct {
	Valid            bool     `json:"valid"`
	TotalTemplates   int      `json:"total_templates"`
	PassedTemplates  int      `json:"passed_templates"`
	FailedTemplates  int      `json:"failed_templates"`
	TotalErrors      int      `json:"total_errors"`
	TotalWarnings    int      `json:"total_warnings"`
	CriticalErrors   int      `json:"critical_errors"`
	FailedFiles      []string `json:"failed_files,omitempty"`
}

func main() {
	fmt.Println("=== Template Validator - CI/CD Integration Example ===\n")

	// Simulate CI validation
	result := validateTemplatesForCI()

	// Print JSON output
	jsonOutput, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(jsonOutput))

	// Exit with appropriate code
	exitCode := determineExitCode(result)
	fmt.Printf("\nExit code: %d\n", exitCode)

	// In real CI, would call: os.Exit(exitCode)
}

func validateTemplatesForCI() CIResult {
	// Simulate validation of templates directory
	return CIResult{
		Valid:           false,
		TotalTemplates:  5,
		PassedTemplates: 3,
		FailedTemplates: 2,
		TotalErrors:     2,
		TotalWarnings:   1,
		CriticalErrors:  1,
		FailedFiles: []string{
			"templates/webhook_invalid.tmpl",
			"templates/security_issue.tmpl",
		},
	}
}

func determineExitCode(result CIResult) int {
	if result.TotalErrors > 0 {
		return 1 // Errors found
	}
	if result.TotalWarnings > 0 {
		return 2 // Warnings only
	}
	return 0 // Success
}

// ================================================================================
// GitHub Actions Workflow Example:
//
// name: Validate Templates
//
// on: [push, pull_request]
//
// jobs:
//   validate:
//     runs-on: ubuntu-latest
//     steps:
//       - uses: actions/checkout@v3
//
//       - name: Setup Go
//         uses: actions/setup-go@v4
//         with:
//           go-version: '1.22'
//
//       - name: Install template-validator
//         run: |
//           go install github.com/ipiton/AMP/cmd/template-validator@latest
//
//       - name: Validate templates
//         run: |
//           template-validator validate templates/ \
//             --output=sarif \
//             --mode=strict \
//             > results.sarif
//
//       - name: Upload SARIF to GitHub Code Scanning
//         uses: github/codeql-action/upload-sarif@v2
//         with:
//           sarif_file: results.sarif
//         if: always()
// ================================================================================

// ================================================================================
// GitLab CI Example:
//
// template-validation:
//   stage: test
//   image: golang:1.22
//   script:
//     - go install github.com/ipiton/AMP/cmd/template-validator@latest
//     - template-validator validate templates/ --output=json --mode=strict > results.json
//   artifacts:
//     reports:
//       codequality: results.json
//     when: always
//   allow_failure: false
// ================================================================================

// ================================================================================
// Real-world CI integration:
//
// package main
//
// import (
//     "context"
//     "encoding/json"
//     "fmt"
//     "os"
//
//     "github.com/ipiton/AMP/pkg/templatevalidator"
//     "github.com/ipiton/AMP/pkg/templatevalidator/formatters"
//     template "github.com/ipiton/AMP/internal/notification/template"
// )
//
// func main() {
//     // Create validator
//     engine := template.NewNotificationTemplateEngine(
//         template.DefaultTemplateEngineOptions(),
//     )
//     validator := templatevalidator.New(engine)
//
//     // Validate templates directory
//     opts := templatevalidator.DefaultValidateOptions().
//         WithMode(templatevalidator.ModeStrict)
//
//     // Get all template files
//     files := findTemplateFiles("templates/")
//
//     // Batch validate
//     inputs := make([]templatevalidator.TemplateInput, len(files))
//     for i, file := range files {
//         content, _ := os.ReadFile(file)
//         inputs[i] = templatevalidator.TemplateInput{
//             Name:    file,
//             Content: string(content),
//             Options: opts,
//         }
//     }
//
//     results, err := validator.ValidateBatch(context.Background(), inputs)
//     if err != nil {
//         fmt.Fprintf(os.Stderr, "Validation error: %v\n", err)
//         os.Exit(1)
//     }
//
//     // Format output (JSON for CI)
//     formatter := formatters.NewJSONFormatter()
//     output, _ := formatter.Format(results, files)
//     fmt.Println(output)
//
//     // Determine exit code
//     hasErrors := false
//     for _, result := range results {
//         if result.HasErrors() {
//             hasErrors = true
//             break
//         }
//     }
//
//     if hasErrors {
//         os.Exit(1) // Fail CI build
//     }
//
//     os.Exit(0) // Success
// }
// ================================================================================
