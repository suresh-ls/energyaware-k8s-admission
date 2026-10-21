package kepler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/suresh-ls/energyaware-k8s-admission/internal/signal"
)

var fixedNow = time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC)

// vectorBody builds a Prometheus instant-query response with one sample per
// value string.
func vectorBody(values ...string) string {
	var results []string
	for i, v := range values {
		results = append(results,
			fmt.Sprintf(`{"metric":{"n":"%d"},"value":[1760000000.123,%q]}`, i, v))
	}
	return fmt.Sprintf(`{"status":"success","data":{"resultType":"vector","result":[%s]}}`,
		strings.Join(results, ","))
}

// newServer starts a fake Prometheus. handler receives query requests;
// /-/ready is answered by ready.
func newServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/query", handler)
	mux.HandleFunc("/-/ready", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Prometheus Server is Ready.\n"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func respond(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

func newProvider(t *testing.T, url string, mutate func(*Config)) *Provider {
	t.Helper()
	cfg := Config{
		PrometheusURL: url,
		Unit:          signal.UnitKilowatt,
		Cap:           10,
		Now:           func() time.Time { return fixedNow },
	}
	if mutate != nil {
		mutate(&cfg)
	}
	p, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func TestGetCurrentSignal(t *testing.T) {
	tests := []struct {
		name       string
		promValue  string // watts, as Prometheus returns it
		unit       string
		cap        float64
		wantValue  float64
		wantStatus signal.DemandStatus
	}{
		{"design doc example: 7500 W = 7.5 kW of 10 kW", "7500", signal.UnitKilowatt, 10, 7.5, signal.StatusWithinCap},
		{"over cap in kW", "12000", signal.UnitKilowatt, 10, 12, signal.StatusExceeded},
		{"exactly at cap", "10000", signal.UnitKilowatt, 10, 10, signal.StatusWithinCap},
		{"unit W is not converted", "42.5", signal.UnitWatt, 50, 42.5, signal.StatusWithinCap},
		{"unit W over cap", "55", signal.UnitWatt, 50, 55, signal.StatusExceeded},
		{"zero power", "0", signal.UnitWatt, 50, 0, signal.StatusWithinCap},
		{"scientific notation (fake meter scale)", "8.7e-05", signal.UnitWatt, 1e-4, 8.7e-5, signal.StatusWithinCap},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := newServer(t, respond(vectorBody(tc.promValue)))
			p := newProvider(t, srv.URL, func(c *Config) { c.Unit, c.Cap = tc.unit, tc.cap })

			sig, err := p.GetCurrentSignal(context.Background())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if diff := sig.Value - tc.wantValue; diff > 1e-12 || diff < -1e-12 {
				t.Errorf("value = %v, want %v", sig.Value, tc.wantValue)
			}
			if sig.Status != tc.wantStatus {
				t.Errorf("status = %s, want %s", sig.Status, tc.wantStatus)
			}
			if sig.Provider != "kepler" || sig.SignalType != "demand" {
				t.Errorf("identity fields wrong: %+v", sig)
			}
			if sig.Unit != tc.unit || sig.Cap != tc.cap {
				t.Errorf("unit/cap wrong: %+v", sig)
			}
			if sig.Quality != signal.QualityFresh {
				t.Errorf("quality = %s, want FRESH", sig.Quality)
			}
			if !sig.Timestamp.Equal(fixedNow) {
				t.Errorf("timestamp = %v, want %v", sig.Timestamp, fixedNow)
			}
			if want := fixedNow.Add(DefaultValidFor); !sig.ValidUntil.Equal(want) {
				t.Errorf("valid_until = %v, want %v", sig.ValidUntil, want)
			}
			if err := sig.Validate(); err != nil {
				t.Errorf("returned signal fails Validate: %v", err)
			}
		})
	}
}

