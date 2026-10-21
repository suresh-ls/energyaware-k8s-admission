package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"time"
)

type ForecastEntry struct {
	DateTime           *time.Time `json:"datetime"`
	CarbonIntensity    *float64   `json:"carbonIntensity"`
	EmissionFactorType string     `json:"emissionFactorType"`
}

type CarbonForecastResponse struct {
	Zone     string          `json:"zone"`
	Forecast []ForecastEntry `json:"forecast"`
}

func fetchCarbonForecast(client *http.Client, baseURL string) ([]Signal, error) {
	token := os.Getenv("ELECTRICITY_MAPS_API_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("ELECTRICITY_MAPS_API_TOKEN is not set")
	}

	zone := os.Getenv("ELECTRICITY_MAPS_ZONE")
	if zone == "" {
		zone = "IN-SO"
	}

	params := url.Values{}
	params.Set("zone", zone)

	horizon := os.Getenv("ELECTRICITY_MAPS_FORECAST_HOURS")
	if horizon == "" {
		horizon = "24"
	}

	if horizon != "6" && horizon != "24" &&
		horizon != "48" && horizon != "72" {
		return nil, fmt.Errorf(
			"ELECTRICITY_MAPS_FORECAST_HOURS must be 6, 24, 48, or 72",
		)
	}

	params.Set("horizonHours", horizon)

	apiURL := baseURL + "/v4/carbon-intensity/forecast?" + params.Encode()

	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("auth-token", token)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request Electricity Maps forecast: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf(
			"Electricity Maps returned HTTP %d: %s",
			resp.StatusCode, string(body),
		)
	}

	var data CarbonForecastResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode forecast response: %w", err)
	}

	if data.Zone == "" {
		return nil, fmt.Errorf("forecast response is missing zone")
	}
	if len(data.Forecast) == 0 {
		return nil, fmt.Errorf("forecast response contains no entries")
	}

	signals := make([]Signal, 0, len(data.Forecast))

	for i, entry := range data.Forecast {
		if entry.DateTime == nil || entry.DateTime.IsZero() {
			return nil, fmt.Errorf("forecast entry %d has invalid timestamp", i)
		}
		if entry.CarbonIntensity == nil {
			return nil, fmt.Errorf("forecast entry %d is missing carbon intensity", i)
		}

		value := *entry.CarbonIntensity
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return nil, fmt.Errorf("forecast entry %d has invalid carbon intensity", i)
		}

		timestamp := entry.DateTime.UTC()
		validUntil := timestamp.Add(time.Hour)

		signals = append(signals, Signal{
			SignalType: "carbon",
			Provider:   "electricity_maps",
			Region:     data.Zone,
			Value:      value,
			Unit:       "gCO2eq/kWh",
			Timestamp:  timestamp,
			ValidUntil: validUntil,
			IsForecast: true,
			Quality:    "FRESH",
		})
	}

	return signals, nil
}
