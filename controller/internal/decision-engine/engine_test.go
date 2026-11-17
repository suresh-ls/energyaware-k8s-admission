package decisionengine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/suresh-ls/energyaware-k8s-admission/controller/internal/signals"
)

type fakeSignals struct {
	current      signals.SignalSet
	forecasts    map[string][]signals.Signal
	currentErr   error
	forecastErrs map[string]error
}

func (f *fakeSignals) GetSignals(context.Context, string) (signals.SignalSet, error) {
	return f.current, f.currentErr
}

func (f *fakeSignals) GetForecastSignals(
	_ context.Context,
	req signals.SignalRequest,
	_ time.Duration,
) ([]signals.Signal, error) {
	if err := f.forecastErrs[req.SignalType]; err != nil {
		return nil, err
	}
	return f.forecasts[req.SignalType], nil
}

func validSignalSet(now time.Time) signals.SignalSet {
	return signals.SignalSet{
		Carbon: signals.Signal{
			SignalType: signals.SignalTypeCarbon,
			Provider:   "test-carbon",
			Region:     "IN-SO",
			Value:      500,
			Unit:       "gCO2e/kWh",
			Timestamp:  now,
			ValidUntil: now.Add(time.Hour),
		},
		Cost: signals.Signal{
			SignalType: signals.SignalTypeCost,
			Provider:   "test-cost",
			Region:     signals.RegionAllIndia,
			Value:      8,
			Unit:       "INR/kWh",
			Timestamp:  now,
			ValidUntil: now.Add(time.Hour),
		},
		DemandCap: signals.Signal{
			SignalType:  signals.SignalTypeDemandCap,
			Provider:    "kepler",
			Region:      "IN-SO",
			Value:       60,
			Unit:        "kW",
			Cap:         100,
			Utilization: 0.6,
			Status:      "WITHIN_CAP",
			Timestamp:   now,
			ValidUntil:  now.Add(time.Minute),
			IsForecast:  false,
		},
	}
}

func testWorkload(now time.Time) WorkloadRequest {
	return WorkloadRequest{
		Name:              "train",
		Namespace:         "default",
		Region:            "IN-SO",
		Priority:          10,
		ExpectedRuntime:   time.Hour,
		Deadline:          now.Add(8 * time.Hour),
		MaxDelay:          4 * time.Hour,
		EstimatedEnergyWh: 200,
	}
}

func newTestEngine(t *testing.T, f *fakeSignals, now time.Time) *Engine {
	t.Helper()

	e, err := NewEngine(f, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}

	e.clock = func() time.Time { return now }
	return e
}

func TestEvaluateRunsWhenForecastUnavailable(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	f := &fakeSignals{
		current: validSignalSet(now),
		forecastErrs: map[string]error{
			signals.SignalTypeCarbon: errors.New("no forecast"),
		},
	}

	d, err := newTestEngine(t, f, now).Evaluate(context.Background(), testWorkload(now))
	if err != nil {
		t.Fatal(err)
	}
	if d.Action != ActionRun {
		t.Fatalf("got %s, want RUN", d.Action)
	}
	if d.Metadata["forecast"] != "unavailable; current option selected" {
		t.Fatalf("unexpected forecast metadata: %q", d.Metadata["forecast"])
	}
}

func TestEvaluateReturnsContextCancellation(t *testing.T) {
	now := time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	f := &fakeSignals{current: validSignalSet(now)}
	_, err := newTestEngine(t, f, now).Evaluate(ctx, testWorkload(now))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context canceled", err)
	}
}

