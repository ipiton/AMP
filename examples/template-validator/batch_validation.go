package main

import (
	"context"
	"fmt"
)

// ================================================================================
// TN-156: Template Validator - Batch Validation Example
// ================================================================================
// Demonstrates batch validation of multiple templates in parallel.

func main() {
	fmt.Println("=== Template Validator - Batch Validation Example ===\n")

	// Simulate batch validation of 5 templates
	templates := []struct {
		Name    string
		Content string
		Valid   bool
	}{
		{
			Name:    "slack_critical.tmpl",
			Content: `{{ .Status | toUpper }}: {{ .Labels.alertname }}`,
			Valid:   true,
		},
		{
			Name:    "slack_warning.tmpl",
			Content: `{{ .Status }}: {{ .Labels.alertname }} - {{ .Annotations.summary }}`,
			Valid:   true,
		},
		{
			Name:    "email.tmpl",
			Content: `{{ .Annotations.description }}`,
			Valid:   true,
		},
		{
			Name:    "pagerduty.tmpl",
			Content: `{{ .Labels.severity | toUpper }}: {{ .Labels.alertname }}`,
			Valid:   true,
		},
		{
			Name:    "webhook_invalid.tmpl",
			Content: `{{ .Status | invalidFunc }}`,
			Valid:   false,
		},
	}

	fmt.Println("Validating 5 templates in parallel...\n")

	// Simulate results
	passedCount := 0
	failedCount := 0

	for _, tmpl := range templates {
		status := "✓"
		statusText := "PASSED"
		if !tmpl.Valid {
			status = "✗"
			statusText = "FAILED"
			failedCount++
		} else {
			passedCount++
		}

		fmt.Printf("%s %s: %s\n", status, tmpl.Name, statusText)
	}

	fmt.Printf("\n=== Summary ===\n")
	fmt.Printf("Templates: %d/%d passed\n", passedCount, len(templates))
	fmt.Printf("Errors: %d\n", failedCount)
	fmt.Printf("Warnings: 0\n")
	fmt.Printf("Duration: ~500ms (parallel)\n")
}

// ================================================================================
// Real-world batch validation:
//
// package main
//
// import (
//     "context"
//     "fmt"
//     "log"
//     "os"
//     "path/filepath"
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
//     // Find all template files
//     files, err := filepath.Glob("templates/*.tmpl")
//     if err != nil {
//         log.Fatal(err)
//     }
//
//     // Prepare batch inputs
//     inputs := make([]templatevalidator.TemplateInput, len(files))
//     for i, file := range files {
//         content, err := os.ReadFile(file)
//         if err != nil {
//             log.Fatal(err)
//         }
//
//         inputs[i] = templatevalidator.TemplateInput{
//             Name:    file,
//             Content: string(content),
//             Options: templatevalidator.DefaultValidateOptions().
//                 WithParallelWorkers(4), // 4 parallel workers
//         }
//     }
//
//     // Batch validate (parallel)
//     fmt.Printf("Validating %d templates in parallel...\n\n", len(inputs))
//
//     results, err := validator.ValidateBatch(context.Background(), inputs)
//     if err != nil {
//         log.Fatal(err)
//     }
//
//     // Print results
//     passedCount := 0
//     failedCount := 0
//
//     for i, result := range results {
//         status := "✓"
//         if !result.Valid {
//             status = "✗"
//             failedCount++
//         } else {
//             passedCount++
//         }
//
//         fmt.Printf("%s %s: %s (%d errors, %d warnings)\n",
//             status,
//             inputs[i].Name,
//             result.Summary(),
//             len(result.Errors),
//             len(result.Warnings),
//         )
//     }
//
//     // Summary
//     fmt.Printf("\n=== Summary ===\n")
//     fmt.Printf("Templates: %d/%d passed\n", passedCount, len(results))
//     fmt.Printf("Failed: %d\n", failedCount)
//
//     // Exit with appropriate code
//     if failedCount > 0 {
//         os.Exit(1)
//     }
// }
// ================================================================================
