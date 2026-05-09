package metrics

import (
	"context"
	"net/http"
)

type PrometheusProvider struct{}

func NewPrometheusProvider(endpoint string, alertNames []string, client *http.Client) *PrometheusProvider {
	return &PrometheusProvider{}
}

func (p *PrometheusProvider) FiringAlerts(_ context.Context) ([]Alert, error) { return nil, nil }
