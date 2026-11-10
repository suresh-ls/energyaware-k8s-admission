package carbon

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGetForecastSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v4/carbon-intensity/forecast" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("zone") != "IN-SO" {
			t.Errorf("unexpected zone: %s", r.URL.Query().Get("zone"))
		}
		if r.URL.Query().Get("horizonHours") != "24" {
			t.Errorf("unexpected horizon: %s", r.URL.Query().Get("horizonHours"))
		}
		if r.Header.Get("auth-token") != "test-token" {
			t.Error("missing API token header")
		}

		first := time.Now().UTC().Add(1 * time.Hour).Truncate(time.Second)
		second := first.Add(1 * time.Hour)

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
			"zone":"IN-SO",
			"forecast":[
				{"datetime":%q,"carbonIntensity":420},
				{"datetime":%q,"carbonIntensity":450}
			]
		}`, first.Format(time.RFC3339), second.Format(time.RFC3339))
	}))
	defer server.Close()

	client := newTestClient(t, server)

	signals, err := client.GetForecast(context.Background(), "IN-SO", 24*time.Hour)
	if err != nil {
		t.Fatalf("GetForecast() error = %v", err)
	}

	if len(signals) != 2 {
		t.Fatalf("expected 2 forecast signals, got %d", len(signals))
	}

	if signals[0].Value != 420 || signals[1].Value != 450 {
		t.Errorf("unexpected forecast values: %v, %v", signals[0].Value, signals[1].Value)
	}

	for _, signal := range signals {
		if signal.SignalType != "carbon" {
			t.Errorf("unexpected signal type: %s", signal.SignalType)
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
		if !signal.IsForecast {
			t.Error("forecast signal should have IsForecast=true")
		}
		if signal.Quality != "FRESH" {
			t.Errorf("expected FRESH quality, got %s", signal.Quality)
		}
		if signal.CollectedAt.IsZero() {
			t.Error("CollectedAt should be populated")
		}
	}
}

func TestGetForecastInvalidHorizons(t *testing.T) {
	client, err := NewClient(Config{APIToken: "test-token"})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	horizons := []time.Duration{
		0,
		5 * time.Hour,
		12 * time.Hour,
		24*time.Hour + 30*time.Minute,
		96 * time.Hour,
	}

	for _, horizon := range horizons {
		t.Run(horizon.String(), func(t *testing.T) {
			_, err := client.GetForecast(context.Background(), "IN-SO", horizon)
			if err == nil {
				t.Errorf("expected error for horizon %v", horizon)
			}
		})
	}
}

func TestGetForecastHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()

	client := newTestClient(t, server)

	_, err := client.GetForecast(context.Background(), "IN-SO", 24*time.Hour)
	if err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("expected HTTP 401 error, got %v", err)
	}
}

func TestGetForecastEmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"zone":"IN-SO","forecast":[]}`))
	}))
	defer server.Close()

	client := newTestClient(t, server)

	_, err := client.GetForecast(context.Background(), "IN-SO", 24*time.Hour)
	if err == nil {
		t.Fatal("expected error for empty forecast")
	}
}

func TestGetForecastInvalidIntensity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		future := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
			"zone":"IN-SO",
			"forecast":[{"datetime":%q,"carbonIntensity":-10}]
		}`, future)
	}))
	defer server.Close()

	client := newTestClient(t, server)

	_, err := client.GetForecast(context.Background(), "IN-SO", 24*time.Hour)
	if err == nil {
		t.Fatal("expected invalid intensity error")
	}
}

func TestGetForecastUnorderedTimestamps(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		first := time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339)
		second := time.Now().UTC().Add(1 * time.Hour).Format(time.RFC3339)

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
			"zone":"IN-SO",
			"forecast":[
				{"datetime":%q,"carbonIntensity":420},
				{"datetime":%q,"carbonIntensity":450}
			]
		}`, first, second)
	}))
	defer server.Close()

	client := newTestClient(t, server)

	_, err := client.GetForecast(context.Background(), "IN-SO", 24*time.Hour)
	if err == nil {
		t.Fatal("expected error for unordered timestamps")
	}
}

func TestGetForecastWrongRegion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"zone":"IN-WE",
			"forecast":[
				{"datetime":"2026-11-05T10:00:00Z","carbonIntensity":420}
			]
		}`))
	}))
	defer server.Close()

	client := newTestClient(t, server)

	_, err := client.GetForecast(context.Background(), "IN-SO", 24*time.Hour)
	if err == nil {
		t.Fatal("expected region mismatch error")
	}
}
