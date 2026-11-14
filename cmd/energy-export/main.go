// Command energy-export writes a JSON prediction dataset to stdout.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/suresh-ls/energyaware-k8s-admission/internal/history"
	"github.com/suresh-ls/energyaware-k8s-admission/internal/logging"
)

func run(ctx context.Context, args []string, out io.Writer) error {
	return runWithDiagnostics(ctx, args, out, os.Stderr)
}

func runWithDiagnostics(ctx context.Context, args []string, out, diagnostics io.Writer) error {
	flags := flag.NewFlagSet("energy-export", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	input := flags.String("workloads", "", "JSON array of completed workload metadata (required)")
	prom := flags.String("prometheus", "http://localhost:9090", "Prometheus base URL")
	allowEdges := flags.Bool("allow-edge-estimates", false, "estimate at most one missing step per boundary using nearest observed power")
	level := flags.String("log-level", "info", "diagnostic log level: debug, info, warn or error")
	step := flags.Duration("step", 15*time.Second, "maximum integration step")
	if err := flags.Parse(args); err != nil {
		return err
	}
	logger, err := logging.New(diagnostics, *level)
	if err != nil {
		return err
	}
	if *input == "" {
		return fmt.Errorf("-workloads is required")
	}
	file, err := os.Open(*input)
	if err != nil {
		return err
	}
	defer file.Close()
	var workloads []history.Workload
	decoder := json.NewDecoder(io.LimitReader(file, 8<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&workloads); err != nil {
		return err
	}
	if len(workloads) == 0 {
		return fmt.Errorf("workload list must not be empty")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("unexpected data after workload list")
	}
	exporter := history.Exporter{PrometheusURL: *prom, Step: *step, AllowEdgeEstimates: *allowEdges, Logger: logger}
	records := make([]history.Record, 0, len(workloads))
	seen := map[string]bool{}
	for _, w := range workloads {
		if seen[w.UID] {
			return fmt.Errorf("duplicate workload UID %q", w.UID)
		}
		seen[w.UID] = true
		record, err := exporter.Export(ctx, w)
		if err != nil {
			return fmt.Errorf("%s/%s: %w", w.Namespace, w.Name, err)
		}
		records = append(records, record)
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(records); err != nil {
		return err
	}
	logger.Info("Completed energy dataset export", "component", "energy_export", "workload_count", len(records))
	return nil
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		logger, _ := logging.New(os.Stderr, "info")
		logger.Error("Energy export command failed", "error_kind", logging.ErrorKind(err))
		os.Exit(1)
	}
}
