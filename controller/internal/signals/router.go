package signals

import (
	"context"
	"fmt"
	"strings"
)

// ProviderRouter routes signal requests to a provider based on signal type.
type ProviderRouter struct {
	providers map[string]SignalProvider
}

// NewProviderRouter creates a router. Keys are trimmed and lowercased.
// Avoid keys that become duplicates after normalization.
func NewProviderRouter(providers map[string]SignalProvider) *ProviderRouter {
	normalized := make(map[string]SignalProvider, len(providers))
	for signalType, provider := range providers {
		normalized[strings.ToLower(strings.TrimSpace(signalType))] = provider
	}
	return &ProviderRouter{providers: normalized}
}

func (r *ProviderRouter) GetCurrentSignal(ctx context.Context, request SignalRequest) (Signal, error) {
	if r == nil {
		return Signal{}, fmt.Errorf("provider router is nil")
	}
	if ctx == nil {
		return Signal{}, fmt.Errorf("signal context is nil")
	}
	if err := ctx.Err(); err != nil {
		return Signal{}, err
	}
	signalType := strings.ToLower(strings.TrimSpace(request.SignalType))
	if signalType == "" {
		return Signal{}, fmt.Errorf("requested signal type is required")
	}
	provider, ok := r.providers[signalType]
	if !ok || provider == nil {
		return Signal{}, fmt.Errorf("no provider registered for signal type %q", request.SignalType)
	}
	return provider.GetCurrentSignal(ctx, request)
}
