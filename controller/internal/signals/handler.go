
package signals

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

type Handler struct {
	provider SignalProvider

	mu    sync.RWMutex
	cache map[SignalRequest]Signal
}

func NewHandler(provider SignalProvider) *Handler {
	return &Handler{
		provider: provider,
		cache:    make(map[SignalRequest]Signal),
	}
}

func (h *Handler) GetSignal(
	ctx context.Context,
	request SignalRequest,
) (Signal, error) {
	if h.provider == nil {
		return Signal{}, fmt.Errorf("signal provider is nil")
	}

	// Normalize whitespace in the request.
	request.SignalType = strings.TrimSpace(request.SignalType)
	request.Region = strings.TrimSpace(request.Region)

	if request.SignalType == "" {
		return Signal{}, fmt.Errorf("requested signal type is required")
	}

	if request.Region == "" {
		return Signal{}, fmt.Errorf("requested signal region is required")
	}

	// Return a valid cached signal if one exists.
	now := time.Now()

	h.mu.RLock()
	cached, found := h.cache[request]

	if found && validateSignal(cached, now) == nil {
		h.mu.RUnlock()
		return cached, nil
	}
	h.mu.RUnlock()

	// Fetch a signal when the cache is empty or expired.
	signal, err := h.provider.GetCurrentSignal(ctx, request)
	if err != nil {
		return Signal{}, fmt.Errorf("retrieve signal: %w", err)
	}

	// Validate the fetched signal before caching it.
	if err := validateSignal(signal, time.Now()); err != nil {
		return Signal{}, err
	}

	// Ensure the returned signal matches the request.
	if !strings.EqualFold(signal.SignalType, request.SignalType) {
		return Signal{}, fmt.Errorf(
			"provider returned signal type %q; requested %q",
			signal.SignalType,
			request.SignalType,
		)
	}

	if !strings.EqualFold(signal.Region, request.Region) {
		return Signal{}, fmt.Errorf(
			"provider returned region %q; requested %q",
			signal.Region,
			request.Region,
		)
	}

	// Cache the validated signal for this type and region.
	h.mu.Lock()
	h.cache[request] = signal
	h.mu.Unlock()

	return signal, nil
}

