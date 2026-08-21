package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

const systemPrompt = `You are opencode-tiny, a minimal coding assistant running on a resource-constrained machine.
You have tools for shell execution (bash), reading files (read_file), writing files (write_file), editing files (edit_file), controlling GPIO pins (gpio_control), superuser access (superuser_access), and host execution (host_shell).
Use them to inspect and modify files, control hardware, and run commands. Be concise. When a task is done, say so plainly.`

// AgentEvent represents a single real-time event unit emitted during an agent turn, streamed to clients as an SSE data frame.
type AgentEvent struct {
	Type    string `json:"type"`              // Event classification: "text", "tool_call", "tool_result", "done", or "error"
	Text    string `json:"text,omitempty"`    // Streamed text token content
	Tool    string `json:"tool,omitempty"`    // Executed tool name
	Args    string `json:"args,omitempty"`    // Arguments JSON passed to the tool
	Output  string `json:"output,omitempty"`  // Execution result/stdout returned by the tool
	Message string `json:"message,omitempty"` // Human-readable error or status message
}

// runAgentTurn drives the LLM completion and tool-execution loop for a user message,
// persisting messages to SQLite and emitting AgentEvent SSE frames to the client.
func runAgentTurn(ctx context.Context, cfg *Config, store *Store, sessionID, userText string, images []string, emit func(AgentEvent)) error {
	history, err := store.loadMessages(sessionID)
	if err != nil {
		return fmt.Errorf("load history: %w", err)
	}

	messages := make([]Message, 0, len(history)+2)
	if len(history) == 0 {
		messages = append(messages, Message{Role: "system", Content: systemPrompt})
	}
	messages = append(messages, history...)

	var userContent any = userText
	if len(images) > 0 {
		var parts []any
		if userText != "" {
			parts = append(parts, map[string]any{"type": "text", "text": userText})
		}
		for _, img := range images {
			parts = append(parts, map[string]any{
				"type": "image_url",
				"image_url": map[string]string{
					"url": img,
				},
			})
		}
		userContent = parts
	}

	userMsg := Message{Role: "user", Content: userContent}
	messages = append(messages, userMsg)
	if err := store.addMessage(sessionID, userMsg); err != nil {
		return fmt.Errorf("save user message: %w", err)
	}

	for turn := 0; turn < cfg.MaxTurns; turn++ {
		var textBuf string
		var finalToolCalls []ToolCall
		var streamErr error

		for ev := range streamChatWithRetry(ctx, cfg, messages, toolSpecs) {
			switch {
			case ev.Err != nil:
				streamErr = ev.Err
			case ev.Text != "":
				textBuf += ev.Text
				emit(AgentEvent{Type: "text", Text: ev.Text})
			case ev.Done:
				finalToolCalls = ev.ToolCalls
			}
		}
		if streamErr != nil {
			emit(AgentEvent{Type: "error", Message: streamErr.Error()})
			return streamErr
		}

		assistantMsg := Message{Role: "assistant", Content: textBuf, ToolCalls: finalToolCalls}
		if err := store.addMessage(sessionID, assistantMsg); err != nil {
			return fmt.Errorf("save assistant message: %w", err)
		}
		messages = append(messages, assistantMsg)

		if len(finalToolCalls) == 0 {
			emit(AgentEvent{Type: "done"})
			return nil
		}

		for _, tc := range finalToolCalls {
			argsPreview := tc.Function.Arguments
			emit(AgentEvent{Type: "tool_call", Tool: tc.Function.Name, Args: argsPreview})

			output := runTool(ctx, cfg, tc.Function.Name, tc.Function.Arguments)
			emit(AgentEvent{Type: "tool_result", Tool: tc.Function.Name, Output: output})

			toolMsg := Message{Role: "tool", Content: output, ToolCallID: tc.ID}
			if err := store.addMessage(sessionID, toolMsg); err != nil {
				return fmt.Errorf("save tool message: %w", err)
			}
			messages = append(messages, toolMsg)
		}
		// loop again: send the tool results back to the model
	}

	msg := fmt.Sprintf("stopped after %d tool-call rounds without a final answer", cfg.MaxTurns)
	emit(AgentEvent{Type: "error", Message: msg})
	return errors.New(msg)
}

// marshalEvent serializes an AgentEvent struct into a JSON string formatted for SSE transmission.
func marshalEvent(ev AgentEvent) string {
	b, err := json.Marshal(ev)
	if err != nil {
		b, _ = json.Marshal(AgentEvent{Type: "error", Message: "failed to encode event"})
	}
	return string(b)
}
