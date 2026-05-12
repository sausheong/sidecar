package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/sausheong/sidecar/internal/adapter"
	"github.com/sausheong/sidecar/internal/store"
)

var eventEmoji = map[Event]string{
	EventSkipped:   "⏭️",
	EventSuggested: "💡",
	EventCompleted: "✅",
	EventFailed:    "❌",
	EventNotified:  "🔔",
}

// SlackNotifier sends Slack block-kit messages to an incoming webhook URL.
type SlackNotifier struct {
	webhookURL string
	client     *http.Client
}

func NewSlackNotifier(webhookURL string) *SlackNotifier {
	return &SlackNotifier{
		webhookURL: webhookURL,
		client:     &http.Client{Timeout: 5 * time.Second},
	}
}

func (s *SlackNotifier) Name() string { return "slack" }

func (s *SlackNotifier) Notify(ctx context.Context, ev Event, sig adapter.Signal, task *store.Task) error {
	emoji := eventEmoji[ev]
	if emoji == "" {
		emoji = "🔔"
	}

	title := fmt.Sprintf("%s Sidecar — %s", emoji, ev)
	body := formatSignalText(sig)
	footer := fmt.Sprintf("Task `%s`  ·  %s", task.ID, task.CreatedAt.Format(time.RFC3339))

	payload := map[string]any{
		"blocks": []any{
			map[string]any{
				"type": "header",
				"text": map[string]any{"type": "plain_text", "text": title},
			},
			map[string]any{
				"type": "section",
				"text": map[string]any{"type": "mrkdwn", "text": body},
			},
			map[string]any{
				"type": "context",
				"elements": []any{
					map[string]any{"type": "mrkdwn", "text": footer},
				},
			},
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("slack: marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.webhookURL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("slack: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("slack: send: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("slack: unexpected status %d", resp.StatusCode)
	}
	return nil
}

func formatSignalText(sig adapter.Signal) string {
	switch sig.Type {
	case adapter.SignalUptimeFailure:
		url, _ := sig.Payload["url"].(string)
		ft, _ := sig.Payload["failure_type"].(string)
		switch ft {
		case "unreachable":
			errMsg, _ := sig.Payload["error"].(string)
			return fmt.Sprintf("*Endpoint unreachable*: `%s`\nError: %s", url, errMsg)
		case "wrong_status":
			got, _ := sig.Payload["got_status"].(int)
			want, _ := sig.Payload["expected_status"].(int)
			return fmt.Sprintf("*Wrong HTTP status*: `%s`\nGot `%d`, expected `%d`", url, got, want)
		case "slow_response":
			ms, _ := sig.Payload["elapsed_ms"].(int64)
			threshold, _ := sig.Payload["threshold_ms"].(int)
			return fmt.Sprintf("*Slow response*: `%s`\nTook %dms (threshold: %dms)", url, ms, threshold)
		}
		return fmt.Sprintf("Uptime check failed: `%s`", url)
	case adapter.SignalLogAnomaly:
		pattern, _ := sig.Payload["pattern"].(string)
		source, _ := sig.Payload["source"].(string)
		line, _ := sig.Payload["line"].(string)
		return fmt.Sprintf("*Log anomaly*: pattern `%s` in `%s`\nSample: %s", pattern, source, line)
	case adapter.SignalMetricAlert:
		name, _ := sig.Payload["alert_name"].(string)
		message, _ := sig.Payload["message"].(string)
		return fmt.Sprintf("*Metric alert*: %s\n%s", name, message)
	case adapter.SignalCIFailure:
		workflow, _ := sig.Payload["workflow_name"].(string)
		url, _ := sig.Payload["html_url"].(string)
		return fmt.Sprintf("*CI failure*: %s\n<%s|View run>", workflow, url)
	case adapter.SignalGitCommit:
		hash, _ := sig.Payload["hash"].(string)
		if len(hash) > 8 {
			hash = hash[:8]
		}
		return fmt.Sprintf("*Git commit*: `%s`", hash)
	default:
		return fmt.Sprintf("Signal: `%s`", sig.Type)
	}
}
