package investigation

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

// AgentLLMClient is the minimal LLM interface the loop needs.
type AgentLLMClient interface {
	InvestigateWithTools(
		ctx context.Context,
		alert *core.Alert,
		classification *core.ClassificationResult,
		tools []ToolDefinition,
		history []AgentMessage,
	) (*AgentResponse, error)
}

// AgentLoopConfig configures loop behaviour.
type AgentLoopConfig struct {
	MaxIterations  int
	TotalTimeout   time.Duration
	PerToolTimeout time.Duration
	MaxHistoryMsgs int
}

// DefaultAgentLoopConfig returns production-safe defaults.
func DefaultAgentLoopConfig() AgentLoopConfig {
	return AgentLoopConfig{
		MaxIterations:  10,
		TotalTimeout:   5 * time.Minute,
		PerToolTimeout: 30 * time.Second,
		MaxHistoryMsgs: 40,
	}
}

// AgentRunResult holds the full output of one Run() call.
type AgentRunResult struct {
	Result          *core.InvestigationResult
	Steps           []InvestigationStep
	IterationsUsed  int
	ToolCallsCount  int
	TerminationKind string // "final_answer" | "max_iterations" | "timeout" | "error"
}

// AgentLoop executes the Think→Act→Observe loop.
type AgentLoop struct {
	llm      AgentLLMClient
	registry *ToolRegistry
	config   AgentLoopConfig
}

// NewAgentLoop creates a loop wired to the given LLM client and tool registry.
// Panics if cfg.MaxHistoryMsgs < 1 to prevent slice index panics in trimHistory.
func NewAgentLoop(llm AgentLLMClient, registry *ToolRegistry, cfg AgentLoopConfig) *AgentLoop {
	if cfg.MaxHistoryMsgs < 1 {
		panic("investigation: AgentLoopConfig.MaxHistoryMsgs must be >= 1")
	}
	return &AgentLoop{llm: llm, registry: registry, config: cfg}
}

// Run performs the agentic investigation and returns a structured result.
func (a *AgentLoop) Run(
	ctx context.Context,
	alert *core.Alert,
	classification *core.ClassificationResult,
) (*AgentRunResult, error) {
	deadline := time.Now().Add(a.config.TotalTimeout)
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	var (
		history    []AgentMessage
		steps      []InvestigationStep
		stepNum    int
		toolCalls  int
		iterations int
	)

	defs := a.registry.Definitions()

	for iterations < a.config.MaxIterations {
		history = trimHistory(history, a.config.MaxHistoryMsgs)

		resp, err := a.llm.InvestigateWithTools(ctx, alert, classification, defs, history)
		if err != nil {
			if ctx.Err() != nil {
				return &AgentRunResult{
					Steps:           steps,
					IterationsUsed:  iterations,
					ToolCallsCount:  toolCalls,
					TerminationKind: "timeout",
				}, fmt.Errorf("agent loop context done: %w", ctx.Err())
			}
			return &AgentRunResult{
				Steps:           steps,
				IterationsUsed:  iterations,
				ToolCallsCount:  toolCalls,
				TerminationKind: "error",
			}, fmt.Errorf("llm call failed: %w", err)
		}

		iterations++

		switch resp.Kind {
		case AgentResponseFinalAnswer:
			stepNum++
			steps = append(steps, InvestigationStep{
				StepNumber: stepNum,
				Type:       StepTypeConclusion,
				Output:     resp.Content,
				Timestamp:  time.Now(),
			})

			result := parseFinalAnswer(resp.Content)
			return &AgentRunResult{
				Result:          result,
				Steps:           steps,
				IterationsUsed:  iterations,
				ToolCallsCount:  toolCalls,
				TerminationKind: "final_answer",
			}, nil

		case AgentResponseToolCalls:
			// Record the thought step.
			stepNum++
			steps = append(steps, InvestigationStep{
				StepNumber: stepNum,
				Type:       StepTypeThought,
				Input:      resp.ToolCalls,
				Timestamp:  time.Now(),
			})

			// Append assistant message with tool_calls to history.
			history = append(history, AgentMessage{
				Role:      RoleAssistant,
				ToolCalls: resp.ToolCalls,
			})

			// Execute each tool and feed observations back.
			for _, tc := range resp.ToolCalls {
				toolCalls++

				stepNum++
				steps = append(steps, InvestigationStep{
					StepNumber: stepNum,
					Type:       StepTypeToolCall,
					ToolName:   tc.Name,
					ToolCallID: tc.ID,
					Input:      tc.Params,
					Timestamp:  time.Now(),
				})

				toolCtx, toolCancel := context.WithTimeout(ctx, a.config.PerToolTimeout)
				res := a.registry.Execute(toolCtx, tc.Name, tc.ID, tc.Params)
				toolCancel()

				content := res.Content
				if res.IsError {
					content = fmt.Sprintf("error: %s", res.Error)
				}

				stepNum++
				steps = append(steps, InvestigationStep{
					StepNumber: stepNum,
					Type:       StepTypeObservation,
					ToolName:   tc.Name,
					ToolCallID: tc.ID,
					Output:     content,
					IsError:    res.IsError,
					Timestamp:  time.Now(),
				})

				history = append(history, AgentMessage{
					Role:       RoleTool,
					Content:    content,
					ToolCallID: tc.ID,
				})
			}

		default:
			return nil, fmt.Errorf("unknown agent response kind: %q", resp.Kind)
		}
	}

	// Reached max iterations without a final answer.
	return &AgentRunResult{
		Steps:           steps,
		IterationsUsed:  iterations,
		ToolCallsCount:  toolCalls,
		TerminationKind: "max_iterations",
	}, nil
}

// trimHistory keeps the history within maxMsgs.
// A leading system message is always preserved.
func trimHistory(history []AgentMessage, maxMsgs int) []AgentMessage {
	if len(history) <= maxMsgs {
		return history
	}
	if len(history) > 0 && history[0].Role == RoleSystem {
		tail := history[len(history)-(maxMsgs-1):]
		out := make([]AgentMessage, 1, maxMsgs)
		out[0] = history[0]
		return append(out, tail...)
	}
	return history[len(history)-maxMsgs:]
}

// parseFinalAnswer attempts to unmarshal the LLM content into InvestigationResult.
// On parse failure it returns a partial result with the raw content as summary.
func parseFinalAnswer(content string) *core.InvestigationResult {
	var r core.InvestigationResult
	if err := json.Unmarshal([]byte(content), &r); err != nil {
		return &core.InvestigationResult{Summary: content}
	}
	return &r
}
