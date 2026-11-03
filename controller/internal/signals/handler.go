package signals

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Handler validates, caches, and returns signals obtained from a provider.
type Handler struct {
	provider SignalProvider
	mu       sync.RWMutex
	cache    map[SignalRequest]Signal
	maxAges  map[string]time.Duration
}

// HandlerOptions configures maximum signal age by signal type.
type HandlerOptions struct {
	DefaultMaxSignalAge time.Duration
	MaxSignalAge        map[string]time.Duration
}

const defaultMaxSignalAge = time.Hour

func NewHandler(provider SignalProvider) *Handler {
	return NewHandlerWithOptions(provider, HandlerOptions{})
}

func NewHandlerWithOptions(provider SignalProvider, options HandlerOptions) *Handler {
	defaultAge := options.DefaultMaxSignalAge
	if defaultAge <= 0 {
		defaultAge = defaultMaxSignalAge
	}
	ages := make(map[string]time.Duration, len(options.MaxSignalAge)+1)
	ages["*"] = defaultAge
	for signalType, age := range options.MaxSignalAge {
		if age > 0 {
			ages[normalizeSignalType(signalType)] = age
		}
	}
	return &Handler{provider: provider, cache: make(map[SignalRequest]Signal), maxAges: ages}
}

func (h *Handler) maxAge(signalType string) time.Duration {
	if age, ok := h.maxAges[normalizeSignalType(signalType)]; ok {
		return age
	}
	return h.maxAges["*"]
}

func (h *Handler) GetSignal(ctx context.Context, request SignalRequest) (Signal, error) {
	if h == nil {
		return Signal{}, fmt.Errorf("signal handler is nil")
	}
	if h.provider == nil {
		return Signal{}, fmt.Errorf("signal provider is nil")
	}
	if ctx == nil {
		return Signal{}, fmt.Errorf("signal context is nil")
	}
	if err := ctx.Err(); err != nil {
		return Signal{}, err
	}

	request.SignalType = normalizeSignalType(request.SignalType)
	request.Region = strings.ToUpper(strings.TrimSpace(request.Region))
	if request.SignalType == "" {
		return Signal{}, fmt.Errorf("requested signal type is required")
	}
	if request.Region == "" {
		return Signal{}, fmt.Errorf("requested signal region is required")
	}

	now := time.Now()
	h.mu.Lock()
	cached, found := h.cache[request]
	if found {
		if err := validateSignal(cached, now, h.maxAge(request.SignalType)); err == nil {
			h.mu.Unlock()
			return cached, nil
		}
		delete(h.cache, request)
	}
	h.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return Signal{}, err
	}
	signal, err := h.provider.GetCurrentSignal(ctx, request)
	if err != nil {
		return Signal{}, fmt.Errorf("retrieve signal: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return Signal{}, err
	}
	if err := validateSignal(signal, time.Now(), h.maxAge(request.SignalType)); err != nil {
		return Signal{}, fmt.Errorf("invalid provider signal: %w", err)
	}
	if normalizeSignalType(signal.SignalType) != request.SignalType {
		return Signal{}, fmt.Errorf("provider returned signal type %q; requested %q", signal.SignalType, request.SignalType)
	}
	if !strings.EqualFold(strings.TrimSpace(signal.Region), request.Region) {
		return Signal{}, fmt.Errorf("provider returned region %q; requested %q", signal.Region, request.Region)
	}

	signal.SignalType = request.SignalType
	signal.Region = request.Region

	h.mu.Lock()
	if err := ctx.Err(); err != nil {
		h.mu.Unlock()
		return Signal{}, err
	}
	h.cache[request] = signal
	h.mu.Unlock()
	return signal, nil
}

// GetSignals retrieves the carbon, cost, and demand-cap signals for a region.
// It reuses GetSignal so each result goes through the existing validation and cache.
// If any retrieval fails, it returns an empty SignalSet and the error.
func (h *Handler) GetSignals(ctx context.Context, region string) (SignalSet, error) {
	if h == nil {
		return SignalSet{}, fmt.Errorf("signal handler is nil")
	}
	region = strings.ToUpper(strings.TrimSpace(region))
	if region == "" {
		return SignalSet{}, fmt.Errorf("requested signal region is required")
	}

	requests := []struct {
		name     string
		typeName string
	}{
		{name: "carbon", typeName: SignalTypeCarbon},
		{name: "cost", typeName: SignalTypeCost},
		{name: "demand-cap", typeName: SignalTypeDemandCap},
	}

	var result SignalSet
	for _, item := range requests {
		signal, err := h.GetSignal(ctx, SignalRequest{SignalType: item.typeName, Region: region})
		if err != nil {
			return SignalSet{}, fmt.Errorf("get %s signal: %w", item.name, err)
		}
		switch item.typeName {
		case SignalTypeCarbon:
			result.Carbon = signal
		case SignalTypeCost:
			result.Cost = signal
		case SignalTypeDemandCap:
			result.DemandCap = signal
		}
	}
	return result, nil
}

// GetForecastSignals retrieves and validates forecast entries from providers
// that implement ForecastSignalProvider. Forecast caching is deferred until
// the project defines a forecast-specific cache policy.
func (h *Handler) GetForecastSignals(ctx context.Context, request SignalRequest, horizon time.Duration) ([]Signal, error) {
	if h == nil || h.provider == nil {
		return nil, fmt.Errorf("signal handler or provider is nil")
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
	request.SignalType = normalizeSignalType(request.SignalType)
	request.Region = strings.ToUpper(strings.TrimSpace(request.Region))
	if request.SignalType == "" || request.Region == "" {
		return nil, fmt.Errorf("requested signal type and region are required")
	}
	provider, ok := h.provider.(ForecastSignalProvider)
	if !ok {
		return nil, fmt.Errorf("configured provider does not support forecasts")
	}
	forecasts, err := provider.GetForecastSignals(ctx, request, horizon)
	if err != nil {
		return nil, fmt.Errorf("retrieve forecast signals: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(forecasts) == 0 {
		return nil, fmt.Errorf("forecast provider returned no entries")
	}
	now := time.Now()
	for i := range forecasts {
		signal := &forecasts[i]
		if err := validateSignal(*signal, now, h.maxAge(request.SignalType)); err != nil {
			return nil, fmt.Errorf("invalid forecast entry %d: %w", i, err)
		}
		if normalizeSignalType(signal.SignalType) != request.SignalType {
			return nil, fmt.Errorf("forecast entry %d has wrong signal type %q", i, signal.SignalType)
		}
		if !strings.EqualFold(strings.TrimSpace(signal.Region), request.Region) {
			return nil, fmt.Errorf("forecast entry %d has wrong region %q", i, signal.Region)
		}
		if !signal.IsForecast {
			return nil, fmt.Errorf("forecast entry %d is not marked as forecast", i)
		}
		signal.SignalType = request.SignalType
		signal.Region = request.Region
	}
	return forecasts, nil
}
