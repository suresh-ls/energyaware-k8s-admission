package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	providerName = "electricity_maps"
	signalType   = "carbon"
	carbonUnit   = "gCO2eq/kWh"
	defaultZone  = "IN-SO"
)

type CarbonResponse struct {
	Zone                string     `json:"zone"`
	CarbonIntensity     *float64   `json:"carbonIntensity"`
	DateTime            *time.Time `json:"datetime"`
	TemporalGranularity string     `json:"temporalGranularity"`
	EmissionFactorType  string     `json:"emissionFactorType"`
}

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

func getAPIConfig() (string, string, error) {
	token := strings.TrimSpace(os.Getenv("ELECTRICITY_MAPS_API_TOKEN"))
	if token == "" {
		return "", "", fmt.Errorf("ELECTRICITY_MAPS_API_TOKEN is not set")
	}

	zone := strings.TrimSpace(os.Getenv("ELECTRICITY_MAPS_ZONE"))
	if zone == "" {
		zone = defaultZone
	}

	return token, zone, nil
}

func buildAPIURL(baseURL, endpoint string, params url.Values) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("invalid base URL: %w", err)
	}

	parsed.Path = strings.TrimRight(parsed.Path, "/") + endpoint
	parsed.RawQuery = params.Encode()

	return parsed.String(), nil
}

func validIntensity(value float64) bool {
	return !math.IsNaN(value) &&
		!math.IsInf(value, 0) &&
		value >= 0
}

func fetchCarbonIntensity(client *http.Client, baseURL string) (*CarbonResponse, error) {
	if client == nil {
		return nil, fmt.Errorf("HTTP client cannot be nil")
	}

	token, zone, err := getAPIConfig()
	if err != nil {
		return nil, err
	}

	params := url.Values{}
	params.Set("zone", zone)

	apiURL, err := buildAPIURL(
		baseURL,
		"/v4/carbon-intensity/latest",
		params,
	)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create carbon request: %w", err)
	}
	req.Header.Set("auth-token", token)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request Electricity Maps: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf(
			"Electricity Maps returned HTTP %d: %s",
			resp.StatusCode, strings.TrimSpace(string(body)),
		)
	}

	var data CarbonResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode carbon response: %w", err)
	}

	if data.Zone == "" || data.CarbonIntensity == nil || data.DateTime == nil {
		return nil, fmt.Errorf("carbon response is missing required fields")
	}

	if data.Zone != zone {
		return nil, fmt.Errorf(
			"requested zone %s but received zone %s",
			zone, data.Zone,
		)
	}

	if !validIntensity(*data.CarbonIntensity) {
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

	validity := time.Hour
	if strings.EqualFold(data.TemporalGranularity, "5_minutes") {
		validity = 5 * time.Minute
	} else if strings.EqualFold(data.TemporalGranularity, "15_minutes") {
		validity = 15 * time.Minute
	}

	validUntil := timestamp.Add(validity)

	quality := "FRESH"
	if time.Now().UTC().After(validUntil) {
		quality = "STALE"
	}

	return Signal{
		SignalType: signalType,
		Provider:   providerName,
		Region:     data.Zone,
		Value:      *data.CarbonIntensity,
		Unit:       carbonUnit,
		Timestamp:  timestamp,
		ValidUntil: validUntil,
		IsForecast: false,
		Quality:    quality,
	}
}

func main() {
	client := &http.Client{Timeout: 15 * time.Second}

	data, err := fetchCarbonIntensity(
		client,
		"https://api.electricitymaps.com",
	)
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
