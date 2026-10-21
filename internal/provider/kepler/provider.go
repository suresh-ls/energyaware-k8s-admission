// Package kepler implements the demand signal provider (design doc 4.5.1 and
// 4.5.6). It reads Kepler power measurements through the Prometheus HTTP API,
// compares them with the configured demand cap and returns the canonical
// signal.DemandSignal.
package kepler

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

	"github.com/suresh-ls/energyaware-k8s-admission/internal/signal"
)

const (
	// ProviderName is reported in every signal and by GetProviderName.
	ProviderName = "kepler"

	// DefaultQuery sums the package-zone power across all nodes, giving one
	// cluster-wide series in watts. The zone label value can differ between
	// machines (for example "package-0"); check it in Prometheus and override
	// Config.Query if needed. The sum() also guarantees a single series, which
	// the provider requires.
	DefaultQuery = `sum(kepler_node_cpu_watts{zone="package"})`

	DefaultTimeout  = 5 * time.Second
	DefaultValidFor = time.Minute

	// maxBodyBytes bounds how much of a Prometheus response is read.
	maxBodyBytes = 1 << 20
)

// Errors returned by the provider. Use errors.Is to test for them.
var (
	// ErrPrometheusUnavailable: the request failed or Prometheus replied
	// with a non-200 status.
	ErrPrometheusUnavailable = errors.New("prometheus unavailable")
	// ErrNoData: the query succeeded but returned no series. The usual causes
	// are Kepler not being scraped or a wrong label in the query.
	ErrNoData = errors.New("query returned no data")
	// ErrMultipleSeries: the query returned more than one series. Wrap the
	// query in sum() or add label filters; the provider will not guess.
	ErrMultipleSeries = errors.New("query returned more than one series")
	// ErrMalformedResponse: the response was not valid Prometheus JSON, or the
	// value was not a finite number.
	ErrMalformedResponse = errors.New("malformed prometheus response")
	// ErrInvalidConfig: the provider was constructed with a bad Config.
	ErrInvalidConfig = errors.New("invalid kepler provider config")
)

// Config configures the provider.
type Config struct {
	// PrometheusURL is the base URL, e.g. "http://localhost:9090". Required.
	PrometheusURL string
	// Query is the PromQL expression returning demand in WATTS as a single
	// series. Default: DefaultQuery.
	Query string
	// Unit is the unit of the reported value and of Cap: signal.UnitWatt or
	// signal.UnitKilowatt. Default: kW.
	Unit string
	// Cap is the maximum permitted demand, in Unit. Required, must be > 0.
	Cap float64
	// Timeout bounds each request to Prometheus. Default: 5s.
	Timeout time.Duration
	// ValidFor is how long a FRESH signal is valid (sets ValidUntil).
	// Default: 1 minute. Assumption: demand is a live measurement, so it
	// expires much sooner than the 1 hour cache max age of the carbon signal.
	ValidFor time.Duration
	// HTTPClient overrides the HTTP client (tests, proxies). Optional.
	HTTPClient *http.Client
	// Now overrides the clock (tests). Optional.
	Now func() time.Time
}

// Provider is the Kepler-based demand signal provider.
type Provider struct {
	cfg    Config
	base   *url.URL
	client *http.Client
	now    func() time.Time
}

var _ signal.SignalProvider[signal.DemandSignal] = (*Provider)(nil)

