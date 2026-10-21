package signals

import "context"

// SignalProvider retrieves a signal matching a request.
// Implementations should honor context cancellation and return a valid Signal.
type SignalProvider interface {
	GetCurrentSignal(ctx context.Context, request SignalRequest) (Signal, error)
}
