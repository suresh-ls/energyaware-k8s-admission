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
	"fmt"
	"os"
	"time"

	"github.com/suresh-ls/energyaware-k8s-admission/internal/provider/kepler"
)

func main() {
	var (
		promURL = flag.String("prometheus", "http://localhost:9090", "Prometheus base URL")
		query   = flag.String("query", "", "PromQL returning watts as one series (default: "+kepler.DefaultQuery+")")
		unit    = flag.String("unit", "W", "unit of the cap and of the printed value: W or kW")
		cap     = flag.Float64("cap", 0, "demand cap in the chosen unit (required, > 0)")
		timeout = flag.Duration("timeout", 5*time.Second, "request timeout")
	)
	flag.Parse()

	p, err := kepler.New(kepler.Config{
		PrometheusURL: *promURL,
		Query:         *query,
		Unit:          *unit,
		Cap:           *cap,
		Timeout:       *timeout,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "config error:", err)
		os.Exit(1)
	}

	ctx := context.Background()
	if st := p.GetProviderStatus(ctx); !st.Available {
		fmt.Fprintf(os.Stderr, "warning: provider status: %s\n", st.Message)
	}

	sig, err := p.GetCurrentSignal(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(sig)

	if sig.IsExceeded() {
		os.Exit(2)
	}
}
