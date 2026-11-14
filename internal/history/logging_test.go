package history

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHistoryStructuredLogs(t *testing.T) {
	for _, estimate := range []bool{false, true} {
		t.Run(fmt.Sprint(estimate), func(t *testing.T) {
			server := historyServer(t, func(samples [][2]any) [][2]any {
				if estimate {
					return samples[:4]
				}
				return samples
			})
			var out bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&out, &slog.HandlerOptions{Level: slog.LevelDebug})).With("request_id", "request-123")
			_, err := (Exporter{PrometheusURL: server.URL, Step: 15 * time.Minute, AllowEdgeEstimates: estimate, Logger: logger}).Export(context.Background(), workload())
			if err != nil {
				t.Fatal(err)
			}
			found, warned := false, false
			for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
				var event map[string]any
				if err := json.Unmarshal([]byte(line), &event); err != nil {
					t.Fatal(err)
				}
				if event["msg"] == "Exported workload energy" {
					found = true
					if event["workload_uid"] != workload().UID || event["namespace"] != workload().Namespace || event["request_id"] != "request-123" || event["energy_wh"] == nil {
						t.Fatalf("incomplete event: %v", event)
					}
				}
				if event["msg"] == "Estimated workload energy at scrape boundaries" {
					warned = event["level"] == "WARN" && event["estimated_boundary_seconds"] == float64(900)
				}
			}
			if !found || warned != estimate {
				t.Fatalf("missing completion or estimation log: %s", out.String())
			}
			if strings.Contains(out.String(), workload().PowerQuery) {
				t.Fatal("query leaked into logs")
			}
		})
	}
}

func TestHistoryErrorLogsExcludeProviderSecrets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503); fmt.Fprint(w, "response-secret") }))
	defer server.Close()
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	w := workload()
	w.PowerQuery = `metric{token="query-secret"}`
	_, err := (Exporter{PrometheusURL: strings.Replace(server.URL, "http://", "http://user:credential-secret@", 1) + "?token=endpoint-secret", Logger: logger}).Export(context.Background(), w)
	if err == nil {
		t.Fatal("expected query failure")
	}
	var event map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &event); err != nil {
		t.Fatalf("invalid failure event: %s", output.String())
	}
	if event["level"] != "ERROR" || event["stage"] != "prometheus_query" || event["workload_uid"] != w.UID {
		t.Fatalf("incomplete failure: %v", event)
	}
	for _, secret := range []string{"query-secret", "credential-secret", "endpoint-secret", "response-secret"} {
		if strings.Contains(output.String(), secret) {
			t.Fatalf("leaked provider details: %s", output.String())
		}
	}
}
