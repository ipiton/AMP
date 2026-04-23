package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/ipiton/AMP/internal/core"
	inv "github.com/ipiton/AMP/internal/core/investigation"
)

// openAIFunction maps an investigation ToolDefinition to the OpenAI function object.
type openAIFunction struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Parameters  inv.JSONSchemaObject `json:"parameters"`
}

// openAITool wraps an openAIFunction for the tools array.
type openAITool struct {
	Type     string         `json:"type"` // "function"
	Function openAIFunction `json:"function"`
}

// openAIChatMessage is a single message in the OpenAI chat format.
type openAIChatMessage struct {
	Role       string              `json:"role"`
	Content    string              `json:"content,omitempty"`
	ToolCalls  []openAIToolCall    `json:"tool_calls,omitempty"`
	ToolCallID string              `json:"tool_call_id,omitempty"`
	Name       string              `json:"name,omitempty"`
}

type openAIToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"` // "function"
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// openAIChatResponse is the subset of the OpenAI chat completion response we use.
type openAIChatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message      openAIChatMessage `json:"message"`
		FinishReason string            `json:"finish_reason"`
	} `json:"choices"`
}

// InvestigateWithTools calls the LLM with a running conversation history and
// available tool definitions. It implements investigation.AgentLLMClient.
func (c *HTTPLLMClient) InvestigateWithTools(
	ctx context.Context,
	alert *core.Alert,
	classification *core.ClassificationResult,
	tools []inv.ToolDefinition,
	history []inv.AgentMessage,
) (*inv.AgentResponse, error) {
	msgs := buildOpenAIMessages(alert, classification, history)

	openAITools := make([]openAITool, len(tools))
	for i, td := range tools {
		openAITools[i] = openAITool{
			Type: "function",
			Function: openAIFunction{
				Name:        td.Name,
				Description: td.Description,
				Parameters:  td.Parameters,
			},
		}
	}

	payload := map[string]any{
		"model":    c.config.Model,
		"messages": msgs,
	}
	if len(openAITools) > 0 {
		payload["tools"] = openAITools
	}
	if c.config.MaxTokens > 0 {
		payload["max_tokens"] = c.config.MaxTokens
	}
	if c.config.Temperature >= 0 {
		payload["temperature"] = c.config.Temperature
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal tools request: %w", err)
	}

	url := buildOpenAIChatCompletionsURL(c.config.BaseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("create tools request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "alert-history-go/1.0.0")
	if c.config.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tools request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read tools response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &HTTPError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("LLM tools error: status %d, body: %s", resp.StatusCode, string(respBody)),
		}
	}

	var chatResp openAIChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("parse tools response: %w", err)
	}
	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("tools response has no choices")
	}

	return parseOpenAIAgentResponse(chatResp.Choices[0].Message, chatResp.Choices[0].FinishReason)
}

// buildOpenAIMessages converts the investigation context + history into OpenAI message format.
func buildOpenAIMessages(alert *core.Alert, classification *core.ClassificationResult, history []inv.AgentMessage) []openAIChatMessage {
	var msgs []openAIChatMessage

	// Prepend system message if not already in history.
	hasSystem := len(history) > 0 && history[0].Role == inv.RoleSystem
	if !hasSystem {
		classCtx := "No classification available."
		if classification != nil {
			classCtx = fmt.Sprintf("Severity: %s, Confidence: %.2f, Reasoning: %s",
				classification.Severity, classification.Confidence, classification.Reasoning)
		}

		llmAlert := CoreAlertToLLMRequest(alert)
		alertJSON := "{}"
		if llmAlert != nil {
			if b, err := json.Marshal(llmAlert); err == nil {
				alertJSON = string(b)
			}
		}

		systemContent := fmt.Sprintf(
			"You are an expert SRE investigating a production alert. "+
				"Use the available tools to gather data, then return a final JSON answer with keys: "+
				"summary, findings, recommendations, confidence.\n\n"+
				"Alert: %s\nClassification: %s",
			alertJSON, classCtx,
		)
		msgs = append(msgs, openAIChatMessage{Role: "system", Content: systemContent})
	}

	for _, m := range history {
		msg := openAIChatMessage{
			Role:       string(m.Role),
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
		}
		for _, tc := range m.ToolCalls {
			argsJSON := "{}"
			if b, err := json.Marshal(tc.Params); err == nil {
				argsJSON = string(b)
			}
			msg.ToolCalls = append(msg.ToolCalls, openAIToolCall{
				ID:   tc.ID,
				Type: "function",
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{Name: tc.Name, Arguments: argsJSON},
			})
		}
		msgs = append(msgs, msg)
	}

	// Add initial user prompt if history is empty (first iteration).
	if len(history) == 0 {
		msgs = append(msgs, openAIChatMessage{
			Role:    "user",
			Content: "Investigate this alert and use the available tools to gather data before drawing conclusions.",
		})
	}

	return msgs
}

// parseOpenAIAgentResponse maps an OpenAI choice to investigation.AgentResponse.
func parseOpenAIAgentResponse(msg openAIChatMessage, finishReason string) (*inv.AgentResponse, error) {
	if finishReason == "tool_calls" || len(msg.ToolCalls) > 0 {
		tcs := make([]inv.ToolCallRequest, 0, len(msg.ToolCalls))
		for _, tc := range msg.ToolCalls {
			var params map[string]any
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &params); err != nil {
				params = map[string]any{"_raw": tc.Function.Arguments}
			}
			tcs = append(tcs, inv.ToolCallRequest{
				ID:     tc.ID,
				Name:   tc.Function.Name,
				Params: params,
			})
		}
		return &inv.AgentResponse{Kind: inv.AgentResponseToolCalls, ToolCalls: tcs}, nil
	}

	content := strings.TrimSpace(msg.Content)
	return &inv.AgentResponse{Kind: inv.AgentResponseFinalAnswer, Content: content}, nil
}
