package controller

import (
	"fmt"
	"math"
	"strconv"
	"time"

	decisionengine "github.com/suresh-ls/energyaware-k8s-admission/controller/internal/decision-engine"
	"github.com/suresh-ls/energyaware-k8s-admission/controller/internal/signals"
	kueuev1beta2 "sigs.k8s.io/kueue/apis/kueue/v1beta2"
)

const (
	spotBudgetAnnotation    = "energygate.io/cloud-spot-budget-usd"
	spotInstancesAnnotation = "energygate.io/cloud-spot-instances"
	spotRuntimeAnnotation   = "energygate.io/expected-runtime"
	spotDeadlineAnnotation  = "energygate.io/deadline"
)

// applyCloudSpotRequest reads opt-in metadata from the Kueue Workload itself.
// All PodSets must target the priced pool; this feature does not select or
// provision nodes. Invalid opt-in metadata is a permanent admission failure.
func applyCloudSpotRequest(workload *kueuev1beta2.Workload, request *decisionengine.WorkloadRequest,
	configured *signals.CloudSpotPool) error {
	budgetText, enabled := workload.Annotations[spotBudgetAnnotation]
	if !enabled {
		return nil
	}
	if configured == nil {
		return fmt.Errorf("cloud spot pricing is not enabled on this controller")
	}
	pool := configured.Canonical()
	if err := pool.Validate(); err != nil {
		return err
	}
	budget, err := strconv.ParseFloat(budgetText, 64)
	if err != nil || budget <= 0 || math.IsNaN(budget) || math.IsInf(budget, 0) {
		return fmt.Errorf("%s must be a finite positive USD estimate limit", spotBudgetAnnotation)
	}
	instances, err := strconv.ParseInt(workload.Annotations[spotInstancesAnnotation], 10, 32)
	if err != nil || instances <= 0 {
		return fmt.Errorf("%s must be a positive billable-instance estimate", spotInstancesAnnotation)
	}
	runtime, err := time.ParseDuration(workload.Annotations[spotRuntimeAnnotation])
	if err != nil || runtime <= 0 {
		return fmt.Errorf("%s must be a positive duration", spotRuntimeAnnotation)
	}
	deadline, err := time.Parse(time.RFC3339, workload.Annotations[spotDeadlineAnnotation])
	if err != nil {
		return fmt.Errorf("%s must be an absolute RFC3339 completion deadline", spotDeadlineAnnotation)
	}
	if len(workload.Spec.PodSets) == 0 {
		return fmt.Errorf("cloud spot workload requires PodSets targeting the priced pool")
	}
	selectors := map[string]string{
		"eks.amazonaws.com/capacityType":   "SPOT",
		"node.kubernetes.io/instance-type": pool.InstanceType,
		"topology.kubernetes.io/zone":      pool.AvailabilityZone,
		"topology.kubernetes.io/region":    pool.Region,
		"kubernetes.io/os":                 "linux",
	}
	for _, podSet := range workload.Spec.PodSets {
		for key, value := range selectors {
			if podSet.Template.Spec.NodeSelector[key] != value {
				return fmt.Errorf("cloud spot PodSet %s requires nodeSelector %s=%s", podSet.Name, key, value)
			}
		}
	}
	request.ExpectedRuntime = runtime
	request.Deadline = deadline
	request.EstimatedEnergyWh *= runtime.Hours() / defaultExpectedRuntime.Hours()
	request.CloudSpot = &decisionengine.CloudSpotRequest{
		Pool: pool, InstanceCount: int32(instances), MaxCostUSD: budget,
	}
	return nil
}
