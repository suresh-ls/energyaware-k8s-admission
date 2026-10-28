package signal

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// DefaultCacheMaxAge is the experimental maximum cache age from design doc
// 4.5.3 ("The initial design can use one hour").
const DefaultCacheMaxAge = time.Hour

var (
	// ErrNoSignal is returned when the provider failed and there is nothing
	// in the cache to fall back on (for example right after startup).
	ErrNoSignal = errors.New("no usable signal: provider failed and nothing is cached")
	// ErrSignalStale is returned by the FailClosed policy when the provider
	// failed and the cached value is older than the maximum cache age.
	ErrSignalStale = errors.New("cached signal is older than the maximum cache age")
)

// FallbackFunc is the configured fallback policy of design doc 4.5.3. It runs
// when the provider failed AND the cache cannot serve the request, that is
// when the cache is empty (stale == nil) or its entry is older than the
// maximum age (stale != nil, already marked QualityStale). cause is the
// provider error.
type FallbackFunc func(providerName string, stale *DemandSignal, cause error) (DemandSignal, error)

// ReturnStale is the default policy. If a stale value exists it is returned
// with Quality STALE and a nil error, so the Decision Engine can see the last
// known value and apply its own allow/reject rule. Callers MUST check
// IsUsable() (or Quality) and never treat the result as a fresh measurement.
// With nothing cached it returns ErrNoSignal.
func ReturnStale(providerName string, stale *DemandSignal, cause error) (DemandSignal, error) {
	if stale == nil {
		return DemandSignal{}, noSignal(providerName, cause)
	}
	return *stale, nil
}

// FailClosed returns an error whenever the cache cannot serve a usable value:
// ErrSignalStale if the cached value is too old, ErrNoSignal if there is none.
// Use it when the caller should not even see a stale demand value.
func FailClosed(providerName string, stale *DemandSignal, cause error) (DemandSignal, error) {
	if stale == nil {
		return DemandSignal{}, noSignal(providerName, cause)
	}
	return DemandSignal{}, fmt.Errorf("%w: provider %q: %w", ErrSignalStale, providerName, cause)
}

func noSignal(providerName string, cause error) error {
	return fmt.Errorf("%w: provider %q: %w", ErrNoSignal, providerName, cause)
}

// CacheConfig configures a CachedProvider.
type CacheConfig struct {
	// MaxAge is how long after the last successful fetch (CachedAt) a cached
	// value may still be served as QualityCached. Default: 1 hour.
	MaxAge time.Duration
	// Fallback runs when the cache cannot serve a usable value. Default:
	// ReturnStale.
	Fallback FallbackFunc
	// OnProviderError, if set, is called with every provider failure
	// (including invalid responses), even when the cache hides it.
	OnProviderError func(error)
	// Now overrides the clock (tests). Optional.
	Now func() time.Time
}

// CachedProvider wraps a demand provider with the caching and failure
// handling of design doc 4.5.3. It implements SignalProvider itself, so the
// Generic Signal Handler can use it in place of the raw provider.
//
// GetCurrentSignal follows the doc's getSignal pseudocode:
//
//	response = provider.getCurrentSignal()
//	valid           -> store in cache, quality FRESH, return it
//	failed:
//	  cache within MaxAge -> quality CACHED, return cached value
//	  otherwise           -> quality STALE, apply the fallback policy
//
// The provider is called on every request; the cache only serves as a
// fallback. It is safe for concurrent use.
type CachedProvider struct {
	inner SignalProvider[DemandSignal]
	cfg   CacheConfig

	mu   sync.Mutex
	last *DemandSignal // last valid signal, CachedAt set; nil until first success
}

var _ SignalProvider[DemandSignal] = (*CachedProvider)(nil)

// NewCachedProvider wraps inner. A zero MaxAge means DefaultCacheMaxAge; a
// negative MaxAge is an error.
func NewCachedProvider(inner SignalProvider[DemandSignal], cfg CacheConfig) (*CachedProvider, error) {
	if inner == nil {
		return nil, errors.New("cached provider: inner provider is required")
	}
	if cfg.MaxAge < 0 {
		return nil, fmt.Errorf("cached provider: MaxAge must not be negative, got %v", cfg.MaxAge)
	}
	if cfg.MaxAge == 0 {
		cfg.MaxAge = DefaultCacheMaxAge
	}
	if cfg.Fallback == nil {
		cfg.Fallback = ReturnStale
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &CachedProvider{inner: inner, cfg: cfg}, nil
}

// GetCurrentSignal returns a FRESH signal from the provider, or falls back to
// the cache as described on CachedProvider.
func (c *CachedProvider) GetCurrentSignal(ctx context.Context) (DemandSignal, error) {
	sig, err := c.inner.GetCurrentSignal(ctx)
	if err == nil {
		err = c.inner.Validate(sig)
	}
	if err == nil {
		return c.store(sig), nil
	}

	if c.cfg.OnProviderError != nil {
		c.cfg.OnProviderError(err)
	}

	c.mu.Lock()
	var last *DemandSignal
	if c.last != nil {
		cp := *c.last // copy: never hand out or mutate the cached entry
		last = &cp
	}
	c.mu.Unlock()

	if last != nil {
		if c.cfg.Now().Sub(last.CachedAt) <= c.cfg.MaxAge {
			last.Quality = QualityCached
			return *last, nil
		}
		last.Quality = QualityStale
	}
	return c.cfg.Fallback(c.inner.GetProviderName(), last, err)
}

// store records a valid response and returns it as FRESH. A response measured
// earlier than the one already cached (concurrent calls finishing out of
// order) is still returned but does not replace the newer cache entry.
func (c *CachedProvider) store(sig DemandSignal) DemandSignal {
	sig.Quality = QualityFresh
	sig.CachedAt = c.cfg.Now()

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.last == nil || !sig.Timestamp.Before(c.last.Timestamp) {
		stored := sig
		c.last = &stored
	}
	return sig
}

// GetForecastSignal delegates to the wrapped provider.
func (c *CachedProvider) GetForecastSignal(ctx context.Context) (DemandSignal, error) {
	return c.inner.GetForecastSignal(ctx)
}

// Validate delegates to the wrapped provider.
func (c *CachedProvider) Validate(sig DemandSignal) error { return c.inner.Validate(sig) }

// GetProviderStatus delegates to the wrapped provider.
func (c *CachedProvider) GetProviderStatus(ctx context.Context) ProviderStatus {
	return c.inner.GetProviderStatus(ctx)
}

// GetProviderName delegates to the wrapped provider.
func (c *CachedProvider) GetProviderName() string { return c.inner.GetProviderName() }