func TestEvaluateWaitsForBetterForecastWindow(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	current := validSignalSet(now)
	current.Carbon.Value = 900
	current.Cost.Value = 90

	futureTime := now.Add(2 * time.Hour)

	lowCarbon := current.Carbon
	lowCarbon.Value = 50
	lowCarbon.Timestamp = futureTime
	lowCarbon.IsForecast = true
	lowCarbon.ValidUntil = now.Add(5 * time.Hour)

	lowCost := current.Cost
	lowCost.Region = signals.RegionAllIndia
	lowCost.Value = 1
	lowCost.Timestamp = time.Date(
		futureTime.UTC().Year(),
		futureTime.UTC().Month(),
		futureTime.UTC().Day(),
		0,
		0,
		0,
		0,
		time.UTC,
	)
	lowCost.IsForecast = true
	lowCost.ValidUntil = now.Add(5 * time.Hour)

	futureDemand := current.DemandCap
	futureDemand.Value = 60
	futureDemand.Cap = 100
	futureDemand.Utilization = 0.6
	futureDemand.Status = "WITHIN_CAP"
	futureDemand.Timestamp = futureTime
	futureDemand.ValidUntil = now.Add(5 * time.Hour)
	futureDemand.IsForecast = true

	f := &fakeSignals{
		current: current,
		forecasts: map[string][]signals.Signal{
			signals.SignalTypeCarbon:    {lowCarbon},
			signals.SignalTypeCost:      {lowCost},
			signals.SignalTypeDemandCap: {futureDemand},
		},
		forecastErrs: map[string]error{},
	}

	d, err := newTestEngine(t, f, now).Evaluate(context.Background(), testWorkload(now))
	if err != nil {
		t.Fatal(err)
	}
	if d.Action != ActionWait {
		t.Fatalf("got %s (%s), want WAIT", d.Action, d.Reason)
	}
	if d.RequeueAfter != 2*time.Hour {
		t.Fatalf("requeue=%s, want 2h", d.RequeueAfter)
	}
	if !d.Selected.StartTime.Equal(futureTime) {
		t.Fatalf("selected start=%s, want %s", d.Selected.StartTime, futureTime)
	}
}

func TestEvaluateDoesNotWaitForInsufficientImprovement(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	current := validSignalSet(now)

	futureTime := now.Add(time.Hour)

	futureCarbon := current.Carbon
	futureCarbon.Value = current.Carbon.Value * 0.98
	futureCarbon.Timestamp = futureTime
	futureCarbon.IsForecast = true
	futureCarbon.ValidUntil = now.Add(5 * time.Hour)

	futureCost := current.Cost
	futureCost.Value = current.Cost.Value * 0.98
	futureCost.Timestamp = time.Date(
		futureTime.Year(),
		futureTime.Month(),
		futureTime.Day(),
		0,
		0,
		0,
		0,
		time.UTC,
	)
	futureCost.IsForecast = true
	futureCost.ValidUntil = now.Add(5 * time.Hour)

	f := &fakeSignals{
		current: current,
		forecasts: map[string][]signals.Signal{
			signals.SignalTypeCarbon: {futureCarbon},
			signals.SignalTypeCost:   {futureCost},
		},
		forecastErrs: map[string]error{},
	}

	d, err := newTestEngine(t, f, now).Evaluate(context.Background(), testWorkload(now))
	if err != nil {
		t.Fatal(err)
	}
	if d.Action != ActionRun {
		t.Fatalf("got %s, want RUN", d.Action)
	}
}

func TestEvaluateWaitsWhenDemandCapExceeded(t *testing.T) {
	now := time.Now()
	s := validSignalSet(now)
	s.DemandCap.Utilization = 1.1
	s.DemandCap.Status = "EXCEEDED"
	s.DemandCap.Value = 110

	f := &fakeSignals{current: s}
	d, err := newTestEngine(t, f, now).Evaluate(context.Background(), testWorkload(now))
	if err != nil {
		t.Fatal(err)
	}
	if d.Action != ActionWait {
		t.Fatalf("got %s, want WAIT", d.Action)
	}
	if d.RequeueAfter != DefaultConfig().DefaultRequeueAfter {
		t.Fatalf("requeue=%s, want %s", d.RequeueAfter, DefaultConfig().DefaultRequeueAfter)
	}
}

func TestEvaluateRunsOnDemandBreachWhenDeadlineIsInfeasible(t *testing.T) {
	now := time.Now()
	s := validSignalSet(now)
	s.DemandCap.Utilization = 1.1
	s.DemandCap.Status = "EXCEEDED"
	s.DemandCap.Value = 110

	f := &fakeSignals{current: s}
	w := testWorkload(now)
	w.Deadline = now.Add(30 * time.Minute)

	d, err := newTestEngine(t, f, now).Evaluate(context.Background(), w)
	if err != nil {
		t.Fatal(err)
	}
	if d.Action != ActionRun {
		t.Fatalf("got %s, want RUN", d.Action)
	}
}

