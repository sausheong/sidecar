package notify

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"

	"github.com/sausheong/sidecar/internal/adapter"
	"github.com/sausheong/sidecar/internal/config"
	"github.com/sausheong/sidecar/internal/store"
)

// EmailNotifier sends plain-text email notifications via SMTP.
// Supports STARTTLS (port 587, the default) and implicit TLS (port 465).
type EmailNotifier struct {
	cfg config.EmailConfig
}

func NewEmailNotifier(cfg config.EmailConfig) *EmailNotifier {
	return &EmailNotifier{cfg: cfg}
}

func (e *EmailNotifier) Name() string { return "email" }

func (e *EmailNotifier) Notify(_ context.Context, ev Event, sig adapter.Signal, task *store.Task) error {
	if len(e.cfg.To) == 0 {
		return fmt.Errorf("email: no recipients configured")
	}

	host := e.cfg.SMTPHost
	port := e.cfg.SMTPPort
	if port == 0 {
		port = 587
	}
	username := e.cfg.ResolveUsername()
	password := e.cfg.ResolvePassword()
	addr := fmt.Sprintf("%s:%d", host, port)

	subject := fmt.Sprintf("[Sidecar] %s — %s", ev, task.Summary)
	body := buildEmailBody(ev, sig, task)

	msg := buildMessage(e.cfg.From, e.cfg.To, subject, body)

	var auth smtp.Auth
	if username != "" {
		auth = smtp.PlainAuth("", username, password, host)
	}

	if port == 465 {
		return sendImplicitTLS(addr, host, auth, e.cfg.From, e.cfg.To, msg)
	}
	return smtp.SendMail(addr, auth, e.cfg.From, e.cfg.To, msg)
}

// sendImplicitTLS dials with TLS from the start (port 465 / SMTPS).
func sendImplicitTLS(addr, host string, auth smtp.Auth, from string, to []string, msg []byte) error {
	tlsCfg := &tls.Config{ServerName: host}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp", addr, tlsCfg)
	if err != nil {
		return fmt.Errorf("email: TLS dial: %w", err)
	}
	defer conn.Close()

	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("email: SMTP client: %w", err)
	}
	defer c.Close()

	if auth != nil {
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("email: auth: %w", err)
		}
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("email: MAIL FROM: %w", err)
	}
	for _, r := range to {
		if err := c.Rcpt(r); err != nil {
			return fmt.Errorf("email: RCPT TO %s: %w", r, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("email: DATA: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("email: write body: %w", err)
	}
	return w.Close()
}

func buildMessage(from string, to []string, subject, body string) []byte {
	var sb strings.Builder
	sb.WriteString("From: " + from + "\r\n")
	sb.WriteString("To: " + strings.Join(to, ", ") + "\r\n")
	sb.WriteString("Subject: " + subject + "\r\n")
	sb.WriteString("MIME-Version: 1.0\r\n")
	sb.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	sb.WriteString("\r\n")
	sb.WriteString(body)
	return []byte(sb.String())
}

func buildEmailBody(ev Event, sig adapter.Signal, task *store.Task) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Sidecar notification\n"))
	sb.WriteString(fmt.Sprintf("Event:   %s\n", ev))
	sb.WriteString(fmt.Sprintf("Signal:  %s\n", sig.Type))
	sb.WriteString(fmt.Sprintf("Summary: %s\n", task.Summary))
	sb.WriteString(fmt.Sprintf("Task ID: %s\n", task.ID))
	sb.WriteString(fmt.Sprintf("Time:    %s\n", task.CreatedAt.Format(time.RFC3339)))
	sb.WriteString("\n")

	// Signal-specific detail
	switch sig.Type {
	case adapter.SignalUptimeFailure:
		url, _ := sig.Payload["url"].(string)
		ft, _ := sig.Payload["failure_type"].(string)
		sb.WriteString(fmt.Sprintf("Endpoint:     %s\n", url))
		sb.WriteString(fmt.Sprintf("Failure type: %s\n", ft))
		switch ft {
		case "unreachable":
			errMsg, _ := sig.Payload["error"].(string)
			sb.WriteString(fmt.Sprintf("Error:        %s\n", errMsg))
		case "wrong_status":
			got, _ := sig.Payload["got_status"].(int)
			want, _ := sig.Payload["expected_status"].(int)
			sb.WriteString(fmt.Sprintf("Got status:   %d (expected %d)\n", got, want))
		case "slow_response":
			ms, _ := sig.Payload["elapsed_ms"].(int64)
			threshold, _ := sig.Payload["threshold_ms"].(int)
			sb.WriteString(fmt.Sprintf("Response:     %dms (threshold: %dms)\n", ms, threshold))
		}
	case adapter.SignalLogAnomaly:
		pattern, _ := sig.Payload["pattern"].(string)
		source, _ := sig.Payload["source"].(string)
		line, _ := sig.Payload["line"].(string)
		sb.WriteString(fmt.Sprintf("Pattern: %s\n", pattern))
		sb.WriteString(fmt.Sprintf("Source:  %s\n", source))
		sb.WriteString(fmt.Sprintf("Sample:  %s\n", line))
	case adapter.SignalMetricAlert:
		name, _ := sig.Payload["alert_name"].(string)
		message, _ := sig.Payload["message"].(string)
		sb.WriteString(fmt.Sprintf("Alert:   %s\n", name))
		sb.WriteString(fmt.Sprintf("Details: %s\n", message))
	case adapter.SignalCIFailure:
		workflow, _ := sig.Payload["workflow_name"].(string)
		url, _ := sig.Payload["html_url"].(string)
		sb.WriteString(fmt.Sprintf("Workflow: %s\n", workflow))
		sb.WriteString(fmt.Sprintf("Run URL:  %s\n", url))
	}

	sb.WriteString("\n-- \nSidecar autonomous engineering agent\n")
	return sb.String()
}
