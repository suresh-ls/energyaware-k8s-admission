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
) (signals.Signal, error) {
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

// Compile-time check that Provider implements the shared interface.
var _ signals.SignalProvider = (*Provider)(nil)
