
package signals

import (
	"context"
	"fmt"
	"strings"
)

// ProviderRouter routes signal requests to the appropriate provider.
type ProviderRouter struct {
	providers map[string]SignalProvider
}

// NewProviderRouter creates a router for the registered providers.
func NewProviderRouter(
	providers map[string]SignalProvider,
) *ProviderRouter {
	normalized := make(map[string]SignalProvider, len(providers))

	for signalType, provider := range providers {
		normalized[strings.ToLower(strings.TrimSpace(signalType))] = provider
	}

	return &ProviderRouter{
		providers: normalized,
	}
}

// GetCurrentSignal routes a request using its signal type.
func (r *ProviderRouter) GetCurrentSignal(
	ctx context.Context,
	request SignalRequest,
) (Signal, error) {
	if r == nil {
		return Signal{}, fmt.Errorf("provider router is nil")
	}

	signalType := strings.ToLower(strings.TrimSpace(request.SignalType))
	provider, ok := r.providers[signalType]

	if !ok || provider == nil {
		return Signal{}, fmt.Errorf(
			"no provider registered for signal type %q",
			request.SignalType,
		)
	}

	return provider.GetCurrentSignal(ctx, request)
}
