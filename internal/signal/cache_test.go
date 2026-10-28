package signal

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

var errProvider = errors.New("prometheus unavailable")

// fakeProvider returns whatever the test sets. Validate is the real one.
type fakeProvider struct {
	mu    sync.Mutex
	sig   DemandSignal
	err   error
	calls int
}

func (f *fakeProvider) set(sig DemandSignal, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sig, f.err = sig, err
}

func (f *fakeProvider) GetCurrentSignal(context.Context) (DemandSignal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.sig, f.err
}
func (f *fakeProvider) GetForecastSignal(context.Context) (DemandSignal, error) {
	return DemandSignal{}, ErrForecastNotSupported
}
func (f *fakeProvider) Validate(s DemandSignal) error { return s.Validate() }
func (f *fakeProvider) GetProviderStatus(context.Context) ProviderStatus {
	return ProviderStatus{Name: "kepler", Available: true}
}
func (f *fakeProvider) GetProviderName() string { return "kepler" }

// fakeClock is a manually advanced clock.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// measured returns a valid signal with the given value (kW, cap 10).
func measured(value float64, at time.Time) DemandSignal {
	s := valid()
	s.Value = value
	s.Utilization = value / s.Cap
	s.Status = StatusWithinCap
	if value > s.Cap {
		s.Status = StatusExceeded
	}
	s.Timestamp = at
	s.ValidUntil = at.Add(time.Minute)
	return s
}

type fixture struct {
	t     *testing.T
	inner *fakeProvider
	clock *fakeClock
	cache *CachedProvider
}

func newFixture(t *testing.T, mutate func(*CacheConfig)) *fixture {
	t.Helper()
	f := &fixture{t: t, inner: &fakeProvider{}, clock: &fakeClock{t: t0}}
	cfg := CacheConfig{MaxAge: time.Hour, Now: f.clock.now}
	if mutate != nil {
		mutate(&cfg)
	}
	c, err := NewCachedProvider(f.inner, cfg)
	if err != nil {
		t.Fatalf("NewCachedProvider: %v", err)
	}
	f.cache = c
	return f
}

func (f *fixture) get() (DemandSignal, error) {
	f.t.Helper()
	return f.cache.GetCurrentSignal(context.Background())
}

// succeed makes the provider return a valid reading taken "now".
func (f *fixture) succeed(value float64) {
	f.inner.set(measured(value, f.clock.now()), nil)
}

func (f *fixture) fail() { f.inner.set(DemandSignal{}, errProvider) }

func TestSuccessReturnsFreshAndSetsCachedAt(t *testing.T) {
	f := newFixture(t, nil)
	f.succeed(7.5)

	got, err := f.get()
	if err != nil {
		t.Fatal(err)
	}
	if got.Quality != QualityFresh {
		t.Errorf("quality = %s, want FRESH", got.Quality)
	}
	if !got.CachedAt.Equal(t0) {
		t.Errorf("cached_at = %v, want %v", got.CachedAt, t0)
	}
	if got.Value != 7.5 || !got.IsUsable() {
		t.Errorf("unexpected signal: %+v", got)
	}
	if err := got.Validate(); err != nil {
		t.Errorf("fresh signal invalid: %v", err)
	}
}

func TestProviderCalledEveryTime(t *testing.T) {
	f := newFixture(t, nil)
	f.succeed(5)
	for i := 0; i < 3; i++ {
		if _, err := f.get(); err != nil {
			t.Fatal(err)
		}
	}
	if f.inner.calls != 3 {
		t.Errorf("provider calls = %d, want 3 (cache is a fallback, not a read-through)", f.inner.calls)
	}
}

func TestFailureWithinMaxAgeReturnsCached(t *testing.T) {
	f := newFixture(t, nil)
	f.succeed(7.5)
	first, _ := f.get()

	f.fail()
	f.clock.advance(30 * time.Minute)
	got, err := f.get()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Quality != QualityCached {
		t.Errorf("quality = %s, want CACHED", got.Quality)
	}
	if got.Value != first.Value || !got.Timestamp.Equal(first.Timestamp) || !got.CachedAt.Equal(first.CachedAt) {
		t.Errorf("cached signal changed: got %+v, first %+v", got, first)
	}
	if !got.IsUsable() {
		t.Error("CACHED signal should be usable")
	}
	if err := got.Validate(); err != nil {
		t.Errorf("cached signal invalid: %v", err)
	}
}

