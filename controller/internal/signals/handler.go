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
}

func NewHandler(provider SignalProvider) *Handler {
	return &Handler{provider: provider, cache: make(map[SignalRequest]Signal)}
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

	request.SignalType = strings.ToLower(strings.TrimSpace(request.SignalType))
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
		if err := validateSignal(cached, now); err == nil {
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
	if err := validateSignal(signal, time.Now()); err != nil {
		return Signal{}, fmt.Errorf("invalid provider signal: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(signal.SignalType), request.SignalType) {
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
