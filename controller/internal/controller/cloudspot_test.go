package controller

import (
	"context"
	"fmt"
	"testing"
	"time"

	decisionengine "github.com/suresh-ls/energyaware-k8s-admission/controller/internal/decision-engine"
	"github.com/suresh-ls/energyaware-k8s-admission/controller/internal/signals"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	kueuev1beta2 "sigs.k8s.io/kueue/apis/kueue/v1beta2"
)

func controllerSpotPool() signals.CloudSpotPool {
	return signals.CloudSpotPool{Provider: "aws", Region: "ap-south-1", AvailabilityZone: "ap-south-1a",
		InstanceType: "c5.xlarge", Product: "Linux/UNIX"}
}

func spotWorkload() *kueuev1beta2.Workload {
	return &kueuev1beta2.Workload{
		ObjectMeta: metav1.ObjectMeta{Name: "spot-job", Namespace: "default", Annotations: map[string]string{
			spotBudgetAnnotation: "0.1", spotInstancesAnnotation: "1", spotRuntimeAnnotation: "5m",
			spotDeadlineAnnotation: time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		}},
		Spec: kueuev1beta2.WorkloadSpec{PodSets: []kueuev1beta2.PodSet{{Name: "main", Count: 1,
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
				NodeSelector: map[string]string{
					"eks.amazonaws.com/capacityType": "SPOT", "node.kubernetes.io/instance-type": "c5.xlarge",
					"topology.kubernetes.io/zone": "ap-south-1a", "topology.kubernetes.io/region": "ap-south-1",
					"kubernetes.io/os": "linux",
				},
				Containers: []corev1.Container{{Name: "worker", Image: "busybox",
					Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1")}}}},
			}},
		}}},
		Status: kueuev1beta2.WorkloadStatus{AdmissionChecks: []kueuev1beta2.AdmissionCheckState{
			{Name: energyGateAdmissionCheck, State: kueuev1beta2.CheckStatePending},
			{Name: "other-controller", State: kueuev1beta2.CheckStateReady, Message: "keep me"},
		}},
	}
}

func TestCloudSpotWorkloadMetadata(t *testing.T) {
	pool := controllerSpotPool()
	cases := []struct {
		name   string
		change func(*kueuev1beta2.Workload)
	}{
		{"bad budget", func(w *kueuev1beta2.Workload) { w.Annotations[spotBudgetAnnotation] = "NaN" }},
		{"no instances", func(w *kueuev1beta2.Workload) { delete(w.Annotations, spotInstancesAnnotation) }},
		{"bad runtime", func(w *kueuev1beta2.Workload) { w.Annotations[spotRuntimeAnnotation] = "0m" }},
		{"no absolute deadline", func(w *kueuev1beta2.Workload) { delete(w.Annotations, spotDeadlineAnnotation) }},
		{"wrong zone", func(w *kueuev1beta2.Workload) {
			w.Spec.PodSets[0].Template.Spec.NodeSelector["topology.kubernetes.io/zone"] = "ap-south-1b"
		}},
		{"on demand", func(w *kueuev1beta2.Workload) {
			w.Spec.PodSets[0].Template.Spec.NodeSelector["eks.amazonaws.com/capacityType"] = "ON_DEMAND"
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := spotWorkload()
			tc.change(w)
			request := buildWorkloadRequest(w)
			if err := applyCloudSpotRequest(w, &request, &pool); err == nil {
				t.Fatal("expected invalid opt-in metadata")
			}
		})
	}
	w := spotWorkload()
	request := buildWorkloadRequest(w)
	if err := applyCloudSpotRequest(w, &request, &pool); err != nil {
		t.Fatal(err)
	}
	if request.CloudSpot == nil || request.CloudSpot.MaxCostUSD != .1 || request.ExpectedRuntime != 5*time.Minute {
		t.Fatalf("incorrect cloud request: %+v", request)
	}
	delete(w.Annotations, spotBudgetAnnotation)
	request = buildWorkloadRequest(w)
	if err := applyCloudSpotRequest(w, &request, nil); err != nil || request.CloudSpot != nil {
		t.Fatal("legacy workload changed")
	}
}

type controllerSpotSignals struct{ price float64 }

func (s *controllerSpotSignals) GetSignals(_ context.Context, region string) (signals.SignalSet, error) {
	now := time.Now().UTC()
	base := signals.Signal{Provider: "test", Region: region, Timestamp: now, ValidUntil: now.Add(time.Minute)}
	carbon, cost, demand := base, base, base
	carbon.SignalType, carbon.Unit, carbon.Value = signals.SignalTypeCarbon, "gCO2e/kWh", 500
	cost.SignalType, cost.Unit, cost.Value, cost.Region = signals.SignalTypeCost, "INR/kWh", 8, signals.RegionAllIndia
	demand.SignalType, demand.Unit, demand.Value = signals.SignalTypeDemandCap, "kW", 50
	demand.Cap, demand.Utilization, demand.Status = 100, .5, "WITHIN_CAP"
	return signals.SignalSet{Carbon: carbon, Cost: cost, DemandCap: demand}, nil
}

func (s *controllerSpotSignals) GetForecastSignals(context.Context, signals.SignalRequest, time.Duration) ([]signals.Signal, error) {
	return nil, fmt.Errorf("no forecasts")
}

func (s *controllerSpotSignals) GetSignal(_ context.Context, request signals.SignalRequest) (signals.Signal, error) {
	now := time.Now().UTC()
	return signals.Signal{SignalType: signals.SignalTypeCloudSpot, Provider: "test", Region: request.Region,
		CloudPool: request.CloudPool, Unit: signals.CloudSpotUnit, Value: s.price,
		Timestamp: now.Add(-time.Hour), CollectedAt: now, ValidUntil: now.Add(time.Minute)}, nil
}

func TestCloudSpotControllerAdmission(t *testing.T) {
	for _, tc := range []struct {
		name, budget string
		want         kueuev1beta2.CheckState
	}{
		{"affordable", "0.1", kueuev1beta2.CheckStateReady},
		{"expensive", "0.001", kueuev1beta2.CheckStateRetry},
		{"invalid", "invalid", kueuev1beta2.CheckStateRejected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := kueuev1beta2.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			w := spotWorkload()
			w.Annotations[spotBudgetAnnotation] = tc.budget
			client := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(w).WithObjects(w).Build()
			source := &controllerSpotSignals{price: .2}
			engine, err := decisionengine.NewEngine(source, decisionengine.DefaultConfig())
			if err != nil {
				t.Fatal(err)
			}
			pool := controllerSpotPool()
			r := &WorkloadControllerReconciler{Client: client, Scheme: scheme, DecisionEngine: engine, CloudSpotPool: &pool}
			key := types.NamespacedName{Name: w.Name, Namespace: w.Namespace}
			if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key}); err != nil {
				t.Fatal(err)
			}
			var got kueuev1beta2.Workload
			if err := client.Get(context.Background(), key, &got); err != nil {
				t.Fatal(err)
			}
			if got.Status.AdmissionChecks[0].State != tc.want || got.Status.AdmissionChecks[1].Message != "keep me" {
				t.Fatalf("incorrect or destructive status update: %+v", got.Status.AdmissionChecks)
			}
			if tc.want == kueuev1beta2.CheckStateReady {
				source.price = 100
				if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key}); err != nil {
					t.Fatal(err)
				}
				if err := client.Get(context.Background(), key, &got); err != nil {
					t.Fatal(err)
				}
				if got.Status.AdmissionChecks[0].State != kueuev1beta2.CheckStateReady {
					t.Fatal("price change evicted ready workload")
				}
			}
		})
	}
}
