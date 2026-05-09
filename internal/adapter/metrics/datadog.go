package metrics

import (
	"context"
	"net/http"
)

const datadogBaseURL = "https://api.datadoghq.com"

type DatadogProvider struct{}

func NewDatadogProvider(apiKey, appKey string, tags, alertNames []string, client *http.Client) *DatadogProvider {
	return NewDatadogProviderWithBaseURL(apiKey, appKey, tags, alertNames, client, datadogBaseURL)
}

func NewDatadogProviderWithBaseURL(apiKey, appKey string, tags, alertNames []string, client *http.Client, baseURL string) *DatadogProvider {
	return &DatadogProvider{}
}

func (p *DatadogProvider) FiringAlerts(_ context.Context) ([]Alert, error) { return nil, nil }
