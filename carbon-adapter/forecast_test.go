package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func setupForecastEnv(t *testing.T) {
	t.Helper()
	t.Setenv("ELECTRICITY_MAPS_API_TOKEN", "test-token")
	t.Setenv("ELECTRICITY_MAPS_ZONE", "IN-SO")
	t.Setenv("ELECTRICITY_MAPS_FORECAST_HOURS", "24")
}

func TestFetchCarbonForecastSuccess(t *testing.T) {
	setupForecastEnv(t)

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
			t.Error("incorrect auth-token")
		}

		fmt.Fprint(w, `{
			"zone":"IN-SO",
			"forecast":[
				{
					"datetime":"2026-10-21T12:00:00Z",
					"carbonIntensity":450
				},
				{
					"datetime":"2026-10-21T13:00:00Z",
					"carbonIntensity":430
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
			signal.Provider != providerName ||
			signal.Region != "IN-SO" ||
			signal.Unit != carbonUnit ||
			!signal.IsForecast {
			t.Errorf("unexpected signal: %+v", signal)
		}

		if signal.Quality != "FRESH" {
			t.Errorf("expected FRESH, got %s", signal.Quality)
		}

		if !signal.ValidUntil.Equal(signal.Timestamp.Add(time.Hour)) {
			t.Error("incorrect forecast validity")
		}
	}

	if signals[0].Value != 450 || signals[1].Value != 430 {
		t.Errorf("unexpected values: %v, %v",
			signals[0].Value, signals[1].Value)
	}
}

func TestFetchCarbonForecastHTTPError(t *testing.T) {
	setupForecastEnv(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "API unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	_, err := fetchCarbonForecast(server.Client(), server.URL)
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("expected HTTP 503 error, got %v", err)
	}
}

func TestFetchCarbonForecastMissingToken(t *testing.T) {
	setupForecastEnv(t)
	t.Setenv("ELECTRICITY_MAPS_API_TOKEN", "")

	_, err := fetchCarbonForecast(http.DefaultClient, "http://localhost")
	if err == nil {
		t.Fatal("expected missing token error")
	}
}

func TestFetchCarbonForecastInvalidJSON(t *testing.T) {
	setupForecastEnv(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `not json`)
	}))
	defer server.Close()

	_, err := fetchCarbonForecast(server.Client(), server.URL)
	if err == nil {
		t.Fatal("expected JSON decoding error")
	}
}

func TestFetchCarbonForecastMissingZone(t *testing.T) {
	setupForecastEnv(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"forecast":[]}`)
	}))
	defer server.Close()

	_, err := fetchCarbonForecast(server.Client(), server.URL)
	if err == nil {
		t.Fatal("expected missing zone error")
	}
}

func TestFetchCarbonForecastWrongZone(t *testing.T) {
	setupForecastEnv(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{
			"zone":"IN-NO",
			"forecast":[{
				"datetime":"2026-10-21T12:00:00Z",
				"carbonIntensity":450
			}]
		}`)
	}))
	defer server.Close()

	_, err := fetchCarbonForecast(server.Client(), server.URL)
	if err == nil {
		t.Fatal("expected zone mismatch error")
	}
}

func TestFetchCarbonForecastEmpty(t *testing.T) {
	setupForecastEnv(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"zone":"IN-SO","forecast":[]}`)
	}))
	defer server.Close()

	_, err := fetchCarbonForecast(server.Client(), server.URL)
	if err == nil {
		t.Fatal("expected empty forecast error")
	}
}

func TestFetchCarbonForecastMissingIntensity(t *testing.T) {
	setupForecastEnv(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{
			"zone":"IN-SO",
			"forecast":[{
				"datetime":"2026-10-21T12:00:00Z"
			}]
		}`)
	}))
	defer server.Close()

	_, err := fetchCarbonForecast(server.Client(), server.URL)
	if err == nil {
		t.Fatal("expected missing intensity error")
	}
}

func TestFetchCarbonForecastNegativeIntensity(t *testing.T) {
	setupForecastEnv(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{
			"zone":"IN-SO",
			"forecast":[{
				"datetime":"2026-10-21T12:00:00Z",
				"carbonIntensity":-10
			}]
		}`)
	}))
	defer server.Close()

	_, err := fetchCarbonForecast(server.Client(), server.URL)
	if err == nil {
		t.Fatal("expected negative intensity error")
	}
}

func TestFetchCarbonForecastMissingTimestamp(t *testing.T) {
	setupForecastEnv(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{
			"zone":"IN-SO",
			"forecast":[{
				"carbonIntensity":450
			}]
		}`)
	}))
	defer server.Close()

	_, err := fetchCarbonForecast(server.Client(), server.URL)
	if err == nil {
		t.Fatal("expected missing timestamp error")
	}
}

func TestFetchCarbonForecastDuplicateTimestamp(t *testing.T) {
	setupForecastEnv(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{
			"zone":"IN-SO",
			"forecast":[
				{"datetime":"2026-10-21T12:00:00Z","carbonIntensity":450},
				{"datetime":"2026-10-21T12:00:00Z","carbonIntensity":430}
			]
		}`)
	}))
	defer server.Close()

	_, err := fetchCarbonForecast(server.Client(), server.URL)
	if err == nil {
		t.Fatal("expected duplicate timestamp error")
	}
}

func TestFetchCarbonForecastOutOfOrder(t *testing.T) {
	setupForecastEnv(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{
			"zone":"IN-SO",
			"forecast":[
				{"datetime":"2026-10-21T13:00:00Z","carbonIntensity":450},
				{"datetime":"2026-10-21T12:00:00Z","carbonIntensity":430}
			]
		}`)
	}))
	defer server.Close()

	_, err := fetchCarbonForecast(server.Client(), server.URL)
	if err == nil {
		t.Fatal("expected out-of-order timestamp error")
	}
}

func TestFetchCarbonForecastInvalidHorizon(t *testing.T) {
	setupForecastEnv(t)
	t.Setenv("ELECTRICITY_MAPS_FORECAST_HOURS", "12")

	_, err := fetchCarbonForecast(http.DefaultClient, "http://localhost")
	if err == nil {
		t.Fatal("expected invalid horizon error")
	}
}

func TestFetchCarbonForecastNilClient(t *testing.T) {
	setupForecastEnv(t)

	_, err := fetchCarbonForecast(nil, "http://localhost")
	if err == nil {
		t.Fatal("expected nil client error")
	}
}

func TestGetForecastHorizonDefault(t *testing.T) {
	t.Setenv("ELECTRICITY_MAPS_FORECAST_HOURS", "")

	hours, err := getForecastHorizon()
	if err != nil {
		t.Fatal(err)
	}
	if hours != 24 {
		t.Errorf("expected default 24, got %d", hours)
	}
}

func TestGetForecastHorizonValidValues(t *testing.T) {
	for _, value := range []string{"6", "24", "48", "72"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("ELECTRICITY_MAPS_FORECAST_HOURS", value)

			hours, err := getForecastHorizon()
			if err != nil {
				t.Fatal(err)
			}
			if hours == 0 {
				t.Error("expected positive horizon")
			}
		})
	}
}
