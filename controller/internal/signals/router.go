package signals

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ProviderRouter routes signal requests to a provider based on signal type.
type ProviderRouter struct {
	providers map[string]SignalProvider
}

// NewProviderRouter normalizes keys and rejects invalid or duplicate keys.
func NewProviderRouter(providers map[string]SignalProvider) (*ProviderRouter, error) {
	normalized := make(map[string]SignalProvider, len(providers))
	for key, provider := range providers {
		key = normalizeSignalType(key)
		if key == "" {
			return nil, fmt.Errorf("provider signal type key must not be empty")
		}
		if provider == nil {
			return nil, fmt.Errorf("provider registered for signal type %q is nil", key)
		}
		if _, exists := normalized[key]; exists {
			return nil, fmt.Errorf("duplicate provider key after normalization: %q", key)
		}
		normalized[key] = provider
	}
	return &ProviderRouter{providers: normalized}, nil
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
	signalType := normalizeSignalType(request.SignalType)
	if signalType == "" {
		return Signal{}, fmt.Errorf("requested signal type is required")
	}
	provider, ok := r.providers[signalType]
	if !ok || provider == nil {
		return Signal{}, fmt.Errorf("no provider registered for signal type %q", request.SignalType)
	}
	request.SignalType = signalType
	return provider.GetCurrentSignal(ctx, request)
}

// GetForecastSignals routes a forecast request only when the selected provider
// implements ForecastSignalProvider.
func (r *ProviderRouter) GetForecastSignals(ctx context.Context, request SignalRequest, horizon time.Duration) ([]Signal, error) {
	if r == nil {
		return nil, fmt.Errorf("provider router is nil")
	}
	if ctx == nil {
		return nil, fmt.Errorf("signal context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if horizon <= 0 {
		return nil, fmt.Errorf("forecast horizon must be positive")
	}
	signalType := normalizeSignalType(request.SignalType)
	if signalType == "" {
		return nil, fmt.Errorf("requested signal type is required")
	}
	provider, ok := r.providers[signalType]
	if !ok {
		return nil, fmt.Errorf("no provider registered for signal type %q", request.SignalType)
	}
	forecastProvider, ok := provider.(ForecastSignalProvider)
	if !ok {
		return nil, fmt.Errorf("provider for signal type %q does not support forecasts", signalType)
	}
	request.SignalType = signalType
	return forecastProvider.GetForecastSignals(ctx, request, horizon)
}

func normalizeSignalType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	// The Kepler CLI emits "demand"; the shared contract uses "demand_cap".
	if value == "demand" {
		return SignalTypeDemandCap
	}
	return value
}
