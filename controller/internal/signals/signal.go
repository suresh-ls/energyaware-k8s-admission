
package signals

import "time"

// SignalRequest identifies the signal requested by the caller.
type SignalRequest struct {
	SignalType string
	Region     string
}

// Signal represents a normalized signal from a provider.
type Signal struct {
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
