package signals

import (
	"context"
	"errors"
	"testing"
	"time"
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
	router, err := NewProviderRouter(map[string]SignalProvider{"carbon": carbonProvider, "cost": costProvider})
	if err != nil {
		t.Fatalf("NewProviderRouter: %v", err)
	}

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
	router, err := NewProviderRouter(map[string]SignalProvider{" CARBON ": provider})
	if err != nil {
		t.Fatalf("NewProviderRouter: %v", err)
	}
	if _, err := router.GetCurrentSignal(context.Background(), SignalRequest{SignalType: "CARBON", Region: "IN-SO"}); err != nil {
		t.Fatalf("expected normalized signal type to route successfully: %v", err)
	}
	if provider.calls != 1 {
		t.Errorf("expected provider called once, got %d", provider.calls)
	}
}

func TestProviderRouterUnknownSignalType(t *testing.T) {
	router, err := NewProviderRouter(map[string]SignalProvider{"carbon": &routerTestProvider{signal: validTestSignal()}})
	if err != nil {
		t.Fatalf("NewProviderRouter: %v", err)
	}
	if _, err := router.GetCurrentSignal(context.Background(), SignalRequest{SignalType: "demand", Region: "IN-SO"}); err == nil {
		t.Fatal("expected an error for an unregistered signal type")
	}
}

func TestProviderRouterNilRegisteredProvider(t *testing.T) {
	_, err := NewProviderRouter(map[string]SignalProvider{"carbon": nil})
	if err == nil {
		t.Fatal("expected nil provider registration to be rejected")
	}
}

func TestProviderRouterPropagatesProviderError(t *testing.T) {
	expectedErr := errors.New("upstream provider unavailable")
	provider := &routerTestProvider{err: expectedErr}
	router, err := NewProviderRouter(map[string]SignalProvider{"carbon": provider})
	if err != nil {
		t.Fatalf("NewProviderRouter: %v", err)
	}
	_, err = router.GetCurrentSignal(context.Background(), SignalRequest{SignalType: "carbon", Region: "IN-SO"})
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected provider error %v, got %v", expectedErr, err)
	}
}

func TestProviderRouterRejectsCancelledContext(t *testing.T) {
	provider := &routerTestProvider{signal: validTestSignal()}
	router, err := NewProviderRouter(map[string]SignalProvider{"carbon": provider})
	if err != nil {
		t.Fatalf("NewProviderRouter: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := router.GetCurrentSignal(ctx, SignalRequest{SignalType: "carbon", Region: "IN-SO"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if provider.calls != 0 {
		t.Errorf("provider should not be called for cancelled context")
	}
}

func TestProviderRouterRejectsDuplicateNormalizedKeys(t *testing.T) {
	first := &routerTestProvider{signal: validTestSignal()}
	second := &routerTestProvider{signal: validTestSignal()}
	_, err := NewProviderRouter(map[string]SignalProvider{
		"carbon":   first,
		" CARBON ": second,
	})
	if err == nil {
		t.Fatal("expected duplicate normalized keys to be rejected")
	}
}

type forecastRouterTestProvider struct {
	routerTestProvider
	forecasts     []Signal
	forecastCalls int
	lastHorizon   time.Duration
	forecastErr   error
}

func (p *forecastRouterTestProvider) GetForecastSignals(
	_ context.Context,
	request SignalRequest,
	horizon time.Duration,
) ([]Signal, error) {
	p.forecastCalls++
	p.lastRequest = request
	p.lastHorizon = horizon

	if p.forecastErr != nil {
		return nil, p.forecastErr
	}

	return p.forecasts, nil
}

func TestProviderRouterRoutesForecastSignals(t *testing.T) {
	forecast := validTestSignal()
	forecast.SignalType = SignalTypeCost
	forecast.IsForecast = true
	forecast.Value = 5.555
	forecast.Unit = "INR/kWh"

	provider := &forecastRouterTestProvider{
		forecasts: []Signal{forecast},
	}

	router, err := NewProviderRouter(map[string]SignalProvider{
		"cost": provider,
	})
	if err != nil {
		t.Fatalf("NewProviderRouter: %v", err)
	}

	horizon := 48 * time.Hour
	got, err := router.GetForecastSignals(
		context.Background(),
		SignalRequest{
			SignalType: " COST ",
			Region:     "IN",
		},
		horizon,
	)

	if err != nil {
		t.Fatalf("unexpected forecast routing error: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("expected 1 forecast, got %d", len(got))
	}

	if !got[0].IsForecast {
		t.Error("expected returned signal to be marked as a forecast")
	}

	if got[0].Value != 5.555 {
		t.Errorf("expected value 5.555, got %f", got[0].Value)
	}

	if provider.forecastCalls != 1 {
		t.Errorf("expected forecast provider called once, got %d", provider.forecastCalls)
	}

	if provider.lastHorizon != horizon {
		t.Errorf("expected horizon %v, got %v", horizon, provider.lastHorizon)
	}

	if provider.lastRequest.SignalType != SignalTypeCost {
		t.Errorf("expected normalized signal type %q, got %q",
			SignalTypeCost, provider.lastRequest.SignalType)
	}
}
