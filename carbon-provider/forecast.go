package carbon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type forecastEntry struct {
	DateTime        *time.Time `json:"datetime"`
	CarbonIntensity *float64   `json:"carbonIntensity"`
}

type forecastResponse struct {
	Zone     string          `json:"zone"`
	Forecast []forecastEntry `json:"forecast"`
}

func (c *Client) GetForecast(
	ctx context.Context,
	region string,
	horizon time.Duration,
) ([]Signal, error) {
	if c == nil {
		return nil, fmt.Errorf("carbon client is nil")
	}
	if ctx == nil {
		return nil, fmt.Errorf("request context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	zone := strings.TrimSpace(region)
	if zone == "" {
		zone = c.config.Zone
	}
	if zone != c.config.Zone {
		return nil, fmt.Errorf(
			"requested region %s does not match configured zone %s",
			zone, c.config.Zone,
		)
	}

	hours := int(horizon / time.Hour)
	if horizon <= 0 || horizon%time.Hour != 0 {
		return nil, fmt.Errorf("forecast horizon must be a whole number of hours")
	}

	switch hours {
	case 6, 24, 48, 72:
	default:
		return nil, fmt.Errorf("forecast horizon must be 6, 24, 48, or 72 hours")
	}

	params := url.Values{}
	params.Set("zone", zone)
	params.Set("horizonHours", strconv.Itoa(hours))

	endpoint := "/v4/carbon-intensity/forecast?" + params.Encode()

	req, err := c.newRequest(ctx, endpoint)
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request carbon forecast: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf(
			"Electricity Maps returned HTTP %d: %s",
			resp.StatusCode, strings.TrimSpace(string(body)),
		)
	}

	var data forecastResponse
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
	collectedAt := c.now()
	var previousTimestamp time.Time

	for i, entry := range data.Forecast {
		if entry.DateTime == nil || entry.DateTime.IsZero() {
			return nil, fmt.Errorf("forecast entry %d has invalid timestamp", i)
		}
		if entry.CarbonIntensity == nil {
			return nil, fmt.Errorf("forecast entry %d is missing carbon intensity", i)
		}
		if !validIntensity(*entry.CarbonIntensity) {
			return nil, fmt.Errorf("forecast entry %d has invalid intensity", i)
		}

		timestamp := entry.DateTime.UTC()
		if i > 0 && !timestamp.After(previousTimestamp) {
			return nil, fmt.Errorf(
				"forecast entry %d has duplicate or out-of-order timestamp", i,
			)
		}
		previousTimestamp = timestamp

		signals = append(signals, Signal{
			SignalType:  "carbon",
			Provider:    ProviderName,
			Region:      data.Zone,
			Value:       *entry.CarbonIntensity,
			Unit:        CarbonUnit,
			Timestamp:   timestamp,
			CollectedAt: collectedAt,
			ValidUntil:  timestamp.Add(time.Hour),
			IsForecast:  true,
			Quality:     "FRESH",
		})
	}

	return signals, nil
}
