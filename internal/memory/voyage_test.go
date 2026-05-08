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

func TestVoyageProvider_Embed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/embeddings", r.URL.Path)
		require.Equal(t, "Bearer voyagekey", r.Header.Get("Authorization"))

		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		assert.Equal(t, "voyage-4", body["model"])
		assert.Equal(t, "document", body["input_type"])
		assert.Equal(t, float64(1536), body["output_dimension"])

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"embedding": make([]float32, 1536), "index": 0},
			},
		})
	}))
	defer server.Close()

	p := memory.NewVoyageWithBaseURL("voyagekey", "voyage-4", server.URL)
	result, err := p.Embed(context.Background(), []string{"test text"}, "document")
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Len(t, result[0], 1536)
}

func TestVoyageProvider_Dims(t *testing.T) {
	p := memory.NewVoyage("key", "")
	assert.Equal(t, 1536, p.Dims())
}
