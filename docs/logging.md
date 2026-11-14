# Kepler demand and history logging

Logging in this change is limited to the production demand adapter, its probe,
the legacy standalone Kepler provider/probe, and historical energy export.
The generic handler, other adapters and admission components keep their existing
logging behavior and remain with their respective owners.

## Production demand adapter

The controller adapter uses `controller-runtime/pkg/log.FromContext`, so it
inherits the controller's logger and request context. It does not create a global
logger or change the shared signal structure.

| Level | Event and fields |
|---|---|
| Debug (`V(1)`) | Retrieval started; Prometheus response with query kind and HTTP status; cancellation |
| Info | Retrieval completed: provider, signal type, region, power, unit, cap, utilization, status, source timestamp, expiry, quality and duration |
| Error | Retrieval failed: provider, region, processing stage, safe error category and duration |

The processing stage identifies request validation, power query, source timestamp
query, measurement validation, or freshness validation. Failed retrievals never
emit a successful completion event. Cancellation is logged at debug rather than
as a provider error.

The production probe initializes the same Zap backend as the controller, using
its standard flags. The default is JSON on stderr:

```sh
# From controller/; stdout remains the demand signal JSON.
go run ./cmd/demand-probe -region IN-SO -cap-kw 10 \
  -zap-log-level=debug > demand.json 2> demand.log
```

Callers can attach correlation fields before invoking the handler/provider:

```go
ctx = log.IntoContext(ctx, log.FromContext(ctx).WithValues(
    "request_id", requestID,
    "workload_namespace", workload.Namespace,
    "workload_name", workload.Name,
))
```

These fields are inherited by demand events. The adapter does not generate a
new unrelated request ID or log kubeconfig contents.

## Historical export and legacy probe

The root module uses the standard-library `log/slog` JSON handler. Both commands
accept `-log-level=debug|info|warn|error`, defaulting to info. Logs go to stderr;
stdout remains only the exported signal/dataset JSON. Flag help and argument
parser usage retain their normal command-line formatting.

```sh
# From repository root:
go run ./cmd/energy-export -workloads workloads.json -log-level debug \
  > energy-history.json 2> energy-history.log

go run ./cmd/demand-probe -cap 20 -unit W -log-level debug \
  > legacy-demand.json 2> legacy-demand.log
```

Library callers can supply `Exporter.Logger` or `kepler.Config.Logger` with
`logger.With("request_id", requestID)` to retain their own correlation fields.
Without an injected logger these libraries use `slog.Default()`; the commands
explicitly configure JSON output without changing global loggers.

History logs identify namespace, workload name and workload UID. Debug events
record export start and HTTP status. Completion records energy Wh, runtime,
sample count, measurement mode, estimated boundary seconds and duration. Explicit
boundary estimation emits a warning. Failures include the processing stage and
a safe error category. Dataset completion records the workload count only after
the JSON data was written successfully.

## Data included and excluded

Logs include measurements and workload identifiers needed to trace a retrieval or
export. They deliberately exclude Prometheus URLs, userinfo, API tokens, request
headers, raw PromQL, response bodies and raw transport/parse error text. Such
errors can embed credentials or provider payloads; safe error categories and
processing stages are logged instead. Returned library errors retain their
existing detail for callers to handle. Do not add `err.Error()` indiscriminately
to logs at the caller boundary.

Tests capture structured records for success, failure and expiry, verify inherited
request/workload fields, exercise estimation warnings, assert that provider
secrets are absent, and confirm that exporter stdout remains valid dataset JSON.
