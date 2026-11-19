package decisionengine

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/suresh-ls/energyaware-k8s-admission/controller/internal/signals"
)

type fakeSpotSource struct {
	*fakeSignals
	quote signals.Signal
	err   error
	calls int
}

func (f *fakeSpotSource) GetSignal(_ context.Context, req signals.SignalRequest) (signals.Signal, error) {
	f.calls++
	if req.SignalType != signals.SignalTypeCloudSpot || req.CloudPool != f.quote.CloudPool {
		return signals.Signal{}, errors.New("wrong pool request")
	}
	return f.quote, f.err
}

func spotSource(now time.Time) *fakeSpotSource {
	pool := signals.CloudSpotPool{Provider: "aws", Region: "ap-south-1", AvailabilityZone: "ap-south-1a",
		InstanceType: "c5.xlarge", Product: "Linux/UNIX"}
	return &fakeSpotSource{
		fakeSignals: &fakeSignals{current: validSignalSet(now)},
		quote: signals.Signal{SignalType: signals.SignalTypeCloudSpot, Provider: "AWS-EC2-Spot-Replay",
			Region: pool.Region, CloudPool: pool, Value: .2, Unit: signals.CloudSpotUnit,
			Timestamp: now.Add(-48 * time.Hour), CollectedAt: now, ValidUntil: now.Add(5 * time.Minute)},
	}
}

func TestCloudSpotBudgetAdmission(t *testing.T) {
	now := time.Now().UTC()
	cases := []struct {
		name   string
		change func(*WorkloadRequest, *fakeSpotSource)
		want   Action
	}{
		{"within budget", func(_ *WorkloadRequest, _ *fakeSpotSource) {}, ActionRun},
		{"over budget", func(w *WorkloadRequest, _ *fakeSpotSource) { w.CloudSpot.MaxCostUSD = .1 }, ActionWait},
		{"exact budget", func(w *WorkloadRequest, _ *fakeSpotSource) { w.CloudSpot.MaxCostUSD = .4 }, ActionRun},
		{"priority cannot override budget", func(w *WorkloadRequest, _ *fakeSpotSource) {
			w.Priority = 100
			w.CloudSpot.MaxCostUSD = .1
			w.Deadline = now.Add(w.ExpectedRuntime)
		}, ActionReject},
		{"quote unavailable", func(_ *WorkloadRequest, f *fakeSpotSource) { f.err = errors.New("offline") }, ActionWait},
		{"expired quote", func(_ *WorkloadRequest, f *fakeSpotSource) { f.quote.ValidUntil = now }, ActionWait},
		{"stale lookup", func(_ *WorkloadRequest, f *fakeSpotSource) { f.quote.CollectedAt = now.Add(-6 * time.Minute) }, ActionWait},
		{"wrong unit", func(_ *WorkloadRequest, f *fakeSpotSource) { f.quote.Unit = "INR/kWh" }, ActionWait},
		{"forecast is not current", func(_ *WorkloadRequest, f *fakeSpotSource) { f.quote.IsForecast = true }, ActionWait},
		{"no delay allowed", func(w *WorkloadRequest, _ *fakeSpotSource) { w.CloudSpot.MaxCostUSD = .1; w.MaxDelay = 0 }, ActionReject},
		{"infeasible deadline", func(w *WorkloadRequest, _ *fakeSpotSource) { w.Deadline = now.Add(time.Minute) }, ActionReject},
		{"demand cap", func(_ *WorkloadRequest, f *fakeSpotSource) {
			f.current.DemandCap.Value = 110
			f.current.DemandCap.Utilization = 1.1
			f.current.DemandCap.Status = "EXCEEDED"
		}, ActionWait},
		{"demand cap at deadline", func(w *WorkloadRequest, f *fakeSpotSource) {
			w.Deadline = now.Add(w.ExpectedRuntime)
			f.current.DemandCap.Value = 110
			f.current.DemandCap.Utilization = 1.1
			f.current.DemandCap.Status = "EXCEEDED"
		}, ActionReject},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := spotSource(now)
			w := testWorkload(now)
			w.CloudSpot = &CloudSpotRequest{Pool: f.quote.CloudPool, InstanceCount: 2, MaxCostUSD: 1}
			tc.change(&w, f)
			e, err := NewEngine(f, DefaultConfig())
			if err != nil {
				t.Fatal(err)
			}
			e.clock = func() time.Time { return now }
			d, err := e.Evaluate(context.Background(), w)
			if err != nil || d.Action != tc.want {
				t.Fatalf("got %s (%s), %v; want %s", d.Action, d.Reason, err, tc.want)
			}
			if d.Action == ActionRun && (d.CloudSpot == nil || math.Abs(d.CloudSpot.EstimatedCostUSD-.4) > 1e-12 || !d.CloudSpot.Replay) {
				t.Fatalf("incorrect cost estimate: %+v", d.CloudSpot)
			}
		})
	}
}

func TestCloudSpotRetryBoundedByDeadline(t *testing.T) {
	now := time.Now().UTC()
	f := spotSource(now)
	w := testWorkload(now)
	w.CloudSpot = &CloudSpotRequest{Pool: f.quote.CloudPool, InstanceCount: 2, MaxCostUSD: .1}
	w.Deadline = now.Add(w.ExpectedRuntime + 2500*time.Millisecond)
	e, err := NewEngine(f, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	e.clock = func() time.Time { return now }
	d, err := e.Evaluate(context.Background(), w)
	if err != nil || d.Action != ActionWait || d.RequeueAfter != 2*time.Second {
		t.Fatalf("unexpected retry: %+v %v", d, err)
	}
}

func TestCloudSpotDoesNotFetchForLegacyWorkload(t *testing.T) {
	now := time.Now().UTC()
	f := spotSource(now)
	f.err = errors.New("offline")
	e, err := NewEngine(f, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	e.clock = func() time.Time { return now }
	d, err := e.Evaluate(context.Background(), testWorkload(now))
	if err != nil || d.Action != ActionRun || f.calls != 0 || d.CloudSpot != nil {
		t.Fatalf("legacy behavior changed: %+v %v", d, err)
	}
}
