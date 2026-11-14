// Package history exports workload-scoped Kepler energy for prediction datasets.
package history

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/suresh-ls/energyaware-k8s-admission/internal/logging"
)

// Workload is supplied by the caller from recorded job/pod metadata. Resource
// fields describe total requested resources, not measured utilization.
// PowerQuery must select ONLY this workload and return one aggregate in watts.
// Freshness filtering belongs in the expression; query_range timestamps alone
// are evaluation times, not proof of recent scrapes.
type Workload struct {
	Namespace       string    `json:"namespace"`
	Name            string    `json:"name"`
	UID             string    `json:"uid"`
	CPUCores        float64   `json:"cpu_cores"`
	MemoryBytes     int64     `json:"memory_bytes"`
	GPUs            int       `json:"gpus"`
	Priority        int32     `json:"priority"`
	Start           time.Time `json:"start"`
	End             time.Time `json:"end"`
	PowerQuery      string    `json:"power_query"`
	MeasurementMode string    `json:"measurement_mode"`
}

type Record struct {
	Workload
	EstimatedBoundarySeconds float64   `json:"estimated_boundary_seconds"`
	EnergyWh                 float64   `json:"energy_wh"`
	RuntimeSeconds           float64   `json:"runtime_seconds"`
	SampleCount              int       `json:"sample_count"`
	StepSeconds              float64   `json:"step_seconds"`
	ExportedAt               time.Time `json:"exported_at"`
	Method                   string    `json:"method"`
}

