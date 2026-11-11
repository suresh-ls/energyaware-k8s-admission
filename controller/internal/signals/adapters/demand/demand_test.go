package demand

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/suresh-ls/energyaware-k8s-admission/controller/internal/signals"
)

const testRegion = "IN-SO"
const testURL = "http://host"

func vector(at time.Time, value string) string {
	return fmt.Sprintf(`{"status":"success","data":{"resultType":"vector","result":[{"value":[%d,%q]}]}}`, at.Unix(), value)
}

func TestProviderRouterHandler(t *testing.T) {
	for _, tc := range []struct {
		name, watts, status string
		utilization         float64
	}{
		{"below", "7500", StatusWithinCap, .75}, {"equal", "10000", StatusWithinCap, 1}, {"above", "12000", StatusExceeded, 1.2}, {"zero", "0", StatusWithinCap, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Second)
			observed := now.Add(-15 * time.Second)
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/api/v1/query" {
					t.Errorf("wrong path: %s", r.URL.Path)
				}
				if r.URL.Query().Get("time") == "" {
					t.Error("missing fixed evaluation instant")
				}
				value := tc.watts
				if r.URL.Query().Get("query") == DefaultTimestampQuery {
					value = fmt.Sprint(observed.Unix())
				}
				_, _ = fmt.Fprint(w, vector(now, value))
			}))
			defer server.Close()
			provider, err := NewProvider(Config{PrometheusURL: server.URL, Region: testRegion, CapKW: 10, Now: func() time.Time { return now }})
			if err != nil {
				t.Fatal(err)
			}
			router, err := signals.NewProviderRouter(map[string]signals.SignalProvider{signals.SignalTypeDemandCap: provider})
			if err != nil {
				t.Fatal(err)
			}
			handler := signals.NewHandler(router)
			request := signals.SignalRequest{SignalType: "demand", Region: "in-so"}
			got, err := handler.GetSignal(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			if got.Provider != "kepler" || got.Unit != "kW" || got.Region != testRegion || got.Status != tc.status || math.Abs(got.Utilization-tc.utilization) > 1e-9 {
				t.Fatalf("bad signal: %+v", got)
			}
			if !got.Timestamp.Equal(observed) || !got.CollectedAt.Equal(now) || !got.ValidUntil.Equal(observed.Add(time.Minute)) {
				t.Fatalf("timestamp lost: %+v", got)
			}
			cached, cacheErr := handler.GetSignal(context.Background(), request)
			if cached.Quality != "CACHED" {
				t.Fatalf("cache quality: %s", cached.Quality)
			}
			if cacheErr != nil || calls != 2 {
				t.Fatalf("handler cache failed: calls=%d, %v", calls, err)
			}
		})
	}
}

func TestProviderErrors(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		code       int
	}{
		{"HTTP error", "", 503}, {"bad JSON", "oops", 200},
		{"empty", `{"status":"success","data":{"resultType":"vector","result":[]}}`, 200},
		{"malformed", `{"status":"success","data":{"resultType":"vector","result":[{"value":[1]}]}}`, 200},
		{"NaN", vector(time.Now(), "NaN"), 200}, {"negative", vector(time.Now(), "-1"), 200},
		{"multiple", `{"status":"success","data":{"resultType":"vector","result":[{},{}]}}`, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.code); _, _ = fmt.Fprint(w, tc.body) }))
			defer server.Close()
			p, err := NewProvider(Config{PrometheusURL: server.URL, Region: testRegion, CapKW: 10})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = p.GetCurrentSignal(context.Background(), signals.SignalRequest{SignalType: signals.SignalTypeDemandCap, Region: testRegion}); err == nil {
				t.Fatal("accepted invalid response")
			}
		})
	}
}

func TestExpiryOutageRecovery(t *testing.T) {
	var mu sync.Mutex
	now := time.Now().UTC().Truncate(time.Second)
	observed := now.Add(-15 * time.Second)
	down := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if down {
			w.WriteHeader(503)
			return
		}
		value := "7500"
		if r.URL.Query().Get("query") == DefaultTimestampQuery {
			value = fmt.Sprint(observed.Unix())
		}
		_, _ = fmt.Fprint(w, vector(now, value))
	}))
	defer server.Close()
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	p, _ := NewProvider(Config{PrometheusURL: server.URL, Region: testRegion, CapKW: 10, Now: clock})
	router, _ := signals.NewProviderRouter(map[string]signals.SignalProvider{signals.SignalTypeDemandCap: p})
	handler := signals.NewHandlerWithOptions(router, signals.HandlerOptions{Now: clock})
	request := signals.SignalRequest{SignalType: signals.SignalTypeDemandCap, Region: testRegion}
	if _, err := handler.GetSignal(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	now = now.Add(time.Minute)
	mu.Unlock()
	if _, err := handler.GetSignal(context.Background(), request); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expected expiry: %v", err)
	}
	mu.Lock()
	down = true
	mu.Unlock()
	if _, err := handler.GetSignal(context.Background(), request); err == nil {
		t.Fatal("outage hidden")
	}
	mu.Lock()
	down = false
	observed = now.Add(-time.Second)
	mu.Unlock()
	if _, err := handler.GetSignal(context.Background(), request); err != nil {
		t.Fatalf("recovery failed: %v", err)
	}
}

func TestConfigAndRequestValidation(t *testing.T) {
	for _, cfg := range []Config{
		{}, {PrometheusURL: "ftp://host", Region: testRegion, CapKW: 10},
		{PrometheusURL: testURL, CapKW: 10}, {PrometheusURL: testURL, Region: testRegion},
		{PrometheusURL: testURL, Region: testRegion, CapKW: math.NaN()},
		{PrometheusURL: testURL, Region: testRegion, CapKW: 10, Query: "custom"},
		{PrometheusURL: testURL, Region: testRegion, CapKW: 10, MaxSampleAge: -time.Second},
	} {
		if _, err := NewProvider(cfg); err == nil {
			t.Fatalf("accepted %+v", cfg)
		}
	}
	p, _ := NewProvider(Config{PrometheusURL: testURL, Region: testRegion, CapKW: 10})
	for _, req := range []signals.SignalRequest{{SignalType: "carbon", Region: testRegion}, {SignalType: "demand_cap", Region: "OTHER"}} {
		if _, err := p.GetCurrentSignal(context.Background(), req); err == nil {
			t.Fatal("accepted invalid request")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.GetCurrentSignal(ctx, signals.SignalRequest{SignalType: "demand_cap", Region: testRegion}); err == nil {
		t.Fatal("ignored cancellation")
	}
}
