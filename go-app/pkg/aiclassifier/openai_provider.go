package aiclassifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

// OpenAIProvider implements LLMProvider for OpenAI.
type OpenAIProvider struct {
	apiKey  string
	baseURL string
	model   string
	timeout time.Duration
	client  *http.Client
}

// NewOpenAIProvider creates a new OpenAI provider.
func NewOpenAIProvider(apiKey, baseURL, model string, timeout time.Duration) *OpenAIProvider {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	if model == "" {
		model = "gpt-4"
	}

	return &OpenAIProvider{
		apiKey:  apiKey,
		baseURL: baseURL,
		model:   model,
		timeout: timeout,
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

// Classify sends alert to OpenAI for classification.
func (p *OpenAIProvider) Classify(ctx context.Context, alert *core.Alert) (*ClassificationResult, error) {
	// Build prompt
	prompt := buildClassificationPrompt(alert)

	// Prepare request
	reqBody := map[string]interface{}{
		"model": p.model,
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": classificationSystemPrompt,
			},
			{
				"role":    "user",
				"content": prompt,
			},
		},
		"temperature": 0.0, // Deterministic
		"max_tokens":  500,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/chat/completions", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	// Send request
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error: status=%d, body=%s", resp.StatusCode, string(body))
	}

	// Parse response
	var openAIResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&openAIResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if len(openAIResp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	// Parse classification from response
	result, err := parseClassificationResponse(openAIResp.Choices[0].Message.Content)
	if err != nil {
		return nil, fmt.Errorf("failed to parse classification: %w", err)
	}

	result.ModelVersion = p.model
	result.ProcessedAt = time.Now()

	return result, nil
}

// Health checks if OpenAI is available.
func (p *OpenAIProvider) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", p.baseURL+"/models", nil)
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check failed: status=%d", resp.StatusCode)
	}

	return nil
}

const classificationSystemPrompt = `You are an expert alert classification system. Analyze the provided alert and classify it with high precision.

Output ONLY valid JSON with this exact structure:
{
  "incident_type": "database|network|application|security|infrastructure|performance",
  "severity": "critical|high|medium|low",
  "urgency": "immediate|high|normal|low",
  "category": "performance|availability|security|compliance",
  "type_confidence": 0.95,
  "severity_confidence": 0.90,
  "urgency_confidence": 0.85,
  "suggested_team": "dba|network|security|ops|sre",
  "suggested_priority": "high|medium|low",
  "suggested_targets": ["slack-channel", "pagerduty"],
  "is_anomaly": false,
  "is_false_positive": false,
  "recommended_action": "Brief action to take",
  "reasoning": "Brief explanation of classification"
}

Focus on accuracy and actionability.`

func buildClassificationPrompt(alert *core.Alert) string {
	return fmt.Sprintf(`Alert Details:
Name: %s
Status: %s
Severity: %s
Labels: %v
Annotations: %v

Classify this alert.`,
		alert.AlertName,
		alert.Status,
		alert.Labels["severity"],
		alert.Labels,
		alert.Annotations,
	)
}

func parseClassificationResponse(content string) (*ClassificationResult, error) {
	var result ClassificationResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON: %w", err)
	}
	return &result, nil
}
