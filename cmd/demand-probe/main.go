// Command demand-probe queries a live Prometheus through the Kepler demand
// provider and prints the resulting DemandSignal as JSON. Use it to demo or
// debug the provider against a real cluster:
//
//	kubectl port-forward -n monitoring svc/prometheus-kube-prometheus-prometheus 9090:9090
//	go run ./cmd/demand-probe -cap 20 -unit W
//
// Exit codes: 0 within cap, 2 cap exceeded, 1 error.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"time"

	"github.com/suresh-ls/energyaware-k8s-admission/internal/logging"
	"github.com/suresh-ls/energyaware-k8s-admission/internal/provider/kepler"
)

func main() {
	var (
		level   = flag.String("log-level", "info", "diagnostic log level: debug, info, warn or error")
		promURL = flag.String("prometheus", "http://localhost:9090", "Prometheus base URL")
		query   = flag.String("query", "", "PromQL returning watts as one series (default: "+kepler.DefaultQuery+")")
		unit    = flag.String("unit", "W", "unit of the cap and of the printed value: W or kW")
		cap     = flag.Float64("cap", 0, "demand cap in the chosen unit (required, > 0)")
		timeout = flag.Duration("timeout", 5*time.Second, "request timeout")
	)
	flag.Parse()

	logger, err := logging.New(os.Stderr, *level)
	if err != nil {
		fallback, _ := logging.New(os.Stderr, "info")
		fallback.Error("Invalid log level", "error_kind", "invalid_config")
		os.Exit(1)
	}
	p, err := kepler.New(kepler.Config{
		PrometheusURL: *promURL,
		Query:         *query,
		Unit:          *unit,
		Cap:           *cap,
		Timeout:       *timeout,
		Logger:        logger,
	})
	if err != nil {
		logger.Error("Invalid demand provider configuration", "error_kind", "invalid_config")
		os.Exit(1)
	}

	ctx := context.Background()
	if st := p.GetProviderStatus(ctx); !st.Available {
		logger.Warn("Prometheus readiness check failed", "provider", "kepler")
	}

	sig, err := p.GetCurrentSignal(ctx)
	if err != nil {
		// The provider already logged its failure without exposing response data.
		os.Exit(1)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(sig); err != nil {
		logger.Error("Could not write demand signal", "error_kind", logging.ErrorKind(err))
		os.Exit(1)
	}

	if sig.IsExceeded() {
		os.Exit(2)
	}
}
