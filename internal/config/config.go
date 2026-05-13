package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config is the root configuration for the agentic pipeline.
type Config struct {
	ActiveProvider string                    `json:"active_provider"`
	Providers      map[string]ProviderConfig `json:"providers"`
	Models         ModelCatalog              `json:"models"`
	Pipeline       PipelineConfig            `json:"pipeline"`
	Registry       RegistryConfig            `json:"registry"`
	Gate           GateConfig                `json:"gate"`
}

// ── Provider ──────────────────────────────────────────────────────────────────

// ProviderConfig holds connection details for one LLM backend.
type ProviderConfig struct {
	Name      string            `json:"name"`
	BaseURL   string            `json:"base_url"`
	APIKeyEnv string            `json:"api_key_env"` // env var name, not the value
	Model     string            `json:"model"`       // default model for this provider
	MaxTokens int               `json:"max_tokens"`
	Headers   map[string]string `json:"headers,omitempty"`
}

// APIKey resolves the actual API key from the environment.
func (p *ProviderConfig) APIKey() string { return os.Getenv(p.APIKeyEnv) }

// ── Model Catalog ─────────────────────────────────────────────────────────────

// ModelCatalog is the full list of known models across all providers.
// New models can be added to config.json without recompiling.
type ModelCatalog struct {
	// Models maps a short alias (e.g. "claude-opus-4") to its definition.
	// The alias is what engineers write in spec docs for overrides.
	Models map[string]ModelEntry `json:"models"`

	// SuggestionRules defines how to pick a model for a deliverable
	// based on its properties. Rules are evaluated in order; first match wins.
	SuggestionRules []SuggestionRule `json:"suggestion_rules"`
}

// ModelEntry defines a single model and when it should be used.
type ModelEntry struct {
	// Alias is the short name used in spec docs, e.g. "claude-opus-4"
	// (same as the map key — duplicated here for serialization convenience)
	Alias string `json:"alias"`

	// Provider must match a key in Config.Providers
	Provider string `json:"provider"`

	// ModelID is the actual model string sent to the API
	ModelID string `json:"model_id"`

	// DisplayName is the human-friendly name shown in spec docs and CLI output
	DisplayName string `json:"display_name"`

	// MaxTokens overrides the provider default for this model
	MaxTokens int `json:"max_tokens"`

	// Tier classifies the model's capability level.
	// Possible values: "flagship" | "standard" | "fast" | "code"
	// Used by suggestion rules to match deliverables.
	Tier string `json:"tier"`

	// Strengths is a free-text list of what this model excels at.
	// Shown to engineers in spec docs. Also used by suggestion rules.
	// Examples: ["complex reasoning", "long context", "code generation"]
	Strengths []string `json:"strengths"`

	// CostTier classifies relative cost: "high" | "medium" | "low"
	CostTier string `json:"cost_tier"`

	// Notes is any additional info shown to engineers, e.g. known limitations.
	Notes string `json:"notes,omitempty"`
}

// SuggestionRule maps deliverable properties to a model alias.
// Rules are evaluated in order; first match wins.
// If no rule matches, the active provider's default model is used.
type SuggestionRule struct {
	// Description explains when this rule applies (shown in logs)
	Description string `json:"description"`

	// Conditions that must ALL be true for this rule to match.
	// All fields are optional — omitted fields are not checked.
	Conditions RuleConditions `json:"conditions"`

	// SuggestModel is the model alias to suggest when this rule matches.
	SuggestModel string `json:"suggest_model"`
}

// RuleConditions are the criteria evaluated against a deliverable.
// All specified fields must match (AND logic).
type RuleConditions struct {
	// Complexity matches the deliverable complexity: "S", "M", "L", "XL"
	// Supports comma-separated values, e.g. "L,XL"
	Complexity string `json:"complexity,omitempty"`

	// HasKeywords checks if the deliverable title or description contains
	// any of these keywords (OR logic within the list, case-insensitive)
	HasKeywords []string `json:"has_keywords,omitempty"`

	// DependencyCount matches if the number of dependencies is >= this value
	MinDependencies int `json:"min_dependencies,omitempty"`

	// RequiresTier ensures the suggested model is at least this tier.
	// Used as a minimum capability floor.
	RequiresTier string `json:"requires_tier,omitempty"`
}

// ── Other configs ─────────────────────────────────────────────────────────────

type PipelineConfig struct {
	MaxRetries        int    `json:"max_retries"`
	RetryDelaySeconds int    `json:"retry_delay_seconds"`
	PromptsDir        string `json:"prompts_dir"`
	Verbose           bool   `json:"verbose"`
}

type RegistryConfig struct {
	BaseDir string `json:"base_dir"`
}

