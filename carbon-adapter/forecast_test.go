package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFetchCarbonForecastSuccess(t *testing.T) {
	t.Setenv("ELECTRICITY_MAPS_API_TOKEN", "test-token")
	t.Setenv("ELECTRICITY_MAPS_ZONE", "IN-SO")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v4/carbon-intensity/forecast" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("zone") != "IN-SO" {
			t.Errorf("unexpected zone: %s", r.URL.Query().Get("zone"))
		}
		if r.Header.Get("auth-token") != "test-token" {
			t.Error("missing API token header")
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"zone": "IN-SO",
			"forecast": [
				{
					"datetime": "2026-10-21T12:00:00Z",
					"carbonIntensity": 450,
					"emissionFactorType": "lifecycle"
				},
				{
					"datetime": "2026-10-21T13:00:00Z",
					"carbonIntensity": 430,
					"emissionFactorType": "lifecycle"
				}
			]
		}`)
	}))
	defer server.Close()

	signals, err := fetchCarbonForecast(server.Client(), server.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(signals) != 2 {
		t.Fatalf("expected 2 signals, got %d", len(signals))
	}

	for _, signal := range signals {
		if signal.SignalType != "carbon" ||
			signal.Provider != "electricity_maps" ||
			signal.Region != "IN-SO" ||
			signal.Unit != "gCO2eq/kWh" ||
			!signal.IsForecast {
			t.Errorf("unexpected signal fields: %+v", signal)
		}
		if signal.Quality != "FRESH" {
			t.Errorf("expected FRESH quality, got %s", signal.Quality)
		}
		if signal.Timestamp.IsZero() || !signal.ValidUntil.Equal(signal.Timestamp.Add(time.Hour)) {
			t.Errorf("invalid timestamp or validity: %+v", signal)
		}
	}

	if signals[0].Value != 450 || signals[1].Value != 430 {
		t.Errorf("unexpected forecast values: %v, %v", signals[0].Value, signals[1].Value)
	}
}

func TestFetchCarbonForecastHTTPError(t *testing.T) {
	t.Setenv("ELECTRICITY_MAPS_API_TOKEN", "test-token")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "API unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	_, err := fetchCarbonForecast(server.Client(), server.URL)
	if err == nil {
		t.Fatal("expected HTTP error")
	}
}

func TestFetchCarbonForecastMissingToken(t *testing.T) {
	t.Setenv("ELECTRICITY_MAPS_API_TOKEN", "")

	_, err := fetchCarbonForecast(http.DefaultClient, "http://localhost")
	if err == nil || !strings.Contains(err.Error(), "API_TOKEN") {
		t.Fatalf("expected missing-token error, got %v", err)
	}
}

func TestFetchCarbonForecastInvalidJSON(t *testing.T) {
	t.Setenv("ELECTRICITY_MAPS_API_TOKEN", "test-token")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `not json`)
	}))
	defer server.Close()

	_, err := fetchCarbonForecast(server.Client(), server.URL)
	if err == nil {
		t.Fatal("expected JSON decoding error")
	}
}

func TestFetchCarbonForecastMissingIntensity(t *testing.T) {
	t.Setenv("ELECTRICITY_MAPS_API_TOKEN", "test-token")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{
			"zone": "IN-SO",
			"forecast": [{
				"datetime": "2026-10-21T12:00:00Z"
			}]
		}`)
	}))
	defer server.Close()

	_, err := fetchCarbonForecast(server.Client(), server.URL)
	if err == nil {
		t.Fatal("expected missing-intensity error")
	}
}

func TestFetchCarbonForecastNegativeIntensity(t *testing.T) {
	t.Setenv("ELECTRICITY_MAPS_API_TOKEN", "test-token")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{
			"zone": "IN-SO",
			"forecast": [{
				"datetime": "2026-10-21T12:00:00Z",
				"carbonIntensity": -10
			}]
		}`)
	}))
	defer server.Close()

	_, err := fetchCarbonForecast(server.Client(), server.URL)
	if err == nil {
		t.Fatal("expected negative-intensity error")
	}
}

func TestFetchCarbonForecastMissingTimestamp(t *testing.T) {
	t.Setenv("ELECTRICITY_MAPS_API_TOKEN", "test-token")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{
			"zone": "IN-SO",
			"forecast": [{
				"carbonIntensity": 450
			}]
		}`)
	}))
	defer server.Close()

	_, err := fetchCarbonForecast(server.Client(), server.URL)
	if err == nil {
		t.Fatal("expected missing-timestamp error")
	}
}
