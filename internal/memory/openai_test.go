package memory_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sausheong/sidecar/internal/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenAIProvider_Embed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/embeddings", r.URL.Path)
		require.Equal(t, "Bearer testkey", r.Header.Get("Authorization"))

		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		assert.Equal(t, "text-embedding-3-small", body["model"])
		assert.Equal(t, float64(1024), body["dimensions"])

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"embedding": make([]float32, 1024), "index": 0},
			},
		})
	}))
	defer server.Close()

	p := memory.NewOpenAIWithBaseURL("testkey", "text-embedding-3-small", server.URL)
	result, err := p.Embed(context.Background(), []string{"hello"}, "document")
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Len(t, result[0], 1024)
}

func TestOpenAIProvider_Dims(t *testing.T) {
	p := memory.NewOpenAI("key", "")
	assert.Equal(t, 1024, p.Dims())
}
