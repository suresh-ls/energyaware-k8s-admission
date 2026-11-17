package decisionengine

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/suresh-ls/energyaware-k8s-admission/controller/internal/signals"
)

// Optional capability: existing three-signal sources and engine callers remain
// compatible when a workload does not request cloud pricing.
type cloudSpotSource interface {
	GetSignal(context.Context, signals.SignalRequest) (signals.Signal, error)
}

func (e *Engine) evaluateCloudSpot(ctx context.Context, workload WorkloadRequest,
	current signals.SignalSet, now time.Time, decision *Decision) (bool, error) {
	spot := workload.CloudSpot
	if spot == nil {
		return false, nil
	}
	if err := spot.Pool.Validate(); err != nil {
		return true, err
	}
	if spot.InstanceCount <= 0 || spot.MaxCostUSD <= 0 || math.IsNaN(spot.MaxCostUSD) || math.IsInf(spot.MaxCostUSD, 0) {
		return true, fmt.Errorf("cloud spot requires positive instance count and finite positive budget")
	}
	slack := workload.Deadline.Sub(now.Add(workload.ExpectedRuntime))
	if slack < 0 {
		decision.Action, decision.Reason = ActionReject, "cloud spot workload cannot finish by its deadline"
		return true, nil
	}
	// For opt-in workloads, power and budget are hard constraints even at a
	// deadline. Legacy workloads keep their existing emergency behavior.
	if demandCapExceeded(current.DemandCap.Utilization, current.DemandCap.Status) {
		e.waitForCloudSpot(workload, now, decision, "cloud spot workload blocked by demand cap")
		return true, nil
	}
	source, ok := e.signalSource.(cloudSpotSource)
	if !ok {
		return true, fmt.Errorf("signal source does not support optional cloud spot retrieval")
	}
	pool := spot.Pool.Canonical()
	quote, err := source.GetSignal(ctx, signals.SignalRequest{
		SignalType: signals.SignalTypeCloudSpot, Region: pool.Region, CloudPool: pool,
	})
	if ctx.Err() != nil {
		return true, ctx.Err()
	}
	if err != nil {
		e.waitForCloudSpot(workload, e.clock(), decision, "current cloud spot quote unavailable")
		return true, nil
	}
	// Retrieval can consume deadline slack. Validate against the post-lookup time.
	now = e.clock()
	if err := signals.ValidateCloudSpotSignal(quote, pool, now); err != nil {
		e.waitForCloudSpot(workload, now, decision, "current cloud spot quote invalid or expired")
		return true, nil
	}
	cost := quote.Value * float64(spot.InstanceCount) * workload.ExpectedRuntime.Hours()
	if math.IsNaN(cost) || math.IsInf(cost, 0) {
		return true, fmt.Errorf("cloud spot estimated cost is not finite")
	}
	decision.CloudSpot = &CloudSpotEstimate{
		Pool: pool, PricePerInstanceHourUSD: quote.Value, EstimatedCostUSD: cost,
		BudgetUSD: spot.MaxCostUSD, QuoteCollectedAt: quote.CollectedAt,
		Replay: strings.HasSuffix(quote.Provider, "-Replay"),
	}
	decision.Metadata["cloud_spot"] = "current quote within budget; future spot prices are unknown"
	if now.Add(workload.ExpectedRuntime).After(workload.Deadline) {
		decision.Action, decision.Reason = ActionReject, "cloud spot workload cannot finish by its deadline"
		return true, nil
	}
	if cost > spot.MaxCostUSD {
		e.waitForCloudSpot(workload, now, decision,
			fmt.Sprintf("estimated cloud spot cost USD %.6f exceeds budget USD %.6f", cost, spot.MaxCostUSD))
		return true, nil
	}
	return false, nil
}

func (e *Engine) waitForCloudSpot(workload WorkloadRequest, now time.Time, decision *Decision, reason string) {
	delay := min(workload.MaxDelay, workload.Deadline.Sub(now.Add(workload.ExpectedRuntime)), e.config.DefaultRequeueAfter)
	decision.Metadata["cloud_spot"] = reason
	if delay < time.Second {
		decision.Action = ActionReject
		decision.Reason = reason + "; no feasible retry remains under the hard budget and deadline"
		return
	}
	// Kueue stores whole seconds and rounds up. Round down to preserve slack.
	decision.Action, decision.Reason = ActionWait, reason
	decision.RequeueAfter = delay.Truncate(time.Second)
}
