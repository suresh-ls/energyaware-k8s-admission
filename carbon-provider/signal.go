package carbon

import (
	"math"
	"time"
)

func validIntensity(value float64) bool {
	return !math.IsNaN(value) &&
		!math.IsInf(value, 0) &&
		value >= 0
}

// Signal mirrors the shared EnergyGate signal contract.
// It will be replaced with signals.Signal during final integration.
type Signal struct {
	SignalType  string    `json:"signal_type"`
	Provider    string    `json:"provider"`
	Region      string    `json:"region"`
	Value       float64   `json:"value"`
	Unit        string    `json:"unit"`
	Timestamp   time.Time `json:"timestamp"`
	CollectedAt time.Time `json:"collected_at,omitempty"`
	ValidUntil  time.Time `json:"valid_until"`
	IsForecast  bool      `json:"is_forecast"`
	Quality     string    `json:"quality"`
}
