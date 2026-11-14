# Historical workload energy export (#12)

`go run ./cmd/energy-export` queries Prometheus history and writes a JSON array
containing each workload's energy in Wh, CPU cores, memory bytes, GPU count,
runtime, priority, namespace/name/UID, interval, query, measurement mode and sample
count. Save stdout to a file for the future prediction layer. No credentials or
Kubernetes client libraries are required.

The input is a JSON array of completed workload metadata, recorded from the Job
and its owned Pods by the caller. CPU, memory and GPU fields are total requested
resources; priority is the workload priority. Start/end delimit actual execution,
not queue time. UID prevents confusing jobs that reuse a name. Select every Pod UID
belonging to that execution, including retries, when building its query.

```json
[
  {
    "namespace": "training",
    "name": "train-a",
    "uid": "job-uid-from-kubernetes",
    "cpu_cores": 2,
    "memory_bytes": 1073741824,
    "gpus": 0,
    "priority": 10,
    "start": "2026-11-10T09:00:00Z",
    "end": "2026-11-10T09:10:00Z",
    "power_query": "sum(kepler_pod_cpu_watts{pod_id=\"actual-pod-uid\",zone=\"package\"} and (time() - timestamp(kepler_pod_cpu_watts{pod_id=\"actual-pod-uid\",zone=\"package\"}) < 60))",
    "measurement_mode": "estimated"
  }
]
```

Replace UIDs, dates, metric labels and expressions with actual recorded values.
Inspect the live Kepler metric first: label names differ across versions.
`pod_id` and `zone="package"` were observed on this installation's v0.12.0 metrics.
Do not substitute node-wide power for a workload's power. Never sum pod and
container representations of the same energy or overlapping RAPL zones.
If querying multiple pods/containers, ensure the expression returns no sample
when an expected contributor is missing (for example using a count guard).

```sh
# From repository root, while Prometheus is port-forwarded:
go run ./cmd/energy-export -workloads workloads.json -step 15s > energy-history.json
```

The command buffers all records before writing JSON; query failures produce a
nonzero exit and no partial dataset (shell redirection can still create an empty
file). Duplicate workload UIDs and unknown input fields are rejected.

The exporter uses `/api/v1/query_range`, requires exactly one finite, nonnegative
watts series over the complete interval and integrates adjacent samples using the
trapezoidal rule: `Wh = sum((W[i-1]+W[i])/2 * seconds) / 3600`. A constant 100 W
for one hour yields 100 Wh. It adjusts the requested step to divide the interval
exactly, includes the end sample, and rejects missing/off-grid samples by default, warnings,
multiple series and API errors. At most 11,000 intervals are allowed. Sampling
still approximates power variation between scrapes; it is not a counter delta.

`measurement_mode` must be `measured`, `estimated`, or `synthetic`. Pod/container
CPU attribution is normally **estimated**. GPU count is metadata only; CPU Kepler
metrics do not measure GPU energy. A source-freshness filter is essential because
Prometheus range evaluation times do not prove recent scrapes. Data already lost
to retention, sparse history, and workloads lacking attribution cannot be
reconstructed by this exporter; it reports failure rather than a false zero.

The exporter intentionally accepts recorded metadata so it also works after Jobs
are deleted and offline from Kubernetes. Automatic metadata collection by the
controller and prediction-model training are separate integration work.


## Scrape boundaries

A workload often starts between scrapes, so its first evaluation has no value.
The default strict mode rejects that incomplete interval. If a bounded estimate
is acceptable, pass `-allow-edge-estimates`. It permits at most one missing
integration step at each edge, holding the nearest observed power constant there.
At least two real evaluation samples are still required, and internal gaps remain
errors. `estimated_boundary_seconds` records exactly how much runtime was filled
this way (zero in strict/full coverage mode). This field must travel with the
training dataset; do not present an edge estimate as a fully observed interval.


Structured logging, log-level flags and output streams are documented in
[Kepler demand and history logging](logging.md).