func TestMaxAgeBoundary(t *testing.T) {
	tests := []struct {
		name string
		age  time.Duration
		want Quality
	}{
		{"just inside", time.Hour - time.Nanosecond, QualityCached},
		{"exactly at max age is still cached", time.Hour, QualityCached},
		{"just past max age is stale", time.Hour + time.Nanosecond, QualityStale},
		{"long past max age is stale", 5 * time.Hour, QualityStale},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, nil)
			f.succeed(7.5)
			if _, err := f.get(); err != nil {
				t.Fatal(err)
			}
			f.fail()
			f.clock.advance(tc.age)
			got, err := f.get()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Quality != tc.want {
				t.Errorf("quality = %s, want %s", got.Quality, tc.want)
			}
		})
	}
}

func TestStaleIsNotUsableAndKeepsLastValue(t *testing.T) {
	f := newFixture(t, nil)
	f.succeed(7.5)
	_, _ = f.get()
	f.fail()
	f.clock.advance(2 * time.Hour)

	got, err := f.get()
	if err != nil {
		t.Fatal(err)
	}
	if got.Quality != QualityStale || got.IsUsable() {
		t.Errorf("want unusable STALE signal, got %+v", got)
	}
	if got.Value != 7.5 {
		t.Errorf("stale value = %v, want last known 7.5", got.Value)
	}
}

func TestAgeCountsFromLastSuccessNotLastFailure(t *testing.T) {
	f := newFixture(t, nil)
	f.succeed(7.5)
	_, _ = f.get()
	f.fail()

	// Serve from cache repeatedly; each serve must not reset the age.
	for i := 0; i < 5; i++ {
		f.clock.advance(15 * time.Minute)
		if _, err := f.get(); err != nil {
			t.Fatal(err)
		}
	}
	// 75 minutes since the last success: must now be stale.
	got, err := f.get()
	if err != nil {
		t.Fatal(err)
	}
	if got.Quality != QualityStale {
		t.Errorf("quality = %s, want STALE (age must count from last success)", got.Quality)
	}
}

func TestStaleDoesNotLeakIntoCacheEntry(t *testing.T) {
	// Marking a returned copy STALE or CACHED must not change what is stored.
	f := newFixture(t, nil)
	f.succeed(7.5)
	_, _ = f.get()
	f.fail()

	f.clock.advance(2 * time.Hour)
	if got, _ := f.get(); got.Quality != QualityStale {
		t.Fatalf("setup: want STALE, got %s", got.Quality)
	}
	// Provider recovers: a fresh signal, quality FRESH again.
	f.succeed(3)
	got, err := f.get()
	if err != nil {
		t.Fatal(err)
	}
	if got.Quality != QualityFresh || got.Value != 3 {
		t.Errorf("after recovery: %+v", got)
	}
	// And the new entry is what is served on the next failure.
	f.fail()
	f.clock.advance(time.Minute)
	got, _ = f.get()
	if got.Quality != QualityCached || got.Value != 3 {
		t.Errorf("after recovery then failure: %+v", got)
	}
}

func TestColdStartFailure(t *testing.T) {
	for name, fb := range map[string]FallbackFunc{
		"ReturnStale": ReturnStale,
		"FailClosed":  FailClosed,
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, func(c *CacheConfig) { c.Fallback = fb })
			f.fail()
			got, err := f.get()
			if !errors.Is(err, ErrNoSignal) {
				t.Fatalf("error = %v, want ErrNoSignal", err)
			}
			if !errors.Is(err, errProvider) {
				t.Errorf("error should wrap the provider error: %v", err)
			}
			if got != (DemandSignal{}) {
				t.Errorf("expected zero signal, got %+v", got)
			}
		})
	}
}

func TestFailClosedPolicy(t *testing.T) {
	f := newFixture(t, func(c *CacheConfig) { c.Fallback = FailClosed })
	f.succeed(7.5)
	_, _ = f.get()
	f.fail()

	// Within max age the cache still serves, whatever the policy.
	f.clock.advance(10 * time.Minute)
	if got, err := f.get(); err != nil || got.Quality != QualityCached {
		t.Fatalf("within max age: %+v, %v", got, err)
	}
	// Past max age FailClosed refuses to return the stale value.
	f.clock.advance(2 * time.Hour)
	got, err := f.get()
	if !errors.Is(err, ErrSignalStale) {
		t.Fatalf("error = %v, want ErrSignalStale", err)
	}
	if !errors.Is(err, errProvider) {
		t.Errorf("error should wrap the provider error: %v", err)
	}
	if got != (DemandSignal{}) {
		t.Errorf("expected zero signal, got %+v", got)
	}
}

