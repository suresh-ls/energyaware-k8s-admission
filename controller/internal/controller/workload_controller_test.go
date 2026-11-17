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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kueuev1beta2 "sigs.k8s.io/kueue/apis/kueue/v1beta2"
)

var _ = Describe("Workload Controller", func() {
	Context("When building a Decision Engine request", func() {
		It("uses Kueue priority and aggregates resources across PodSets", func() {
			priority := int32(90)
			workload := &kueuev1beta2.Workload{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "demo",
					Namespace: "default",
				},
				Spec: kueuev1beta2.WorkloadSpec{
					Priority: &priority,
					PodSets: []kueuev1beta2.PodSet{
						{
							Name:  "worker-a",
							Count: 2,
							Template: corev1.PodTemplateSpec{
								Spec: corev1.PodSpec{
									Containers: []corev1.Container{
										{
											Name: "main",
											Resources: corev1.ResourceRequirements{
												Requests: corev1.ResourceList{
													corev1.ResourceCPU:    resource.MustParse("500m"),
													corev1.ResourceMemory: resource.MustParse("256Mi"),
												},
											},
										},
									},
									RestartPolicy: corev1.RestartPolicyNever,
								},
							},
						},
						{
							Name:  "worker-b",
							Count: 1,
							Template: corev1.PodTemplateSpec{
								Spec: corev1.PodSpec{
									Containers: []corev1.Container{
										{
											Name: "sidecar",
											Resources: corev1.ResourceRequirements{
												Requests: corev1.ResourceList{
													corev1.ResourceCPU: resource.MustParse("1"),
												},
											},
										},
									},
									RestartPolicy: corev1.RestartPolicyNever,
								},
							},
						},
					},
				},
			}

			request := buildWorkloadRequest(workload)

			Expect(request.Priority).To(Equal(int32(90)))
			Expect(request.CPURequest).To(Equal("2"))
			Expect(request.MemoryRequest).To(Equal("512Mi"))
		})
	})
})
