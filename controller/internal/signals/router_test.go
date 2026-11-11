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

type forecastRouterTestProvider struct {
	forecasts []Signal
	err       error
}

func (p *forecastRouterTestProvider) GetCurrentSignal(_ context.Context, _ SignalRequest) (Signal, error) {
	return Signal{}, errors.New("current signal not implemented for forecast test provider")
}

func (p *forecastRouterTestProvider) GetForecastSignals(
	_ context.Context,
	_ SignalRequest,
	_ time.Duration,
) ([]Signal, error) {
	if p.err != nil {
		return nil, p.err
	}
	return p.forecasts, nil
}

func TestProviderRouterRoutesBySignalType(t *testing.T) {
	carbonSignal := validTestSignal()
	costSignal := validTestSignal()
	costSignal.SignalType = "cost"
	costSignal.Value = 8.5
	costSignal.Unit = "INR/kWh"

	carbonProvider := &routerTestProvider{signal: carbonSignal}
	costProvider := &routerTestProvider{signal: costSignal}

	router, err := NewProviderRouter(map[string]SignalProvider{
		"carbon": carbonProvider,
		"cost":   costProvider,
	})
	if err != nil {
		t.Fatalf("NewProviderRouter: %v", err)
	}

	gotCarbon, err := router.GetCurrentSignal(
		context.Background(),
		SignalRequest{SignalType: "carbon", Region: "IN-SO"},
	)
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

	gotCost, err := router.GetCurrentSignal(
		context.Background(),
		SignalRequest{SignalType: "cost", Region: "IN-SO"},
	)
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

func TestProviderRouterRoutesForecastSignals(t *testing.T) {
	provider := &forecastRouterTestProvider{
		forecasts: []Signal{
			{
				SignalType: "cost",
				Provider:   "EnergyMap-IEX",
				Region:     RegionAllIndia,
				Value:      5.5,
				Unit:       "INR/kWh",
				Timestamp:  time.Now(),
				ValidUntil: time.Now().Add(24 * time.Hour),
				IsForecast: true,
				Quality:    "forecast",
			},
		},
	}

	router, err := NewProviderRouter(map[string]SignalProvider{
		"cost": provider,
	})
	if err != nil {
		t.Fatalf("NewProviderRouter: %v", err)
	}

	forecasts, err := router.GetForecastSignals(
		context.Background(),
		SignalRequest{
			SignalType: "cost",
			Region:     RegionAllIndia,
		},
		24*time.Hour,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(forecasts) != 1 {
		t.Fatalf("expected 1 forecast, got %d", len(forecasts))
	}

	if forecasts[0].SignalType != "cost" {
		t.Errorf("expected cost forecast, got %q", forecasts[0].SignalType)
	}

	if forecasts[0].Provider != "EnergyMap-IEX" {
		t.Errorf("expected EnergyMap-IEX provider, got %q", forecasts[0].Provider)
	}
}

func TestProviderRouterNormalizesSignalType(t *testing.T) {
	provider := &routerTestProvider{signal: validTestSignal()}

	router, err := NewProviderRouter(map[string]SignalProvider{
		" CARBON ": provider,
	})
	if err != nil {
		t.Fatalf("NewProviderRouter: %v", err)
	}

	if _, err := router.GetCurrentSignal(
		context.Background(),
		SignalRequest{SignalType: "CARBON", Region: "IN-SO"},
	); err != nil {
		t.Fatalf("expected normalized signal type to route successfully: %v", err)
	}

	if provider.calls != 1 {
		t.Errorf("expected provider called once, got %d", provider.calls)
	}
}

func TestProviderRouterUnknownSignalType(t *testing.T) {
	router, err := NewProviderRouter(map[string]SignalProvider{
		"carbon": &routerTestProvider{signal: validTestSignal()},
	})
	if err != nil {
		t.Fatalf("NewProviderRouter: %v", err)
	}

	if _, err := router.GetCurrentSignal(
		context.Background(),
		SignalRequest{SignalType: "demand", Region: "IN-SO"},
	); err == nil {
		t.Fatal("expected an error for an unregistered signal type")
	}
}

func TestProviderRouterNilRegisteredProvider(t *testing.T) {
	_, err := NewProviderRouter(map[string]SignalProvider{
		"carbon": nil,
	})
	if err == nil {
		t.Fatal("expected nil provider registration to be rejected")
	}
}

func TestProviderRouterPropagatesProviderError(t *testing.T) {
	expectedErr := errors.New("upstream provider unavailable")
	provider := &routerTestProvider{err: expectedErr}

	router, err := NewProviderRouter(map[string]SignalProvider{
		"carbon": provider,
	})
	if err != nil {
		t.Fatalf("NewProviderRouter: %v", err)
	}

	_, err = router.GetCurrentSignal(
		context.Background(),
		SignalRequest{SignalType: "carbon", Region: "IN-SO"},
	)
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected provider error %v, got %v", expectedErr, err)
	}
}

func TestProviderRouterRejectsCancelledContext(t *testing.T) {
	provider := &routerTestProvider{signal: validTestSignal()}

	router, err := NewProviderRouter(map[string]SignalProvider{
		"carbon": provider,
	})
	if err != nil {
		t.Fatalf("NewProviderRouter: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := router.GetCurrentSignal(
		ctx,
		SignalRequest{SignalType: "carbon", Region: "IN-SO"},
	); !errors.Is(err, context.Canceled) {
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
