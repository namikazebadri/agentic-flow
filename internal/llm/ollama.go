package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// OllamaProvider uses Ollama's OpenAI-compatible /api/chat endpoint.
type OllamaProvider struct {
	model   string
	baseURL string
	client  *http.Client
}

func NewOllamaProvider(model, baseURL string) *OllamaProvider {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	return &OllamaProvider{
		model:   model,
		baseURL: baseURL,
		client:  &http.Client{Timeout: 300 * time.Second}, // local models can be slow
	}
}

func (p *OllamaProvider) Name() string  { return "ollama" }
func (p *OllamaProvider) Model() string { return p.model }

type ollamaRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
	Options  ollamaOptions   `json:"options,omitempty"`
}

type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaOptions struct {
	NumPredict  int     `json:"num_predict,omitempty"`
	Temperature float64 `json:"temperature,omitempty"`
}

type ollamaResponse struct {
	Model   string `json:"model"`
	Message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"message"`
	Done            bool   `json:"done"`
	PromptEvalCount int    `json:"prompt_eval_count"`
	EvalCount       int    `json:"eval_count"`
	Error           string `json:"error,omitempty"`
}

func (p *OllamaProvider) Complete(ctx context.Context, req Request) (*Response, error) {
	messages := make([]ollamaMessage, 0, len(req.Messages)+1)

	if req.SystemPrompt != "" {
		messages = append(messages, ollamaMessage{Role: "system", Content: req.SystemPrompt})
	}
	for _, m := range req.Messages {
		messages = append(messages, ollamaMessage{Role: m.Role, Content: m.Content})
	}

	numPredict := req.MaxTokens
	if numPredict == 0 {
		numPredict = 4096
	}

	payload := ollamaRequest{
		Model:    p.model,
		Messages: messages,
		Stream:   false,
		Options:  ollamaOptions{NumPredict: numPredict, Temperature: req.Temperature},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, &ErrProvider{Provider: "ollama", Message: err.Error()}
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	var or ollamaResponse
	if err := json.Unmarshal(respBody, &or); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	if or.Error != "" {
		return nil, &ErrProvider{
			Provider:   "ollama",
			StatusCode: resp.StatusCode,
			Message:    or.Error,
		}
	}

	if resp.StatusCode != http.StatusOK {
		return nil, &ErrProvider{
			Provider:   "ollama",
			StatusCode: resp.StatusCode,
			Message:    string(respBody),
		}
	}

	return &Response{
		Content:      or.Message.Content,
		InputTokens:  or.PromptEvalCount,
		OutputTokens: or.EvalCount,
		Model:        or.Model,
		StopReason:   "stop",
	}, nil
}
