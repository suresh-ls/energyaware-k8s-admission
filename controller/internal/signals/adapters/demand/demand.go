// Package demand supplies real Kepler CPU-package power to the shared handler.
package demand

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/suresh-ls/energyaware-k8s-admission/controller/internal/signals"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const ProviderName = "kepler"

const StatusWithinCap = "WITHIN_CAP"
const StatusExceeded = "EXCEEDED"

const DefaultQuery = `sum(kepler_node_cpu_watts{zone="package"})`
const DefaultTimestampQuery = `min(timestamp(kepler_node_cpu_watts{zone="package"}))`

// Config binds one monitored cluster to its canonical deployment region.
// Query returns one aggregate in watts. TimestampQuery returns the oldest
// contributing scrape timestamp as its VALUE, not its evaluation timestamp.
// Override BOTH expressions when changing the monitored metric/label selection.
type Config struct {
	PrometheusURL  string
	Region         string
	CapKW          float64
	Query          string
	TimestampQuery string
	MaxSampleAge   time.Duration
	Timeout        time.Duration
	Synthetic      bool
	HTTPClient     *http.Client
	Now            func() time.Time
}

type Provider struct {
	cfg    Config
	base   *url.URL
	client *http.Client
}

var _ signals.SignalProvider = (*Provider)(nil)

func NewProvider(cfg Config) (*Provider, error) {
	base, err := url.Parse(cfg.PrometheusURL)
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") {
		return nil, fmt.Errorf("valid HTTP(S) PrometheusURL required")
	}
	cfg.Region = strings.ToUpper(strings.TrimSpace(cfg.Region))
	if cfg.Region == "" {
		return nil, fmt.Errorf("deployment region required")
	}
	if !finite(cfg.CapKW) || cfg.CapKW <= 0 {
		return nil, fmt.Errorf("CapKW must be finite and positive")
	}
	if cfg.MaxSampleAge < 0 || cfg.Timeout < 0 {
		return nil, fmt.Errorf("durations must not be negative")
	}
	if cfg.Query == "" {
		cfg.Query = DefaultQuery
		if cfg.TimestampQuery == "" {
			cfg.TimestampQuery = DefaultTimestampQuery
		}
	}
	if cfg.TimestampQuery == "" {
		return nil, fmt.Errorf("custom Query requires a matching TimestampQuery")
	}
	if cfg.MaxSampleAge == 0 {
		cfg.MaxSampleAge = time.Minute
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Second
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{}
	}
	return &Provider{cfg: cfg, base: base, client: client}, nil
}