type Exporter struct {
	// Logger overrides the default structured logger.
	Logger *slog.Logger
	// AllowEdgeEstimates permits at most one missing evaluation at each edge.
	// The nearest observed power is held constant over that bounded gap.
	AllowEdgeEstimates bool
	PrometheusURL      string
	Step               time.Duration
	Client             *http.Client
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// Export computes trapezoidal watt-seconds / 3600 over the complete runtime.
// Strict mode requires every evaluation sample. AllowEdgeEstimates explicitly
// permits bounded boundary gaps; internal gaps and partial responses fail.
func (e Exporter) Export(ctx context.Context, w Workload) (record Record, err error) {
	logger := logging.OrDefault(e.Logger).With("component", "energy_history", "provider", "kepler",
		"namespace", w.Namespace, "workload", w.Name, "workload_uid", w.UID)
	started := time.Now()
	stage := "workload_validation"
	logger.Debug("Starting workload energy export", "start", w.Start, "end", w.End)
	defer func() {
		if err != nil {
			if logging.ErrorKind(err) == "cancelled" {
				logger.Debug("Workload energy export cancelled", "stage", stage, "duration_ms", time.Since(started).Milliseconds())
			} else {
				logger.Error("Could not export workload energy", "stage", stage, "error_kind", logging.ErrorKind(err), "duration_ms", time.Since(started).Milliseconds())
			}
			return
		}
		logger.Info("Exported workload energy", "energy_wh", record.EnergyWh, "runtime_seconds", record.RuntimeSeconds,
			"measurement_mode", record.MeasurementMode,
			"estimated_boundary_seconds", record.EstimatedBoundarySeconds, "duration_ms", time.Since(started).Milliseconds())
	}()

	var empty Record
	if ctx == nil {
		return empty, fmt.Errorf("context required")
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if strings.TrimSpace(w.Namespace) == "" || strings.TrimSpace(w.Name) == "" || strings.TrimSpace(w.UID) == "" || strings.TrimSpace(w.PowerQuery) == "" {
		return empty, fmt.Errorf("namespace, name, UID and workload power query required")
	}
	if !finite(w.CPUCores) || w.CPUCores < 0 || w.MemoryBytes < 0 || w.GPUs < 0 {
		return empty, fmt.Errorf("invalid workload resources")
	}
	if w.MeasurementMode != "measured" && w.MeasurementMode != "estimated" && w.MeasurementMode != "synthetic" {
		return empty, fmt.Errorf("measurement_mode must be measured, estimated or synthetic")
	}
	if w.Start.IsZero() || !w.End.After(w.Start) || w.End.After(time.Now()) {
		return empty, fmt.Errorf("completed workload interval required")
	}
	step := e.Step
	if step == 0 {
		step = 15 * time.Second
	}
	if step < time.Second {
		return empty, fmt.Errorf("step must be at least one second")
	}
	duration := w.End.Sub(w.Start)
	// Shorten the step to cover short jobs. Choose an exact division of the
	// interval so the final evaluation lands on End and no tail is dropped.
	intervals := int64(math.Ceil(float64(duration) / float64(step)))
	if intervals > 11000 {
		return empty, fmt.Errorf("more than 11000 intervals: increase step")
	}
	stepSeconds := duration.Seconds() / float64(intervals)
	u, err := url.Parse(e.PrometheusURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return empty, fmt.Errorf("valid HTTP(S) Prometheus URL required")
	}
	u = u.JoinPath("api", "v1", "query_range")
	q := u.Query()
	q.Set("query", w.PowerQuery)
	q.Set("start", w.Start.Format(time.RFC3339Nano))
	q.Set("end", w.End.Format(time.RFC3339Nano))
	q.Set("step", strconv.FormatFloat(stepSeconds, 'f', 9, 64))
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return empty, err
	}
	client := e.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	stage = "prometheus_query"
	resp, err := client.Do(req)
	if err != nil {
		return empty, fmt.Errorf("query history: %w", err)
	}
	defer resp.Body.Close()
	logger.Debug("Received Prometheus history response", "http_status", resp.StatusCode)
	if resp.StatusCode != http.StatusOK {
		return empty, fmt.Errorf("Prometheus HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if err != nil {
		return empty, err
	}
	if len(body) > 8<<20 {
		return empty, fmt.Errorf("history response too large")
	}
	stage = "response_validation"
	var result struct {
		Status   string   `json:"status"`
		Warnings []string `json:"warnings"`
		Data     struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Values [][]json.RawMessage `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return empty, fmt.Errorf("decode history: %w", err)
	}
	if result.Status != "success" || result.Data.ResultType != "matrix" || len(result.Data.Result) != 1 || len(result.Warnings) > 0 {
		return empty, fmt.Errorf("expected one complete workload matrix without warnings")
	}
	stage = "coverage_validation"
	samples := result.Data.Result[0].Values
	logger = logger.With("sample_count", len(samples), "expected_samples", intervals+1)
	if len(samples) < 2 || len(samples[0]) != 2 {
		return empty, fmt.Errorf("at least two complete samples required")
	}
	var firstTime float64
	if err := json.Unmarshal(samples[0][0], &firstTime); err != nil || !finite(firstTime) {
		return empty, fmt.Errorf("invalid first timestamp")
	}
	startSeconds := float64(w.Start.Unix()) + float64(w.Start.Nanosecond())/1e9
	firstIndex := int64(math.Round((firstTime - startSeconds) / stepSeconds))
	missingTail := intervals - (firstIndex + int64(len(samples)) - 1)
	if firstIndex < 0 || firstIndex > 1 || missingTail < 0 || missingTail > 1 || (!e.AllowEdgeEstimates && (firstIndex != 0 || missingTail != 0)) {
		return empty, fmt.Errorf("incomplete coverage: got %d samples, want %d (edge estimation requires explicit opt-in)", len(samples), intervals+1)
	}
	stage = "energy_integration"
	var energy, lastTime, lastWatts, firstWatts float64

	for i, sample := range samples {
		if len(sample) != 2 {
			return empty, fmt.Errorf("malformed sample %d", i)
		}
		var at float64
		var raw string
		if err := json.Unmarshal(sample[0], &at); err != nil || !finite(at) {
			return empty, fmt.Errorf("invalid timestamp at %d", i)
		}
		if err := json.Unmarshal(sample[1], &raw); err != nil {
			return empty, fmt.Errorf("invalid value at %d", i)
		}
		watts, err := strconv.ParseFloat(raw, 64)
		if err != nil || !finite(watts) || watts < 0 {
			return empty, fmt.Errorf("invalid power at %d", i)
		}
		expected := startSeconds + float64(int64(i)+firstIndex)*stepSeconds
		if math.Abs(at-expected) > .002 || (i > 0 && at <= lastTime) {
			return empty, fmt.Errorf("sample %d is off the expected grid", i)
		}
		if i == 0 {
			firstWatts = watts
		}
		if i > 0 {
			energy += (lastWatts + watts) / 2 * (at - lastTime) / 3600
		}
		lastTime, lastWatts = at, watts
	}
	edgeSeconds := float64(firstIndex+missingTail) * stepSeconds
	if edgeSeconds > 0 {
		logger.Warn("Estimated workload energy at scrape boundaries", "estimated_boundary_seconds", edgeSeconds)
	}
	energy += (float64(firstIndex)*stepSeconds*firstWatts + float64(missingTail)*stepSeconds*lastWatts) / 3600
	if !finite(energy) {
		return empty, fmt.Errorf("energy overflow")
	}
	return Record{Workload: w, EnergyWh: energy, RuntimeSeconds: duration.Seconds(), SampleCount: len(samples), StepSeconds: stepSeconds, ExportedAt: time.Now().UTC(), Method: "trapezoidal_power_integration", EstimatedBoundarySeconds: edgeSeconds}, nil
}
