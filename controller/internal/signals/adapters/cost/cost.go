package cost

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/suresh-ls/energyaware-k8s-admission/controller/internal/signals"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	defaultBaseURL = "https://api.energymap.in"
	priceUnit      = "INR/kWh"
)

type IEXRecord struct {
	Timestamp  time.Time `json:"timestamp"`
	MarketType string    `json:"market_type"`
	Region     string    `json:"region"`
	Price      string    `json:"mcp_rs_mwh"`
	Source     string    `json:"source"`
}

type APIResponse struct {
	Data []IEXRecord `json:"data"`
	Meta struct {
		FreshnessLagMinutes int `json:"freshness_lag_minutes"`
	} `json:"meta"`
}

type ForecastRecord struct {
	TargetDate   string    `json:"target_date"`
	HorizonDays  int       `json:"horizon_days"`
	VWAPQ10      float64   `json:"vwap_q10"`
	VWAPQ50      float64   `json:"vwap_q50"`
	VWAPQ90      float64   `json:"vwap_q90"`
	VWAPMean     float64   `json:"vwap_mean"`
	ModelVersion string    `json:"model_version"`
	IssuedAt     time.Time `json:"issued_at"`
}

type ForecastAPIResponse struct {
	Data []ForecastRecord `json:"data"`
	Meta struct {
		Market       string    `json:"market"`
		Product      string    `json:"product"`
		AsOf         string    `json:"as_of"`
		DeliveryDays int       `json:"delivery_days"`
		IssuedAt     time.Time `json:"issued_at"`
		ModelVersion string    `json:"model_version"`
		Freshness    struct {
			IssuedAt         time.Time `json:"issued_at"`
			StalenessMinutes int       `json:"staleness_minutes"`
		} `json:"freshness"`
	} `json:"meta"`
}

// Provider retrieves electricity cost signals from EnergyMap's IEX API.
type Provider struct {
	apiKey  string
	client  *http.Client
	baseURL string
}

// NewProvider creates an EnergyMap cost provider.
func NewProvider(apiKey string, client *http.Client, baseURL string) *Provider {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	return &Provider{
		apiKey:  strings.TrimSpace(apiKey),
		client:  client,
		baseURL: baseURL,
	}
}

