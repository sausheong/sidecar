package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// PrometheusProvider fetches firing alerting rules from the Prometheus Rules API.
type PrometheusProvider struct {
	endpoint   string
	alertNames []string
	client     *http.Client
}

// NewPrometheusProvider creates a PrometheusProvider. endpoint is the Prometheus base URL.
func NewPrometheusProvider(endpoint string, alertNames []string, client *http.Client) *PrometheusProvider {
	return &PrometheusProvider{
		endpoint:   strings.TrimRight(endpoint, "/"),
		alertNames: alertNames,
		client:     client,
	}
}

type promRulesResponse struct {
	Status string `json:"status"`
	Data   struct {
		Groups []struct {
			Rules []promRule `json:"rules"`
		} `json:"groups"`
	} `json:"data"`
}

type promRule struct {
	Type   string            `json:"type"`
	Name   string            `json:"name"`
	Query  string            `json:"query"`
	State  string            `json:"state"`
	Labels map[string]string `json:"labels"`
}

// FiringAlerts returns alerting rules currently in "firing" state.
func (p *PrometheusProvider) FiringAlerts(ctx context.Context) ([]Alert, error) {
	url := p.endpoint + "/api/v1/rules?type=alert"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("prometheus api status %d", resp.StatusCode)
	}

	var pr promRulesResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, err
	}

	allowList := make(map[string]bool, len(p.alertNames))
	for _, n := range p.alertNames {
		allowList[n] = true
	}

	var alerts []Alert
	for _, group := range pr.Data.Groups {
		for _, rule := range group.Rules {
			if rule.Type != "alerting" || rule.State != "firing" {
				continue
			}
			if len(allowList) > 0 && !allowList[rule.Name] {
				continue
			}
			alerts = append(alerts, Alert{
				ID:      prometheusAlertID(rule.Name, rule.Labels),
				Name:    rule.Name,
				Message: rule.Query,
				Labels:  rule.Labels,
			})
		}
	}
	return alerts, nil
}

// prometheusAlertID produces a stable identifier from alert name and sorted label pairs.
func prometheusAlertID(name string, labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+labels[k])
	}
	return name + "|" + strings.Join(parts, ",")
}
