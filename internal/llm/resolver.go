package llm

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/agentflow/core/internal/config"
	"github.com/agentflow/core/internal/registry"
)

// DeliverableProvider wraps a Provider with the resolved model info
// for one specific deliverable.
type DeliverableProvider struct {
	Provider  Provider
	ModelUsed config.ModelEntry
}

// ResolveForDeliverable picks the right LLM provider+model for a deliverable.
//
// Resolution order:
//  1. OverrideModel from deliverable registry (set by reading spec doc)
//  2. SuggestedModel from deliverable registry (set by decomposer)
//  3. Active provider's default model
func ResolveForDeliverable(
	cfg *config.Config,
	d *registry.Deliverable,
) (*DeliverableProvider, error) {

	// Prefer override (engineer-set), then suggestion (agent-set)
	alias := d.OverrideModel
	if alias == "" {
		alias = d.SuggestedModel
	}

	model := cfg.ResolveModel(alias)
	provider, err := buildProviderForModel(cfg, model)
	if err != nil {
		return nil, fmt.Errorf("building provider for model %q: %w", model.Alias, err)
	}

	return &DeliverableProvider{
		Provider:  provider,
		ModelUsed: model,
	}, nil
}

// ReadOverrideFromSpecDoc parses the spec doc (.md) for the deliverable
// and extracts the engineer-set model override if present.
//
// It looks for a line like:
//
//	**Model**: `claude-opus-4`   ← suggested by agentflow
//
// or after engineer edits:
//
//	**Model**: `claude-sonnet-4` ← overridden by engineer
//
// Returns empty string if not found or if the value matches "suggested".
func ReadOverrideFromSpecDoc(specDocPath string, suggestedAlias string) (string, error) {
	if specDocPath == "" {
		return "", nil
	}
	data, err := os.ReadFile(specDocPath)
	if err != nil {
		// spec doc missing is not fatal — just no override
		return "", nil
	}

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "**Model**:") {
			continue
		}
		// Extract value between backticks: **Model**: `claude-opus-4`
		start := strings.Index(line, "`")
		end := strings.LastIndex(line, "`")
		if start == -1 || end == -1 || start == end {
			continue
		}
		alias := strings.TrimSpace(line[start+1 : end])
		// If it's the same as suggested, not an override
		if alias == suggestedAlias || alias == "" {
			return "", nil
		}
		return alias, nil
	}
	return "", nil
}

// buildProviderForModel constructs the right LLM Provider for a ModelEntry.
func buildProviderForModel(cfg *config.Config, model config.ModelEntry) (Provider, error) {
	providerName := model.Provider
	if providerName == "" {
		providerName = cfg.ActiveProvider
	}

	pc, ok := cfg.Providers[providerName]
	if !ok {
		return nil, fmt.Errorf("provider %q not found in config", providerName)
	}

	apiKey := pc.APIKey()
	if apiKey == "" {
		return nil, fmt.Errorf("env var %q for provider %q is not set", pc.APIKeyEnv, providerName)
	}

	// Use model-specific model ID if set, otherwise fall back to provider default
	modelID := model.ModelID
	if modelID == "" {
		modelID = pc.Model
	}

	maxTokens := model.MaxTokens
	if maxTokens == 0 {
		maxTokens = pc.MaxTokens
	}
	if maxTokens == 0 {
		maxTokens = 4096
	}

	switch providerName {
	case "anthropic":
		p := NewAnthropicProvider(apiKey, modelID, pc.BaseURL)
		return &cappedProvider{Provider: p, maxTokens: maxTokens}, nil
	case "openai":
		p := NewOpenAIProvider(apiKey, modelID, pc.BaseURL)
		return &cappedProvider{Provider: p, maxTokens: maxTokens}, nil
	case "ollama":
		p := NewOllamaProvider(modelID, pc.BaseURL)
		return &cappedProvider{Provider: p, maxTokens: maxTokens}, nil
	default:
		return nil, fmt.Errorf("unsupported provider: %s", providerName)
	}
}

// cappedProvider wraps a Provider and enforces a per-model MaxTokens cap.
type cappedProvider struct {
	Provider
	maxTokens int
}

func (c *cappedProvider) Complete(ctx context.Context, req Request) (*Response, error) {
	if req.MaxTokens == 0 || req.MaxTokens > c.maxTokens {
		req.MaxTokens = c.maxTokens
	}
	return c.Provider.Complete(ctx, req)
}
