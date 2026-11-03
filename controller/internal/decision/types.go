package decision

import (
	"context"
	"time"
)

// Action represents the decision made for a workload.
type Action string

const (
	ActionRun    Action = "RUN"
	ActionWait   Action = "WAIT"
	ActionReject Action = "REJECT"
)

// WorkloadInfo contains the workload metadata used for a decision.
type WorkloadInfo struct {
	Name            string
	Namespace       string
	Priority        int32
	GPUCount        int64
	ExpectedRuntime time.Duration
	Deadline        *time.Time
}

// Signals contains normalized signals used by the decision policy.
// Values intended to be normalized should be in the range [0, 1].
type Signals struct {
	Carbon            float64
	ElectricityPrice  float64
	CloudSpotPrice    float64
	DemandUtilization float64
	DemandStatus      string
}

// Request contains the workload, current signals, and evaluation time.
type Request struct {
	Workload WorkloadInfo
	Signals  Signals
	Now      time.Time
}

// Decision describes the action selected by the engine.
// RetryAfter is optional and is primarily relevant to WAIT decisions.
type Decision struct {
	Action     Action
	Reason     string
	RetryAfter *time.Duration
}

// Engine evaluates a workload using the supplied request.
// A non-nil error indicates that the engine could not complete evaluation.
type Engine interface {
	Decide(ctx context.Context, req Request) (Decision, error)
}
