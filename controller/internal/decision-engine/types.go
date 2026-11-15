package decisionengine

import "time"

// Action is the admission decision returned to the caller.
type Action string

const (
	ActionRun  Action = "RUN"
	ActionWait Action = "WAIT"
)

// WorkloadRequest contains the scheduling information extracted from a Kueue
// Workload. Deadline is the latest acceptable completion time, not start time.
type WorkloadRequest struct {
	Name              string
	Namespace         string
	Region            string
	Priority          int32
	CPURequest        string
	MemoryRequest     string
	GPURequest        string
	ExpectedRuntime   time.Duration
	Deadline          time.Time
	MaxDelay          time.Duration
	EstimatedEnergyWh float64
}

// ScoreBreakdown contains normalized components. Lower values are better.
type ScoreBreakdown struct {
	CarbonImpact float64 `json:"carbon_impact"`
	Energy       float64 `json:"energy"`
	Cost         float64 `json:"cost"`
	Delay        float64 `json:"delay"`
	Total        float64 `json:"total"`
}

// ExecutionWindow describes one possible workload start time.
type ExecutionWindow struct {
	StartTime time.Time      `json:"start_time"`
	EndTime   time.Time      `json:"end_time"`
	Score     ScoreBreakdown `json:"score"`
}

// Decision is the engine result. RequeueAfter is meaningful for WAIT.
type Decision struct {
	Action       Action            `json:"action"`
	Reason       string            `json:"reason"`
	Region       string            `json:"region"`
	Selected     ExecutionWindow   `json:"selected_window"`
	Current      ExecutionWindow   `json:"current_window"`
	RequeueAfter time.Duration     `json:"requeue_after"`
	EvaluatedAt  time.Time         `json:"evaluated_at"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}
