package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const voyageBase = "https://api.voyageai.com"
const voyageDefaultModel = "voyage-4"

// VoyageProvider embeds text using Voyage AI's embedding API.
// Output is fixed at 1536 dimensions to match the memory_entries schema.
type VoyageProvider struct {
	apiKey  string
	model   string
	baseURL string
	client  *http.Client
}

// NewVoyage creates a provider using the Voyage AI API.
func NewVoyage(apiKey, model string) *VoyageProvider {
	return NewVoyageWithBaseURL(apiKey, model, voyageBase)
}

// NewVoyageWithBaseURL creates a provider with a custom base URL (used in tests).
func NewVoyageWithBaseURL(apiKey, model, baseURL string) *VoyageProvider {
	if model == "" {
		model = voyageDefaultModel
	}
	return &VoyageProvider{
		apiKey:  apiKey,
		model:   model,
		baseURL: baseURL,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

// Dims returns 1536 (output_dimension configured to match the schema).
func (p *VoyageProvider) Dims() int { return 1536 }

// Embed calls the Voyage AI embeddings API.
// inputType is "document" (when storing) or "query" (when searching).
func (p *VoyageProvider) Embed(ctx context.Context, texts []string, inputType string) ([][]float32, error) {
	payload := map[string]any{
		"input":            texts,
		"model":            p.model,
		"input_type":       inputType,
		"output_dimension": 1536, // must match memory_entries.embedding dimension
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshalling voyage request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/embeddings", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("voyage embed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("voyage embed status %d", resp.StatusCode)
	}

	var result struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
			Index     int       `json:"index"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding voyage response: %w", err)
	}

	embeddings := make([][]float32, len(texts))
	for _, d := range result.Data {
		if d.Index < len(embeddings) {
			embeddings[d.Index] = d.Embedding
		}
	}
	return embeddings, nil
}