// GetCurrentSignal implements signals.SignalProvider.
func (p *Provider) GetCurrentSignal(
	ctx context.Context,
	request signals.SignalRequest,
) (output signals.Signal, retErr error) {
	started := time.Now()

	logger := ctrl.Log.WithName("cost-provider").WithValues(
		"component", "cost-provider",
		"operation", "get_current_signal",
		"signal_type", request.SignalType,
		"provider", "energymap",
		"region", request.Region,
	)

	logger.Info("operation started")

	defer func() {
		duration := time.Since(started).Milliseconds()

		if retErr != nil {
			logger.Error(retErr, "operation failed",
				"status", "failure",
				"duration_ms", duration,
			)
		} else {
			logger.Info("operation completed",
				"status", "success",
				"duration_ms", duration,
			)
		}
	}()
	var empty signals.Signal

	if p == nil {
		return empty, fmt.Errorf("cost provider must not be nil")
	}
	if ctx == nil {
		return empty, fmt.Errorf("context must not be nil")
	}
	if request.SignalType != signals.SignalTypeCost {
		return empty, fmt.Errorf(
			"unsupported signal type %q: expected %q",
			request.SignalType,
			signals.SignalTypeCost,
		)
	}
	if p.apiKey == "" {
		return empty, fmt.Errorf("ENERGYMAP_API_KEY is not set")
	}

	parsedURL, err := url.Parse(p.baseURL)
	if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
		return empty, fmt.Errorf("invalid EnergyMap API base URL")
	}

	parsedURL.Path = "/developer/v1/market/iex/latest"
	query := parsedURL.Query()
	query.Set("limit", "1")
	query.Set("market_type", "DAM")
	parsedURL.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		parsedURL.String(),
		nil,
	)
	if err != nil {
		return empty, fmt.Errorf("create EnergyMap request: %w", err)
	}

	req.Header.Set("X-API-Key", p.apiKey)
	req.Header.Set("Accept", "application/json")
	logger.V(1).Info("sending current price request",
		"method", http.MethodGet,
		"endpoint", parsedURL.Path,
		"market_type", "DAM",
	)
	collectedAt := time.Now().UTC()

	resp, err := p.client.Do(req)
	if err != nil {
		return empty, fmt.Errorf("request electricity price: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return empty, fmt.Errorf("EnergyMap API returned HTTP %d", resp.StatusCode)
	}

	var result APIResponse
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	if err := decoder.Decode(&result); err != nil {
		return empty, fmt.Errorf("decode EnergyMap response: %w", err)
	}

	if len(result.Data) == 0 {
		return empty, fmt.Errorf("EnergyMap API returned no price records")
	}

	record := result.Data[0]

	if record.MarketType != "DAM" {
		return empty, fmt.Errorf("unexpected market type %q", record.MarketType)
	}
	if record.Region == "" {
		return empty, fmt.Errorf("EnergyMap response is missing region")
	}
	if request.Region != "" &&
		!strings.EqualFold(strings.TrimSpace(request.Region), strings.TrimSpace(record.Region)) {
		return empty, fmt.Errorf(
			"requested region %q does not match EnergyMap region %q",
			request.Region,
			record.Region,
		)
	}
	if record.Timestamp.IsZero() {
		return empty, fmt.Errorf("EnergyMap response is missing timestamp")
	}

	price, err := strconv.ParseFloat(record.Price, 64)
	if err != nil {
		return empty, fmt.Errorf("invalid electricity price %q: %w", record.Price, err)
	}
	if math.IsNaN(price) || math.IsInf(price, 0) || price < 0 {
		return empty, fmt.Errorf("electricity price must be finite and non-negative")
	}

	// Convert INR/MWh to INR/kWh.
	pricePerKWh := price / 1000.0

	// Provisional validity: one 15-minute delivery block.
	validUntil := record.Timestamp.Add(15 * time.Minute)

	quality := "reported"
	if result.Meta.FreshnessLagMinutes > 15 {
		quality = "possibly_stale"
	}

	return signals.Signal{
		SignalType:  signals.SignalTypeCost,
		Provider:    "EnergyMap-IEX",
		Region:      record.Region,
		Value:       pricePerKWh,
		Unit:        priceUnit,
		Timestamp:   record.Timestamp,
		CollectedAt: collectedAt,
		ValidUntil:  validUntil,
		IsForecast:  false,
		Quality:     quality,
	}, nil
}

