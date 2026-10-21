package signals

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"
)

type mockProvider struct {
	signal Signal
	err    error
}

func (m mockProvider) GetCurrentSignal(_ context.Context, _ SignalRequest) (Signal, error) {
	return m.signal, m.err
}

type countingProvider struct {
	signal Signal
	mu     sync.Mutex
	calls  int
}

func (p *countingProvider) GetCurrentSignal(_ context.Context, _ SignalRequest) (Signal, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	return p.signal, nil
}

func (p *countingProvider) CallCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func validTestSignal() Signal {
	now := time.Now()
	return Signal{
		SignalType: "carbon", Provider: "test-provider", Region: "IN-SO",
		Value: 450, Unit: "gCO2eq/kWh", Timestamp: now.Add(-time.Minute),
		ValidUntil: now.Add(time.Hour), Quality: "reported",
	}
}

func carbonRequest() SignalRequest {
	return SignalRequest{SignalType: "carbon", Region: "IN-SO"}
}

func TestHandlerGetSignalValidSignal(t *testing.T) {
	signal := validTestSignal()
	handler := NewHandler(mockProvider{signal: signal})
	got, err := handler.GetSignal(context.Background(), carbonRequest())
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if got != signal {
		t.Errorf("expected signal %+v, got %+v", signal, got)
	}
}

func TestHandlerGetSignalExpiredSignal(t *testing.T) {
	signal := validTestSignal()
	signal.Timestamp = time.Now().Add(-2 * time.Minute)
	signal.ValidUntil = time.Now().Add(-time.Minute)
	if _, err := NewHandler(mockProvider{signal: signal}).GetSignal(context.Background(), carbonRequest()); err == nil {
		t.Fatal("expected an error for an expired signal")
	}
}

func TestHandlerGetSignalMissingProviderField(t *testing.T) {
	signal := validTestSignal()
	signal.Provider = ""
	if _, err := NewHandler(mockProvider{signal: signal}).GetSignal(context.Background(), carbonRequest()); err == nil {
		t.Fatal("expected an error for a missing provider field")
	}
}

func TestHandlerGetSignalProviderError(t *testing.T) {
	expectedErr := errors.New("API unavailable")
	_, err := NewHandler(mockProvider{err: expectedErr}).GetSignal(context.Background(), carbonRequest())
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected wrapped error %v, got %v", expectedErr, err)
	}
}

func TestHandlerGetSignalNilProvider(t *testing.T) {
	if _, err := NewHandler(nil).GetSignal(context.Background(), carbonRequest()); err == nil {
		t.Fatal("expected an error for a nil provider")
	}
}

func TestHandlerGetSignalUsesCache(t *testing.T) {
	provider := &countingProvider{signal: validTestSignal()}
	handler := NewHandler(provider)
	for i := 0; i < 2; i++ {
		if _, err := handler.GetSignal(context.Background(), carbonRequest()); err != nil {
			t.Fatalf("call %d failed: %v", i+1, err)
		}
	}
	if got := provider.CallCount(); got != 1 {
		t.Errorf("expected one provider call, got %d", got)
	}
}

func TestHandlerGetSignalWrongType(t *testing.T) {
	signal := validTestSignal()
	signal.SignalType = "cost"
	if _, err := NewHandler(mockProvider{signal: signal}).GetSignal(context.Background(), carbonRequest()); err == nil {
		t.Fatal("expected an error for a mismatched signal type")
	}
}

func TestHandlerGetSignalWrongRegion(t *testing.T) {
	signal := validTestSignal()
	signal.Region = "IN-WE"
	if _, err := NewHandler(mockProvider{signal: signal}).GetSignal(context.Background(), carbonRequest()); err == nil {
		t.Fatal("expected an error for a mismatched region")
	}
}

type requestAwareProvider struct{ signals map[SignalRequest]Signal }

func (p *requestAwareProvider) GetCurrentSignal(_ context.Context, request SignalRequest) (Signal, error) {
	signal, ok := p.signals[request]
	if !ok {
		return Signal{}, fmt.Errorf("no test signal for type %q and region %q", request.SignalType, request.Region)
	}
	return signal, nil
}

