package carbon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()

	client, err := NewClient(Config{
		APIToken:   "test-token",
		Zone:       "IN-SO",
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	return client
}

func TestGetCurrentSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v4/carbon-intensity/latest" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("zone") != "IN-SO" {
			t.Errorf("unexpected zone: %s", r.URL.Query().Get("zone"))
		}
		if r.Header.Get("auth-token") != "test-token" {
			t.Error("missing API token header")
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"zone":"IN-SO",
			"carbonIntensity":466,
			"datetime":"2026-11-04T10:00:00Z",
			"temporalGranularity":"5_minutes"
		}`))
	}))
	defer server.Close()

	client := newTestClient(t, server)

	signal, err := client.GetCurrent(context.Background(), "IN-SO")
	if err != nil {
		t.Fatalf("GetCurrent() error = %v", err)
	}

	if signal.Value != 466 {
		t.Errorf("expected value 466, got %v", signal.Value)
	}
	if signal.Provider != ProviderName {
		t.Errorf("unexpected provider: %s", signal.Provider)
	}
	if signal.Region != "IN-SO" {
		t.Errorf("unexpected region: %s", signal.Region)
	}
	if signal.Unit != CarbonUnit {
		t.Errorf("unexpected unit: %s", signal.Unit)
	}
	if signal.IsForecast {
		t.Error("current signal should not be a forecast")
	}
	if signal.Quality != "STALE" {
		t.Errorf("expected STALE for past observation, got %s", signal.Quality)
	}
	if signal.ValidUntil.Sub(signal.Timestamp) != 5*time.Minute {
		t.Error("incorrect validity duration")
	}
	if signal.CollectedAt.IsZero() {
		t.Error("CollectedAt should be populated")
	}
}

func TestGetCurrentHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()

	client := newTestClient(t, server)

	_, err := client.GetCurrent(context.Background(), "IN-SO")
	if err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("expected HTTP 401 error, got %v", err)
	}
}

func TestGetCurrentInvalidResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"zone":"IN-SO"}`))
	}))
	defer server.Close()

	client := newTestClient(t, server)

	_, err := client.GetCurrent(context.Background(), "IN-SO")
	if err == nil {
		t.Fatal("expected missing fields error")
	}
}

func TestGetCurrentInvalidIntensity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"zone":"IN-SO",
			"carbonIntensity":-10,
			"datetime":"2026-11-04T10:00:00Z",
			"temporalGranularity":"hourly"
		}`))
	}))
	defer server.Close()

	client := newTestClient(t, server)

	_, err := client.GetCurrent(context.Background(), "IN-SO")
	if err == nil {
		t.Fatal("expected invalid intensity error")
	}
}

func TestGetCurrentWrongRegion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"zone":"IN-WE",
			"carbonIntensity":300,
			"datetime":"2026-11-04T10:00:00Z",
			"temporalGranularity":"hourly"
		}`))
	}))
	defer server.Close()

	client := newTestClient(t, server)

	_, err := client.GetCurrent(context.Background(), "IN-SO")
	if err == nil {
		t.Fatal("expected region mismatch error")
	}
}

func TestGetCurrentCancelledContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("server should not receive a cancelled request")
	}))
	defer server.Close()

	client := newTestClient(t, server)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.GetCurrent(ctx, "IN-SO")
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

func TestNewClientRequiresToken(t *testing.T) {
	_, err := NewClient(Config{Zone: "IN-SO"})
	if err == nil {
		t.Fatal("expected missing token error")
	}
}

func TestNewClientUsesDefaultZone(t *testing.T) {
	client, err := NewClient(Config{APIToken: "test-token"})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	if client.config.Zone != DefaultZone {
		t.Errorf("expected default zone %s, got %s", DefaultZone, client.config.Zone)
	}
}