type GateConfig struct {
	CoverageThreshold float64  `json:"coverage_threshold"`
	EnableLint        bool     `json:"enable_lint"`
	EnableTests       bool     `json:"enable_tests"`
	EnableTypeCheck   bool     `json:"enable_type_check"`
	CustomCommands    []string `json:"custom_commands,omitempty"`
}

// ── Load & validate ───────────────────────────────────────────────────────────

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	applyEnvOverrides(&cfg)
	if err := validate(&cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	// Populate alias field from map key for convenience
	for alias, m := range cfg.Models.Models {
		m.Alias = alias
		cfg.Models.Models[alias] = m
	}
	return &cfg, nil
}

func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("AGENTFLOW_PROVIDER"); v != "" {
		cfg.ActiveProvider = v
	}
	if v := os.Getenv("AGENTFLOW_VERBOSE"); v == "true" {
		cfg.Pipeline.Verbose = true
	}
}

func validate(cfg *Config) error {
	if cfg.ActiveProvider == "" {
		return fmt.Errorf("active_provider is required")
	}
	provider, ok := cfg.Providers[cfg.ActiveProvider]
	if !ok {
		return fmt.Errorf("active_provider %q not found in providers", cfg.ActiveProvider)
	}
	if provider.APIKeyEnv == "" {
		return fmt.Errorf("provider %q: api_key_env is required", cfg.ActiveProvider)
	}
	if os.Getenv(provider.APIKeyEnv) == "" {
		return fmt.Errorf("env var %q for provider %q is not set", provider.APIKeyEnv, cfg.ActiveProvider)
	}
	if cfg.Pipeline.PromptsDir == "" {
		return fmt.Errorf("pipeline.prompts_dir is required")
	}
	// Validate suggestion rules reference known model aliases
	for i, rule := range cfg.Models.SuggestionRules {
		if rule.SuggestModel == "" {
			return fmt.Errorf("suggestion_rules[%d]: suggest_model is required", i)
		}
		if _, ok := cfg.Models.Models[rule.SuggestModel]; !ok {
			return fmt.Errorf("suggestion_rules[%d]: suggest_model %q not in models catalog", i, rule.SuggestModel)
		}
	}
	return nil
}

// ── Accessors ─────────────────────────────────────────────────────────────────

func (c *Config) ActiveProviderConfig() ProviderConfig {
	return c.Providers[c.ActiveProvider]
}

func (c *Config) PromptPath(name string) string {
	if !strings.HasSuffix(name, ".md") {
		name += ".md"
	}
	return filepath.Join(c.Pipeline.PromptsDir, name)
}

// ResolveModel resolves a model alias to its ModelEntry.
// Falls back to the active provider's default model if alias is empty or unknown.
func (c *Config) ResolveModel(alias string) ModelEntry {
	if alias != "" {
		if m, ok := c.Models.Models[alias]; ok {
			return m
		}
	}
	// Fallback: wrap the provider's default model as a ModelEntry
	pc := c.ActiveProviderConfig()
	return ModelEntry{
		Alias:       "default",
		Provider:    c.ActiveProvider,
		ModelID:     pc.Model,
		DisplayName: pc.Name + " (default)",
		MaxTokens:   pc.MaxTokens,
	}
}

// SuggestModel picks the best model alias for a deliverable based on
// suggestion rules. Returns empty string if no rule matches (caller uses default).
func (c *Config) SuggestModel(complexity string, title, description string, depCount int) string {
	titleLower := strings.ToLower(title + " " + description)

	for _, rule := range c.Models.SuggestionRules {
		if !matchesRule(rule.Conditions, complexity, titleLower, depCount) {
			continue
		}
		if _, ok := c.Models.Models[rule.SuggestModel]; ok {
			return rule.SuggestModel
		}
	}
	return "" // no match — caller uses provider default
}

func matchesRule(cond RuleConditions, complexity, titleLower string, depCount int) bool {
	// Check complexity (comma-separated list)
	if cond.Complexity != "" {
		allowed := strings.Split(cond.Complexity, ",")
		found := false
		for _, c := range allowed {
			if strings.EqualFold(strings.TrimSpace(c), complexity) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// Check keywords (any match = true)
	if len(cond.HasKeywords) > 0 {
		found := false
		for _, kw := range cond.HasKeywords {
			if strings.Contains(titleLower, strings.ToLower(kw)) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// Check minimum dependency count
	if cond.MinDependencies > 0 && depCount < cond.MinDependencies {
		return false
	}

	return true
}

// ListModels returns all models sorted by tier then cost.
func (c *Config) ListModels() []ModelEntry {
	models := make([]ModelEntry, 0, len(c.Models.Models))
	for _, m := range c.Models.Models {
		models = append(models, m)
	}
	return models
}
