package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const openAIBase = "https://api.openai.com"
const openAIDefaultModel = "text-embedding-3-small"

// OpenAIProvider embeds text using OpenAI's embedding API.
type OpenAIProvider struct {
	apiKey  string
	model   string
	baseURL string
	client  *http.Client
}

// NewOpenAI creates a provider using the OpenAI API.
func NewOpenAI(apiKey, model string) *OpenAIProvider {
	return NewOpenAIWithBaseURL(apiKey, model, openAIBase)
}

// NewOpenAIWithBaseURL creates a provider with a custom base URL (used in tests).
func NewOpenAIWithBaseURL(apiKey, model, baseURL string) *OpenAIProvider {
	if model == "" {
		model = openAIDefaultModel
	}
	return &OpenAIProvider{
		apiKey:  apiKey,
		model:   model,
		baseURL: baseURL,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

// Dims returns 1536 (text-embedding-3-small fixed dimension).
func (p *OpenAIProvider) Dims() int { return 1536 }

// Embed calls the OpenAI embeddings API. inputType is ignored (OpenAI has no query/document distinction).
func (p *OpenAIProvider) Embed(ctx context.Context, texts []string, _ string) ([][]float32, error) {
	payload := map[string]any{
		"input": texts,
		"model": p.model,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshalling openai request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/embeddings", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai embed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openai embed status %d", resp.StatusCode)
	}

	var result struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
			Index     int       `json:"index"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding openai response: %w", err)
	}

	embeddings := make([][]float32, len(texts))
	for _, d := range result.Data {
		if d.Index < len(embeddings) {
			embeddings[d.Index] = d.Embedding
		}
	}
	return embeddings, nil
}
