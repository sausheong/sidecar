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

func TestDatadogProvider_FiringAlerts_AlertState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "test-api-key", r.Header.Get("DD-API-KEY"))
		assert.Equal(t, "test-app-key", r.Header.Get("DD-APPLICATION-KEY"))
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": int64(1001), "name": "High Error Rate", "message": "errors > 5%",
				"overall_state": "Alert", "tags": []string{"env:prod"}},
		})
	}))
	defer server.Close()

	p := metricsadapter.NewDatadogProviderWithBaseURL(
		"test-api-key", "test-app-key", nil, nil, server.Client(), server.URL)
	alerts, err := p.FiringAlerts(context.Background())
	require.NoError(t, err)
	require.Len(t, alerts, 1)
	assert.Equal(t, "1001", alerts[0].ID)
	assert.Equal(t, "High Error Rate", alerts[0].Name)
	assert.Equal(t, "errors > 5%", alerts[0].Message)
}

func TestDatadogProvider_FiringAlerts_OKState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": int64(1001), "name": "High Error Rate", "message": "msg",
				"overall_state": "OK", "tags": []string{}},
		})
	}))
	defer server.Close()

	p := metricsadapter.NewDatadogProviderWithBaseURL(
		"key", "appkey", nil, nil, server.Client(), server.URL)
	alerts, err := p.FiringAlerts(context.Background())
	require.NoError(t, err)
	assert.Empty(t, alerts)
}

func TestDatadogProvider_FiringAlerts_AlertNameFilter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": int64(1001), "name": "High Error Rate", "message": "msg1",
				"overall_state": "Alert", "tags": []string{}},
			{"id": int64(1002), "name": "High Latency", "message": "msg2",
				"overall_state": "Alert", "tags": []string{}},
		})
	}))
	defer server.Close()

	p := metricsadapter.NewDatadogProviderWithBaseURL(
		"key", "appkey", nil, []string{"High Error Rate"}, server.Client(), server.URL)
	alerts, err := p.FiringAlerts(context.Background())
	require.NoError(t, err)
	require.Len(t, alerts, 1)
	assert.Equal(t, "High Error Rate", alerts[0].Name)
}