func TestHandlerSeparatesSignalsByTypeAndRegion(t *testing.T) {
	carbon := validTestSignal()
	cost := validTestSignal()
	cost.SignalType = "cost"
	cost.Value = 8.5
	cost.Unit = "INR/kWh"
	provider := &requestAwareProvider{signals: map[SignalRequest]Signal{
		{SignalType: "carbon", Region: "IN-SO"}: carbon,
		{SignalType: "cost", Region: "IN-SO"}:   cost,
	}}
	handler := NewHandler(provider)
	carbonResult, err := handler.GetSignal(context.Background(), carbonRequest())
	if err != nil {
		t.Fatalf("carbon request failed: %v", err)
	}
	costResult, err := handler.GetSignal(context.Background(), SignalRequest{SignalType: "cost", Region: "IN-SO"})
	if err != nil {
		t.Fatalf("cost request failed: %v", err)
	}
	if carbonResult.Value != 450 || carbonResult.SignalType != "carbon" {
		t.Errorf("unexpected carbon result: %+v", carbonResult)
	}
	if costResult.Value != 8.5 || costResult.SignalType != "cost" {
		t.Errorf("unexpected cost result: %+v", costResult)
	}
}

func TestHandlerCacheNormalizesRequest(t *testing.T) {
	provider := &countingProvider{signal: validTestSignal()}
	handler := NewHandler(provider)
	requests := []SignalRequest{
		{SignalType: "carbon", Region: "IN-SO"},
		{SignalType: " CARBON ", Region: "in-so"},
		{SignalType: "CARBON", Region: "In-So"},
	}
	for _, request := range requests {
		if _, err := handler.GetSignal(context.Background(), request); err != nil {
			t.Fatalf("GetSignal(%+v) returned error: %v", request, err)
		}
	}
	if got := provider.CallCount(); got != 1 {
		t.Errorf("expected one provider call, got %d", got)
	}
}

func TestHandlerRefreshesExpiredCachedSignal(t *testing.T) {
	provider := &countingProvider{signal: validTestSignal()}
	handler := NewHandler(provider)
	request := carbonRequest()
	if _, err := handler.GetSignal(context.Background(), request); err != nil {
		t.Fatalf("first GetSignal failed: %v", err)
	}
	if got := provider.CallCount(); got != 1 {
		t.Fatalf("expected one provider call, got %d", got)
	}

	handler.mu.Lock()
	cached := handler.cache[request]
	cached.ValidUntil = time.Now().Add(-time.Minute)
	handler.cache[request] = cached
	handler.mu.Unlock()

	if _, err := handler.GetSignal(context.Background(), request); err != nil {
		t.Fatalf("GetSignal after cache expiry failed: %v", err)
	}
	if got := provider.CallCount(); got != 2 {
		t.Errorf("expected two provider calls, got %d", got)
	}
}

func TestHandlerRejectsNonFiniteSignalValue(t *testing.T) {
	tests := []struct {
		name  string
		value float64
	}{
		{name: "NaN", value: math.NaN()},
		{name: "positive infinity", value: math.Inf(1)},
		{name: "negative infinity", value: math.Inf(-1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &countingProvider{signal: validTestSignal()}
			provider.signal.Value = tt.value
			if _, err := NewHandler(provider).GetSignal(context.Background(), carbonRequest()); err == nil {
				t.Fatal("expected invalid signal value to be rejected")
			}
			if got := provider.CallCount(); got != 1 {
				t.Errorf("expected one provider call, got %d", got)
			}
		})
	}
}

func TestHandlerDoesNotCacheInvalidSignal(t *testing.T) {
	signal := validTestSignal()
	signal.Unit = ""
	handler := NewHandler(&countingProvider{signal: signal})
	request := carbonRequest()
	if _, err := handler.GetSignal(context.Background(), request); err == nil {
		t.Fatal("expected invalid signal to be rejected")
	}
	handler.mu.RLock()
	_, found := handler.cache[request]
	handler.mu.RUnlock()
	if found {
		t.Fatal("invalid signal must not be cached")
	}
}

type contextCheckingProvider struct{}

func (p *contextCheckingProvider) GetCurrentSignal(ctx context.Context, _ SignalRequest) (Signal, error) {
	if err := ctx.Err(); err != nil {
		return Signal{}, err
	}
	return validTestSignal(), nil
}

