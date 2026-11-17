package decisionengine

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
)

// Weights are experimental parameters and must sum to 1.
type Weights struct {
	CarbonImpact float64
	Energy       float64
	Cost         float64
	Delay        float64
}

// NormalizationBounds define the expected ranges of raw source values.
// Configure these to match the units emitted by the selected providers.
type NormalizationBounds struct {
	MaxCarbonImpact float64 // gCO2e; energy(Wh) * carbon(gCO2e/kWh) / 1000
	MaxEnergyWh     float64
	MaxCostPerKWh   float64 // currency/kWh, as returned by the cost adapter
}

type ForecastFailurePolicy string

const (
	ForecastFailureRunCurrent ForecastFailurePolicy = "RUN_CURRENT"
	ForecastFailureWait       ForecastFailurePolicy = "WAIT_FOR_FORECAST"
)

type Config struct {
	Weights                Weights
	Bounds                 NormalizationBounds
	MinimumImprovement     float64
	ForecastHorizon        time.Duration
	PriorityRunThreshold   int32
	PriorityDeadlineBuffer time.Duration
	DefaultRequeueAfter    time.Duration
	ForecastFailurePolicy  ForecastFailurePolicy
}

func DefaultConfig() Config {
	return Config{
		Weights:                Weights{CarbonImpact: 0.40, Energy: 0.20, Cost: 0.25, Delay: 0.15},
		Bounds:                 NormalizationBounds{MaxCarbonImpact: 1000, MaxEnergyWh: 1000, MaxCostPerKWh: 100},
		MinimumImprovement:     0.05,
		ForecastHorizon:        24 * time.Hour,
		PriorityRunThreshold:   80,
		PriorityDeadlineBuffer: 15 * time.Minute,
		DefaultRequeueAfter:    5 * time.Minute,
		ForecastFailurePolicy:  ForecastFailureRunCurrent,
	}
}

func (c Config) Validate() error {
	w := c.Weights
	values := []float64{w.CarbonImpact, w.Energy, w.Cost, w.Delay}
	sum := 0.0

	for _, v := range values {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return fmt.Errorf("weights must be finite and non-negative")
		}
		sum += v
	}

	if math.Abs(sum-1) > 1e-9 {
		return fmt.Errorf("decision weights must sum to 1; got %.6f", sum)
	}

	bounds := []float64{
		c.Bounds.MaxCarbonImpact,
		c.Bounds.MaxEnergyWh,
		c.Bounds.MaxCostPerKWh,
	}
	for _, v := range bounds {
		if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
			return fmt.Errorf("normalization bounds must be finite and positive")
		}
	}

	if math.IsNaN(c.MinimumImprovement) ||
		math.IsInf(c.MinimumImprovement, 0) ||
		c.MinimumImprovement < 0 ||
		c.MinimumImprovement >= 1 {
		return fmt.Errorf("minimum improvement must be finite and in [0,1)")
	}

	if c.ForecastHorizon <= 0 || c.DefaultRequeueAfter <= 0 {
		return fmt.Errorf("forecast horizon and default requeue interval must be positive")
	}

	if c.ForecastFailurePolicy != ForecastFailureRunCurrent &&
		c.ForecastFailurePolicy != ForecastFailureWait {
		return fmt.Errorf("unsupported forecast failure policy %q", c.ForecastFailurePolicy)
	}

	if c.PriorityDeadlineBuffer < 0 {
		return fmt.Errorf("priority deadline buffer cannot be negative")
	}

	return nil
}

// ConfigFromEnv overlays deployment-time environment variables on top of the
// supplied configuration. Invalid values return an error rather than silently
// changing scheduling policy.
func ConfigFromEnv(base Config) (Config, error) {
	cfg := base

	if value := strings.TrimSpace(os.Getenv("ENERGYGATE_FORECAST_FAILURE_POLICY")); value != "" {
		cfg.ForecastFailurePolicy = ForecastFailurePolicy(strings.ToUpper(value))
	}
	if err := envFloat("ENERGYGATE_MINIMUM_IMPROVEMENT", &cfg.MinimumImprovement); err != nil {
		return Config{}, err
	}
	if err := envFloat("ENERGYGATE_MAX_CARBON_IMPACT", &cfg.Bounds.MaxCarbonImpact); err != nil {
		return Config{}, err
	}
	if err := envFloat("ENERGYGATE_MAX_ENERGY_WH", &cfg.Bounds.MaxEnergyWh); err != nil {
		return Config{}, err
	}
	if err := envFloat("ENERGYGATE_MAX_COST_PER_KWH", &cfg.Bounds.MaxCostPerKWh); err != nil {
		return Config{}, err
	}
	if err := envFloat("ENERGYGATE_WEIGHT_CARBON", &cfg.Weights.CarbonImpact); err != nil {
		return Config{}, err
	}
	if err := envFloat("ENERGYGATE_WEIGHT_ENERGY", &cfg.Weights.Energy); err != nil {
		return Config{}, err
	}
	if err := envFloat("ENERGYGATE_WEIGHT_COST", &cfg.Weights.Cost); err != nil {
		return Config{}, err
	}
	if err := envFloat("ENERGYGATE_WEIGHT_DELAY", &cfg.Weights.Delay); err != nil {
		return Config{}, err
	}
	if err := envInt32("ENERGYGATE_PRIORITY_RUN_THRESHOLD", &cfg.PriorityRunThreshold); err != nil {
		return Config{}, err
	}
	if err := envDuration("ENERGYGATE_FORECAST_HORIZON", &cfg.ForecastHorizon); err != nil {
		return Config{}, err
	}
	if err := envDuration("ENERGYGATE_DEFAULT_REQUEUE_AFTER", &cfg.DefaultRequeueAfter); err != nil {
		return Config{}, err
	}
	if err := envDuration("ENERGYGATE_PRIORITY_DEADLINE_BUFFER", &cfg.PriorityDeadlineBuffer); err != nil {
		return Config{}, err
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func envFloat(name string, target *float64) error {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fmt.Errorf("%s must be a number: %w", name, err)
	}
	*target = parsed
	return nil
}

func envInt32(name string, target *int32) error {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return nil
	}
	parsed, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		return fmt.Errorf("%s must be an integer: %w", name, err)
	}
	*target = int32(parsed)
	return nil
}

func envDuration(name string, target *time.Duration) error {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fmt.Errorf("%s must be a duration such as 5m or 24h: %w", name, err)
	}
	*target = parsed
	return nil
}
