# CarbonGate A1 — Kubernetes and Kueue Setup

## Objective

Set up a local Kubernetes cluster using Kind and configure Kueue for workload queueing and admission.

## Prerequisites

- Docker
- Kind
- kubectl

## Cluster Configuration

The Kind cluster is configured in `deploy/kind-config.yaml` with one control-plane node and two worker nodes.

## Kueue Configuration

Kueue v0.20.1 is installed in the `kueue-system` namespace.

The queue configuration is stored in `cluster/kueue-queues.yaml` and defines:

- **ResourceFlavor:** `default-flavor`
- **ClusterQueue:** `carbongate-cluster-queue`
- **LocalQueue:** `carbongate-local-queue` in the `default` namespace
- **CPU quota:** 4
- **Memory quota:** 8 GiB

Both queues were verified to be active.

## Smoke Test

The test workload is defined in `cluster/sample-job.yaml`.

It requests 100 millicores of CPU and 64 MiB of memory and prints:

`CarbonGate A1 smoke test passed`

### Verification

The test completed successfully:

- Kueue workload admitted: `True`
- Kueue workload finished: `True`
- Kubernetes Job: `Complete`
- Job completions: `1/1`
- Expected output appeared in the container logs

## Useful Commands

Check cluster nodes:

`kubectl get nodes`

Check Kueue:

`kubectl rollout status deployment/kueue-controller-manager -n kueue-system`

Check queues:

`kubectl get resourceflavor`
`kubectl get clusterqueue`
`kubectl get localqueue -n default`

Apply queue configuration:

`kubectl apply -f cluster/kueue-queues.yaml`

Run the smoke test:

`kubectl apply -f cluster/sample-job.yaml`

Check its result:

`kubectl get jobs -n default`
`kubectl logs job/carbongate-smoke-test`

## Scope

This component establishes the Kubernetes and Kueue foundation for CarbonGate. Energy, carbon-intensity, electricity-cost signals, and admission decision logic are handled by the other project components.
