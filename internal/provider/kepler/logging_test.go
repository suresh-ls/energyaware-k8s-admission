package kepler

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

func TestLegacyDemandStructuredLogs(t *testing.T) {
	server := newServer(t, respond(vectorBody("7500")))
	var output bytes.Buffer
	provider := newProvider(t, server.URL, func(cfg *Config) {
		cfg.Logger = slog.New(slog.NewJSONHandler(&output, nil))
		cfg.Query = "query-secret"
	})
	if _, err := provider.GetCurrentSignal(context.Background()); err != nil {
		t.Fatal(err)
	}
	var event map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &event); err != nil {
		t.Fatal(err)
	}
	if event["msg"] != "Retrieved demand signal" || event["provider"] != "kepler" || event["unit"] != "kW" || event["value"] != 7.5 {
		t.Fatalf("incomplete event: %v", event)
	}
	if strings.Contains(output.String(), "query-secret") {
		t.Fatal("query leaked")
	}
}

func TestLegacyProviderFailureDoesNotLogResponseText(t *testing.T) {
	server := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		_, _ = w.Write([]byte("response-secret"))
	})
	var output bytes.Buffer
	provider := newProvider(t, server.URL, func(cfg *Config) { cfg.Logger = slog.New(slog.NewJSONHandler(&output, nil)) })
	if _, err := provider.GetCurrentSignal(context.Background()); err == nil {
		t.Fatal("expected failure")
	}
	var event map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &event); err != nil {
		t.Fatal(err)
	}
	if event["level"] != "ERROR" || event["stage"] != "power_query" || strings.Contains(output.String(), "response-secret") {
		t.Fatalf("unsafe or incomplete failure event: %s", output.String())
	}
}
