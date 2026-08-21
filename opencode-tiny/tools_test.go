package main

import (
	"context"
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
}
