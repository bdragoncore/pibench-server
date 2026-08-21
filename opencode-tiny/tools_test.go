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
