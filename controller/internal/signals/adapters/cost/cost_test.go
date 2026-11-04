package cost

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/suresh-ls/energyaware-k8s-admission/controller/internal/signals"
)

func TestGetCurrentSignalSuccess(t *testing.T) {
	t.Setenv("ENERGYMAP_API_KEY", "test-key")

	marketTime := time.Now().UTC().Truncate(time.Minute)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/developer/v1/market/iex/latest" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("market_type") != "DAM" {
			t.Errorf("expected DAM market type")
		}
		if r.Header.Get("X-API-Key") != "test-key" {
			t.Errorf("API key header missing")
		}

		fmt.Fprintf(w, `{
			"data": [{
				"timestamp": %q,
				"market_type": "DAM",
				"region": "All India",
				"mcp_rs_mwh": "5000",
				"source": "test"
			}],
			"meta": {"freshness_lag_minutes": 0}
		}`, marketTime.Format(time.RFC3339))
	}))
	defer server.Close()

	provider := NewProvider("test-key", server.Client(), server.URL)

	signal, err := provider.GetCurrentSignal(
		context.Background(),
		signals.SignalRequest{
			SignalType: signals.SignalTypeCost,
			Region:     "All India",
		},
	)
	if err != nil {
		t.Fatalf("GetCurrentSignal returned error: %v", err)
	}

	if signal.SignalType != signals.SignalTypeCost {
		t.Errorf("expected signal type cost, got %q", signal.SignalType)
	}
	if signal.Provider != "EnergyMap-IEX" {
		t.Errorf("unexpected provider: %q", signal.Provider)
	}
	if signal.Value != 5 {
		t.Errorf("expected 5 INR/kWh, got %f", signal.Value)
	}
	if signal.Unit != "INR/kWh" {
		t.Errorf("unexpected unit: %q", signal.Unit)
	}
	if signal.Timestamp != marketTime {
		t.Errorf("market timestamp was not preserved")
	}
	if signal.CollectedAt.IsZero() {
		t.Error("CollectedAt should be populated")
	}
	if signal.CollectedAt.Before(marketTime.Add(-time.Hour)) {
		t.Error("CollectedAt should represent retrieval time, not market time")
	}
	if !signal.ValidUntil.Equal(marketTime.Add(15 * time.Minute)) {
		t.Errorf("unexpected ValidUntil: %v", signal.ValidUntil)
	}
	if signal.IsForecast {
		t.Error("latest-price endpoint should not be marked as forecast")
	}
	if signal.Quality != "reported" {
		t.Errorf("unexpected quality: %q", signal.Quality)
	}
}

func TestGetCurrentSignalMissingAPIKey(t *testing.T) {
	provider := NewProvider("", http.DefaultClient, "http://localhost")

	_, err := provider.GetCurrentSignal(
		context.Background(),
		signals.SignalRequest{SignalType: signals.SignalTypeCost},
	)
	if err == nil {
		t.Fatal("expected missing API key error")
	}
}

func TestGetCurrentSignalHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server error", http.StatusInternalServerError)
	}))
	defer server.Close()

	provider := NewProvider("test-key", server.Client(), server.URL)

	_, err := provider.GetCurrentSignal(
		context.Background(),
		signals.SignalRequest{SignalType: signals.SignalTypeCost},
	)
	if err == nil {
		t.Fatal("expected HTTP error")
	}
}

func TestGetCurrentSignalWrongType(t *testing.T) {
	provider := NewProvider("test-key", http.DefaultClient, "")

	_, err := provider.GetCurrentSignal(
		context.Background(),
		signals.SignalRequest{SignalType: signals.SignalTypeCarbon},
	)
	if err == nil {
		t.Fatal("expected unsupported signal type error")
	}
}

func TestGetCurrentSignalEmptyRecords(t *testing.T) {
	t.Setenv("ENERGYMAP_API_KEY", "test-key")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[],"meta":{"freshness_lag_minutes":0}}`)
	}))
	defer server.Close()

	provider := NewProvider("test-key", server.Client(), server.URL)

	_, err := provider.GetCurrentSignal(
		context.Background(),
		signals.SignalRequest{SignalType: signals.SignalTypeCost},
	)
	if err == nil {
		t.Fatal("expected empty records error")
	}
}

func TestCostProviderRouterHandlerIntegration(t *testing.T) {
	marketTime := time.Now().UTC().Truncate(time.Minute)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "test-key" {
			t.Errorf("missing API key")
		}
		if r.URL.Path != "/developer/v1/market/iex/latest" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		if r.URL.Query().Get("market_type") != "DAM" {
			t.Errorf("expected DAM market type")
		}

		fmt.Fprintf(w, `{
            "data": [{
                "timestamp": %q,
                "market_type": "DAM",
                "region": "All India",
                "mcp_rs_mwh": "5000",
                "source": "test"
            }],
            "meta": {"freshness_lag_minutes": 0}
        }`, marketTime.Format(time.RFC3339))
	}))
	defer server.Close()

	costProvider := NewProvider("test-key", server.Client(), server.URL)

	router, err := signals.NewProviderRouter(
		map[string]signals.SignalProvider{
			signals.SignalTypeCost: costProvider,
		},
	)
	if err != nil {
		t.Fatalf("failed to create router: %v", err)
	}

	handler := signals.NewHandler(router)

	got, err := handler.GetSignal(
		context.Background(),
		signals.SignalRequest{
			SignalType: signals.SignalTypeCost,
			Region:     "All India",
		},
	)
	if err != nil {
		t.Fatalf("handler failed to retrieve cost signal: %v", err)
	}

	if got.Value != 5 {
		t.Errorf("expected 5 INR/kWh, got %f", got.Value)
	}

	if got.Provider != "EnergyMap-IEX" {
		t.Errorf("unexpected provider: %s", got.Provider)
	}

	if got.SignalType != signals.SignalTypeCost {
		t.Errorf("unexpected signal type: %s", got.SignalType)
	}

	if got.Region != "ALL INDIA" {
		t.Errorf("expected normalized region ALL INDIA, got %s", got.Region)
	}

	if got.IsForecast {
		t.Error("expected a current signal, not a forecast")
	}
}