// GetCurrentSignal retrieves a measurement without adding a second cache.
// Demand forecasts are deliberately not implemented; Kepler measures the present.
func (p *Provider) GetCurrentSignal(ctx context.Context, request signals.SignalRequest) (result signals.Signal, err error) {
	logCtx := ctx
	if logCtx == nil {
		logCtx = context.Background()
	}
	logger := log.FromContext(logCtx).WithName("demand").WithValues(
		"provider", ProviderName, "signal_type", request.SignalType, "region", strings.ToUpper(strings.TrimSpace(request.Region)), "unit", "kW")
	started := time.Now()
	stage := "request_validation"
	logger.V(1).Info("Retrieving demand signal")
	defer func() {
		if err != nil {
			kind := "operation_failed"
			if errors.Is(err, context.Canceled) {
				kind = "cancelled"
			}
			if errors.Is(err, context.DeadlineExceeded) {
				kind = "timeout"
			}
			// Raw transport/parse errors can contain URLs, queries or response data.
			if kind == "cancelled" {
				logger.V(1).Info("Demand signal retrieval cancelled", "stage", stage, "duration_ms", time.Since(started).Milliseconds())
			} else {
				logger.Error(nil, "Could not retrieve demand signal", "stage", stage, "error_kind", kind, "duration_ms", time.Since(started).Milliseconds())
			}
			return
		}
		logger.Info("Retrieved demand signal", "value", result.Value, "cap", result.Cap, "utilization", result.Utilization,
			"status", result.Status, "timestamp", result.Timestamp, "valid_until", result.ValidUntil, "quality", result.Quality,
			"duration_ms", time.Since(started).Milliseconds())
	}()

	if p == nil || ctx == nil {
		return signals.Signal{}, fmt.Errorf("provider and context required")
	}
	if err := ctx.Err(); err != nil {
		return signals.Signal{}, err
	}
	if request.SignalType != signals.SignalTypeDemandCap {
		return signals.Signal{}, fmt.Errorf("unsupported signal type %q", request.SignalType)
	}
	if !strings.EqualFold(strings.TrimSpace(request.Region), p.cfg.Region) {
		return signals.Signal{}, fmt.Errorf("requested region %q does not match monitored deployment %q", request.Region, p.cfg.Region)
	}
	ctx, cancel := context.WithTimeout(ctx, p.cfg.Timeout)
	defer cancel()
	// Evaluate both queries at the same instant. A timestamp() query is needed:
	// an aggregate's result timestamp alone does not reveal stale source scrapes.
	evaluation := p.cfg.Now().UTC()
	stage = "power_query"
	watts, err := p.query(ctx, p.cfg.Query, evaluation, "power")
	if err != nil {
		return signals.Signal{}, err
	}
	stage = "source_timestamp_query"
	seconds, err := p.query(ctx, p.cfg.TimestampQuery, evaluation, "source_timestamp")
	if err != nil {
		return signals.Signal{}, fmt.Errorf("source timestamp: %w", err)
	}
	stage = "measurement_validation"
	if watts < 0 || seconds <= 0 || seconds > float64(evaluation.Unix())+float64(evaluation.Nanosecond())/1e9 {
		return signals.Signal{}, fmt.Errorf("invalid power or future source timestamp")
	}
	if seconds > 1e12 {
		return signals.Signal{}, fmt.Errorf("source timestamp out of range")
	}
	whole, fraction := math.Modf(seconds)
	observed := time.Unix(int64(whole), int64(fraction*1e9)).UTC()
	collected := p.cfg.Now().UTC()
	expiry := observed.Add(p.cfg.MaxSampleAge)
	stage = "freshness_validation"
	logger = logger.WithValues("source_timestamp", observed, "source_valid_until", expiry)
	if !collected.Before(expiry) {
		return signals.Signal{}, fmt.Errorf("kepler measurement expired at %s", expiry.Format(time.RFC3339Nano))
	}
	value := watts / 1000
	utilization := value / p.cfg.CapKW
	if !finite(utilization) {
		return signals.Signal{}, fmt.Errorf("demand utilization overflow")
	}
	status := StatusWithinCap
	if value > p.cfg.CapKW {
		status = StatusExceeded
	}
	quality := "FRESH"
	if p.cfg.Synthetic {
		quality = "simulated"
	}
	return signals.Signal{SignalType: signals.SignalTypeDemandCap, Provider: ProviderName, Region: p.cfg.Region,
		Value: value, Unit: "kW", Cap: p.cfg.CapKW, Utilization: utilization, Status: status,
		Timestamp: observed, CollectedAt: collected, ValidUntil: expiry, Quality: quality}, nil
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func (p *Provider) query(ctx context.Context, expression string, at time.Time, queryKind string) (float64, error) {
	u := p.base.JoinPath("api", "v1", "query")
	q := u.Query()
	q.Set("query", expression)
	q.Set("time", at.Format(time.RFC3339Nano))
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return 0, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("query Prometheus: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	log.FromContext(ctx).WithName("demand").V(1).Info("Received Prometheus response",
		"provider", ProviderName, "query_kind", queryKind, "http_status", resp.StatusCode)
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("prometheus HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil {
		return 0, err
	}
	if len(body) > 1<<20 {
		return 0, fmt.Errorf("prometheus response too large")
	}
	var result struct {
		Status   string   `json:"status"`
		Error    string   `json:"error"`
		Warnings []string `json:"warnings"`
		Data     struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Value []json.RawMessage `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, fmt.Errorf("decode Prometheus: %w", err)
	}
	if result.Status != "success" || result.Data.ResultType != "vector" || len(result.Data.Result) != 1 || len(result.Warnings) > 0 {
		return 0, fmt.Errorf("expected one complete Prometheus vector: %s", result.Error)
	}
	sample := result.Data.Result[0].Value
	if len(sample) != 2 {
		return 0, fmt.Errorf("invalid Prometheus sample")
	}
	var timestamp float64
	if err := json.Unmarshal(sample[0], &timestamp); err != nil || !finite(timestamp) || timestamp <= 0 {
		return 0, fmt.Errorf("invalid sample timestamp")
	}
	var raw string
	if err := json.Unmarshal(sample[1], &raw); err != nil {
		return 0, fmt.Errorf("invalid sample value")
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || !finite(value) {
		return 0, fmt.Errorf("invalid numeric sample %q", raw)
	}
	return value, nil
}
