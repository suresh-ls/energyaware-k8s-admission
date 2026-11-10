package demand

import (
	"context"
	"testing"
	"time"

	"github.com/suresh-ls/energyaware-k8s-admission/controller/internal/signals"
)

func TestGetCurrentSignalSuccess(t *testing.T) {
	provider := NewProvider()

	request := signals.SignalRequest{
		SignalType: signals.SignalTypeDemandCap,
		Region:     "IN-SO",
	}

	signal, err := provider.GetCurrentSignal(context.Background(), request)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if signal.SignalType != signals.SignalTypeDemandCap {
		t.Errorf(
			"expected signal type %q, got %q",
			signals.SignalTypeDemandCap,
			signal.SignalType,
		)
	}

	if signal.Provider != "DummyDemandCap" {
		t.Errorf(
			"expected provider %q, got %q",
			"DummyDemandCap",
			signal.Provider,
		)
	}

	if signal.Region != "IN-SO" {
		t.Errorf(
			"expected region %q, got %q",
			"IN-SO",
			signal.Region,
		)
	}

	if signal.Unit != "percent" {
		t.Errorf(
			"expected unit %q, got %q",
			"percent",
			signal.Unit,
		)
	}

	if signal.Value != 80 {
		t.Errorf(
			"expected value 80, got %v",
			signal.Value,
		)
	}

	if signal.Cap != 100 {
		t.Errorf(
			"expected cap 100, got %v",
			signal.Cap,
		)
	}

	if signal.Utilization != 80 {
		t.Errorf(
			"expected utilization 80, got %v",
			signal.Utilization,
		)
	}

	if signal.Status != "within_cap" {
		t.Errorf(
			"expected status %q, got %q",
			"within_cap",
			signal.Status,
		)
	}

	if signal.IsForecast {
		t.Error("expected signal to be non-forecast")
	}

	if signal.Quality != "simulated" {
		t.Errorf(
			"expected quality %q, got %q",
			"simulated",
			signal.Quality,
		)
	}

	if signal.Timestamp.IsZero() {
		t.Error("expected timestamp to be populated")
	}

	if signal.CollectedAt.IsZero() {
		t.Error("expected collected_at to be populated")
	}

	if signal.ValidUntil.IsZero() {
		t.Error("expected valid_until to be populated")
	}

	if !signal.ValidUntil.After(signal.Timestamp) {
		t.Error("expected valid_until to be after timestamp")
	}
}

func TestGetCurrentSignalWrongType(t *testing.T) {
	provider := NewProvider()

	request := signals.SignalRequest{
		SignalType: signals.SignalTypeCarbon,
		Region:     "IN-SO",
	}

	_, err := provider.GetCurrentSignal(context.Background(), request)

	if err == nil {
		t.Fatal("expected error for unsupported signal type")
	}
}

func TestGetCurrentSignalEmptyRegion(t *testing.T) {
	provider := NewProvider()

	request := signals.SignalRequest{
		SignalType: signals.SignalTypeDemandCap,
		Region:     "   ",
	}

	_, err := provider.GetCurrentSignal(context.Background(), request)

	if err == nil {
		t.Fatal("expected error for empty region")
	}
}

func TestDemandCapProviderRouterHandlerIntegration(t *testing.T) {
	provider := NewProvider()

	router, err := signals.NewProviderRouter(map[string]signals.SignalProvider{
		signals.SignalTypeDemandCap: provider,
	})
	if err != nil {
		t.Fatalf("failed to create provider router: %v", err)
	}

	handler := signals.NewHandler(router)

	request := signals.SignalRequest{
		SignalType: signals.SignalTypeDemandCap,
		Region:     "IN-SO",
	}

	signal, err := handler.GetSignal(context.Background(), request)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if signal.SignalType != signals.SignalTypeDemandCap {
		t.Errorf(
			"expected signal type %q, got %q",
			signals.SignalTypeDemandCap,
			signal.SignalType,
		)
	}

	if signal.Provider != "DummyDemandCap" {
		t.Errorf(
			"expected provider %q, got %q",
			"DummyDemandCap",
			signal.Provider,
		)
	}

	if signal.Region != "IN-SO" {
		t.Errorf(
			"expected region %q, got %q",
			"IN-SO",
			signal.Region,
		)
	}

	if signal.Cap != 100 {
		t.Errorf(
			"expected cap 100, got %v",
			signal.Cap,
		)
	}

	if signal.Utilization != 80 {
		t.Errorf(
			"expected utilization 80, got %v",
			signal.Utilization,
		)
	}

	if signal.Status != "within_cap" {
		t.Errorf(
			"expected status %q, got %q",
			"within_cap",
			signal.Status,
		)

	}

	if signal.Timestamp.IsZero() {
		t.Error("expected timestamp to be populated")
	}

	if signal.ValidUntil.Before(time.Now().UTC()) {
		t.Error("expected signal to still be valid")
	}
}