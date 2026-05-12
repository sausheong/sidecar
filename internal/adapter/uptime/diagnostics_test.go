package uptime_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	uptimeadapter "github.com/sausheong/sidecar/internal/adapter/uptime"
	"github.com/sausheong/sidecar/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestCheckDNS_ValidHost(t *testing.T) {
	results := uptimeadapter.RunDiagnostics(context.Background(),
		config.UptimeEndpoint{
			URL:         "http://localhost/health",
			Diagnostics: []config.UptimeDiagnostic{{Check: "dns"}},
		}, nil)
	r, ok := results["dns"]
	assert.True(t, ok)
	assert.True(t, r.OK, "localhost should resolve")
}

func TestCheckTCP_Reachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	results := uptimeadapter.RunDiagnostics(context.Background(),
		config.UptimeEndpoint{
			URL:         srv.URL,
			Diagnostics: []config.UptimeDiagnostic{{Check: "tcp"}},
		}, nil)
	r := results["tcp"]
	assert.True(t, r.OK, "should connect to test server")
}

func TestCheckTCP_Unreachable(t *testing.T) {
	results := uptimeadapter.RunDiagnostics(context.Background(),
		config.UptimeEndpoint{
			URL:         "http://127.0.0.1:19999",
			Timeout:     "200ms",
			Diagnostics: []config.UptimeDiagnostic{{Check: "tcp"}},
		}, nil)
	r := results["tcp"]
	assert.False(t, r.OK, "nothing listening on 19999")
}

func TestCheckHTTP_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	results := uptimeadapter.RunDiagnostics(context.Background(),
		config.UptimeEndpoint{
			URL:         "http://127.0.0.1:1/unused",
			Diagnostics: []config.UptimeDiagnostic{{Check: "http", URL: srv.URL}},
		}, nil)
	key := "http:" + srv.URL
	r := results[key]
	assert.True(t, r.OK)
}

func TestCheckHTTP_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	results := uptimeadapter.RunDiagnostics(context.Background(),
		config.UptimeEndpoint{
			URL:         "http://127.0.0.1:1/unused",
			Diagnostics: []config.UptimeDiagnostic{{Check: "http", URL: srv.URL}},
		}, nil)
	key := "http:" + srv.URL
	r := results[key]
	assert.False(t, r.OK)
}

func TestCheckShell_Success(t *testing.T) {
	results := uptimeadapter.RunDiagnostics(context.Background(),
		config.UptimeEndpoint{
			URL:         "http://example.com",
			Diagnostics: []config.UptimeDiagnostic{{Check: "shell", Command: "echo ok"}},
		}, nil)
	r := results["shell:echo ok"]
	assert.True(t, r.OK)
	assert.Equal(t, "ok", r.Detail)
}

func TestCheckShell_Failure(t *testing.T) {
	results := uptimeadapter.RunDiagnostics(context.Background(),
		config.UptimeEndpoint{
			URL:         "http://example.com",
			Diagnostics: []config.UptimeDiagnostic{{Check: "shell", Command: "exit 1"}},
		}, nil)
	r := results["shell:exit 1"]
	assert.False(t, r.OK)
}

func TestCheckCross_AllHealthy(t *testing.T) {
	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer srv1.Close()
	defer srv2.Close()

	all := []config.UptimeEndpoint{{URL: srv1.URL}, {URL: srv2.URL}}
	results := uptimeadapter.RunDiagnostics(context.Background(),
		config.UptimeEndpoint{
			URL:         srv1.URL,
			Diagnostics: []config.UptimeDiagnostic{{Check: "cross"}},
		}, all)
	r := results["cross"]
	assert.True(t, r.OK, "other endpoints are healthy — failure is isolated")
}

func TestCheckCross_AllDown(t *testing.T) {
	// Peer that returns 503
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer peer.Close()

	all := []config.UptimeEndpoint{{URL: "http://127.0.0.1:1/failed"}, {URL: peer.URL}}
	results := uptimeadapter.RunDiagnostics(context.Background(),
		config.UptimeEndpoint{
			URL:         "http://127.0.0.1:1/failed",
			Diagnostics: []config.UptimeDiagnostic{{Check: "cross"}},
		}, all)
	r := results["cross"]
	assert.False(t, r.OK, "all peers down → infra outage")
}

func TestDefaultDiagnostics_HTTP(t *testing.T) {
	// Empty diagnostics → auto-run dns + tcp (no tls for http)
	results := uptimeadapter.RunDiagnostics(context.Background(),
		config.UptimeEndpoint{URL: "http://localhost/health"}, nil)
	_, hasDNS := results["dns"]
	_, hasTCP := results["tcp"]
	_, hasTLS := results["tls"]
	assert.True(t, hasDNS)
	assert.True(t, hasTCP)
	assert.False(t, hasTLS, "tls not auto-run for http://")
}

func TestDiagnosticSummary_Format(t *testing.T) {
	results := map[string]uptimeadapter.DiagnosticResult{
		"dns":  {OK: true, Detail: "resolved to 1.2.3.4"},
		"tcp":  {OK: false, Error: "connection refused"},
		"cross": {OK: true, Detail: "all 2 other endpoints healthy"},
	}
	s := uptimeadapter.DiagnosticSummary(results)
	assert.Contains(t, s, "dns:ok")
	assert.Contains(t, s, "tcp:FAIL")
}

func TestUnknownCheck(t *testing.T) {
	results := uptimeadapter.RunDiagnostics(context.Background(),
		config.UptimeEndpoint{
			URL:         "http://example.com",
			Diagnostics: []config.UptimeDiagnostic{{Check: "magic"}},
		}, nil)
	r := results["magic"]
	assert.False(t, r.OK)
	assert.Contains(t, r.Error, "unknown check")
}