func TestEvaluateRunsWhenDeadlineCannotAccommodateWait(t *testing.T) {
	now := time.Now()
	f := &fakeSignals{
		current: validSignalSet(now),
	}

	w := testWorkload(now)
	w.Deadline = now.Add(30 * time.Minute)

	d, err := newTestEngine(t, f, now).Evaluate(context.Background(), w)
	if err != nil {
		t.Fatal(err)
	}
	if d.Action != ActionRun {
		t.Fatalf("got %s, want RUN", d.Action)
	}
}

func TestEvaluateRunsImmediatelyWhenMaxDelayIsZero(t *testing.T) {
	now := time.Now()
	f := &fakeSignals{current: validSignalSet(now)}
	w := testWorkload(now)
	w.MaxDelay = 0

	d, err := newTestEngine(t, f, now).Evaluate(context.Background(), w)
	if err != nil {
		t.Fatal(err)
	}
	if d.Action != ActionRun {
		t.Fatalf("got %s, want RUN", d.Action)
	}
}

func TestEvaluateAcceptsAllIndiaRegionAlias(t *testing.T) {
	now := time.Now()
	current := validSignalSet(now)
	current.Carbon.Region = signals.RegionAllIndia
	current.Cost.Region = signals.RegionAllIndia
	current.DemandCap.Region = signals.RegionAllIndia

	f := &fakeSignals{
		current: current,
	}

	w := testWorkload(now)
	w.Region = "All India"

	d, err := newTestEngine(t, f, now).Evaluate(context.Background(), w)
	if err != nil {
		t.Fatal(err)
	}
	if d.Region != signals.RegionAllIndia {
		t.Fatalf("got region %q, want %q", d.Region, signals.RegionAllIndia)
	}
}

func TestEvaluateRejectsWrongSignalRegion(t *testing.T) {
	now := time.Now()
	current := validSignalSet(now)
	current.Cost.Region = "IN-WEST"

	f := &fakeSignals{current: current}
	_, err := newTestEngine(t, f, now).Evaluate(
		context.Background(),
		testWorkload(now),
	)
	if err == nil {
		t.Fatal("expected region validation error")
	}
}

func TestEvaluateRejectsInvalidDemandUtilization(t *testing.T) {
	now := time.Now()
	current := validSignalSet(now)
	current.DemandCap.Utilization = 0.9

	f := &fakeSignals{current: current}
	_, err := newTestEngine(t, f, now).Evaluate(
		context.Background(),
		testWorkload(now),
	)
	if err == nil {
		t.Fatal("expected demand utilization validation error")
	}
}

func TestEvaluateRejectsExpiredCurrentSignal(t *testing.T) {
	now := time.Now()
	current := validSignalSet(now)
	current.Cost.ValidUntil = now

	f := &fakeSignals{current: current}
	_, err := newTestEngine(t, f, now).Evaluate(
		context.Background(),
		testWorkload(now),
	)
	if err == nil {
		t.Fatal("expected expired signal error")
	}
}

