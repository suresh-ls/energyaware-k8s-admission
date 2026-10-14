package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func setTestAPIKey(t *testing.T, key string) {
	t.Helper()

	oldKey, existed := os.LookupEnv("ENERGYMAP_API_KEY")
	t.Cleanup(func() {
		if existed {
			_ = os.Setenv("ENERGYMAP_API_KEY", oldKey)
		} else {
			_ = os.Unsetenv("ENERGYMAP_API_KEY")
		}
	})

	if key == "" {
		_ = os.Unsetenv("ENERGYMAP_API_KEY")
	} else {
		_ = os.Setenv("ENERGYMAP_API_KEY", key)
	}
}

func TestFetchCostSignalSuccess(t *testing.T) {
	setTestAPIKey(t, "test-api-key")

	timestamp := time.Now().UTC().Truncate(time.Second)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/developer/v1/market/iex/latest" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("limit") != "1" {
			t.Errorf("unexpected limit: %s", r.URL.Query().Get("limit"))
		}
		if r.URL.Query().Get("market_type") != "DAM" {
			t.Errorf("unexpected market_type: %s", r.URL.Query().Get("market_type"))
		}
		if r.Header.Get("X-API-Key") != "test-api-key" {
			t.Errorf("API key header was not set correctly")
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []map[string]interface{}{
				{
					"timestamp":   timestamp.Format(time.RFC3339),
					"market_type": "DAM",
					"region":      "India",
					"mcp_rs_mwh":  "5000",
					"source":      "IEX",
				},
			},
			"meta": map[string]interface{}{
				"freshness_lag_minutes": 5,
			},
		})
	}))
	defer server.Close()

	signal, err := fetchCostSignal(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatalf("fetchCostSignal returned an unexpected error: %v", err)
	}

	if signal.Value != 5 {
		t.Errorf("expected 5000 INR/MWh to convert to 5 INR/kWh, got %v", signal.Value)
	}
	if signal.Unit != "INR/kWh" {
		t.Errorf("expected unit INR/kWh, got %q", signal.Unit)
	}
	if signal.Region != "India" {
		t.Errorf("expected region India, got %q", signal.Region)
	}
	if signal.IsForecast {
		t.Error("expected IsForecast to be false for a reported market price")
	}
	if signal.Quality != "reported" {
		t.Errorf("expected quality reported, got %q", signal.Quality)
	}
	if !signal.Timestamp.Equal(timestamp) {
		t.Errorf("unexpected timestamp: got %v, want %v", signal.Timestamp, timestamp)
	}
	if !signal.ValidUntil.Equal(timestamp.Add(15 * time.Minute)) {
		t.Errorf("unexpected ValidUntil: got %v", signal.ValidUntil)
	}
}

func TestFetchCostSignalMissingAPIKey(t *testing.T) {
	setTestAPIKey(t, "")

	_, err := fetchCostSignal(context.Background(), http.DefaultClient, "http://localhost")
	if err == nil || !strings.Contains(err.Error(), "ENERGYMAP_API_KEY is not set") {
		t.Fatalf("expected missing API key error, got %v", err)
	}
}

func TestFetchCostSignalHTTPError(t *testing.T) {
	setTestAPIKey(t, "test-api-key")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	_, err := fetchCostSignal(context.Background(), server.Client(), server.URL)
	if err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("expected HTTP 503 error, got %v", err)
	}
}

func TestFetchCostSignalMalformedJSON(t *testing.T) {
	setTestAPIKey(t, "test-api-key")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{invalid json"))
	}))
	defer server.Close()

	_, err := fetchCostSignal(context.Background(), server.Client(), server.URL)
	if err == nil || !strings.Contains(err.Error(), "decode API response") {
		t.Fatalf("expected JSON decoding error, got %v", err)
	}
}

func TestFetchCostSignalEmptyRecords(t *testing.T) {
	setTestAPIKey(t, "test-api-key")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[],"meta":{"freshness_lag_minutes":0}}`))
	}))
	defer server.Close()

	_, err := fetchCostSignal(context.Background(), server.Client(), server.URL)
	if err == nil || !strings.Contains(err.Error(), "no price records") {
		t.Fatalf("expected empty-records error, got %v", err)
	}
}

func TestFetchCostSignalInvalidPrice(t *testing.T) {
	setTestAPIKey(t, "test-api-key")

	timestamp := time.Now().UTC().Add(-time.Minute).Truncate(time.Second).Format(time.RFC3339)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		response := `{
			"data":[{
				"timestamp":"` + timestamp + `",
				"market_type":"DAM",
				"region":"India",
				"mcp_rs_mwh":"not-a-number",
				"source":"IEX"
			}],
			"meta":{"freshness_lag_minutes":0}
		}`
		_, _ = w.Write([]byte(response))
	}))
	defer server.Close()

	_, err := fetchCostSignal(context.Background(), server.Client(), server.URL)
	if err == nil || !strings.Contains(err.Error(), "invalid electricity price") {
		t.Fatalf("expected invalid-price error, got %v", err)
	}
}

func TestFetchCostSignalStaleQuality(t *testing.T) {
	setTestAPIKey(t, "test-api-key")

	timestamp := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []map[string]interface{}{
				{
					"timestamp":   timestamp.Format(time.RFC3339),
					"market_type": "DAM",
					"region":      "India",
					"mcp_rs_mwh":  "5000",
					"source":      "IEX",
				},
			},
			"meta": map[string]interface{}{
				"freshness_lag_minutes": 20,
			},
		})
	}))
	defer server.Close()

	signal, err := fetchCostSignal(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatalf("fetchCostSignal returned an unexpected error: %v", err)
	}
	if signal.Quality != "possibly_stale" {
		t.Errorf("expected possibly_stale quality, got %q", signal.Quality)
	}
}
