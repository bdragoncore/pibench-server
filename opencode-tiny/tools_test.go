package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestToolWebSearch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	cfg := &Config{Workdir: "."}
	args := map[string]any{
		"query":       "Raspberry Pi Zero 2 W",
		"num_results": 3,
	}

	result := toolWebSearch(ctx, cfg, args)
	t.Logf("Search result:\n%s", result)

	if strings.HasPrefix(result, "error:") {
		t.Fatalf("toolWebSearch returned error: %s", result)
	}

	if !strings.Contains(strings.ToLower(result), "raspberry") && !strings.Contains(strings.ToLower(result), "pi") {
		t.Fatalf("Expected search results to mention Raspberry Pi, got:\n%s", result)
	}
}

func TestToolWebFetch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	cfg := &Config{Workdir: "."}
	args := map[string]any{
		"url": "https://go.dev",
	}

	result := toolWebFetch(ctx, cfg, args)
	t.Logf("Fetch result:\n%s", result)

	if strings.HasPrefix(result, "error:") {
		t.Fatalf("toolWebFetch returned error: %s", result)
	}

	if !strings.Contains(strings.ToLower(result), "go") {
		t.Fatalf("Expected fetched content to mention Go, got:\n%s", result)
	}
}

func TestSanitizeMessages(t *testing.T) {
	// Scenario 1: Dangling tool call followed directly by user message
	raw := []Message{
		{Role: "system", Content: "You are opencode-tiny."},
		{Role: "user", Content: "search for something"},
		{Role: "assistant", Content: "Looking...", ToolCalls: []ToolCall{{ID: "call_1", Function: FunctionCall{Name: "websearch", Arguments: `{"query":"test"}`}}}},
		{Role: "user", Content: "continue"},
	}

	clean := sanitizeMessages(raw)
	if len(clean) != 4 {
		t.Fatalf("Expected 4 messages, got %d", len(clean))
	}
	if len(clean[2].ToolCalls) != 0 {
		t.Fatalf("Expected dangling tool calls to be stripped, got %d", len(clean[2].ToolCalls))
	}
	if clean[2].Content != "Looking..." {
		t.Fatalf("Expected content preserved, got %v", clean[2].Content)
	}

	// Scenario 2: Valid paired tool call
	validRaw := []Message{
		{Role: "user", Content: "search"},
		{Role: "assistant", Content: "Searching", ToolCalls: []ToolCall{{ID: "call_1", Function: FunctionCall{Name: "websearch"}}}},
		{Role: "tool", ToolCallID: "call_1", Content: "Result"},
		{Role: "assistant", Content: "Done"},
	}
	validClean := sanitizeMessages(validRaw)
	if len(validClean) != 4 {
		t.Fatalf("Expected 4 messages, got %d", len(validClean))
	}
	if len(validClean[1].ToolCalls) != 1 {
		t.Fatalf("Expected tool call to remain intact")
	}

	// Scenario 3: Empty assistant message
	emptyRaw := []Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: ""},
		{Role: "user", Content: "test"},
	}
	emptyClean := sanitizeMessages(emptyRaw)
	if len(emptyClean) != 2 {
		t.Fatalf("Expected empty assistant message to be dropped, got %d messages", len(emptyClean))
	}

	// Scenario 4: Multiple parallel tool calls (Anthropic/OpenAI conformance)
	parallelRaw := []Message{
		{Role: "user", Content: "fetch pages"},
		{
			Role:    "assistant",
			Content: "Fetching...",
			ToolCalls: []ToolCall{
				{ID: "call_a", Function: FunctionCall{Name: "webfetch", Arguments: `{"url":"https://a.com"}`}},
				{ID: "call_b", Function: FunctionCall{Name: "webfetch", Arguments: `{"url":"https://b.com"}`}},
				{ID: "call_c", Function: FunctionCall{Name: "webfetch", Arguments: `{"url":"https://c.com"}`}},
			},
		},
		{Role: "tool", ToolCallID: "call_a", Content: "Page A content"},
		{Role: "tool", ToolCallID: "call_b", Content: "Page B content"},
		{Role: "tool", ToolCallID: "call_c", Content: "Page C content"},
		{Role: "assistant", Content: "All pages fetched."},
	}
	parallelClean := sanitizeMessages(parallelRaw)
	if len(parallelClean) != 6 {
		t.Fatalf("Expected 6 messages for parallel tool calls, got %d", len(parallelClean))
	}
	if len(parallelClean[1].ToolCalls) != 3 {
		t.Fatalf("Expected 3 tool calls in assistant message, got %d", len(parallelClean[1].ToolCalls))
	}
	for i := 2; i <= 4; i++ {
		if parallelClean[i].Role != "tool" {
			t.Fatalf("Expected parallelClean[%d] to be role 'tool', got %q", i, parallelClean[i].Role)
		}
	}
}

func TestPruneMessagesForContext(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "System prompt"},
		{Role: "user", Content: "Initial goal"},
	}

	for i := 0; i < 30; i++ {
		msgs = append(msgs,
			Message{Role: "assistant", Content: fmt.Sprintf("Turn %d", i), ToolCalls: []ToolCall{{ID: fmt.Sprintf("call_%d", i), Function: FunctionCall{Name: "bash"}}}},
			Message{Role: "tool", ToolCallID: fmt.Sprintf("call_%d", i), Content: strings.Repeat("A", 4000)},
		)
	}

	pruned := pruneMessagesForContext(msgs)
	if len(pruned) >= len(msgs) {
		t.Fatalf("Expected pruned length < original (%d), got %d", len(msgs), len(pruned))
	}
	if pruned[0].Role != "system" {
		t.Fatalf("Expected system prompt preserved at index 0")
	}

	// Verify large tool content in older turns was truncated
	foundTruncated := false
	for _, m := range pruned {
		if m.Role == "tool" && strings.Contains(m.Content.(string), "[truncated older output]") {
			foundTruncated = true
			break
		}
	}
	if !foundTruncated {
		t.Fatalf("Expected older tool content to be compacted with truncation marker")
	}
}

func TestCleanModelName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"openmind/zen-big-pickle", "zen-big-pickle"},
		{"opencode/zen-hy3-free", "zen-hy3-free"},
		{"kilo-stepfun/step-3.7-flash:free", "kilo-stepfun/step-3.7-flash:free"},
		{"kilo-kilo-auto/free", "kilo-kilo-auto/free"},
		{"hy3-free", "zen-hy3-free"},
		{"ox-alpha-free", "zen-x-preview-f-free"},
	}

	for _, tt := range tests {
		got := cleanModelName(tt.input)
		if got != tt.want {
			t.Errorf("cleanModelName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
