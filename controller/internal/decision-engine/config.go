package decisionengine

import (
	"fmt"
	"math"
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

type Config struct {
	Weights                Weights
	Bounds                 NormalizationBounds
	MinimumImprovement     float64
	ForecastHorizon        time.Duration
	PriorityRunThreshold   int32
	PriorityDeadlineBuffer time.Duration
	DefaultRequeueAfter    time.Duration
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

	if c.PriorityDeadlineBuffer < 0 {
		return fmt.Errorf("priority deadline buffer cannot be negative")
	}

	return nil
}
