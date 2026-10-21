
package signals

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

type mockProvider struct {
	signal Signal
	err    error
}

func (m mockProvider) GetCurrentSignal(
	ctx context.Context,
	request SignalRequest,
) (Signal, error) {
	return m.signal, m.err
}

type countingProvider struct {
	signal Signal
	calls  int
}

func (p *countingProvider) GetCurrentSignal(
	ctx context.Context,
	request SignalRequest,
) (Signal, error) {
	p.calls++
	return p.signal, nil
}

func validTestSignal() Signal {
	now := time.Now()

	return Signal{
		SignalType: "carbon",
		Provider:   "test-provider",
		Region:     "IN-SO",
		Value:      450,
		Unit:       "gCO2/kWh",
		Timestamp:  now.Add(-time.Minute),
		ValidUntil: now.Add(time.Hour),
		IsForecast: false,
		Quality:    "reported",
	}
}

func carbonRequest() SignalRequest {
	return SignalRequest{
		SignalType: "carbon",
		Region:     "IN-SO",
	}
}

func TestHandlerGetSignalValidSignal(t *testing.T) {
	signal := validTestSignal()
	handler := NewHandler(mockProvider{signal: signal})

	got, err := handler.GetSignal(
		context.Background(),
		carbonRequest(),
	)
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

	handler := NewHandler(mockProvider{signal: signal})

	_, err := handler.GetSignal(
		context.Background(),
		carbonRequest(),
	)
	if err == nil {
		t.Fatal("expected an error for an expired signal, got nil")
	}
}

func TestHandlerGetSignalMissingProviderField(t *testing.T) {
	signal := validTestSignal()
	signal.Provider = ""

	handler := NewHandler(mockProvider{signal: signal})

	_, err := handler.GetSignal(
		context.Background(),
		carbonRequest(),
	)
	if err == nil {
		t.Fatal("expected an error for a missing provider field")
	}
}

func TestHandlerGetSignalProviderError(t *testing.T) {
	expectedErr := fmt.Errorf("API unavailable")
	handler := NewHandler(mockProvider{err: expectedErr})

	_, err := handler.GetSignal(
		context.Background(),
		carbonRequest(),
	)
	if err == nil {
		t.Fatal("expected an error when provider fails")
	}

	if !errors.Is(err, expectedErr) {
		t.Errorf("expected wrapped error %v, got %v", expectedErr, err)
	}
}

func TestHandlerGetSignalNilProvider(t *testing.T) {
	handler := NewHandler(nil)

	_, err := handler.GetSignal(
		context.Background(),
		carbonRequest(),
	)
	if err == nil {
		t.Fatal("expected an error for a nil provider")
	}
}

func TestHandlerGetSignalUsesCache(t *testing.T) {
	signal := validTestSignal()
	provider := &countingProvider{signal: signal}
	handler := NewHandler(provider)

	for i := 0; i < 2; i++ {
		_, err := handler.GetSignal(
			context.Background(),
			carbonRequest(),
		)
		if err != nil {
			t.Fatalf("call %d failed: %v", i+1, err)
		}
	}

	if provider.calls != 1 {
		t.Errorf(
			"expected provider to be called once, got %d calls",
			provider.calls,
		)
	}
}

func TestHandlerGetSignalWrongType(t *testing.T) {
	signal := validTestSignal()
	signal.SignalType = "cost"

	handler := NewHandler(mockProvider{signal: signal})

	_, err := handler.GetSignal(
		context.Background(),
		carbonRequest(),
	)
	if err == nil {
		t.Fatal("expected an error for a mismatched signal type")
	}
}

func TestHandlerGetSignalWrongRegion(t *testing.T) {
	signal := validTestSignal()
	signal.Region = "IN-WE"

	handler := NewHandler(mockProvider{signal: signal})

	_, err := handler.GetSignal(
		context.Background(),
		carbonRequest(),
	)
	if err == nil {
		t.Fatal("expected an error for a mismatched region")
	}
}


func TestHandlerSeparatesSignalsByTypeAndRegion(t *testing.T) {
	carbon := validTestSignal()

	cost := validTestSignal()
	cost.SignalType = "cost"
	cost.Value = 8.5
	cost.Unit = "INR/kWh"

	provider := &requestAwareProvider{
		signals: map[SignalRequest]Signal{
			{SignalType: "carbon", Region: "IN-SO"}: carbon,
			{SignalType: "cost", Region: "IN-SO"}:   cost,
		},
	}

	handler := NewHandler(provider)

	carbonResult, err := handler.GetSignal(
		context.Background(),
		SignalRequest{SignalType: "carbon", Region: "IN-SO"},
	)
	if err != nil {
		t.Fatalf("carbon request failed: %v", err)
	}

	costResult, err := handler.GetSignal(
		context.Background(),
		SignalRequest{SignalType: "cost", Region: "IN-SO"},
	)
	if err != nil {
		t.Fatalf("cost request failed: %v", err)
	}

	if carbonResult.SignalType != "carbon" {
		t.Errorf("expected carbon signal, got %q", carbonResult.SignalType)
	}

	if costResult.SignalType != "cost" {
		t.Errorf("expected cost signal, got %q", costResult.SignalType)
	}

	if carbonResult.Value != 450 {
		t.Errorf("expected carbon value 450, got %v", carbonResult.Value)
	}

	if costResult.Value != 8.5 {
		t.Errorf("expected cost value 8.5, got %v", costResult.Value)
	}
}

type requestAwareProvider struct {
	signals map[SignalRequest]Signal
}

func (p *requestAwareProvider) GetCurrentSignal(
	ctx context.Context,
	request SignalRequest,
) (Signal, error) {
	signal, ok := p.signals[request]
	if !ok {
		return Signal{}, fmt.Errorf(
			"no test signal for type %q and region %q",
			request.SignalType,
			request.Region,
		)
	}

	return signal, nil
}