// New validates cfg, applies defaults and returns a Provider.
func New(cfg Config) (*Provider, error) {
	if strings.TrimSpace(cfg.PrometheusURL) == "" {
		return nil, fmt.Errorf("%w: PrometheusURL is required", ErrInvalidConfig)
	}
	base, err := url.Parse(cfg.PrometheusURL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("%w: PrometheusURL %q is not a valid URL", ErrInvalidConfig, cfg.PrometheusURL)
	}
	if cfg.Query == "" {
		cfg.Query = DefaultQuery
	}
	if cfg.Unit == "" {
		cfg.Unit = signal.UnitKilowatt
	}
	if cfg.Unit != signal.UnitWatt && cfg.Unit != signal.UnitKilowatt {
		return nil, fmt.Errorf("%w: unsupported unit %q", ErrInvalidConfig, cfg.Unit)
	}
	if math.IsNaN(cfg.Cap) || math.IsInf(cfg.Cap, 0) || cfg.Cap <= 0 {
		return nil, fmt.Errorf("%w: Cap must be > 0, got %v", ErrInvalidConfig, cfg.Cap)
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.ValidFor <= 0 {
		cfg.ValidFor = DefaultValidFor
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{}
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Provider{cfg: cfg, base: base, client: client, now: now}, nil
}

// GetProviderName returns "kepler".
func (p *Provider) GetProviderName() string { return ProviderName }

// GetForecastSignal is not supported: Kepler measures present power only.
func (p *Provider) GetForecastSignal(context.Context) (signal.DemandSignal, error) {
	return signal.DemandSignal{}, signal.ErrForecastNotSupported
}

// Validate checks a demand signal and that it came from this provider.
func (p *Provider) Validate(sig signal.DemandSignal) error {
	if sig.Provider != ProviderName {
		return fmt.Errorf("signal provider %q is not %q", sig.Provider, ProviderName)
	}
	return sig.Validate()
}

// GetCurrentSignal queries Prometheus for current power, evaluates it against
// the cap and returns a FRESH DemandSignal. It never returns a cached value;
// caching and stale handling are layered on top of the provider.
func (p *Provider) GetCurrentSignal(ctx context.Context) (signal.DemandSignal, error) {
	watts, err := p.queryWatts(ctx)
	if err != nil {
		return signal.DemandSignal{}, err
	}

	value := watts
	if p.cfg.Unit == signal.UnitKilowatt {
		value = watts / 1000
	}

	ev, err := signal.EvaluateDemand(value, p.cfg.Cap)
	if err != nil {
		return signal.DemandSignal{}, err
	}

	now := p.now()
	sig := signal.DemandSignal{
		SignalType:  signal.SignalTypeDemand,
		Provider:    ProviderName,
		Value:       ev.Value,
		Unit:        p.cfg.Unit,
		Cap:         ev.Cap,
		Utilization: ev.Utilization,
		Status:      ev.Status,
		Timestamp:   now,
		ValidUntil:  now.Add(p.cfg.ValidFor),
		Quality:     signal.QualityFresh,
	}
	if err := p.Validate(sig); err != nil {
		return signal.DemandSignal{}, fmt.Errorf("built an invalid signal: %w", err)
	}
	return sig, nil
}

// GetProviderStatus reports whether Prometheus answers its readiness check.
func (p *Provider) GetProviderStatus(ctx context.Context) signal.ProviderStatus {
	st := signal.ProviderStatus{Name: ProviderName, CheckedAt: p.now()}

	ctx, cancel := context.WithTimeout(ctx, p.cfg.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.base.JoinPath("-", "ready").String(), nil)
	if err != nil {
		st.Message = err.Error()
		return st
	}
	resp, err := p.client.Do(req)
	if err != nil {
		st.Message = fmt.Sprintf("%v: %v", ErrPrometheusUnavailable, err)
		return st
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBodyBytes))

	if resp.StatusCode != http.StatusOK {
		st.Message = fmt.Sprintf("prometheus not ready: HTTP %d", resp.StatusCode)
		return st
	}
	st.Available = true
	return st
}

// promResponse is the subset of the Prometheus instant-query response we use.
type promResponse struct {
	Status    string `json:"status"`
	ErrorType string `json:"errorType"`
	Error     string `json:"error"`
	Data      struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Value []json.RawMessage `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

// queryWatts runs the configured query and returns the single resulting value.
func (p *Provider) queryWatts(ctx context.Context) (float64, error) {
	ctx, cancel := context.WithTimeout(ctx, p.cfg.Timeout)
	defer cancel()

	u := p.base.JoinPath("api", "v1", "query")
	u.RawQuery = url.Values{"query": {p.cfg.Query}}.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrPrometheusUnavailable, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return 0, fmt.Errorf("%w: reading body: %v", ErrPrometheusUnavailable, err)
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("%w: HTTP %d: %s", ErrPrometheusUnavailable, resp.StatusCode, snippet(body))
	}

	var pr promResponse
	if err := json.Unmarshal(body, &pr); err != nil {
		return 0, fmt.Errorf("%w: %v", ErrMalformedResponse, err)
	}
	if pr.Status != "success" {
		return 0, fmt.Errorf("%w: status %q: %s %s", ErrMalformedResponse, pr.Status, pr.ErrorType, pr.Error)
	}
	if pr.Data.ResultType != "vector" {
		return 0, fmt.Errorf("%w: result type %q, want vector", ErrMalformedResponse, pr.Data.ResultType)
	}

	switch len(pr.Data.Result) {
	case 0:
		return 0, fmt.Errorf("%w (query: %s)", ErrNoData, p.cfg.Query)
	case 1:
	default:
		return 0, fmt.Errorf("%w: got %d (query: %s)", ErrMultipleSeries, len(pr.Data.Result), p.cfg.Query)
	}

	// A vector sample value is [<unix timestamp>, "<value as string>"].
	v := pr.Data.Result[0].Value
	if len(v) != 2 {
		return 0, fmt.Errorf("%w: sample has %d elements, want 2", ErrMalformedResponse, len(v))
	}
	var raw string
	if err := json.Unmarshal(v[1], &raw); err != nil {
		return 0, fmt.Errorf("%w: sample value is not a string: %v", ErrMalformedResponse, err)
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, fmt.Errorf("%w: sample value %q is not a finite number", ErrMalformedResponse, raw)
	}
	return f, nil
}

func snippet(b []byte) string {
	const max = 200
	s := strings.TrimSpace(string(b))
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}
