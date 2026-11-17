package controller

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/suresh-ls/energyaware-k8s-admission/controller/internal/decision-engine"
	"github.com/suresh-ls/energyaware-k8s-admission/controller/internal/signals"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	kueuev1beta2 "sigs.k8s.io/kueue/apis/kueue/v1beta2"
)

const (
	energyGateAdmissionCheck = "energygate-carbon-check"

	// Temporary defaults for the current demo.
	// These should eventually come from the workload specification.
	defaultRegion          = "IN-SO"
	defaultExpectedRuntime = 5 * time.Minute
	defaultMaxDelay        = 30 * time.Minute
)

type WorkloadControllerReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	DecisionEngine *decisionengine.Engine
	CloudSpotPool  *signals.CloudSpotPool
}

func (r *WorkloadControllerReconciler) Reconcile(
	ctx context.Context,
	req ctrl.Request,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var workload kueuev1beta2.Workload

	if err := r.Get(ctx, req.NamespacedName, &workload); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}

		return ctrl.Result{}, err
	}

	// Find the EnergyGate admission check attached to this workload.
	check, found := findAdmissionCheck(
		workload.Status.AdmissionChecks,
		energyGateAdmissionCheck,
	)

	if !found {
		// This workload is not waiting for EnergyGate.
		return ctrl.Result{}, nil
	}

	// If Kueue already considers this check ready or rejected,
	// there is nothing for us to do.
	if check.State == kueuev1beta2.CheckStateReady ||
		check.State == kueuev1beta2.CheckStateRejected {
		return ctrl.Result{}, nil
	}

	if r.DecisionEngine == nil {
		return ctrl.Result{}, fmt.Errorf("decision engine is nil")
	}

	request := buildWorkloadRequest(&workload)
	if err := applyCloudSpotRequest(&workload, &request, r.CloudSpotPool); err != nil {
		return r.updateAdmissionCheck(ctx, &workload, kueuev1beta2.CheckStateRejected, err.Error(), 0)
	}

	log.Info(
		"Evaluating workload with EnergyGate",
		"workload", workload.Name,
		"namespace", workload.Namespace,
		"region", request.Region,
		"priority", request.Priority,
		"cpuRequest", request.CPURequest,
		"memoryRequest", request.MemoryRequest,
		"expectedRuntime", request.ExpectedRuntime,
		"maxDelay", request.MaxDelay,
	)

	decision, err := r.DecisionEngine.Evaluate(ctx, request)
	if err != nil {
		log.Error(
			err,
			"Decision engine evaluation failed",
			"workload", workload.Name,
			"namespace", workload.Namespace,
		)

		// Do not admit the workload when the decision engine fails.
		// Retry instead so a temporary signal/provider failure
		// does not permanently reject the workload.
		return r.updateAdmissionCheck(
			ctx,
			&workload,
			kueuev1beta2.CheckStateRetry,
			fmt.Sprintf("EnergyGate decision engine error: %v", err),
			30*time.Second,
		)
	}

	log.Info(
		"EnergyGate decision completed",
		"workload", workload.Name,
		"namespace", workload.Namespace,
		"action", decision.Action,
		"reason", decision.Reason,
		"requeueAfter", decision.RequeueAfter,
	)
	if estimate := decision.CloudSpot; estimate != nil {
		log.Info("Cloud spot compute cost estimated",
			"workload", workload.Name, "namespace", workload.Namespace,
			"pool", estimate.Pool, "pricePerInstanceHourUSD", estimate.PricePerInstanceHourUSD,
			"estimatedCostUSD", estimate.EstimatedCostUSD, "budgetUSD", estimate.BudgetUSD,
			"quoteCollectedAt", estimate.QuoteCollectedAt, "replay", estimate.Replay)
	}

	switch decision.Action {
	case decisionengine.ActionReject:
		return r.updateAdmissionCheck(ctx, &workload, kueuev1beta2.CheckStateRejected, decision.Reason, 0)

	case decisionengine.ActionRun:
		return r.updateAdmissionCheck(
			ctx,
			&workload,
			kueuev1beta2.CheckStateReady,
			decision.Reason,
			0,
		)

	case decisionengine.ActionWait:
		requeueAfter := decision.RequeueAfter

		if requeueAfter <= 0 {
			requeueAfter = 30 * time.Second
		}

		return r.updateAdmissionCheck(
			ctx,
			&workload,
			kueuev1beta2.CheckStateRetry,
			decision.Reason,
			requeueAfter,
		)

	default:
		log.Info(
			"Unknown decision action; retrying",
			"workload", workload.Name,
			"action", decision.Action,
		)

		return r.updateAdmissionCheck(
			ctx,
			&workload,
			kueuev1beta2.CheckStateRetry,
			fmt.Sprintf("unknown decision action: %q", decision.Action),
			30*time.Second,
		)
	}
}

