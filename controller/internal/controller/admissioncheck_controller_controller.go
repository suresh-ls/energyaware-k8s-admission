/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	kueuev1beta2 "sigs.k8s.io/kueue/apis/kueue/v1beta2"
)

const (
	energyGateControllerName = "energygate.io/carbon-check"
	admissionCheckReason     = "ControllerReady"
	admissionCheckMessage    = "EnergyGate controller is ready to evaluate admission checks"
)

// AdmissioncheckControllerReconciler reconciles Kueue AdmissionCheck objects.
type AdmissioncheckControllerReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=kueue.x-k8s.io,resources=admissionchecks,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=kueue.x-k8s.io,resources=admissionchecks/status,verbs=get;update;patch

// Reconcile observes an AdmissionCheck and updates its Active condition
// when the check is owned by EnergyGate.
func (r *AdmissioncheckControllerReconciler) Reconcile(
	ctx context.Context,
	req ctrl.Request,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var admissionCheck kueuev1beta2.AdmissionCheck
	if err := r.Get(ctx, req.NamespacedName, &admissionCheck); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Do not modify AdmissionChecks owned by other controllers.
	if admissionCheck.Spec.ControllerName != energyGateControllerName {
		return ctrl.Result{}, nil
	}

	condition := metav1.Condition{
		Type:               kueuev1beta2.AdmissionCheckActive,
		Status:             metav1.ConditionTrue,
		Reason:             admissionCheckReason,
		Message:            admissionCheckMessage,
		ObservedGeneration: admissionCheck.Generation,
	}

	if !meta.SetStatusCondition(&admissionCheck.Status.Conditions, condition) {
		log.V(1).Info("AdmissionCheck Active condition is already current",
			"admissionCheck", admissionCheck.Name)
		return ctrl.Result{}, nil
	}

	if err := r.Status().Update(ctx, &admissionCheck); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("Marked EnergyGate AdmissionCheck active",
		"admissionCheck", admissionCheck.Name)

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *AdmissioncheckControllerReconciler) SetupWithManager(
	mgr ctrl.Manager,
) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kueuev1beta2.AdmissionCheck{}).
		Named("admissioncheck-controller").
		Complete(r)
}
