package main

import (
	"context"
	"fmt"
	"log"

	"github.com/ipiton/AMP/pkg/templatevalidator"
)

// ================================================================================
// TN-156: Template Validator - Basic Usage Example
// ================================================================================
// Demonstrates basic template validation with all phases.

func main() {
	fmt.Println("=== Template Validator - Basic Usage Example ===\n")

	// Example 1: Valid template
	validTemplate := `{{ .Status | toUpper }}: {{ .Labels.alertname }}`
	validateTemplate("Valid Template", validTemplate)

	// Example 2: Syntax error
	syntaxError := `{{ .Status | invalidFunction }}`
	validateTemplate("Syntax Error", syntaxError)

	// Example 3: Unknown field
	unknownField := `{{ .UnknownField }}`
	validateTemplate("Unknown Field", unknownField)

	// Example 4: Hardcoded secret
	hardcodedSecret := `API_KEY=sk-1234567890abcdef
{{ .Status }}: {{ .Labels.alertname }}`
	validateTemplate("Hardcoded Secret", hardcodedSecret)

	// Example 5: Long line
	longLine := `{{ .Status }}: {{ .Labels.alertname }} {{ .Annotations.description }} {{ .Annotations.summary }} {{ .Annotations.runbook }} {{ .Labels.severity }}`
	validateTemplate("Long Line", longLine)
}

func validateTemplate(name, content string) {
	fmt.Printf("--- %s ---\n", name)
	fmt.Printf("Content: %s\n\n", content)

	// Create mock validator (in real usage, would use TN-153 engine)
	// validator := templatevalidator.New(engine)
	// For demo, we'll show expected output

	// Simulate validation
	fmt.Println("Validation Result:")

	switch name {
	case "Valid Template":
		fmt.Println("✓ PASSED")
		fmt.Println("  0 errors, 0 warnings, 0 suggestions")

	case "Syntax Error":
		fmt.Println("✗ FAILED")
		fmt.Println("  Errors:")
		fmt.Println("    ✗ line 1, column 13: function \"invalidFunction\" not defined")
		fmt.Println("      💡 Did you mean 'toUpper'?")

	case "Unknown Field":
		fmt.Println("✗ FAILED")
		fmt.Println("  Errors:")
		fmt.Println("    ✗ line 1: Field 'UnknownField' does not exist in Alertmanager data model")
		fmt.Println("      💡 Available fields: Status, Labels, Annotations, StartsAt, EndsAt, GeneratorURL, Fingerprint")

	case "Hardcoded Secret":
		fmt.Println("✗ FAILED")
		fmt.Println("  Errors:")
		fmt.Println("    ✗ line 1, column 9: API Key detected: Hardcoded API key detected. Use environment variables or secret management.")
		fmt.Println("      💡 Use environment variables, K8s secrets, or secret management system (Vault, AWS Secrets Manager).")

	case "Long Line":
		fmt.Println("✓ PASSED")
		fmt.Println("  0 errors, 0 warnings")
		fmt.Println("  Suggestions:")
		fmt.Println("    💡 line 1: Line length exceeds 120 characters (actual: 147)")
		fmt.Println("      → Break line into multiple lines for better readability")
	}

	fmt.Println()
}

// ================================================================================
// Real-world usage with TN-153 engine:
//
// package main
//
// import (
//     "context"
//     "fmt"
//     "log"
//
//     "github.com/ipiton/AMP/pkg/templatevalidator"
//     template "github.com/ipiton/AMP/internal/notification/template"
// )
//
// func main() {
//     // Create TN-153 engine
//     engine := template.NewNotificationTemplateEngine(
//         template.DefaultTemplateEngineOptions(),
//     )
//
//     // Create validator
//     validator := templatevalidator.New(engine)
//
//     // Validate template
//     content := `{{ .Status | toUpper }}: {{ .Labels.alertname }}`
//     opts := templatevalidator.DefaultValidateOptions()
//
//     result, err := validator.Validate(context.Background(), content, opts)
//     if err != nil {
//         log.Fatal(err)
//     }
//
//     // Check result
//     if !result.Valid {
//         fmt.Println("❌ Validation failed:")
//         for _, err := range result.Errors {
//             fmt.Printf("  - %s: %s\n", err.Location(), err.Message)
//             if err.Suggestion != "" {
//                 fmt.Printf("    💡 %s\n", err.Suggestion)
//             }
//         }
//     } else {
//         fmt.Println("✅ Validation passed")
//     }
//
//     // Print warnings
//     if len(result.Warnings) > 0 {
//         fmt.Println("\n⚠️  Warnings:")
//         for _, warning := range result.Warnings {
//             fmt.Printf("  - %s: %s\n", warning.Location(), warning.Message)
//         }
//     }
//
//     // Print suggestions
//     if len(result.Suggestions) > 0 {
//         fmt.Println("\n💡 Suggestions:")
//         for _, suggestion := range result.Suggestions {
//             fmt.Printf("  - %s: %s\n", suggestion.Location(), suggestion.Message)
//         }
//     }
// }
// ================================================================================
