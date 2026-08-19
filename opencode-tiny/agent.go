package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

const systemPrompt = `You are opencode-tiny, a minimal coding assistant running on a resource-constrained machine.
You have four tools: bash, read_file, write_file, edit_file. Use them to inspect and modify files
and run commands on this machine. Be concise. When a task is done, say so plainly without re-explaining
what you already did.`

// AgentEvent is one unit of progress emitted during a turn, sent to the
// browser as an SSE frame.
type AgentEvent struct {
	Type    string `json:"type"` // text | tool_call | tool_result | done | error
	Text    string `json:"text,omitempty"`
	Tool    string `json:"tool,omitempty"`
	Args    string `json:"args,omitempty"`
	Output  string `json:"output,omitempty"`
	Message string `json:"message,omitempty"`
}

// runAgentTurn drives the model+tool loop for one user message, persisting
// every message to the store and emitting AgentEvents via emit as it goes.
func runAgentTurn(ctx context.Context, cfg *Config, store *Store, sessionID, userText string, emit func(AgentEvent)) error {
	history, err := store.loadMessages(sessionID)
	if err != nil {
		return fmt.Errorf("load history: %w", err)
	}

	messages := make([]Message, 0, len(history)+2)
	if len(history) == 0 {
		messages = append(messages, Message{Role: "system", Content: systemPrompt})
	}
	messages = append(messages, history...)

	userMsg := Message{Role: "user", Content: userText}
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

// marshalEvent is a small helper used by the HTTP layer to write an
// AgentEvent as an SSE "data: ..." line.
func marshalEvent(ev AgentEvent) string {
	b, err := json.Marshal(ev)
	if err != nil {
		b, _ = json.Marshal(AgentEvent{Type: "error", Message: "failed to encode event"})
	}
	return string(b)
}
