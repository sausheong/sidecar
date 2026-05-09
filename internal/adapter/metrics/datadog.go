package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const datadogBaseURL = "https://api.datadoghq.com"

// DatadogProvider fetches firing monitors from the Datadog Monitors API.
type DatadogProvider struct {
	apiKey     string
	appKey     string
	tags       []string
	alertNames []string
	baseURL    string
	client     *http.Client
}

// NewDatadogProvider creates a DatadogProvider using the production Datadog API.
func NewDatadogProvider(apiKey, appKey string, tags, alertNames []string, client *http.Client) *DatadogProvider {
	return NewDatadogProviderWithBaseURL(apiKey, appKey, tags, alertNames, client, datadogBaseURL)
}

// NewDatadogProviderWithBaseURL creates a DatadogProvider with a custom base URL (used in tests).
func NewDatadogProviderWithBaseURL(apiKey, appKey string, tags, alertNames []string, client *http.Client, baseURL string) *DatadogProvider {
	return &DatadogProvider{
		apiKey:     apiKey,
		appKey:     appKey,
		tags:       tags,
		alertNames: alertNames,
		baseURL:    baseURL,
		client:     client,
	}
}

type datadogMonitor struct {
	ID           int64    `json:"id"`
	Name         string   `json:"name"`
	Message      string   `json:"message"`
	OverallState string   `json:"overall_state"`
	Tags         []string `json:"tags"`
}

// FiringAlerts returns monitors in "Alert" or "Warn" state.
func (p *DatadogProvider) FiringAlerts(ctx context.Context) ([]Alert, error) {
	url := p.baseURL + "/api/v1/monitor"
	if len(p.tags) > 0 {
		url += "?monitor_tags=" + strings.Join(p.tags, ",")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("DD-API-KEY", p.apiKey)
	req.Header.Set("DD-APPLICATION-KEY", p.appKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("datadog api status %d", resp.StatusCode)
	}

	var monitors []datadogMonitor
	if err := json.NewDecoder(resp.Body).Decode(&monitors); err != nil {
		return nil, err
	}

	allowList := make(map[string]bool, len(p.alertNames))
	for _, n := range p.alertNames {
		allowList[n] = true
	}

	var alerts []Alert
	for _, m := range monitors {
		if m.OverallState != "Alert" && m.OverallState != "Warn" {
			continue
		}
		if len(allowList) > 0 && !allowList[m.Name] {
			continue
		}
		msg := m.Message
		if len([]rune(msg)) > 500 {
			msg = string([]rune(msg)[:500])
		}
		alerts = append(alerts, Alert{
			ID:      fmt.Sprintf("%d", m.ID),
			Name:    m.Name,
			Message: msg,
			Labels:  map[string]string{"tags": strings.Join(m.Tags, ",")},
		})
	}
	return alerts, nil
}
