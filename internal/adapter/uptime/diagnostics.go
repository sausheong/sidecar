package uptime

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/sausheong/sidecar/internal/config"
)

// DiagnosticResult holds the outcome of one diagnostic check.
type DiagnosticResult struct {
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"` // human-readable summary
	Error  string `json:"error,omitempty"`  // set when OK is false
}

// RunDiagnostics executes the configured diagnostics for ep on failure.
// allEndpoints is the full list so the "cross" check can probe peers.
// If ep.Diagnostics is empty, a default set is used (dns, tcp, tls for https).
func RunDiagnostics(ctx context.Context, ep config.UptimeEndpoint, allEndpoints []config.UptimeEndpoint) map[string]DiagnosticResult {
	checks := ep.Diagnostics
	if len(checks) == 0 {
		checks = defaultDiagnostics(ep.URL)
	}

	results := make(map[string]DiagnosticResult, len(checks))
	for _, d := range checks {
		key, result := runOne(ctx, d, ep, allEndpoints)
		results[key] = result
	}
	return results
}

// defaultDiagnostics returns the auto-run set when none are configured.
func defaultDiagnostics(rawURL string) []config.UptimeDiagnostic {
	checks := []config.UptimeDiagnostic{
		{Check: "dns"},
		{Check: "tcp"},
	}
	if strings.HasPrefix(rawURL, "https://") {
		checks = append(checks, config.UptimeDiagnostic{Check: "tls"})
	}
	checks = append(checks, config.UptimeDiagnostic{Check: "cross"})
	return checks
}

func runOne(ctx context.Context, d config.UptimeDiagnostic, ep config.UptimeEndpoint, all []config.UptimeEndpoint) (string, DiagnosticResult) {
	switch d.Check {
	case "dns":
		return "dns", checkDNS(ctx, ep.URL)
	case "tcp":
		return "tcp", checkTCP(ctx, ep.URL, ep.Timeout)
	case "tls":
		return "tls", checkTLS(ctx, ep.URL)
	case "ping":
		return "ping", checkPing(ctx, ep.URL)
	case "cross":
		return "cross", checkCross(ctx, ep.URL, all)
	case "http":
		key := "http:" + d.URL
		return key, checkHTTP(ctx, d.URL)
	case "shell":
		key := "shell:" + d.Command
		return key, checkShell(ctx, d.Command)
	default:
		return d.Check, DiagnosticResult{OK: false, Error: fmt.Sprintf("unknown check %q", d.Check)}
	}
}

// ── Built-in checks ───────────────────────────────────────────────────────────

func checkDNS(ctx context.Context, rawURL string) DiagnosticResult {
	host := extractHost(rawURL)
	if host == "" {
		return DiagnosticResult{OK: false, Error: "could not parse hostname from URL"}
	}
	// Strip port if present
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	addrs, err := net.DefaultResolver.LookupHost(ctx, host)
	if err != nil {
		return DiagnosticResult{OK: false, Error: err.Error()}
	}
	return DiagnosticResult{OK: true, Detail: fmt.Sprintf("resolved to %s", strings.Join(addrs, ", "))}
}

func checkTCP(ctx context.Context, rawURL string, timeoutStr string) DiagnosticResult {
	hostPort := extractHostPort(rawURL)
	if hostPort == "" {
		return DiagnosticResult{OK: false, Error: "could not parse host:port from URL"}
	}
	timeout := 5 * time.Second
	if d, err := time.ParseDuration(timeoutStr); err == nil && d > 0 {
		timeout = d
	}
	dialer := &net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", hostPort)
	if err != nil {
		return DiagnosticResult{OK: false, Error: err.Error()}
	}
	conn.Close()
	return DiagnosticResult{OK: true, Detail: fmt.Sprintf("TCP connection to %s succeeded", hostPort)}
}

func checkTLS(ctx context.Context, rawURL string) DiagnosticResult {
	hostPort := extractHostPort(rawURL)
	if hostPort == "" {
		return DiagnosticResult{OK: false, Error: "could not parse host:port from URL"}
	}
	host := extractHost(rawURL)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	dialer := &tls.Dialer{Config: &tls.Config{ServerName: host}}
	conn, err := dialer.DialContext(ctx, "tcp", hostPort)
	if err != nil {
		return DiagnosticResult{OK: false, Error: fmt.Sprintf("TLS handshake failed: %s", err)}
	}
	defer conn.Close()
	tlsConn := conn.(*tls.Conn)
	certs := tlsConn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return DiagnosticResult{OK: false, Error: "no certificates presented"}
	}
	cert := certs[0]
	daysLeft := int(time.Until(cert.NotAfter).Hours() / 24)
	if daysLeft < 0 {
		return DiagnosticResult{OK: false, Error: fmt.Sprintf("certificate expired %d days ago", -daysLeft)}
	}
	detail := fmt.Sprintf("cert valid for %d more days (issuer: %s)", daysLeft, cert.Issuer.CommonName)
	if daysLeft < 14 {
		return DiagnosticResult{OK: false, Error: detail}
	}
	return DiagnosticResult{OK: true, Detail: detail}
}

