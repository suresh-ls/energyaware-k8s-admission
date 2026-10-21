package signals

import (
	"context"
	"errors"
	"testing"
)

type routerTestProvider struct {
	signal      Signal
	err         error
	calls       int
	lastRequest SignalRequest
}

func (p *routerTestProvider) GetCurrentSignal(_ context.Context, request SignalRequest) (Signal, error) {
	p.calls++
	p.lastRequest = request
	if p.err != nil {
		return Signal{}, p.err
	}
	return p.signal, nil
}

func TestProviderRouterRoutesBySignalType(t *testing.T) {
	carbonSignal := validTestSignal()
	costSignal := validTestSignal()
	costSignal.SignalType = "cost"
	costSignal.Value = 8.5
	costSignal.Unit = "INR/kWh"
	carbonProvider := &routerTestProvider{signal: carbonSignal}
	costProvider := &routerTestProvider{signal: costSignal}
	router := NewProviderRouter(map[string]SignalProvider{"carbon": carbonProvider, "cost": costProvider})

	gotCarbon, err := router.GetCurrentSignal(context.Background(), SignalRequest{SignalType: "carbon", Region: "IN-SO"})
	if err != nil {
		t.Fatalf("unexpected error for carbon: %v", err)
	}
	if gotCarbon.SignalType != "carbon" {
		t.Errorf("expected carbon signal, got %q", gotCarbon.SignalType)
	}
	if carbonProvider.calls != 1 {
		t.Errorf("expected carbon provider called once, got %d", carbonProvider.calls)
	}
	if costProvider.calls != 0 {
		t.Errorf("cost provider should not be called for carbon request")
	}

	gotCost, err := router.GetCurrentSignal(context.Background(), SignalRequest{SignalType: "cost", Region: "IN-SO"})
	if err != nil {
		t.Fatalf("unexpected error for cost: %v", err)
	}
	if gotCost.SignalType != "cost" || gotCost.Value != 8.5 {
		t.Errorf("unexpected cost result: %+v", gotCost)
	}
	if costProvider.calls != 1 {
		t.Errorf("expected cost provider called once, got %d", costProvider.calls)
	}
}

func TestProviderRouterNormalizesSignalType(t *testing.T) {
	provider := &routerTestProvider{signal: validTestSignal()}
	router := NewProviderRouter(map[string]SignalProvider{" CARBON ": provider})
	if _, err := router.GetCurrentSignal(context.Background(), SignalRequest{SignalType: "CARBON", Region: "IN-SO"}); err != nil {
		t.Fatalf("expected normalized signal type to route successfully: %v", err)
	}
	if provider.calls != 1 {
		t.Errorf("expected provider called once, got %d", provider.calls)
	}
}

func TestProviderRouterUnknownSignalType(t *testing.T) {
	router := NewProviderRouter(map[string]SignalProvider{"carbon": &routerTestProvider{signal: validTestSignal()}})
	if _, err := router.GetCurrentSignal(context.Background(), SignalRequest{SignalType: "demand", Region: "IN-SO"}); err == nil {
		t.Fatal("expected an error for an unregistered signal type")
	}
}

func TestProviderRouterNilRegisteredProvider(t *testing.T) {
	router := NewProviderRouter(map[string]SignalProvider{"carbon": nil})
	if _, err := router.GetCurrentSignal(context.Background(), SignalRequest{SignalType: "carbon", Region: "IN-SO"}); err == nil {
		t.Fatal("expected an error for a nil provider")
	}
}

func TestProviderRouterPropagatesProviderError(t *testing.T) {
	expectedErr := errors.New("upstream provider unavailable")
	provider := &routerTestProvider{err: expectedErr}
	router := NewProviderRouter(map[string]SignalProvider{"carbon": provider})
	_, err := router.GetCurrentSignal(context.Background(), SignalRequest{SignalType: "carbon", Region: "IN-SO"})
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected provider error %v, got %v", expectedErr, err)
	}
}

func TestProviderRouterRejectsCancelledContext(t *testing.T) {
	provider := &routerTestProvider{signal: validTestSignal()}
	router := NewProviderRouter(map[string]SignalProvider{"carbon": provider})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := router.GetCurrentSignal(ctx, SignalRequest{SignalType: "carbon", Region: "IN-SO"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if provider.calls != 0 {
		t.Errorf("provider should not be called for cancelled context")
	}
}
