// Command demand-probe exercises the production provider through the handler.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"os/signal"
	"time"

	"github.com/suresh-ls/energyaware-k8s-admission/controller/internal/signals"
	"github.com/suresh-ls/energyaware-k8s-admission/controller/internal/signals/adapters/demand"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func run(ctx context.Context) error {
	prom := flag.String("prometheus", "http://localhost:9090", "Prometheus base URL")
	region := flag.String("region", "", "canonical monitored deployment region (required)")
	capKW := flag.Float64("cap-kw", 0, "positive demand cap in kW (required)")
	query := flag.String("query", "", "custom aggregate power expression in watts")
	timestampQuery := flag.String("timestamp-query", "", "oldest source timestamp expression; required with custom query")
	age := flag.Duration("max-sample-age", time.Minute, "maximum source scrape age")
	timeout := flag.Duration("timeout", 5*time.Second, "total provider query timeout")
	synthetic := flag.Bool("synthetic", false, "label fake-meter measurements as simulated")
	opts := zap.Options{}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	ctx = log.IntoContext(ctx, ctrl.Log.WithName("demand-probe"))
	provider, err := demand.NewProvider(demand.Config{
		PrometheusURL: *prom, Region: *region, CapKW: *capKW,
		Query: *query, TimestampQuery: *timestampQuery,
		MaxSampleAge: *age, Timeout: *timeout, Synthetic: *synthetic,
	})
	if err != nil {
		return err
	}
	router, err := signals.NewProviderRouter(map[string]signals.SignalProvider{signals.SignalTypeDemandCap: provider})
	if err != nil {
		return err
	}
	handler := signals.NewHandlerWithOptions(router, signals.HandlerOptions{
		MaxSignalAge: map[string]time.Duration{signals.SignalTypeDemandCap: *age},
	})
	result, err := handler.GetSignal(ctx, signals.SignalRequest{SignalType: signals.SignalTypeDemandCap, Region: *region})
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := run(ctx); err != nil {
		log.FromContext(context.Background()).Error(nil, "Demand probe failed", "error_kind", "command_failed")
		os.Exit(1)
	}
}
