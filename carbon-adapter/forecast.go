package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
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

func getForecastHorizon() (int, error) {
	horizon := strings.TrimSpace(os.Getenv("ELECTRICITY_MAPS_FORECAST_HOURS"))

	if horizon == "" {
		return 24, nil
	}

	hours, err := strconv.Atoi(horizon)
	if err != nil {
		return 0, fmt.Errorf("invalid forecast horizon: %s", horizon)
	}

	switch hours {
	case 6, 24, 48, 72:
		return hours, nil
	default:
		return 0, fmt.Errorf(
			"ELECTRICITY_MAPS_FORECAST_HOURS must be 6, 24, 48, or 72",
		)
	}
}

func fetchCarbonForecast(client *http.Client, baseURL string) ([]Signal, error) {
	if client == nil {
		return nil, fmt.Errorf("HTTP client cannot be nil")
	}

	token, zone, err := getAPIConfig()
	if err != nil {
		return nil, err
	}

	horizon, err := getForecastHorizon()
	if err != nil {
		return nil, err
	}

	params := url.Values{}
	params.Set("zone", zone)
	params.Set("horizonHours", strconv.Itoa(horizon))

	apiURL, err := buildAPIURL(
		baseURL,
		"/v4/carbon-intensity/forecast",
		params,
	)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create forecast request: %w", err)
	}
	req.Header.Set("auth-token", token)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request Electricity Maps forecast: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf(
			"Electricity Maps returned HTTP %d: %s",
			resp.StatusCode, strings.TrimSpace(string(body)),
		)
	}

	var data CarbonForecastResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode forecast response: %w", err)
	}

	if data.Zone == "" {
		return nil, fmt.Errorf("forecast response is missing zone")
	}

	if data.Zone != zone {
		return nil, fmt.Errorf(
			"requested zone %s but received zone %s",
			zone, data.Zone,
		)
	}

	if len(data.Forecast) == 0 {
		return nil, fmt.Errorf("forecast response contains no entries")
	}

	signals := make([]Signal, 0, len(data.Forecast))
	var previousTimestamp time.Time

	for i, entry := range data.Forecast {
		if entry.DateTime == nil || entry.DateTime.IsZero() {
			return nil, fmt.Errorf(
				"forecast entry %d has invalid timestamp", i,
			)
		}

		if entry.CarbonIntensity == nil {
			return nil, fmt.Errorf(
				"forecast entry %d is missing carbon intensity", i,
			)
		}

		value := *entry.CarbonIntensity
		if !validIntensity(value) {
			return nil, fmt.Errorf(
				"forecast entry %d has invalid carbon intensity", i,
			)
		}

		timestamp := entry.DateTime.UTC()

		if i > 0 && !timestamp.After(previousTimestamp) {
			return nil, fmt.Errorf(
				"forecast entry %d has duplicate or out-of-order timestamp",
				i,
			)
		}

		previousTimestamp = timestamp
		validUntil := timestamp.Add(time.Hour)

		signals = append(signals, Signal{
			SignalType: signalType,
			Provider:   providerName,
			Region:     data.Zone,
			Value:      value,
			Unit:       carbonUnit,
			Timestamp:  timestamp,
			ValidUntil: validUntil,
			IsForecast: true,
			Quality:    "FRESH",
		})
	}

	return signals, nil
}
