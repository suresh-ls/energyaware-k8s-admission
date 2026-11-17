# Cloud spot pricing admission

This opt-in feature adds a current AWS EC2 spot-price budget gate to the decision
engine. It is based on `feature/decision-engine` (PR #41). Existing carbon,
electricity cost, demand-cap signals, scoring weights, and non-cloud workloads
keep their existing behavior. The existing `SignalSource` interface is unchanged;
only cloud workloads require its optional `GetSignal` capability.

## What the feature does

The adapter retrieves a quote for one configured AWS region, availability zone,
instance type, and Linux/UNIX product. It handles pagination and selects the most
recent matching effective price, regardless of response order. An old effective
timestamp is valid if the lookup is fresh: AWS may not have changed the price.
Collection time and a five-minute cache TTL control freshness separately.

The signal has type `cloud_spot` and unit `USD/instance-hour`. Cloud pool identity
is part of the cache key and is validated on the returned quote. It never uses
the electricity cost signal's `ALL-INDIA` region or `INR/kWh` unit.

For an opted-in workload:

```text
estimated compute cost = current USD/instance-hour
                       * estimated billable instance count
                       * expected runtime in hours
```

- Within budget: continue the existing carbon/electricity decision process.
- Above budget or unavailable/invalid quote: WAIT with a retry bounded by the
  engine's default retry, allowed delay, and latest feasible start time.
- No feasible retry remains, the deadline is already infeasible, or waiting is
  forbidden: REJECT. This maps to Kueue Rejected and deactivates the Workload.
- Invalid opt-in metadata or a pool mismatch: REJECT with a diagnostic message.
- An opted-in workload never uses the legacy deadline override to bypass the
  current power cap or its hard estimated-cost budget.
- Existing Ready/Rejected checks are not re-evaluated, so a later price change
  does not evict a running workload. Other controllers' checks are preserved.

The estimate is included in `Decision.CloudSpot`, with pool, price, budget,
collection time, and replay indicator. Current cloud prices do not change the
carbon/electricity score weights. Future-price modeling and a separate cloud
score component are deliberately left for a later feature: current quotes are
not assumed to be known prices for future execution windows. Admission always
re-evaluates current pricing after a retry.

## Controller configuration

With `CLOUD_SPOT_MODE` unset the adapter is disabled and AWS credentials are not
loaded. A workload that explicitly opts in while it is disabled is rejected.

```sh
export CLOUD_SPOT_MODE=aws
export CLOUD_SPOT_REGION=ap-south-1
export CLOUD_SPOT_AVAILABILITY_ZONE=ap-south-1a
export CLOUD_SPOT_INSTANCE_TYPE=c5.xlarge
```

Use the AWS SDK default credential chain (for example a workload IAM role or
local AWS profile). No credentials are embedded in the code or manifests.
The price lookup needs only `ec2:DescribeSpotPriceHistory`; it never launches or
terminates instances. The SDK client and the adapter have bounded requests.
The region, zone, instance type, and product must match the execution pool.

For an offline simulation, use the same pool variables and:

```sh
export CLOUD_SPOT_MODE=replay
export CLOUD_SPOT_TRACE_FILE=/absolute/path/to/cluster/cloud-spot-trace.json
```

The trace is explicitly synthetic and is not a real AWS price claim. Replay
selects only records effective at or before the evaluation clock; future records
cannot leak into current quotes. Each price remains effective until the next
record. Unit tests inject a clock to exercise trace transitions without sleeping.
Replay does not need AWS credentials, but the existing carbon, electricity, and
demand providers still need their normal configuration or test doubles.

When deploying the manager, supply these variables in its Deployment and mount
the trace file for replay mode. This feature does not change the default manager
manifest or your cluster automatically.

## Workload contract

Set these annotations **on the Kueue Workload**, not just on its owning Job:

| Annotation | Example | Meaning |
|---|---|---|
| `energygate.io/cloud-spot-budget-usd` | `"0.10"` | Presence opts in; positive finite estimated compute-cost limit |
| `energygate.io/cloud-spot-instances` | `"1"` | Positive estimated number of billable nodes; not GPU count |
| `energygate.io/expected-runtime` | `"5m"` | Positive Go duration |
| `energygate.io/deadline` | `"2026-12-01T12:00:00Z"` | Fixed absolute completion deadline, not reset on each reconcile |

This PR does not add Job-to-Workload annotation propagation. Configure an
appropriate Kueue admission policy or annotate a suspended/pending Workload
before allowing EnergyGate to evaluate it. The standalone Workload example
`cluster/cloud-spot-workload.yaml` is a control-plane demo; it has no owning Job
and therefore does not launch application Pods. Set its deadline to a future time
before applying it to an isolated test cluster with the normal queues and check.

Every PodSet must explicitly select the configured pool with:

```yaml
nodeSelector:
  eks.amazonaws.com/capacityType: SPOT
  node.kubernetes.io/instance-type: c5.xlarge
  topology.kubernetes.io/zone: ap-south-1a
  topology.kubernetes.io/region: ap-south-1
  kubernetes.io/os: linux
```

Use equivalent labels on simulated nodes. In a real cluster, configure a matching
Kueue ResourceFlavor, appropriate quotas, and a node provisioner if needed.
Flavor-assigned labels must not conflict with the selectors. This gate does not
choose a new flavor, move jobs across regions, or guarantee capacity.

## Validation and limits

Tests cover paginated/unsorted AWS results, pool filtering, old effective prices
with fresh collection, invalid records, cancellation, pagination loops, replay
time, cross-pool cache isolation/expiry, feature-off compatibility, cost/deadline
decisions, node selectors, and controller status updates with a fake API client.
The repository-root `Cloud spot compatibility` workflow runs on Linux, including
the pre-existing signal and engine tests, race checks, controller fake-client
integration, production build, vet, and module consistency. It does not require
cloud credentials or a live Kubernetes cluster. Run these checks locally with:

```sh
cd controller
go test -race ./internal/signals/... ./internal/decision-engine
go test -race ./internal/controller -run '^TestCloudSpot'
go build -o /tmp/energygate ./cmd/main.go
go vet ./cmd/... ./internal/...
```

The hard limit applies to the **estimate**, not the eventual invoice. Instance
count/runtime are workload estimates. Prices can change during execution;
storage, network, retries, and idle nodes can add cost. Electricity tariffs must
not be added a second time to an actual cloud compute bill. Waiting on already
paid nodes alone does not establish savings. Replay/Kind/KWOK demonstrations
show simulated decisions, not measured cloud savings or real GPU execution.

AWS history is not a future-price forecast. Spot capacity and interruption
recovery are separate concerns; deadline completion is not guaranteed. Use
retryable/checkpoint-capable jobs and node lifecycle handling for real deployments.

References:

- [AWS price history API](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DescribeSpotPriceHistory.html)
- [AWS SDK configuration and credentials](https://docs.aws.amazon.com/sdk-for-go/v2/developer-guide/configure-gosdk.html)
- [Kueue flavors](https://kueue.sigs.k8s.io/docs/concepts/resource_flavor/)
- [Kueue admission states](https://kueue.sigs.k8s.io/docs/concepts/admission_check/)
