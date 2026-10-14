package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFetchCarbonIntensitySuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v4/carbon-intensity/latest" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("zone") != "IN-SO" {
			t.Errorf("unexpected zone: %s", r.URL.Query().Get("zone"))
		}
		if r.Header.Get("auth-token") != "test-token" {
			t.Error("missing or incorrect auth-token header")
		}

		fmt.Fprint(w, `{
			"zone":"IN-SO",
			"carbonIntensity":466,
			"datetime":"2026-10-14T03:00:00Z",
			"temporalGranularity":"hourly",
			"emissionFactorType":"lifecycle"
		}`)
	}))
	defer server.Close()

	t.Setenv("ELECTRICITY_MAPS_API_TOKEN", "test-token")
	t.Setenv("ELECTRICITY_MAPS_ZONE", "IN-SO")

	client := &http.Client{Timeout: 2 * time.Second}
	data, err := fetchCarbonIntensity(client, server.URL)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}

	if data.Zone != "IN-SO" {
		t.Errorf("expected zone IN-SO, got %s", data.Zone)
	}
	if data.CarbonIntensity == nil || *data.CarbonIntensity != 466 {
		t.Errorf("expected intensity 466, got %v", data.CarbonIntensity)
	}
}

func TestFetchCarbonIntensityHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()

	t.Setenv("ELECTRICITY_MAPS_API_TOKEN", "test-token")
	t.Setenv("ELECTRICITY_MAPS_ZONE", "IN-SO")

	client := &http.Client{Timeout: 2 * time.Second}
	_, err := fetchCarbonIntensity(client, server.URL)
	if err == nil {
		t.Fatal("expected an error for HTTP 401")
	}
}

func TestFetchCarbonIntensityMissingValue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{
			"zone":"IN-SO",
			"datetime":"2026-10-14T03:00:00Z",
			"temporalGranularity":"hourly"
		}`)
	}))
	defer server.Close()

	t.Setenv("ELECTRICITY_MAPS_API_TOKEN", "test-token")
	t.Setenv("ELECTRICITY_MAPS_ZONE", "IN-SO")

	client := &http.Client{Timeout: 2 * time.Second}
	_, err := fetchCarbonIntensity(client, server.URL)
	if err == nil {
		t.Fatal("expected an error when carbonIntensity is missing")
	}
}

func TestFetchCarbonIntensityNegativeValue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{
			"zone":"IN-SO",
			"carbonIntensity":-10,
			"datetime":"2026-10-14T03:00:00Z",
			"temporalGranularity":"hourly"
		}`)
	}))
	defer server.Close()

	t.Setenv("ELECTRICITY_MAPS_API_TOKEN", "test-token")
	t.Setenv("ELECTRICITY_MAPS_ZONE", "IN-SO")

	client := &http.Client{Timeout: 2 * time.Second}
	_, err := fetchCarbonIntensity(client, server.URL)
	if err == nil {
		t.Fatal("expected an error for negative carbon intensity")
	}
}

func TestFetchCarbonIntensityMissingTimestamp(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{
			"zone":"IN-SO",
			"carbonIntensity":466,
			"temporalGranularity":"hourly"
		}`)
	}))
	defer server.Close()

	t.Setenv("ELECTRICITY_MAPS_API_TOKEN", "test-token")
	t.Setenv("ELECTRICITY_MAPS_ZONE", "IN-SO")

	client := &http.Client{Timeout: 2 * time.Second}
	_, err := fetchCarbonIntensity(client, server.URL)
	if err == nil {
		t.Fatal("expected an error when timestamp is missing")
	}
}

func TestFetchCarbonIntensityInvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `not valid json`)
	}))
	defer server.Close()

	t.Setenv("ELECTRICITY_MAPS_API_TOKEN", "test-token")
	t.Setenv("ELECTRICITY_MAPS_ZONE", "IN-SO")

	client := &http.Client{Timeout: 2 * time.Second}
	_, err := fetchCarbonIntensity(client, server.URL)
	if err == nil {
		t.Fatal("expected an error for invalid JSON")
	}
}

func TestFetchCarbonIntensityMissingZone(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{
			"carbonIntensity":466,
			"datetime":"2026-10-14T03:00:00Z",
			"temporalGranularity":"hourly"
		}`)
	}))
	defer server.Close()

	t.Setenv("ELECTRICITY_MAPS_API_TOKEN", "test-token")
	t.Setenv("ELECTRICITY_MAPS_ZONE", "IN-SO")

	client := &http.Client{Timeout: 2 * time.Second}
	_, err := fetchCarbonIntensity(client, server.URL)
	if err == nil {
		t.Fatal("expected an error when zone is missing")
	}
}

func TestFetchCarbonIntensityMissingGranularity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{
			"zone":"IN-SO",
			"carbonIntensity":466,
			"datetime":"2026-10-14T03:00:00Z"
		}`)
	}))
	defer server.Close()

	t.Setenv("ELECTRICITY_MAPS_API_TOKEN", "test-token")
	t.Setenv("ELECTRICITY_MAPS_ZONE", "IN-SO")

	client := &http.Client{Timeout: 2 * time.Second}
	_, err := fetchCarbonIntensity(client, server.URL)
	if err == nil {
		t.Fatal("expected an error when temporal granularity is missing")
	}
}

func TestFetchCarbonIntensityZeroTimestamp(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{
			"zone":"IN-SO",
			"carbonIntensity":466,
			"datetime":"0001-01-01T00:00:00Z",
			"temporalGranularity":"hourly"
		}`)
	}))
	defer server.Close()

	t.Setenv("ELECTRICITY_MAPS_API_TOKEN", "test-token")
	t.Setenv("ELECTRICITY_MAPS_ZONE", "IN-SO")

	client := &http.Client{Timeout: 2 * time.Second}
	_, err := fetchCarbonIntensity(client, server.URL)
	if err == nil {
		t.Fatal("expected an error for zero timestamp")
	}
}

func TestFetchCarbonIntensityMissingToken(t *testing.T) {
	t.Setenv("ELECTRICITY_MAPS_API_TOKEN", "")
	t.Setenv("ELECTRICITY_MAPS_ZONE", "IN-SO")

	client := &http.Client{Timeout: 2 * time.Second}
	_, err := fetchCarbonIntensity(client, "http://127.0.0.1:1")
	if err == nil {
		t.Fatal("expected an error when API token is missing")
	}
}
