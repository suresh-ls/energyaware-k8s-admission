package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func setupCurrentEnv(t *testing.T) {
	t.Helper()
	t.Setenv("ELECTRICITY_MAPS_API_TOKEN", "test-token")
	t.Setenv("ELECTRICITY_MAPS_ZONE", "IN-SO")
}

func TestFetchCarbonIntensitySuccess(t *testing.T) {
	setupCurrentEnv(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v4/carbon-intensity/latest" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("zone") != "IN-SO" {
			t.Errorf("unexpected zone: %s", r.URL.Query().Get("zone"))
		}
		if r.Header.Get("auth-token") != "test-token" {
			t.Error("incorrect auth-token")
		}

		fmt.Fprint(w, `{
			"zone":"IN-SO",
			"carbonIntensity":466,
			"datetime":"2026-10-14T03:00:00Z",
			"temporalGranularity":"hourly"
		}`)
	}))
	defer server.Close()

	data, err := fetchCarbonIntensity(server.Client(), server.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if data.Zone != "IN-SO" || *data.CarbonIntensity != 466 {
		t.Fatalf("unexpected response: %+v", data)
	}
}

func TestFetchCarbonIntensityHTTPError(t *testing.T) {
	setupCurrentEnv(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()

	_, err := fetchCarbonIntensity(server.Client(), server.URL)
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected HTTP 401 error, got %v", err)
	}
}

func TestFetchCarbonIntensityMissingToken(t *testing.T) {
	t.Setenv("ELECTRICITY_MAPS_API_TOKEN", "")
	t.Setenv("ELECTRICITY_MAPS_ZONE", "IN-SO")

	_, err := fetchCarbonIntensity(http.DefaultClient, "http://localhost")
	if err == nil {
		t.Fatal("expected missing token error")
	}
}

func TestFetchCarbonIntensityDefaultZone(t *testing.T) {
	t.Setenv("ELECTRICITY_MAPS_API_TOKEN", "test-token")
	t.Setenv("ELECTRICITY_MAPS_ZONE", "")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("zone") != defaultZone {
			t.Errorf("expected default zone %s", defaultZone)
		}
		fmt.Fprint(w, `{
			"zone":"IN-SO",
			"carbonIntensity":400,
			"datetime":"2026-10-14T03:00:00Z",
			"temporalGranularity":"hourly"
		}`)
	}))
	defer server.Close()

	_, err := fetchCarbonIntensity(server.Client(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
}

func TestFetchCarbonIntensityMissingValue(t *testing.T) {
	setupCurrentEnv(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{
			"zone":"IN-SO",
			"datetime":"2026-10-14T03:00:00Z",
			"temporalGranularity":"hourly"
		}`)
	}))
	defer server.Close()

	_, err := fetchCarbonIntensity(server.Client(), server.URL)
	if err == nil {
		t.Fatal("expected missing intensity error")
	}
}

func TestFetchCarbonIntensityNegativeValue(t *testing.T) {
	setupCurrentEnv(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{
			"zone":"IN-SO",
			"carbonIntensity":-10,
			"datetime":"2026-10-14T03:00:00Z",
			"temporalGranularity":"hourly"
		}`)
	}))
	defer server.Close()

	_, err := fetchCarbonIntensity(server.Client(), server.URL)
	if err == nil {
		t.Fatal("expected negative intensity error")
	}
}

func TestFetchCarbonIntensityInvalidJSON(t *testing.T) {
	setupCurrentEnv(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `not valid json`)
	}))
	defer server.Close()

	_, err := fetchCarbonIntensity(server.Client(), server.URL)
	if err == nil {
		t.Fatal("expected JSON decoding error")
	}
}

