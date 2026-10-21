
package signals

import "context"

// SignalProvider retrieves a signal matching a request.
type SignalProvider interface {
	GetCurrentSignal(
		ctx context.Context,
		request SignalRequest,
	) (Signal, error)
}
