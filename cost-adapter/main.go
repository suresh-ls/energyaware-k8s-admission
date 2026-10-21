package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

const (
	defaultBaseURL = "https://api.energymap.in"
	priceUnit      = "INR/kWh"
	sourceUnit     = "INR/MWh"
)

// IEXRecord represents one electricity-market price record.
type IEXRecord struct {
	Timestamp  time.Time `json:"timestamp"`
	MarketType string    `json:"market_type"`
	Region     string    `json:"region"`
	Price      string    `json:"mcp_rs_mwh"`
	Source     string    `json:"source"`
}

// APIResponse represents the EnergyMap IEX API response.
type APIResponse struct {
	Data []IEXRecord `json:"data"`
	Meta struct {
		FreshnessLagMinutes int `json:"freshness_lag_minutes"`
	} `json:"meta"`
}

// CostSignal is the initial cost-signal representation.
// Its final fields and freshness policy must be aligned
// with the shared EnergyGate signal contract.
type CostSignal struct {
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

// fetchCostSignal fetches the latest IEX DAM price and converts
// the value from INR/MWh to INR/kWh.
func fetchCostSignal(
	ctx context.Context,
	client *http.Client,
	baseURL string,
) (*CostSignal, error) {
	apiKey := os.Getenv("ENERGYMAP_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("ENERGYMAP_API_KEY is not set")
	}

	if client == nil {
		return nil, fmt.Errorf("HTTP client must not be nil")
	}

	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	parsedURL, err := url.Parse(baseURL)
	if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
		return nil, fmt.Errorf("invalid API base URL")
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
		return nil, fmt.Errorf("create API request: %w", err)
	}

	req.Header.Set("X-API-Key", apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request electricity price: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("EnergyMap API returned HTTP %d", resp.StatusCode)
	}

	var result APIResponse
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	if err := decoder.Decode(&result); err != nil {
		return nil, fmt.Errorf("decode API response: %w", err)
	}

	if len(result.Data) == 0 {
		return nil, fmt.Errorf("EnergyMap API returned no price records")
	}

	record := result.Data[0]

	if record.MarketType != "DAM" {
		return nil, fmt.Errorf("unexpected market type %q", record.MarketType)
	}
	if record.Region == "" {
		return nil, fmt.Errorf("API response is missing region")
	}
	if record.Timestamp.IsZero() {
		return nil, fmt.Errorf("API response is missing timestamp")
	}
	price, err := strconv.ParseFloat(record.Price, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid electricity price %q: %w", record.Price, err)
	}
	if math.IsNaN(price) || math.IsInf(price, 0) || price < 0 {
		return nil, fmt.Errorf("electricity price must be finite and non-negative")
	}

	// 1 MWh = 1000 kWh.
	pricePerKWh := price / 1000.0

	// The source timestamp identifies the market delivery block.
	// Provisional expiry: one 15-minute delivery block after the timestamp.
	validUntil := record.Timestamp.Add(15 * time.Minute)

	quality := "reported"
	if result.Meta.FreshnessLagMinutes > 15 {
		quality = "possibly_stale"
	}

	return &CostSignal{
		SignalType: "electricity_price",
		Provider:   "EnergyMap-IEX",
		Region:     record.Region,
		Value:      pricePerKWh,
		Unit:       priceUnit,
		Timestamp:  record.Timestamp,
		ValidUntil: validUntil,
		IsForecast: false,
		Quality:    quality,
	}, nil
}

func main() {
	_ = godotenv.Load()

	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	ctx := context.Background()

	signal, err := fetchCostSignal(ctx, client, defaultBaseURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cost signal error: %v\n", err)
		os.Exit(1)
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(signal); err != nil {
		fmt.Fprintf(os.Stderr, "Encode signal: %v\n", err)
		os.Exit(1)
	}
}
