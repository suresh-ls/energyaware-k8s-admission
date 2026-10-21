package signal

import (
	"context"
	"errors"
	"time"
)

// ErrForecastNotSupported is returned by providers that cannot produce a
// forecast (for example the Kepler demand provider, which only measures the
// present).
var ErrForecastNotSupported = errors.New("forecast signal not supported by this provider")

// ProviderStatus describes whether a provider's data source is reachable.
type ProviderStatus struct {
	Name      string    `json:"name"`
	Available bool      `json:"available"`
	Message   string    `json:"message,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
}

// SignalProvider is the common provider abstraction from design doc 4.5.1.
// T is the canonical signal type the provider returns (DemandSignal for the
// Kepler provider).
//
// The design doc lists only method names. The signatures here are a proposal:
// they take a context for cancellation and timeouts, and Validate receives the
// signal to check. If the team agrees a different shared interface, adapt this
// file and keep the Kepler provider's method set.
type SignalProvider[T any] interface {
	GetCurrentSignal(ctx context.Context) (T, error)
	GetForecastSignal(ctx context.Context) (T, error)
	Validate(sig T) error
	GetProviderStatus(ctx context.Context) ProviderStatus
	GetProviderName() string
}
