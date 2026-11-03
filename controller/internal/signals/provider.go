package signals

import (
	"context"
	"time"
)

// SignalProvider retrieves a current signal matching a request.
// Implementations should honor context cancellation and return a valid Signal.
type SignalProvider interface {
	GetCurrentSignal(ctx context.Context, request SignalRequest) (Signal, error)
}

// ForecastSignalProvider is an optional capability for providers that support
// forecasts. It is separate so current-only providers remain compatible.
type ForecastSignalProvider interface {
	GetForecastSignals(ctx context.Context, request SignalRequest, horizon time.Duration) ([]Signal, error)
}
