# Kepler + Prometheus on Kind



## Environment

- Kind cluster `energygate` (WSL2), Kepler v0.12.0, kube-prometheus-stack



## Meter mode

Kind/WSL exposes no RAPL or hwmon, so Kepler crashes with

"no RAPL zones found". We enable the fake CPU meter in the `kepler`

ConfigMap (`dev.fake-cpu-meter.enabled: true`). Values are synthetic.



## Metrics

| Metric | Type | Labels |

|---|---|---|

| `kepler_node_cpu_watts` | gauge | node_name, zone (core, dram, package) |

| `kepler_node_cpu_joules_total` | counter | node_name, zone |



`package` includes `core`; do not sum zones.

Proposed demand metric: `kepler_node_cpu_watts{zone="package"}`.



## Observed values

Idle: about 3e-5 to 1.2e-4 W. Under `stress --cpu`: spike to about 1.5e-4 W.



## Pipeline

```mermaid

flowchart LR

  K[Kepler DaemonSet] -->|/metrics :28282| P[Prometheus]

  SM[ServiceMonitor] -.selects.-> K

  P --> Q[PromQL query]

  Q --> D[KeplerDemandProvider]

```



## Setup

1. `kind create cluster --name energygate`

2. Install kube-prometheus-stack (namespace `monitoring`)

3. Install Kepler Helm chart (namespace `kepler`), enable fake meter

4. `kubectl apply -f deploy/kepler-servicemonitor.yaml`

5. Verify: Prometheus Targets shows `kepler` UP
