package llm

import (
	"context"
	"fmt"
)

// Message represents a single chat message.
type Message struct {
	Role    string `json:"role"` // "user" | "assistant" | "system"
	Content string `json:"content"`
}

// Request is the provider-agnostic completion request.
type Request struct {
	Messages     []Message
	SystemPrompt string
	MaxTokens    int
	Temperature  float64
}

// Response is the provider-agnostic completion response.
type Response struct {
	Content      string
	InputTokens  int
	OutputTokens int
	Model        string
	StopReason   string
}

// Provider is the interface every LLM backend must implement.
type Provider interface {
	// Complete sends a request and returns the model response.
	Complete(ctx context.Context, req Request) (*Response, error)
	// Name returns the provider identifier (e.g. "anthropic", "openai").
	Name() string
	// Model returns the model being used.
	Model() string
}

// ErrProvider wraps LLM provider errors with context.
type ErrProvider struct {
	Provider   string
	StatusCode int
	Message    string
}

func (e *ErrProvider) Error() string {
	if e.StatusCode != 0 {
		return fmt.Sprintf("llm provider %s (HTTP %d): %s", e.Provider, e.StatusCode, e.Message)
	}
	return fmt.Sprintf("llm provider %s: %s", e.Provider, e.Message)
}

// Registry holds all registered providers and resolves the active one.
type Registry struct {
	providers map[string]Provider
}

func NewRegistry() *Registry {
	return &Registry{providers: make(map[string]Provider)}
}

func (r *Registry) Register(p Provider) {
	r.providers[p.Name()] = p
}

func (r *Registry) Get(name string) (Provider, error) {
	p, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf("llm provider %q not registered", name)
	}
	return p, nil
}