func TestGetCurrentSignalSendsConfiguredQuery(t *testing.T) {
	var gotQuery, gotMethod string
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("query")
		gotMethod = r.Method
		_, _ = w.Write([]byte(vectorBody("1000")))
	})

	// default query
	p := newProvider(t, srv.URL, nil)
	if _, err := p.GetCurrentSignal(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gotQuery != DefaultQuery {
		t.Errorf("default query = %q, want %q", gotQuery, DefaultQuery)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %s, want GET", gotMethod)
	}

	// custom query, including characters that need URL encoding
	custom := `sum(kepler_node_cpu_watts{zone="package-0", node_name=~"a|b"})`
	p = newProvider(t, srv.URL, func(c *Config) { c.Query = custom })
	if _, err := p.GetCurrentSignal(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gotQuery != custom {
		t.Errorf("custom query = %q, want %q", gotQuery, custom)
	}
}

func TestGetCurrentSignalValidForConfigurable(t *testing.T) {
	srv := newServer(t, respond(vectorBody("1000")))
	p := newProvider(t, srv.URL, func(c *Config) { c.ValidFor = 5 * time.Minute })
	sig, err := p.GetCurrentSignal(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := fixedNow.Add(5 * time.Minute); !sig.ValidUntil.Equal(want) {
		t.Errorf("valid_until = %v, want %v", sig.ValidUntil, want)
	}
}

func TestGetCurrentSignalErrors(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr error
	}{
		{"empty result (no data)", respond(`{"status":"success","data":{"resultType":"vector","result":[]}}`), ErrNoData},
		{"two series", respond(vectorBody("1", "2")), ErrMultipleSeries},
		{"NaN value", respond(vectorBody("NaN")), ErrMalformedResponse},
		{"+Inf value", respond(vectorBody("+Inf")), ErrMalformedResponse},
		{"non-numeric value", respond(vectorBody("abc")), ErrMalformedResponse},
		{"not JSON", respond(`<html>oops</html>`), ErrMalformedResponse},
		{"empty body", respond(``), ErrMalformedResponse},
		{"status error from prometheus", respond(`{"status":"error","errorType":"bad_data","error":"parse error"}`), ErrMalformedResponse},
		{"matrix result type", respond(`{"status":"success","data":{"resultType":"matrix","result":[]}}`), ErrMalformedResponse},
		{"sample missing value", respond(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1]}]}}`), ErrMalformedResponse},
		{"sample value not a string", respond(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1,5]}]}}`), ErrMalformedResponse},
		{"HTTP 500", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		}, ErrPrometheusUnavailable},
		{"HTTP 503", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
		}, ErrPrometheusUnavailable},
		{"negative power", respond(vectorBody("-5")), signal.ErrInvalidDemand},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := newServer(t, tc.handler)
			p := newProvider(t, srv.URL, nil)
			sig, err := p.GetCurrentSignal(context.Background())
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
			if sig != (signal.DemandSignal{}) {
				t.Errorf("expected zero signal on error, got %+v", sig)
			}
		})
	}
}

func TestGetCurrentSignalPrometheusDown(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // nothing is listening any more

	p := newProvider(t, url, nil)
	_, err := p.GetCurrentSignal(context.Background())
	if !errors.Is(err, ErrPrometheusUnavailable) {
		t.Fatalf("error = %v, want ErrPrometheusUnavailable", err)
	}
}

func TestGetCurrentSignalTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	defer close(release)

	p := newProvider(t, srv.URL, func(c *Config) { c.Timeout = 50 * time.Millisecond })
	start := time.Now()
	_, err := p.GetCurrentSignal(context.Background())
	if !errors.Is(err, ErrPrometheusUnavailable) {
		t.Fatalf("error = %v, want ErrPrometheusUnavailable", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("timeout not enforced, took %v", time.Since(start))
	}
}

