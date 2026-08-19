package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// Message is a single chat message in OpenAI's format.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type ToolCall struct {
	Index    int          `json:"index"`
	ID       string       `json:"id,omitempty"`
	Type     string       `json:"type,omitempty"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// ToolSpec describes a callable tool in OpenAI's function-calling schema.
type ToolSpec struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type chatRequest struct {
	Model    string     `json:"model"`
	Messages []Message  `json:"messages"`
	Tools    []ToolSpec `json:"tools,omitempty"`
	Stream   bool       `json:"stream"`
}

type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content   string     `json:"content"`
			ToolCalls []ToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	// Some gateways (e.g. openmind's zen proxy) report upstream failures
	// as an in-band SSE data frame instead of an HTTP error status.
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// StreamEvent is what the agent loop emits as the model streams a reply.
// One of Text or ToolCallsDone will be set per event; FinishReason is set
// once the stream for this turn ends.
type StreamEvent struct {
	Text         string
	Done         bool
	ToolCalls    []ToolCall // final, fully-assembled tool calls (only on Done)
	FinishReason string
	Err          error
}

// streamChat calls the OpenAI-compatible /chat/completions endpoint with
// stream=true and emits StreamEvents on the returned channel as content
// deltas and tool-call fragments arrive. It assembles fragmented
// tool_calls (OpenAI streams them incrementally, keyed by index) into
// complete ToolCall values before signalling Done.
func streamChat(ctx context.Context, cfg *Config, messages []Message, tools []ToolSpec) <-chan StreamEvent {
	out := make(chan StreamEvent, 8)

	go func() {
		defer close(out)

		// The upstream gateway occasionally stalls with no bytes at all
		// (seen in practice: connection held open ~165s then EOF). Cap the
		// wait for the *first* byte tightly so a stall fails fast and can
		// be retried; once tokens start flowing, allow a much longer cap
		// for legitimately long generations.
		reqCtx, cancel := context.WithTimeout(ctx, 150*time.Second)
		defer cancel()
		stallTimer := time.AfterFunc(30*time.Second, cancel)
		defer stallTimer.Stop()

		reqBody := chatRequest{
			Model:    cleanModelName(cfg.Model),
			Messages: messages,
			Tools:    tools,
			Stream:   true,
		}
		buf, err := json.Marshal(reqBody)
		if err != nil {
			out <- StreamEvent{Err: fmt.Errorf("encode request: %w", err)}
			return
		}

		url := strings.TrimRight(cfg.BaseURL, "/") + "/chat/completions"
		req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(buf))
		if err != nil {
			out <- StreamEvent{Err: fmt.Errorf("build request: %w", err)}
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		if cfg.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			out <- StreamEvent{Err: fmt.Errorf("request failed: %w", err)}
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body := make([]byte, 4096)
			n, _ := resp.Body.Read(body)
			out <- StreamEvent{Err: fmt.Errorf("upstream returned %s: %s", resp.Status, string(body[:n]))}
			return
		}

		toolAcc := map[int]*ToolCall{}
		var order []int
		finishReason := ""

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		debug := os.Getenv("OPENCODE_TINY_DEBUG") != ""
		for scanner.Scan() {
			line := scanner.Text()
			if debug {
				fmt.Fprintf(os.Stderr, "[sse] %s\n", line)
			}
			if !strings.HasPrefix(line, "data: ") {
				continue // comment/keepalive lines (e.g. ": openmind accepted ...") don't count as real progress
			}
			stallTimer.Stop() // a genuine data frame arrived; the fast-fail stall deadline no longer applies
			payload := strings.TrimPrefix(line, "data: ")
			if payload == "[DONE]" {
				break
			}

			var chunk streamChunk
			if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
				continue // skip malformed/keepalive lines rather than aborting the whole turn
			}
			if chunk.Error != nil {
				out <- StreamEvent{Err: fmt.Errorf("upstream error [%s/%s]: %s", chunk.Error.Type, chunk.Error.Code, chunk.Error.Message)}
				return
			}
			if len(chunk.Choices) == 0 {
				continue
			}
			choice := chunk.Choices[0]

			if choice.Delta.Content != "" {
				out <- StreamEvent{Text: choice.Delta.Content}
			}

			for _, tc := range choice.Delta.ToolCalls {
				existing, ok := toolAcc[tc.Index]
				if !ok {
					existing = &ToolCall{Index: tc.Index, Type: "function"}
					toolAcc[tc.Index] = existing
					order = append(order, tc.Index)
				}
				if tc.ID != "" {
					existing.ID = tc.ID
				}
				if tc.Function.Name != "" {
					existing.Function.Name += tc.Function.Name
				}
				if tc.Function.Arguments != "" {
					existing.Function.Arguments += tc.Function.Arguments
				}
			}

			if choice.FinishReason != "" {
				finishReason = choice.FinishReason
			}
		}
		if err := scanner.Err(); err != nil {
			out <- StreamEvent{Err: fmt.Errorf("stream read error: %w", err)}
			return
		}

		var finalCalls []ToolCall
		for _, idx := range order {
			finalCalls = append(finalCalls, *toolAcc[idx])
		}

		out <- StreamEvent{Done: true, ToolCalls: finalCalls, FinishReason: finishReason}
	}()

	return out
}

// isTransientUpstreamError matches the gateway hiccups we've actually seen
// in practice (openmind's zen proxy occasionally reports a 502/503 from
// the real provider as an in-band SSE error frame) rather than a real
// request problem worth surfacing immediately.
func isTransientUpstreamError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, marker := range []string{
		"502", "503", "upstream_error", // in-band gateway error frames
		"deadline exceeded", "context canceled", // our own stall timeout firing (manual cancel -> Canceled, not DeadlineExceeded)
		"EOF", // connection dropped with no usable data
		"connection reset",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// streamChatWithRetry wraps streamChat with a couple of silent retries when
// the very first event of an attempt is a transient upstream error — i.e.
// nothing has been streamed to the user yet, so a retry is safe. Once any
// real content (text or tool calls) has started flowing, errors are passed
// through as-is rather than retried, since partial output can't be undone.
func streamChatWithRetry(ctx context.Context, cfg *Config, messages []Message, tools []ToolSpec) <-chan StreamEvent {
	out := make(chan StreamEvent, 8)

	go func() {
		defer close(out)

		const maxAttempts = 3
		backoff := time.Second

		for attempt := 1; attempt <= maxAttempts; attempt++ {
			ch := streamChat(ctx, cfg, messages, tools)
			first, ok := <-ch
			if !ok {
				return // upstream closed with nothing at all; give up quietly
			}

			if first.Err != nil && isTransientUpstreamError(first.Err) && attempt < maxAttempts {
				select {
				case <-ctx.Done():
					out <- StreamEvent{Err: ctx.Err()}
					return
				case <-time.After(backoff):
				}
				backoff *= 2
				continue // retry: nothing has been emitted to the caller yet
			}

			// Either a real error, a non-transient error, content, or the
			// last allowed attempt: forward this event and the rest of the
			// stream as-is.
			out <- first
			for ev := range ch {
				out <- ev
			}
			return
		}
	}()

	return out
}

func cleanModelName(model string) string {
	if idx := strings.LastIndex(model, "/"); idx != -1 {
		return model[idx+1:]
	}
	return model
}
