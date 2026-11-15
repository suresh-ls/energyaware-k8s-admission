package signals

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/suresh-ls/energyaware-k8s-admission/controller/internal/logging"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// Handler validates, caches, and returns signals obtained from a provider.
type Handler struct {
	provider SignalProvider
	mu       sync.RWMutex
	cache    map[SignalRequest]Signal
	maxAges  map[string]time.Duration
	now      func() time.Time
}

// HandlerOptions configures maximum signal age by signal type.
type HandlerOptions struct {
	Now                 func() time.Time
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
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Handler{
		provider: provider,
		cache:    make(map[SignalRequest]Signal),
		maxAges:  ages,
		now:      now,
	}
}

func (h *Handler) maxAge(signalType string) time.Duration {
	if age, ok := h.maxAges[normalizeSignalType(signalType)]; ok {
		return age
	}
	return h.maxAges["*"]
}

func (h *Handler) GetSignal(
	ctx context.Context,
	request SignalRequest,
) (Signal, error) {

	if ctx == nil {
		ctx = context.Background()
	}

	start := time.Now()

	log := logf.FromContext(ctx)

	if h == nil {
		err := fmt.Errorf("signal handler is nil")

		log.Error(
			err,
			"Signal handler request failed",
			logging.FieldComponent, logging.ComponentSignalHandler,
			logging.FieldOperation, "get_signal",
			logging.FieldSignal, request.SignalType,
			logging.FieldRegion, request.Region,
			logging.FieldStatus, logging.StatusFailure,
			logging.FieldDuration, logging.SinceMillis(start),
		)

		return Signal{}, err
	}

	if h.provider == nil {
		err := fmt.Errorf("signal provider is nil")

		log.Error(
			err,
			"Signal handler request failed",
			logging.FieldComponent, logging.ComponentSignalHandler,
			logging.FieldOperation, "get_signal",
			logging.FieldSignal, request.SignalType,
			logging.FieldRegion, request.Region,
			logging.FieldStatus, logging.StatusFailure,
			logging.FieldDuration, logging.SinceMillis(start),
		)

		return Signal{}, err
	}

	if ctx == nil {
		return Signal{}, fmt.Errorf("signal context is nil")
	}

	if err := ctx.Err(); err != nil {
		log.Error(
			err,
			"Signal handler request cancelled",
			logging.FieldComponent, logging.ComponentSignalHandler,
			logging.FieldOperation, "get_signal",
			logging.FieldSignal, request.SignalType,
			logging.FieldRegion, request.Region,
			logging.FieldStatus, logging.StatusFailure,
			logging.FieldDuration, logging.SinceMillis(start),
		)

		return Signal{}, err
	}

	request = normalizeSignalRequest(request)

	if request.SignalType == "" {
		err := fmt.Errorf("requested signal type is required")

		log.Error(
			err,
			"Invalid signal request",
			logging.FieldComponent, logging.ComponentSignalHandler,
			logging.FieldOperation, "get_signal",
			logging.FieldStatus, logging.StatusFailure,
			logging.FieldDuration, logging.SinceMillis(start),
		)

		return Signal{}, err
	}

	if request.Region == "" {
		err := fmt.Errorf("requested signal region is required")

		log.Error(
			err,
			"Invalid signal request",
			logging.FieldComponent, logging.ComponentSignalHandler,
			logging.FieldOperation, "get_signal",
			logging.FieldSignal, request.SignalType,
			logging.FieldStatus, logging.StatusFailure,
			logging.FieldDuration, logging.SinceMillis(start),
		)

		return Signal{}, err
	}

	log.V(1).Info(
		"Signal retrieval started",
		logging.FieldComponent, logging.ComponentSignalHandler,
		logging.FieldOperation, "get_signal",
		logging.FieldSignal, request.SignalType,
		logging.FieldRegion, request.Region,
		logging.FieldStatus, logging.StatusStarted,
	)

	now := h.now()

	// Cache lookup.
	h.mu.Lock()
	cached, found := h.cache[request]

	if found {
		if err := validateSignal(
			cached,
			now,
			h.maxAge(request.SignalType),
		); err == nil {
			h.mu.Unlock()

			if cached.Quality == "FRESH" {
				cached.Quality = "CACHED"
			}

			log.Info(
				"Signal cache hit",
				logging.FieldComponent, logging.ComponentSignalHandler,
				logging.FieldOperation, "cache_lookup",
				logging.FieldSignal, request.SignalType,
				logging.FieldRegion, request.Region,
				logging.FieldStatus, logging.StatusCacheHit,
				logging.FieldDuration, logging.SinceMillis(start),
			)

			return cached, nil
		}

		delete(h.cache, request)
	}

	h.mu.Unlock()

	log.V(1).Info(
		"Signal cache miss",
		logging.FieldComponent, logging.ComponentSignalHandler,
		logging.FieldOperation, "cache_lookup",
		logging.FieldSignal, request.SignalType,
		logging.FieldRegion, request.Region,
		logging.FieldStatus, logging.StatusCacheMiss,
		logging.FieldDuration, logging.SinceMillis(start),
	)

	if err := ctx.Err(); err != nil {
		log.Error(
			err,
			"Signal request cancelled before provider call",
			logging.FieldComponent, logging.ComponentSignalHandler,
			logging.FieldOperation, "get_signal",
			logging.FieldSignal, request.SignalType,
			logging.FieldRegion, request.Region,
			logging.FieldStatus, logging.StatusFailure,
			logging.FieldDuration, logging.SinceMillis(start),
		)

		return Signal{}, err
	}

	// Provider request.
	providerStart := time.Now()

	signal, err := h.provider.GetCurrentSignal(ctx, request)
	if err != nil {
		log.Error(
			err,
			"Signal provider request failed",
			logging.FieldComponent, logging.ComponentSignalHandler,
			logging.FieldOperation, "get_signal",
			logging.FieldSignal, request.SignalType,
			logging.FieldRegion, request.Region,
			logging.FieldStatus, logging.StatusFailure,
			logging.FieldDuration, logging.SinceMillis(providerStart),
		)

		return Signal{}, fmt.Errorf("retrieve signal: %w", err)
	}

	log.V(1).Info(
		"Signal provider request completed",
		logging.FieldComponent, logging.ComponentSignalHandler,
		logging.FieldOperation, "get_signal",
		logging.FieldSignal, request.SignalType,
		logging.FieldRegion, request.Region,
		logging.FieldStatus, logging.StatusSuccess,
		logging.FieldDuration, logging.SinceMillis(providerStart),
	)

	if err := ctx.Err(); err != nil {
		log.Error(
			err,
			"Signal request cancelled after provider call",
			logging.FieldComponent, logging.ComponentSignalHandler,
			logging.FieldOperation, "get_signal",
			logging.FieldSignal, request.SignalType,
			logging.FieldRegion, request.Region,
			logging.FieldStatus, logging.StatusFailure,
			logging.FieldDuration, logging.SinceMillis(start),
		)

		return Signal{}, err
	}

	// Validate provider response.
	if err := validateSignal(
		signal,
		h.now(),
		h.maxAge(request.SignalType),
	); err != nil {
		log.Error(
			err,
			"Provider returned invalid signal",
			logging.FieldComponent, logging.ComponentSignalHandler,
			logging.FieldOperation, "validate_signal",
			logging.FieldSignal, request.SignalType,
			logging.FieldRegion, request.Region,
			logging.FieldStatus, logging.StatusFailure,
			logging.FieldDuration, logging.SinceMillis(start),
		)

		return Signal{}, fmt.Errorf("invalid provider signal: %w", err)
	}

	if normalizeSignalType(signal.SignalType) != request.SignalType {
		err := fmt.Errorf(
			"provider returned signal type %q; requested %q",
			signal.SignalType,
			request.SignalType,
		)

		log.Error(
			err,
			"Provider returned unexpected signal type",
			logging.FieldComponent, logging.ComponentSignalHandler,
			logging.FieldOperation, "validate_signal",
			logging.FieldSignal, request.SignalType,
			logging.FieldRegion, request.Region,
			logging.FieldStatus, logging.StatusFailure,
			logging.FieldDuration, logging.SinceMillis(start),
		)

		return Signal{}, err
	}

	providerRegion := normalizeSignalRegion(signal.Region)

	if providerRegion != request.Region {
		err := fmt.Errorf(
			"provider returned region %q; requested %q",
			signal.Region,
			request.Region,
		)

		log.Error(
			err,
			"Provider returned unexpected region",
			logging.FieldComponent, logging.ComponentSignalHandler,
			logging.FieldOperation, "validate_signal",
			logging.FieldSignal, request.SignalType,
			logging.FieldRegion, request.Region,
			logging.FieldStatus, logging.StatusFailure,
			logging.FieldDuration, logging.SinceMillis(start),
		)

		return Signal{}, err
	}

	signal.SignalType = request.SignalType
	signal.Region = providerRegion

	// Store in cache.
	h.mu.Lock()

	if err := ctx.Err(); err != nil {
		h.mu.Unlock()

		log.Error(
			err,
			"Signal request cancelled before cache update",
			logging.FieldComponent, logging.ComponentSignalHandler,
			logging.FieldOperation, "cache_store",
			logging.FieldSignal, request.SignalType,
			logging.FieldRegion, request.Region,
			logging.FieldStatus, logging.StatusFailure,
			logging.FieldDuration, logging.SinceMillis(start),
		)

		return Signal{}, err
	}

	h.cache[request] = signal
	h.mu.Unlock()

	log.Info(
		"Signal retrieval completed",
		logging.FieldComponent, logging.ComponentSignalHandler,
		logging.FieldOperation, "get_signal",
		logging.FieldSignal, request.SignalType,
		logging.FieldRegion, request.Region,
		logging.FieldStatus, logging.StatusSuccess,
		logging.FieldDuration, logging.SinceMillis(start),
	)

	return signal, nil
}