func TestCustomFallbackReceivesArguments(t *testing.T) {
	var gotName string
	var gotStale *DemandSignal
	var gotCause error
	want := errors.New("policy says no")
	f := newFixture(t, func(c *CacheConfig) {
		c.Fallback = func(name string, stale *DemandSignal, cause error) (DemandSignal, error) {
			gotName, gotStale, gotCause = name, stale, cause
			return DemandSignal{}, want
		}
	})

	// cold start: stale is nil
	f.fail()
	if _, err := f.get(); !errors.Is(err, want) {
		t.Fatalf("error = %v, want policy error", err)
	}
	if gotName != "kepler" || gotStale != nil || !errors.Is(gotCause, errProvider) {
		t.Errorf("cold start args: %q %v %v", gotName, gotStale, gotCause)
	}

	// stale: stale is non-nil and already marked STALE
	f.succeed(7.5)
	_, _ = f.get()
	f.fail()
	f.clock.advance(2 * time.Hour)
	_, _ = f.get()
	if gotStale == nil || gotStale.Quality != QualityStale || gotStale.Value != 7.5 {
		t.Errorf("stale arg = %+v", gotStale)
	}
}

func TestInvalidResponseTreatedAsFailure(t *testing.T) {
	f := newFixture(t, nil)
	f.succeed(7.5)
	_, _ = f.get()

	// The provider "succeeds" but returns a signal that fails validation.
	bad := measured(5, f.clock.now())
	bad.Cap = 0
	f.inner.set(bad, nil)
	f.clock.advance(time.Minute)

	got, err := f.get()
	if err != nil {
		t.Fatal(err)
	}
	if got.Quality != QualityCached || got.Value != 7.5 {
		t.Errorf("invalid response must fall back to cache, got %+v", got)
	}
}

func TestInvalidResponseNeverCached(t *testing.T) {
	f := newFixture(t, nil)
	bad := measured(5, t0)
	bad.Unit = "MW"
	f.inner.set(bad, nil)
	if _, err := f.get(); !errors.Is(err, ErrNoSignal) {
		t.Fatalf("error = %v, want ErrNoSignal (invalid data must not be cached)", err)
	}
}

func TestOnProviderErrorCalled(t *testing.T) {
	var seen []error
	f := newFixture(t, func(c *CacheConfig) {
		c.OnProviderError = func(err error) { seen = append(seen, err) }
	})
	f.succeed(7.5)
	_, _ = f.get()
	if len(seen) != 0 {
		t.Fatalf("callback called on success: %v", seen)
	}
	f.fail()
	_, _ = f.get() // hidden by the cache, but still reported
	if len(seen) != 1 || !errors.Is(seen[0], errProvider) {
		t.Errorf("seen = %v", seen)
	}
}

func TestOlderResponseDoesNotReplaceNewerCacheEntry(t *testing.T) {
	f := newFixture(t, nil)

	newer := measured(8, t0.Add(10*time.Second))
	older := measured(2, t0)

	f.inner.set(newer, nil)
	_, _ = f.get()
	f.inner.set(older, nil) // finishes late, measured earlier
	got, err := f.get()
	if err != nil || got.Quality != QualityFresh || got.Value != 2 {
		t.Fatalf("late response should still be returned FRESH: %+v, %v", got, err)
	}

	f.fail()
	f.clock.advance(time.Minute)
	got, _ = f.get()
	if got.Value != 8 {
		t.Errorf("cache fell back to %v, want the newer measurement 8", got.Value)
	}
}

func TestNewCachedProviderConfig(t *testing.T) {
	if _, err := NewCachedProvider(nil, CacheConfig{}); err == nil {
		t.Error("nil inner accepted")
	}
	if _, err := NewCachedProvider(&fakeProvider{}, CacheConfig{MaxAge: -time.Second}); err == nil {
		t.Error("negative MaxAge accepted")
	}
	c, err := NewCachedProvider(&fakeProvider{}, CacheConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if c.cfg.MaxAge != time.Hour || c.cfg.Fallback == nil || c.cfg.Now == nil {
		t.Errorf("defaults not applied: %+v", c.cfg)
	}
}

func TestCachedProviderDelegates(t *testing.T) {
	f := newFixture(t, nil)
	if f.cache.GetProviderName() != "kepler" {
		t.Error("name not delegated")
	}
	if _, err := f.cache.GetForecastSignal(context.Background()); !errors.Is(err, ErrForecastNotSupported) {
		t.Errorf("forecast error = %v", err)
	}
	if st := f.cache.GetProviderStatus(context.Background()); !st.Available {
		t.Errorf("status not delegated: %+v", st)
	}
	if err := f.cache.Validate(measured(1, t0)); err != nil {
		t.Errorf("validate not delegated: %v", err)
	}
	if err := f.cache.Validate(DemandSignal{}); err == nil {
		t.Error("validate accepted an empty signal")
	}
}

func TestConcurrentUse(t *testing.T) {
	f := newFixture(t, nil)
	f.succeed(5)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				if (i+j)%3 == 0 {
					f.fail()
				} else {
					f.succeed(float64(j % 9))
				}
				_, _ = f.get()
				f.clock.advance(time.Millisecond)
			}
		}(i)
	}
	wg.Wait()
}
