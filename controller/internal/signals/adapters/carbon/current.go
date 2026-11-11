package carbon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/suresh-ls/energyaware-k8s-admission/controller/internal/signals"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type carbonResponse struct {
	Zone                string     `json:"zone"`
	CarbonIntensity     *float64   `json:"carbonIntensity"`
	DateTime            *time.Time `json:"datetime"`
	TemporalGranularity string     `json:"temporalGranularity"`
}

func (c *Client) GetCurrent(ctx context.Context, region string) (result signals.Signal, retErr error) {
	if c == nil {
		return signals.Signal{}, fmt.Errorf("carbon client is nil")
	}
	if ctx == nil {
		return signals.Signal{}, fmt.Errorf("request context is nil")
	}
	if err := ctx.Err(); err != nil {
		return signals.Signal{}, err
	}
	logger := log.FromContext(ctx).WithName("carbon")
	start := time.Now()

	defer func() {
		duration := time.Since(start).Milliseconds()

		if retErr != nil {
			logger.Error(retErr, "Failed to fetch current carbon signal",
				"zone", region,
				"provider", ProviderName,
				"durationMs", duration,
			)
			return
		}

		logger.V(1).Info("Fetched current carbon signal",
			"zone", result.Region,
			"provider", ProviderName,
			"intensity", result.Value,
			"quality", result.Quality,
			"durationMs", duration,
		)
	}()

	zone := strings.TrimSpace(region)
	if zone == "" {
		zone = c.config.Zone
	}
	if zone != c.config.Zone {
		return signals.Signal{}, fmt.Errorf(
			"requested region %s does not match configured zone %s",
			zone, c.config.Zone,
		)
	}

	endpoint := "/v4/carbon-intensity/latest?zone=" +
		url.QueryEscape(zone)

	req, err := c.newRequest(ctx, endpoint)
	if err != nil {
		return signals.Signal{}, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return signals.Signal{}, fmt.Errorf("request current carbon intensity: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return signals.Signal{}, fmt.Errorf(
			"Electricity Maps returned HTTP %d: %s",
			resp.StatusCode, strings.TrimSpace(string(body)),
		)
	}

	var data carbonResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return signals.Signal{}, fmt.Errorf("decode carbon response: %w", err)
	}

	if data.Zone == "" || data.CarbonIntensity == nil || data.DateTime == nil {
		return signals.Signal{}, fmt.Errorf("carbon response is missing required fields")
	}
	if data.Zone != zone {
		return signals.Signal{}, fmt.Errorf(
			"requested zone %s but received zone %s",
			zone, data.Zone,
		)
	}
	if !validIntensity(*data.CarbonIntensity) {
		return signals.Signal{}, fmt.Errorf("invalid carbon intensity value")
	}
	if data.DateTime.IsZero() {
		return signals.Signal{}, fmt.Errorf("invalid carbon intensity timestamp")
	}
	if data.TemporalGranularity == "" {
		return signals.Signal{}, fmt.Errorf("missing temporal granularity")
	}

	validity := time.Hour
	switch strings.ToLower(data.TemporalGranularity) {
	case "5_minutes":
		validity = 5 * time.Minute
	case "15_minutes":
		validity = 15 * time.Minute
	}

	timestamp := data.DateTime.UTC()
	validUntil := timestamp.Add(validity)
	collectedAt := c.now()

	quality := "FRESH"
	if collectedAt.After(validUntil) {
		quality = "STALE"
	}

	result = signals.Signal{
		SignalType:  "carbon",
		Provider:    ProviderName,
		Region:      data.Zone,
		Value:       *data.CarbonIntensity,
		Unit:        CarbonUnit,
		Timestamp:   timestamp,
		CollectedAt: collectedAt,
		ValidUntil:  validUntil,
		IsForecast:  false,
		Quality:     quality,
	}

	return result, nil
}