func (r *WorkloadControllerReconciler) updateAdmissionCheck(
	ctx context.Context,
	workload *kueuev1beta2.Workload,
	state kueuev1beta2.CheckState,
	message string,
	requeueAfter time.Duration,
) (ctrl.Result, error) {

	updated := workload.DeepCopy()

	index := findAdmissionCheckIndex(
		updated.Status.AdmissionChecks,
		energyGateAdmissionCheck,
	)

	if index < 0 {
		return ctrl.Result{}, fmt.Errorf(
			"admission check %q not found on workload %s/%s",
			energyGateAdmissionCheck,
			workload.Namespace,
			workload.Name,
		)
	}

	current := updated.Status.AdmissionChecks[index]

	// Avoid unnecessary status updates.
	if current.State == state &&
		current.Message == message &&
		sameRequeueAfter(current.RequeueAfterSeconds, requeueAfter) {
		return ctrl.Result{}, nil
	}

	current.Name = kueuev1beta2.AdmissionCheckReference(
		energyGateAdmissionCheck,
	)

	current.State = state
	current.Message = message
	current.LastTransitionTime = metav1.Now()

	if requeueAfter > 0 {
		seconds := int32(math.Ceil(requeueAfter.Seconds()))

		if seconds < 1 {
			seconds = 1
		}

		current.RequeueAfterSeconds = &seconds
	} else {
		current.RequeueAfterSeconds = nil
	}

	updated.Status.AdmissionChecks[index] = current

	if err := r.Status().Update(ctx, updated); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func buildWorkloadRequest(
	workload *kueuev1beta2.Workload,
) decisionengine.WorkloadRequest {

	var cpuRequest string
	var memoryRequest string
	var gpuRequest string

	if len(workload.Spec.PodSets) > 0 {
		for _, container := range workload.Spec.PodSets[0].Template.Spec.Containers {

			if quantity, ok := container.Resources.Requests["cpu"]; ok {
				cpuRequest = quantity.String()
			}

			if quantity, ok := container.Resources.Requests["memory"]; ok {
				memoryRequest = quantity.String()
			}

			if quantity, ok := container.Resources.Requests["nvidia.com/gpu"]; ok {
				gpuRequest = quantity.String()
			}
		}
	}

	return decisionengine.WorkloadRequest{
		Name:          workload.Name,
		Namespace:     workload.Namespace,
		Region:        defaultRegion,
		Priority:      0,
		CPURequest:    cpuRequest,
		MemoryRequest: memoryRequest,
		GPURequest:    gpuRequest,

		// Temporary demo values.
		ExpectedRuntime: defaultExpectedRuntime,
		Deadline: time.Now().Add(
			defaultExpectedRuntime + defaultMaxDelay,
		),
		MaxDelay: defaultMaxDelay,

		// Temporary estimate based on requested CPU.
		EstimatedEnergyWh: estimateEnergy(cpuRequest),
	}
}

func estimateEnergy(cpuRequest string) float64 {
	if cpuRequest == "" {
		return 0
	}

	cpuCores, err := parseCPU(cpuRequest)
	if err != nil {
		return 0
	}

	// Temporary estimate:
	//
	// CPU cores × runtime in hours × 20 W/core
	//
	// The real carbon/cost/demand information comes from
	// the signal pipeline.
	return cpuCores * defaultExpectedRuntime.Hours() * 20
}

func parseCPU(value string) (float64, error) {
	value = strings.TrimSpace(value)

	if strings.HasSuffix(value, "m") {
		var millicores float64

		if _, err := fmt.Sscanf(
			strings.TrimSuffix(value, "m"),
			"%f",
			&millicores,
		); err != nil {
			return 0, err
		}

		return millicores / 1000, nil
	}

	var cores float64

	if _, err := fmt.Sscanf(value, "%f", &cores); err != nil {
		return 0, err
	}

	return cores, nil
}

func findAdmissionCheck(
	checks []kueuev1beta2.AdmissionCheckState,
	name string,
) (kueuev1beta2.AdmissionCheckState, bool) {

	index := findAdmissionCheckIndex(checks, name)

	if index < 0 {
		return kueuev1beta2.AdmissionCheckState{}, false
	}

	return checks[index], true
}

func findAdmissionCheckIndex(
	checks []kueuev1beta2.AdmissionCheckState,
	name string,
) int {

	for i := range checks {
		if string(checks[i].Name) == name {
			return i
		}
	}

	return -1
}

func sameRequeueAfter(
	current *int32,
	duration time.Duration,
) bool {

	if duration <= 0 {
		return current == nil
	}

	if current == nil {
		return false
	}

	expected := int32(math.Ceil(duration.Seconds()))

	if expected < 1 {
		expected = 1
	}

	return *current == expected
}

func (r *WorkloadControllerReconciler) SetupWithManager(
	mgr ctrl.Manager,
) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kueuev1beta2.Workload{}).
		Named("workload-controller").
		Complete(r)
}