func TestFetchCarbonIntensityMissingTimestamp(t *testing.T) {
	setupCurrentEnv(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{
			"zone":"IN-SO",
			"carbonIntensity":400,
			"temporalGranularity":"hourly"
		}`)
	}))
	defer server.Close()

	_, err := fetchCarbonIntensity(server.Client(), server.URL)
	if err == nil {
		t.Fatal("expected missing timestamp error")
	}
}

func TestFetchCarbonIntensityMissingGranularity(t *testing.T) {
	setupCurrentEnv(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{
			"zone":"IN-SO",
			"carbonIntensity":400,
			"datetime":"2026-10-14T03:00:00Z"
		}`)
	}))
	defer server.Close()

	_, err := fetchCarbonIntensity(server.Client(), server.URL)
	if err == nil {
		t.Fatal("expected missing granularity error")
	}
}

func TestFetchCarbonIntensityWrongZone(t *testing.T) {
	setupCurrentEnv(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{
			"zone":"IN-NO",
			"carbonIntensity":400,
			"datetime":"2026-10-14T03:00:00Z",
			"temporalGranularity":"hourly"
		}`)
	}))
	defer server.Close()

	_, err := fetchCarbonIntensity(server.Client(), server.URL)
	if err == nil {
		t.Fatal("expected zone mismatch error")
	}
}

func TestFetchCarbonIntensityNilClient(t *testing.T) {
	setupCurrentEnv(t)

	_, err := fetchCarbonIntensity(nil, "http://localhost")
	if err == nil {
		t.Fatal("expected nil client error")
	}
}

func TestValidIntensity(t *testing.T) {
	tests := []struct {
		name  string
		value float64
		valid bool
	}{
		{"positive", 466, true},
		{"zero", 0, true},
		{"negative", -1, false},
		{"NaN", math.NaN(), false},
		{"positive infinity", math.Inf(1), false},
		{"negative infinity", math.Inf(-1), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validIntensity(tt.value); got != tt.valid {
				t.Errorf("validIntensity(%v) = %v, want %v",
					tt.value, got, tt.valid)
			}
		})
	}
}

func TestBuildSignal(t *testing.T) {
	value := 466.0
	timestamp := time.Now().UTC()

	data := &CarbonResponse{
		Zone:                "IN-SO",
		CarbonIntensity:     &value,
		DateTime:            &timestamp,
		TemporalGranularity: "hourly",
	}

	signal := buildSignal(data)

	if signal.SignalType != "carbon" ||
		signal.Provider != "electricity_maps" ||
		signal.Region != "IN-SO" ||
		signal.Unit != "gCO2eq/kWh" ||
		signal.IsForecast {
		t.Fatalf("unexpected signal: %+v", signal)
	}

	if !signal.ValidUntil.Equal(timestamp.Add(time.Hour)) {
		t.Error("incorrect validity period")
	}

	if signal.Quality != "FRESH" {
		t.Errorf("expected FRESH, got %s", signal.Quality)
	}
}

func TestBuildSignalStale(t *testing.T) {
	value := 466.0
	timestamp := time.Now().UTC().Add(-2 * time.Hour)

	data := &CarbonResponse{
		Zone:                "IN-SO",
		CarbonIntensity:     &value,
		DateTime:            &timestamp,
		TemporalGranularity: "hourly",
	}

	signal := buildSignal(data)

	if signal.Quality != "STALE" {
		t.Errorf("expected STALE, got %s", signal.Quality)
	}
}

func TestSignalJSONFormat(t *testing.T) {
	signal := Signal{
		SignalType: "carbon",
		Provider:   providerName,
		Region:     "IN-SO",
		Value:      466,
		Unit:       carbonUnit,
		Timestamp:  time.Now().UTC(),
		ValidUntil: time.Now().UTC().Add(time.Hour),
		IsForecast: false,
		Quality:    "FRESH",
	}

	output, err := json.Marshal(signal)
	if err != nil {
		t.Fatal(err)
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(output, &decoded); err != nil {
		t.Fatal(err)
	}

	fields := []string{
		"signal_type", "provider", "region", "value",
		"unit", "timestamp", "valid_until",
		"is_forecast", "quality",
	}

	for _, field := range fields {
		if _, exists := decoded[field]; !exists {
			t.Errorf("missing JSON field: %s", field)
		}
	}
}
