package metrics_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	metricsadapter "github.com/sausheong/sidecar/internal/adapter/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makePromServer(t *testing.T, rules []map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data": map[string]any{
				"groups": []map[string]any{
					{"rules": rules},
				},
			},
		})
	}))
}

func TestPrometheusProvider_FiringAlerts_Firing(t *testing.T) {
	server := makePromServer(t, []map[string]any{
		{"type": "alerting", "name": "HighMemory", "query": "mem > 90",
			"state": "firing", "labels": map[string]string{"severity": "critical"}},
	})
	defer server.Close()

	p := metricsadapter.NewPrometheusProvider(server.URL, nil, server.Client())
	alerts, err := p.FiringAlerts(context.Background())
	require.NoError(t, err)
	require.Len(t, alerts, 1)
	assert.Equal(t, "HighMemory", alerts[0].Name)
	assert.Equal(t, "mem > 90", alerts[0].Message)
	assert.Equal(t, map[string]string{"severity": "critical"}, alerts[0].Labels)
	assert.NotEmpty(t, alerts[0].ID)
}

func TestPrometheusProvider_FiringAlerts_Inactive(t *testing.T) {
	server := makePromServer(t, []map[string]any{
		{"type": "alerting", "name": "HighMemory", "query": "mem > 90",
			"state": "inactive", "labels": map[string]string{}},
	})
	defer server.Close()

	p := metricsadapter.NewPrometheusProvider(server.URL, nil, server.Client())
	alerts, err := p.FiringAlerts(context.Background())
	require.NoError(t, err)
	assert.Empty(t, alerts)
}

func TestPrometheusProvider_AlertID_Stable(t *testing.T) {
	server := makePromServer(t, []map[string]any{
		{"type": "alerting", "name": "HighMemory", "query": "q",
			"state": "firing", "labels": map[string]string{"env": "prod", "job": "api"}},
	})
	defer server.Close()

	p := metricsadapter.NewPrometheusProvider(server.URL, nil, server.Client())

	alerts1, err := p.FiringAlerts(context.Background())
	require.NoError(t, err)
	require.Len(t, alerts1, 1)

	alerts2, err := p.FiringAlerts(context.Background())
	require.NoError(t, err)
	require.Len(t, alerts2, 1)

	assert.Equal(t, alerts1[0].ID, alerts2[0].ID, "alert ID must be stable across polls")
	assert.NotEmpty(t, alerts1[0].ID)
}
