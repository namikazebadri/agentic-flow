package agent

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/agentflow/core/internal/llm"
)

// Base provides shared capabilities for all agents.
type Base struct {
	llm        llm.Provider
	promptsDir string
	maxRetries int
	verbose    bool
}

func NewBase(provider llm.Provider, promptsDir string, maxRetries int, verbose bool) *Base {
	return &Base{
		llm:        provider,
		promptsDir: promptsDir,
		maxRetries: maxRetries,
		verbose:    verbose,
	}
}

// LoadPrompt reads a system prompt from the prompts directory.
func (b *Base) LoadPrompt(name string) (string, error) {
	path := b.promptsDir + "/" + name
	if !strings.HasSuffix(path, ".md") {
		path += ".md"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("loading prompt %q: %w", name, err)
	}
	return string(data), nil
}

// Complete sends a request to the LLM with exponential backoff retry.
func (b *Base) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	var lastErr error
	for attempt := 0; attempt <= b.maxRetries; attempt++ {
		if attempt > 0 {
			delay := time.Duration(math.Pow(2, float64(attempt))) * time.Second
			b.log("  retry %d/%d after %v (error: %v)", attempt, b.maxRetries, delay, lastErr)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		resp, err := b.llm.Complete(ctx, req)
		if err == nil {
			if b.verbose {
				b.log("  tokens used: %d in / %d out", resp.InputTokens, resp.OutputTokens)
			}
			return resp, nil
		}

		// Don't retry on context cancellation
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		lastErr = err
	}
	return nil, fmt.Errorf("all %d attempts failed: %w", b.maxRetries+1, lastErr)
}

func (b *Base) log(format string, args ...any) {
	if b.verbose {
		fmt.Printf("[agent] "+format+"\n", args...)
	}
}

// ExtractBlock extracts content between ```lang and ``` fences.
// If lang is empty, matches any fence.
func ExtractBlock(content, lang string) string {
	fence := "```"
	if lang != "" {
		fence = "```" + lang
	}

	start := strings.Index(content, fence)
	if start == -1 {
		// Try without language tag if specific one not found
		start = strings.Index(content, "```")
		if start == -1 {
			return strings.TrimSpace(content)
		}
		// Skip past the opening fence line
		start = strings.Index(content[start:], "\n") + start + 1
	} else {
		start = strings.Index(content[start:], "\n") + start + 1
	}

	end := strings.Index(content[start:], "```")
	if end == -1 {
		return strings.TrimSpace(content[start:])
	}

	return strings.TrimSpace(content[start : start+end])
}

// ExtractJSON extracts JSON content from a response that may contain prose.
func ExtractJSON(content string) string {
	// Try ```json block first
	if block := ExtractBlock(content, "json"); block != "" && strings.HasPrefix(block, "{") {
		return block
	}
	// Fallback: find first { to last }
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start != -1 && end != -1 && end > start {
		return content[start : end+1]
	}
	return content
}
