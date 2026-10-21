package signals

import "time"

const (
	SignalTypeCarbon    = "carbon"
	SignalTypeCost      = "cost"
	SignalTypeDemandCap = "demand_cap"
)

// SignalRequest identifies the signal requested by the caller.
type SignalRequest struct {
	SignalType string
	Region     string
}

// Signal represents a normalized signal returned by a provider.
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

// SignalSet contains the three signals required by the EnergyGate decision flow.
// GetSignals returns a complete set or an error; it never returns a partial set.
type SignalSet struct {
	Carbon    Signal `json:"carbon"`
	Cost      Signal `json:"cost"`
	DemandCap Signal `json:"demand_cap"`
}
