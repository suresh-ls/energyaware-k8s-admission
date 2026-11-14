package demand

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

	"github.com/go-logr/logr"
	"github.com/suresh-ls/energyaware-k8s-admission/controller/internal/signals"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const loggingSuccessCase = "success"
const retrievalFailureMessage = "Could not retrieve demand signal"

func TestDemandStructuredLogs(t *testing.T) {
	for _, tc := range []struct {
		name string
		code int
		age  time.Duration
		want string
	}{
		{loggingSuccessCase, 200, 15 * time.Second, "Retrieved demand signal"},
		{"HTTP failure", 503, 15 * time.Second, retrievalFailureMessage},
		{"transport failure", 0, 15 * time.Second, retrievalFailureMessage},
		{"expired", 200, 2 * time.Minute, retrievalFailureMessage},
		{"invalid number", 200, 15 * time.Second, retrievalFailureMessage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Second)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.code)
				if tc.code != 200 {
					_, _ = fmt.Fprint(w, "response-secret")
					return
				}
				value := "7500"
				if tc.name == "invalid number" {
					value = "numeric-secret"
				}
				if r.URL.Query().Get("query") == DefaultTimestampQuery {
					value = fmt.Sprint(now.Add(-tc.age).Unix())
				}
				_, _ = fmt.Fprint(w, vector(now, value))
			}))
			defer server.Close()
			if tc.code == 0 {
				server.Close()
			}
			var output bytes.Buffer
			logger := logr.FromSlogHandler(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug})).WithValues("request_id", "request-123")
			ctx := log.IntoContext(context.Background(), logger)
			provider, err := NewProvider(Config{PrometheusURL: strings.Replace(server.URL, "http://", "http://user:credential-secret@", 1) + "?token=endpoint-secret", Query: `sum(metric{token="query-secret"})`, TimestampQuery: DefaultTimestampQuery, Region: testRegion, CapKW: 10, Now: func() time.Time { return now }})
			if err != nil {
				t.Fatal(err)
			}
			_, err = provider.GetCurrentSignal(ctx, signals.SignalRequest{SignalType: signals.SignalTypeDemandCap, Region: testRegion})
			if (err == nil) != (tc.name == loggingSuccessCase) {
				t.Fatalf("unexpected result: %v", err)
			}
			found := false
			for line := range strings.SplitSeq(strings.TrimSpace(output.String()), "\n") {
				var event map[string]any
				if err := json.Unmarshal([]byte(line), &event); err != nil {
					t.Fatalf("invalid structured log: %s", line)
				}
				if event["msg"] == tc.want {
					found = true
					if event["provider"] != "kepler" || event["region"] != testRegion || event["request_id"] != "request-123" {
						t.Fatalf("lost context: %v", event)
					}
					if tc.name == loggingSuccessCase && (event["value"] != 7.5 || event["status"] != StatusWithinCap) {
						t.Fatalf("lost measurement: %v", event)
					}
					if tc.name != loggingSuccessCase && (event["level"] != "ERROR" || event["stage"] == nil) {
						t.Fatalf("missing failure fields: %v", event)
					}
				}
				if tc.name != loggingSuccessCase && event["msg"] == "Retrieved demand signal" {
					t.Fatal("failure logged as success")
				}
			}
			if !found {
				t.Fatalf("missing event: %s", output.String())
			}
			for _, secret := range []string{"credential-secret", "endpoint-secret", "response-secret", "query-secret", "numeric-secret", DefaultQuery, DefaultTimestampQuery} {
				if strings.Contains(output.String(), secret) {
					t.Fatalf("sensitive provider detail logged: %s", secret)
				}
			}
		})
	}
}

func TestDemandCancellationLogsAtDebug(t *testing.T) {
	var output bytes.Buffer
	logger := logr.FromSlogHandler(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx, cancel := context.WithCancel(log.IntoContext(context.Background(), logger))
	cancel()
	provider, err := NewProvider(Config{PrometheusURL: testURL, Region: testRegion, CapKW: 10})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.GetCurrentSignal(ctx, signals.SignalRequest{SignalType: signals.SignalTypeDemandCap, Region: testRegion}); err == nil {
		t.Fatal("expected cancellation")
	}
	if !strings.Contains(output.String(), "Demand signal retrieval cancelled") || strings.Contains(output.String(), `"level":"ERROR"`) {
		t.Fatalf("cancellation should not be logged as failure: %s", output.String())
	}
}