func TestEvaluateRejectsMissingEnergyEstimate(t *testing.T) {
	now := time.Now()
	f := &fakeSignals{
		current: validSignalSet(now),
	}

	w := testWorkload(now)
	w.EstimatedEnergyWh = 0

	if _, err := newTestEngine(t, f, now).Evaluate(context.Background(), w); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestConfigRejectsWeightsNotSummingToOne(t *testing.T) {
	c := DefaultConfig()
	c.Weights.Delay = 0.5

	if err := c.Validate(); err == nil {
		t.Fatal("expected invalid weight error")
	}
}

func TestConfigRejectsNonFiniteBounds(t *testing.T) {
	c := DefaultConfig()
	c.Bounds.MaxEnergyWh = 0

	if err := c.Validate(); err == nil {
		t.Fatal("expected invalid bound error")
	}
}

func TestDemandCapExceededUsesStatus(t *testing.T) {
	if !demandCapExceeded(0.5, "EXCEEDED") {
		t.Fatal("expected EXCEEDED status to trigger demand-cap constraint")
	}
}

func TestDemandCapExceededUsesUtilization(t *testing.T) {
	if !demandCapExceeded(1.0, "WITHIN_CAP") {
		t.Fatal("expected utilization of 1.0 to trigger demand-cap constraint")
	}
}

func TestEvaluateSkipsForecastThatDoesNotCoverExecutionWindow(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	current := validSignalSet(now)
	futureTime := now.Add(2 * time.Hour)

	futureCarbon := current.Carbon
	futureCarbon.Value = 10
	futureCarbon.Timestamp = futureTime
	futureCarbon.IsForecast = true
	futureCarbon.ValidUntil = futureTime.Add(30 * time.Minute) // shorter than 1h runtime

	futureCost := current.Cost
	futureCost.Value = 1
	futureCost.Timestamp = futureTime
	futureCost.IsForecast = true
	futureCost.ValidUntil = futureTime.Add(30 * time.Minute)

	futureDemand := current.DemandCap
	futureDemand.Timestamp = futureTime
	futureDemand.ValidUntil = futureTime.Add(30 * time.Minute)
	futureDemand.IsForecast = true

	f := &fakeSignals{
		current: current,
		forecasts: map[string][]signals.Signal{
			signals.SignalTypeCarbon:    {futureCarbon},
			signals.SignalTypeCost:      {futureCost},
			signals.SignalTypeDemandCap: {futureDemand},
		},
		forecastErrs: map[string]error{},
	}

	d, err := newTestEngine(t, f, now).Evaluate(context.Background(), testWorkload(now))
	if err != nil {
		t.Fatal(err)
	}
	if d.Action != ActionRun {
		t.Fatalf("got %s, want RUN because the forecast does not cover the full runtime", d.Action)
	}
}

func TestEvaluateRunsCurrentWhenDemandForecastUnavailable(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	current := validSignalSet(now)
	futureTime := now.Add(2 * time.Hour)

	futureCarbon := current.Carbon
	futureCarbon.Value = 10
	futureCarbon.Timestamp = futureTime
	futureCarbon.IsForecast = true
	futureCarbon.ValidUntil = now.Add(5 * time.Hour)

	futureCost := current.Cost
	futureCost.Value = 1
	futureCost.Timestamp = futureTime
	futureCost.IsForecast = true
	futureCost.ValidUntil = now.Add(5 * time.Hour)

	f := &fakeSignals{
		current: current,
		forecasts: map[string][]signals.Signal{
			signals.SignalTypeCarbon: {futureCarbon},
			signals.SignalTypeCost:   {futureCost},
		},
		forecastErrs: map[string]error{
			signals.SignalTypeDemandCap: errors.New("demand forecast unavailable"),
		},
	}

	d, err := newTestEngine(t, f, now).Evaluate(
		context.Background(),
		testWorkload(now),
	)
	if err != nil {
		t.Fatal(err)
	}

	if d.Action != ActionRun {
		t.Fatalf("got %s, want RUN when future demand cannot be evaluated", d.Action)
	}

	if d.Metadata["demand_forecast"] != "unavailable; future windows skipped" {
		t.Fatalf(
			"unexpected demand forecast metadata: %q",
			d.Metadata["demand_forecast"],
		)
	}
}

func TestDemandCapBoundaryIsConsistent(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	s := validSignalSet(now)
	s.DemandCap.Value = 100
	s.DemandCap.Cap = 100
	s.DemandCap.Utilization = 1
	s.DemandCap.Status = "EXCEEDED"

	if err := validateSignalSet(s, "IN-SO", now); err != nil {
		t.Fatalf("expected exactly-full demand cap to be valid: %v", err)
	}
	if !demandCapExceeded(s.DemandCap.Utilization, s.DemandCap.Status) {
		t.Fatal("expected exactly-full demand cap to block scheduling")
	}
}

func TestForecastFailurePolicyCanWait(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	f := &fakeSignals{
		current: validSignalSet(now),
		forecastErrs: map[string]error{
			signals.SignalTypeCarbon: errors.New("no forecast"),
		},
	}

	config := DefaultConfig()
	config.ForecastFailurePolicy = ForecastFailureWait
	e, err := NewEngine(f, config)
	if err != nil {
		t.Fatal(err)
	}
	e.clock = func() time.Time { return now }

	d, err := e.Evaluate(context.Background(), testWorkload(now))
	if err != nil {
		t.Fatal(err)
	}
	if d.Action != ActionWait {
		t.Fatalf("got %s, want WAIT", d.Action)
	}
	if d.RequeueAfter != config.DefaultRequeueAfter {
		t.Fatalf("requeue=%s, want %s", d.RequeueAfter, config.DefaultRequeueAfter)
	}
}