func checkPing(ctx context.Context, rawURL string) DiagnosticResult {
	host := extractHost(rawURL)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	// Use system ping; -c 1 = one packet, -W 3 = 3s wait (Linux/macOS compatible)
	out, err := exec.CommandContext(ctx, "ping", "-c", "1", "-W", "3", host).CombinedOutput()
	if err != nil {
		return DiagnosticResult{OK: false, Error: fmt.Sprintf("ping failed: %s", strings.TrimSpace(string(out)))}
	}
	// Extract round-trip time from output
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "time=") || strings.Contains(line, "round-trip") || strings.Contains(line, "rtt") {
			return DiagnosticResult{OK: true, Detail: strings.TrimSpace(line)}
		}
	}
	return DiagnosticResult{OK: true, Detail: "host is reachable"}
}

func checkCross(ctx context.Context, failedURL string, all []config.UptimeEndpoint) DiagnosticResult {
	peers := make([]config.UptimeEndpoint, 0, len(all))
	for _, ep := range all {
		if ep.URL != failedURL {
			peers = append(peers, ep)
		}
	}
	if len(peers) == 0 {
		return DiagnosticResult{OK: true, Detail: "no other endpoints configured to compare"}
	}
	client := &http.Client{Timeout: 5 * time.Second}
	down := 0
	for _, peer := range peers {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, peer.URL, nil)
		if err != nil {
			down++
			continue
		}
		resp, err := client.Do(req)
		if err != nil || resp.StatusCode >= 500 {
			down++
		} else {
			resp.Body.Close()
		}
	}
	if down == len(peers) {
		return DiagnosticResult{
			OK:    false,
			Error: fmt.Sprintf("all %d other endpoint(s) are also down — likely infrastructure outage", len(peers)),
		}
	}
	if down > 0 {
		return DiagnosticResult{
			OK:     false,
			Detail: fmt.Sprintf("%d of %d other endpoints also failing — possible partial outage", down, len(peers)),
		}
	}
	return DiagnosticResult{OK: true, Detail: fmt.Sprintf("all %d other endpoint(s) are healthy — failure is isolated to this endpoint", len(peers))}
}

func checkHTTP(ctx context.Context, url string) DiagnosticResult {
	if url == "" {
		return DiagnosticResult{OK: false, Error: "no URL specified for http check"}
	}
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return DiagnosticResult{OK: false, Error: err.Error()}
	}
	resp, err := client.Do(req)
	if err != nil {
		return DiagnosticResult{OK: false, Error: err.Error()}
	}
	defer resp.Body.Close()
	ok := resp.StatusCode < 500
	detail := fmt.Sprintf("HTTP %d from %s", resp.StatusCode, url)
	if ok {
		return DiagnosticResult{OK: true, Detail: detail}
	}
	return DiagnosticResult{OK: false, Error: detail}
}

func checkShell(ctx context.Context, command string) DiagnosticResult {
	if command == "" {
		return DiagnosticResult{OK: false, Error: "no command specified for shell check"}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "sh", "-c", command).CombinedOutput()
	output := strings.TrimSpace(string(out))
	if err != nil {
		detail := output
		if detail == "" {
			detail = err.Error()
		}
		return DiagnosticResult{OK: false, Error: detail}
	}
	return DiagnosticResult{OK: true, Detail: output}
}

// ── URL parsing helpers ───────────────────────────────────────────────────────

func extractHost(rawURL string) string {
	// Strip scheme
	s := rawURL
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	// Strip path
	if i := strings.Index(s, "/"); i >= 0 {
		s = s[:i]
	}
	return s
}

func extractHostPort(rawURL string) string {
	host := extractHost(rawURL)
	// If port already present, return as-is
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	// Infer default port from scheme
	if strings.HasPrefix(rawURL, "https://") {
		return host + ":443"
	}
	return host + ":80"
}

// DiagnosticSummary produces a compact one-line summary of diagnostic results
// suitable for inclusion in triage messages.
func DiagnosticSummary(results map[string]DiagnosticResult) string {
	if len(results) == 0 {
		return ""
	}
	var parts []string
	order := []string{"dns", "tcp", "tls", "ping", "cross"}
	seen := map[string]bool{}
	for _, k := range order {
		if r, ok := results[k]; ok {
			seen[k] = true
			if r.OK {
				parts = append(parts, fmt.Sprintf("%s:ok", k))
			} else {
				parts = append(parts, fmt.Sprintf("%s:FAIL(%s)", k, r.Error))
			}
		}
	}
	// Append custom checks (http:*, shell:*)
	for k, r := range results {
		if seen[k] {
			continue
		}
		label := k
		if i := strings.Index(k, ":"); i >= 0 {
			label = k[:i] + "(" + k[i+1:] + ")"
		}
		if r.OK {
			parts = append(parts, label+":ok")
		} else {
			parts = append(parts, label+":FAIL")
		}
	}
	return strings.Join(parts, "  ")
}