// GetSignals retrieves the carbon, cost, and demand-cap signals for a region.
// It reuses GetSignal so each result goes through the existing validation and cache.
// If any retrieval fails, it returns an empty SignalSet and the error.
func (h *Handler) GetSignals(
	ctx context.Context,
	region string,
) (SignalSet, error) {

	start := time.Now()

	log := logf.FromContext(ctx)

	if h == nil {
		err := fmt.Errorf("signal handler is nil")

		log.Error(
			err,
			"Signal set retrieval failed",
			logging.FieldComponent, logging.ComponentSignalHandler,
			logging.FieldOperation, "get_signals",
			logging.FieldRegion, region,
			logging.FieldStatus, logging.StatusFailure,
			logging.FieldDuration, logging.SinceMillis(start),
		)

		return SignalSet{}, err
	}

	region = normalizeSignalRegion(region)

	if region == "" {
		err := fmt.Errorf("requested signal region is required")

		log.Error(
			err,
			"Signal set retrieval failed",
			logging.FieldComponent, logging.ComponentSignalHandler,
			logging.FieldOperation, "get_signals",
			logging.FieldStatus, logging.StatusFailure,
			logging.FieldDuration, logging.SinceMillis(start),
		)

		return SignalSet{}, err
	}

	log.Info(
		"Signal set retrieval started",
		logging.FieldComponent, logging.ComponentSignalHandler,
		logging.FieldOperation, "get_signals",
		logging.FieldRegion, region,
		logging.FieldStatus, logging.StatusStarted,
	)

	requests := []struct {
		name     string
		typeName string
		region   string
	}{
		{
			name:     "carbon",
			typeName: SignalTypeCarbon,
			region:   region,
		},
		{
			name:     "cost",
			typeName: SignalTypeCost,
			region:   RegionAllIndia,
		},
		{
			name:     "demand-cap",
			typeName: SignalTypeDemandCap,
			region:   region,
		},
	}

	var result SignalSet

	for _, item := range requests {
		signal, err := h.GetSignal(
			ctx,
			SignalRequest{
				SignalType: item.typeName,
				Region:     item.region,
			},
		)

		if err != nil {
			log.Error(
				err,
				"Signal set retrieval failed",
				logging.FieldComponent, logging.ComponentSignalHandler,
				logging.FieldOperation, "get_signals",
				logging.FieldSignal, item.typeName,
				logging.FieldRegion, item.region,
				logging.FieldStatus, logging.StatusFailure,
				logging.FieldDuration, logging.SinceMillis(start),
			)

			return SignalSet{}, fmt.Errorf(
				"get %s signal: %w",
				item.name,
				err,
			)
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

	log.Info(
		"Signal set retrieval completed",
		logging.FieldComponent, logging.ComponentSignalHandler,
		logging.FieldOperation, "get_signals",
		logging.FieldRegion, region,
		logging.FieldStatus, logging.StatusSuccess,
		logging.FieldDuration, logging.SinceMillis(start),
	)

	return result, nil
}

// normalizeSignalRegion converts user-facing region names into the canonical
// region values used by signal providers.
func normalizeSignalRegion(region string) string {
	region = strings.ToUpper(strings.TrimSpace(region))

	switch region {
	case "ALL INDIA", "ALL-INDIA", "ALL_INDIA":
		return RegionAllIndia
	default:
		return region
	}
}

// normalizeSignalRequest applies the canonical region contract for each
// signal type. Carbon and demand-cap use the requested regional zone, while
// cost is always sourced from the national ALL-INDIA market.
func normalizeSignalRequest(request SignalRequest) SignalRequest {
	request.SignalType = normalizeSignalType(request.SignalType)
	request.Region = normalizeSignalRegion(request.Region)

	if request.SignalType == SignalTypeCost {
		request.Region = RegionAllIndia
	}

	return request
}

// GetForecastSignals retrieves and validates forecast entries from providers
// that implement ForecastSignalProvider. Forecast caching is deferred until
// the project defines a forecast-specific cache policy.
func (h *Handler) GetForecastSignals(
	ctx context.Context,
	request SignalRequest,
	horizon time.Duration,
) ([]Signal, error) {
	start := time.Now()

	log := logf.FromContext(ctx)

	if h == nil || h.provider == nil {
		err := fmt.Errorf("signal handler or provider is nil")

		log.Error(
			err,
			"Forecast signal retrieval failed",
			logging.FieldComponent, logging.ComponentSignalHandler,
			logging.FieldOperation, "get_forecast",
			logging.FieldSignal, request.SignalType,
			logging.FieldRegion, request.Region,
			logging.FieldStatus, logging.StatusFailure,
			logging.FieldDuration, logging.SinceMillis(start),
		)

		return nil, err
	}

	if ctx == nil {
		return nil, fmt.Errorf("signal context is nil")
	}

	if err := ctx.Err(); err != nil {
		log.Error(
			err,
			"Forecast signal request cancelled",
			logging.FieldComponent, logging.ComponentSignalHandler,
			logging.FieldOperation, "get_forecast",
			logging.FieldSignal, request.SignalType,
			logging.FieldRegion, request.Region,
			logging.FieldStatus, logging.StatusFailure,
			logging.FieldDuration, logging.SinceMillis(start),
		)

		return nil, err
	}

	if horizon <= 0 {
		err := fmt.Errorf("forecast horizon must be positive")

		log.Error(
			err,
			"Invalid forecast request",
			logging.FieldComponent, logging.ComponentSignalHandler,
			logging.FieldOperation, "get_forecast",
			logging.FieldSignal, request.SignalType,
			logging.FieldRegion, request.Region,
			logging.FieldStatus, logging.StatusFailure,
			logging.FieldDuration, logging.SinceMillis(start),
		)

		return nil, err
	}

	request = normalizeSignalRequest(request)

	if request.SignalType == "" || request.Region == "" {
		err := fmt.Errorf("requested signal type and region are required")

		log.Error(
			err,
			"Invalid forecast request",
			logging.FieldComponent, logging.ComponentSignalHandler,
			logging.FieldOperation, "get_forecast",
			logging.FieldStatus, logging.StatusFailure,
			logging.FieldDuration, logging.SinceMillis(start),
		)

		return nil, err
	}

	log.Info(
		"Forecast signal retrieval started",
		logging.FieldComponent, logging.ComponentSignalHandler,
		logging.FieldOperation, "get_forecast",
		logging.FieldSignal, request.SignalType,
		logging.FieldRegion, request.Region,
		logging.FieldStatus, logging.StatusStarted,
	)

	provider, ok := h.provider.(ForecastSignalProvider)
	if !ok {
		err := fmt.Errorf(
			"configured provider does not support forecasts",
		)

		log.Error(
			err,
			"Forecast provider is unavailable",
			logging.FieldComponent, logging.ComponentSignalHandler,
			logging.FieldOperation, "get_forecast",
			logging.FieldSignal, request.SignalType,
			logging.FieldRegion, request.Region,
			logging.FieldStatus, logging.StatusFailure,
			logging.FieldDuration, logging.SinceMillis(start),
		)

		return nil, err
	}

	providerStart := time.Now()

	forecasts, err := provider.GetForecastSignals(
		ctx,
		request,
		horizon,
	)

	if err != nil {
		log.Error(
			err,
			"Forecast provider request failed",
			logging.FieldComponent, logging.ComponentSignalHandler,
			logging.FieldOperation, "get_forecast",
			logging.FieldSignal, request.SignalType,
			logging.FieldRegion, request.Region,
			logging.FieldStatus, logging.StatusFailure,
			logging.FieldDuration, logging.SinceMillis(providerStart),
		)

		return nil, fmt.Errorf(
			"retrieve forecast signals: %w",
			err,
		)
	}

	if err := ctx.Err(); err != nil {
		log.Error(
			err,
			"Forecast request cancelled",
			logging.FieldComponent, logging.ComponentSignalHandler,
			logging.FieldOperation, "get_forecast",
			logging.FieldSignal, request.SignalType,
			logging.FieldRegion, request.Region,
			logging.FieldStatus, logging.StatusFailure,
			logging.FieldDuration, logging.SinceMillis(start),
		)

		return nil, err
	}

	if len(forecasts) == 0 {
		err := fmt.Errorf("forecast provider returned no entries")

		log.Error(
			err,
			"Forecast provider returned no data",
			logging.FieldComponent, logging.ComponentSignalHandler,
			logging.FieldOperation, "get_forecast",
			logging.FieldSignal, request.SignalType,
			logging.FieldRegion, request.Region,
			logging.FieldStatus, logging.StatusFailure,
			logging.FieldDuration, logging.SinceMillis(start),
		)

		return nil, err
	}

	now := h.now()

	for i := range forecasts {
		signal := &forecasts[i]

		if err := validateSignal(
			*signal,
			now,
			h.maxAge(request.SignalType),
		); err != nil {
			log.Error(
				err,
				"Invalid forecast signal",
				logging.FieldComponent, logging.ComponentSignalHandler,
				logging.FieldOperation, "validate_forecast",
				logging.FieldSignal, request.SignalType,
				logging.FieldRegion, request.Region,
				logging.FieldStatus, logging.StatusFailure,
				logging.FieldDuration, logging.SinceMillis(start),
			)

			return nil, fmt.Errorf(
				"invalid forecast entry %d: %w",
				i,
				err,
			)
		}

		if normalizeSignalType(signal.SignalType) != request.SignalType {
			err := fmt.Errorf(
				"forecast entry %d has wrong signal type %q",
				i,
				signal.SignalType,
			)

			log.Error(
				err,
				"Forecast signal type validation failed",
				logging.FieldComponent, logging.ComponentSignalHandler,
				logging.FieldOperation, "validate_forecast",
				logging.FieldSignal, request.SignalType,
				logging.FieldRegion, request.Region,
				logging.FieldStatus, logging.StatusFailure,
				logging.FieldDuration, logging.SinceMillis(start),
			)

			return nil, err
		}

		providerRegion := normalizeSignalRegion(signal.Region)

		if providerRegion != request.Region {
			err := fmt.Errorf(
				"forecast entry %d has wrong region %q",
				i,
				signal.Region,
			)

			log.Error(
				err,
				"Forecast signal region validation failed",
				logging.FieldComponent, logging.ComponentSignalHandler,
				logging.FieldOperation, "validate_forecast",
				logging.FieldSignal, request.SignalType,
				logging.FieldRegion, request.Region,
				logging.FieldStatus, logging.StatusFailure,
				logging.FieldDuration, logging.SinceMillis(start),
			)

			return nil, err
		}

		if !signal.IsForecast {
			err := fmt.Errorf(
				"forecast entry %d is not marked as forecast",
				i,
			)

			log.Error(
				err,
				"Forecast flag validation failed",
				logging.FieldComponent, logging.ComponentSignalHandler,
				logging.FieldOperation, "validate_forecast",
				logging.FieldSignal, request.SignalType,
				logging.FieldRegion, request.Region,
				logging.FieldStatus, logging.StatusFailure,
				logging.FieldDuration, logging.SinceMillis(start),
			)

			return nil, err
		}

		signal.SignalType = request.SignalType
		signal.Region = providerRegion
	}

	log.Info(
		"Forecast signal retrieval completed",
		logging.FieldComponent, logging.ComponentSignalHandler,
		logging.FieldOperation, "get_forecast",
		logging.FieldSignal, request.SignalType,
		logging.FieldRegion, request.Region,
		logging.FieldStatus, logging.StatusSuccess,
		logging.FieldDuration, logging.SinceMillis(start),
	)

	return forecasts, nil
}