// GetForecastSignals retrieves daily IEX DAM price forecasts from EnergyMap.
func (p *Provider) GetForecastSignals(
	ctx context.Context,
	request signals.SignalRequest,
	horizon time.Duration,
) (output []signals.Signal, retErr error) {
	started := time.Now()

	logger := ctrl.Log.WithName("cost-provider").WithValues(
		"component", "cost-provider",
		"operation", "get_forecast_signals",
		"signal_type", request.SignalType,
		"provider", "energymap",
		"region", request.Region,
	)

	logger.Info("operation started")

	defer func() {
		duration := time.Since(started).Milliseconds()

		if retErr != nil {
			logger.Error(retErr, "operation failed",
				"status", "failure",
				"duration_ms", duration,
			)
		} else {
			logger.Info("operation completed",
				"status", "success",
				"duration_ms", duration,
				"forecast_count", len(output),
			)
		}
	}()
	if p == nil {
		return nil, fmt.Errorf("cost provider must not be nil")
	}
	if ctx == nil {
		return nil, fmt.Errorf("context must not be nil")
	}
	if request.SignalType != signals.SignalTypeCost {
		return nil, fmt.Errorf("unsupported signal type %q: expected %q",
			request.SignalType, signals.SignalTypeCost)
	}
	if strings.TrimSpace(request.Region) == "" {
		return nil, fmt.Errorf("region is required for cost forecasts")
	}
	if horizon <= 0 {
		return nil, fmt.Errorf("forecast horizon must be positive")
	}
	if p.apiKey == "" {
		return nil, fmt.Errorf("ENERGYMAP_API_KEY is not set")
	}

	parsedURL, err := url.Parse(p.baseURL)
	if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
		return nil, fmt.Errorf("invalid EnergyMap API base URL")
	}

	parsedURL.Path = "/developer/v1/forecast/iex/dam/latest"
	parsedURL.RawQuery = ""

	req, err := http.NewRequestWithContext(
		ctx, http.MethodGet, parsedURL.String(), nil,
	)
	if err != nil {
		return nil, fmt.Errorf("create EnergyMap forecast request: %w", err)
	}

	req.Header.Set("X-API-Key", p.apiKey)
	req.Header.Set("Accept", "application/json")
	logger.V(1).Info("sending forecast request",
		"method", http.MethodGet,
		"endpoint", parsedURL.Path,
		"horizon_hours", horizon.Hours(),
	)
	collectedAt := time.Now().UTC()

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request electricity price forecast: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("EnergyMap forecast API returned HTTP %d", resp.StatusCode)
	}

	var result ForecastAPIResponse
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	if err := decoder.Decode(&result); err != nil {
		return nil, fmt.Errorf("decode EnergyMap forecast response: %w", err)
	}

	if len(result.Data) == 0 {
		return nil, fmt.Errorf("EnergyMap forecast API returned no records")
	}

	// The API returns daily forecasts. Include delivery days within the
	// requested duration, rounding partial days up to the next delivery day.
	maxDays := int(math.Ceil(horizon.Hours() / 24))
	if maxDays < 1 {
		maxDays = 1
	}

	quality := "forecast"
	if result.Meta.Freshness.StalenessMinutes > 1440 {
		quality = "possibly_stale"
	}

	signalsOut := make([]signals.Signal, 0, len(result.Data))

	for _, record := range result.Data {
		if record.HorizonDays < 1 || record.HorizonDays > maxDays {
			continue
		}

		targetDate, err := time.Parse("2006-01-02", record.TargetDate)
		if err != nil {
			return nil, fmt.Errorf(
				"invalid forecast target date %q: %w",
				record.TargetDate, err,
			)
		}

		if math.IsNaN(record.VWAPQ50) ||
			math.IsInf(record.VWAPQ50, 0) ||
			record.VWAPQ50 < 0 {
			return nil, fmt.Errorf(
				"invalid forecast median price for %s",
				record.TargetDate,
			)
		}

		issuedAt := record.IssuedAt
		if issuedAt.IsZero() {
			issuedAt = result.Meta.IssuedAt
		}
		if issuedAt.IsZero() {
			return nil, fmt.Errorf(
				"forecast record %s is missing issued_at",
				record.TargetDate,
			)
		}

		// Forecast validity is bounded to 24 hours after publication.
		validUntil := issuedAt.Add(24 * time.Hour)
		if !collectedAt.Before(validUntil) {
			return nil, fmt.Errorf(
				"forecast for %s has expired",
				record.TargetDate,
			)
		}

		// Convert INR/MWh to INR/kWh.
		pricePerKWh := record.VWAPQ50 / 1000.0

		signalsOut = append(signalsOut, signals.Signal{
			SignalType:  signals.SignalTypeCost,
			Provider:    "EnergyMap-IEX",
			Region:      signals.RegionAllIndia,
			Value:       pricePerKWh,
			Unit:        priceUnit,
			Timestamp:   targetDate,
			CollectedAt: collectedAt,
			ValidUntil:  validUntil,
			IsForecast:  true,
			Quality:     quality,
		})
	}

	if len(signalsOut) == 0 {
		return nil, fmt.Errorf(
			"EnergyMap returned no forecast records within the requested %s horizon",
			horizon,
		)
	}

	return signalsOut, nil
}

// Compile-time checks that Provider implements the shared interfaces.
var (
	_ signals.SignalProvider         = (*Provider)(nil)
	_ signals.ForecastSignalProvider = (*Provider)(nil)
)