func TestGetCurrentSignalContextCancelled(t *testing.T) {
	srv := newServer(t, respond(vectorBody("1000")))
	p := newProvider(t, srv.URL, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.GetCurrentSignal(ctx); !errors.Is(err, ErrPrometheusUnavailable) {
		t.Fatalf("error = %v, want ErrPrometheusUnavailable", err)
	}
}

func TestGetForecastSignalNotSupported(t *testing.T) {
	p := newProvider(t, "http://localhost:9090", nil)
	_, err := p.GetForecastSignal(context.Background())
	if !errors.Is(err, signal.ErrForecastNotSupported) {
		t.Fatalf("error = %v, want ErrForecastNotSupported", err)
	}
}

func TestGetProviderName(t *testing.T) {
	p := newProvider(t, "http://localhost:9090", nil)
	if got := p.GetProviderName(); got != "kepler" {
		t.Errorf("name = %q, want kepler", got)
	}
}

func TestGetProviderStatus(t *testing.T) {
	t.Run("ready", func(t *testing.T) {
		srv := newServer(t, respond(vectorBody("1")))
		st := newProvider(t, srv.URL, nil).GetProviderStatus(context.Background())
		if !st.Available || st.Name != "kepler" || !st.CheckedAt.Equal(fixedNow) {
			t.Errorf("unexpected status: %+v", st)
		}
	})
	t.Run("not ready (503)", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/-/ready", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()
		st := newProvider(t, srv.URL, nil).GetProviderStatus(context.Background())
		if st.Available || !strings.Contains(st.Message, "503") {
			t.Errorf("unexpected status: %+v", st)
		}
	})
	t.Run("unreachable", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		url := srv.URL
		srv.Close()
		st := newProvider(t, url, nil).GetProviderStatus(context.Background())
		if st.Available || st.Message == "" {
			t.Errorf("unexpected status: %+v", st)
		}
	})
}

func TestValidate(t *testing.T) {
	srv := newServer(t, respond(vectorBody("7500")))
	p := newProvider(t, srv.URL, nil)
	good, err := p.GetCurrentSignal(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Validate(good); err != nil {
		t.Errorf("good signal rejected: %v", err)
	}

	other := good
	other.Provider = "electricitymaps"
	if err := p.Validate(other); err == nil {
		t.Error("signal from another provider accepted")
	}

	bad := good
	bad.Cap = 0
	if err := p.Validate(bad); err == nil {
		t.Error("invalid signal accepted")
	}
}

func TestNewConfigErrors(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"missing url", func(c *Config) { c.PrometheusURL = "" }},
		{"blank url", func(c *Config) { c.PrometheusURL = "   " }},
		{"url without scheme", func(c *Config) { c.PrometheusURL = "localhost:9090" }},
		{"url without host", func(c *Config) { c.PrometheusURL = "http://" }},
		{"zero cap", func(c *Config) { c.Cap = 0 }},
		{"negative cap", func(c *Config) { c.Cap = -1 }},
		{"unsupported unit", func(c *Config) { c.Unit = "MW" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{PrometheusURL: "http://localhost:9090", Unit: signal.UnitKilowatt, Cap: 10}
			tc.mutate(&cfg)
			if _, err := New(cfg); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("error = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestNewAppliesDefaults(t *testing.T) {
	p, err := New(Config{PrometheusURL: "http://localhost:9090", Cap: 10})
	if err != nil {
		t.Fatal(err)
	}
	if p.cfg.Query != DefaultQuery || p.cfg.Unit != signal.UnitKilowatt ||
		p.cfg.Timeout != DefaultTimeout || p.cfg.ValidFor != DefaultValidFor {
		t.Errorf("defaults not applied: %+v", p.cfg)
	}
}

func TestBaseURLWithTrailingSlashAndPath(t *testing.T) {
	var gotPath string
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(vectorBody("1000")))
	})
	p := newProvider(t, srv.URL+"/", nil)
	if _, err := p.GetCurrentSignal(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/v1/query" {
		t.Errorf("path = %q, want /api/v1/query", gotPath)
	}
}
