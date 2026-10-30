# Kubernetes and Kueue setup

Run these commands from the repository root with Docker, Kind, and kubectl installed.

## Create the cluster

```sh
kind create cluster --config deploy/kind-config.yaml
kubectl config use-context kind-carbongate
kubectl get nodes
kubectl apply --server-side -f https://github.com/kubernetes-sigs/kueue/releases/download/v0.20.1/manifests.yaml
kubectl rollout status deployment/kueue-controller-manager -n kueue-system --timeout=180s
```

The Kind configuration creates one control-plane node and two workers. Skip cluster
creation if `carbongate` already exists. Kueue v0.20.1 matches the controller dependency.

## Run the standalone smoke test

```sh
kubectl apply -f cluster/kueue-queues.yaml
kubectl wait --for=condition=Active clusterqueue/carbongate-cluster-queue --timeout=60s
kubectl create -f cluster/smoke-job.yaml
kubectl wait --for=condition=Complete job/carbongate-smoke-test --timeout=180s
kubectl logs job/carbongate-smoke-test
kubectl get workloads
```

Expected output: `CarbonGate A1 smoke test passed`. The workload should be admitted
and finished. The queue provides 4 CPU and 8 GiB of memory; the job requests 100m
CPU and 64 MiB. To repeat the test, delete the completed job first:

```sh
kubectl delete job carbongate-smoke-test
```

## Exercise the EnergyGate controller

`cluster/kueue-test.yaml` and `cluster/sample-job.yaml` come from the controller
foundation on `main`. They use a separate queue with the `energygate-check`
AdmissionCheck. After starting the controller as described in
[controller/README.md](../controller/README.md), run:

```sh
kubectl apply -f cluster/kueue-test.yaml
kubectl create -f cluster/sample-job.yaml
kubectl get admissionchecks,clusterqueues
kubectl get workloads,jobs
```

The current Workload controller is a scaffold: it does not mark workload admission
checks Ready yet. This sample is expected to remain suspended while that decision
logic is pending. Use `smoke-job.yaml` to verify standalone Kueue admission and job
completion. Both sample jobs start suspended so Kueue controls their admission.

