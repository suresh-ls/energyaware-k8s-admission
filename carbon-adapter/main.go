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

// Electricity Maps API response.
type CarbonResponse struct {
	Zone                string     `json:"zone"`
	CarbonIntensity     *float64   `json:"carbonIntensity"`
	DateTime            *time.Time `json:"datetime"`
	TemporalGranularity string     `json:"temporalGranularity"`
	EmissionFactorType  string     `json:"emissionFactorType"`
}

// Common signal format used by EnergyGate.
type Signal struct {
	SignalType string    `json:"signal_type"`
	Provider   string    `json:"provider"`
	Region     string    `json:"region"`
	Value      float64   `json:"value"`
	Unit       string    `json:"unit"`
	Timestamp  time.Time `json:"timestamp"`
	ValidUntil time.Time `json:"valid_until"`
	IsForecast bool      `json:"is_forecast"`
	Quality    string    `json:"quality"`
}

func fetchCarbonIntensity(client *http.Client, baseURL string) (*CarbonResponse, error) {
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

	apiURL := baseURL + "/v4/carbon-intensity/latest?" + params.Encode()

	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("auth-token", token)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request Electricity Maps: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf(
			"Electricity Maps returned HTTP %d: %s",
			resp.StatusCode, string(body),
		)
	}

	var data CarbonResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode carbon response: %w", err)
	}

	if data.Zone == "" || data.CarbonIntensity == nil ||
		data.DateTime == nil {
		return nil, fmt.Errorf("carbon response is missing required fields")
	}

	if math.IsNaN(*data.CarbonIntensity) ||
		math.IsInf(*data.CarbonIntensity, 0) ||
		*data.CarbonIntensity < 0 {
		return nil, fmt.Errorf("invalid carbon intensity value")
	}

	if data.DateTime.IsZero() {
		return nil, fmt.Errorf("invalid carbon intensity timestamp")
	}

	if data.TemporalGranularity == "" {
		return nil, fmt.Errorf("missing temporal granularity")
	}

	return &data, nil
}

func buildSignal(data *CarbonResponse) Signal {
	timestamp := data.DateTime.UTC()
	validUntil := timestamp.Add(time.Hour)

	quality := "FRESH"
	if time.Now().UTC().After(validUntil) {
		quality = "STALE"
	}

	return Signal{
		SignalType: "carbon",
		Provider:   "electricity_maps",
		Region:     data.Zone,
		Value:      *data.CarbonIntensity,
		Unit:       "gCO2eq/kWh",
		Timestamp:  timestamp,
		ValidUntil: validUntil,
		IsForecast: false,
		Quality:    quality,
	}
}

func main() {
	client := &http.Client{Timeout: 15 * time.Second}
	data, err := fetchCarbonIntensity(client, "https://api.electricitymaps.com")
	if err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}

	signal := buildSignal(data)

	output, err := json.MarshalIndent(signal, "", "  ")
	if err != nil {
		fmt.Println("Error formatting signal:", err)
		os.Exit(1)
	}

	fmt.Println(string(output))
}