func TestHandlerPassesCancelledContextToProvider(t *testing.T) {
	handler := NewHandler(&contextCheckingProvider{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := handler.GetSignal(ctx, carbonRequest()); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestHandlerRejectsAlreadyCancelledContextWithCachedSignal(t *testing.T) {
	handler := NewHandler(&countingProvider{signal: validTestSignal()})
	if _, err := handler.GetSignal(context.Background(), carbonRequest()); err != nil {
		t.Fatalf("initial request failed: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := handler.GetSignal(ctx, carbonRequest()); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestHandlerConcurrentGetSignal(t *testing.T) {
	handler := NewHandler(&countingProvider{signal: validTestSignal()})
	const workers = 20
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := handler.GetSignal(context.Background(), carbonRequest()); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent GetSignal failed: %v", err)
	}
}

type signalMapProvider struct {
	signals map[string]Signal
	errors  map[string]error
	calls   map[string]int
}

func (p *signalMapProvider) GetCurrentSignal(_ context.Context, request SignalRequest) (Signal, error) {
	if p.calls == nil {
		p.calls = make(map[string]int)
	}
	p.calls[request.SignalType]++
	if err := p.errors[request.SignalType]; err != nil {
		return Signal{}, err
	}
	signal, ok := p.signals[request.SignalType]
	if !ok {
		return Signal{}, fmt.Errorf("no test signal for %q", request.SignalType)
	}
	return signal, nil
}

func TestHandlerGetSignalsReturnsAllThreeSignals(t *testing.T) {
	carbon := validTestSignal()
	cost := validTestSignal()
	cost.SignalType, cost.Value, cost.Unit = SignalTypeCost, 8.5, "INR/kWh"
	demand := validTestSignal()
	demand.SignalType, demand.Value, demand.Unit = SignalTypeDemandCap, 100, "kW"
	provider := &signalMapProvider{signals: map[string]Signal{
		SignalTypeCarbon: carbon, SignalTypeCost: cost, SignalTypeDemandCap: demand,
	}}
	handler := NewHandler(provider)

	got, err := handler.GetSignals(context.Background(), "in-so")
	if err != nil {
		t.Fatalf("GetSignals returned error: %v", err)
	}
	if got.Carbon.SignalType != SignalTypeCarbon || got.Carbon.Value != carbon.Value {
		t.Errorf("unexpected carbon signal: %+v", got.Carbon)
	}
	if got.Cost.SignalType != SignalTypeCost || got.Cost.Value != cost.Value {
		t.Errorf("unexpected cost signal: %+v", got.Cost)
	}
	if got.DemandCap.SignalType != SignalTypeDemandCap || got.DemandCap.Value != demand.Value {
		t.Errorf("unexpected demand-cap signal: %+v", got.DemandCap)
	}
	if len(provider.calls) != 3 {
		t.Errorf("expected all three provider calls, got %d", len(provider.calls))
	}
}

func TestHandlerGetSignalsFailsWithoutReturningPartialSet(t *testing.T) {
	carbon := validTestSignal()
	cost := validTestSignal()
	cost.SignalType = SignalTypeCost
	demand := validTestSignal()
	demand.SignalType = SignalTypeDemandCap
	expectedErr := errors.New("cost provider unavailable")
	provider := &signalMapProvider{
		signals: map[string]Signal{SignalTypeCarbon: carbon, SignalTypeDemandCap: demand},
		errors:  map[string]error{SignalTypeCost: expectedErr},
	}
	got, err := NewHandler(provider).GetSignals(context.Background(), "IN-SO")
	if err == nil {
		t.Fatal("expected combined retrieval to fail")
	}
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected wrapped provider error, got %v", err)
	}
	if got != (SignalSet{}) {
		t.Fatalf("expected empty result on partial failure, got %+v", got)
	}
}

func TestHandlerGetSignalsRejectsEmptyRegion(t *testing.T) {
	if _, err := NewHandler(&signalMapProvider{}).GetSignals(context.Background(), "  "); err == nil {
		t.Fatal("expected empty region to be rejected")
	}
}
