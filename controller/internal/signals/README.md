# Generic Signal Handler

## Included

- `signal.go`: shared signal and signal-set structures.
- `provider.go`: current-signal contract plus optional forecast capability.
- `handler.go`: caching, validation orchestration, combined signal retrieval, and forecast retrieval.
- `router.go`: normalized provider routing, duplicate-key rejection, and forecast routing.
- `validator.go`: shared signal validation and freshness rules.
- `adapters/carbon/`: Carbon current and forecast signal provider.
- `adapters/cost/`: Cost current and forecast signal provider.
- `adapters/demand/`: Kepler/Prometheus DemandCap signal provider.
- `handler_test.go`, `router_test.go`: handler and router tests.
- Adapter test files covering Carbon, Cost, and DemandCap integrations.

## Integration Notes

1. `NewProviderRouter` returns `(*ProviderRouter, error)`. Callers should handle the constructor error:
   `router, err := signals.NewProviderRouter(providers)`.

2. Forecast support is optional through:
   `GetForecastSignals(ctx, request, horizon) ([]Signal, error)`.

3. Carbon and Cost implement forecast support. DemandCap provides current cluster-demand signals only.

4. Demand signals use the shared `demand_cap` signal type. DemandCap results preserve `Cap`, `Utilization`, and `Status`.

5. `Timestamp` represents the source observation or market-period timestamp. `CollectedAt` records when the adapter retrieved the signal.

6. The default maximum signal age is one hour. Per-signal-type limits can be configured using `NewHandlerWithOptions`.

7. Forecast entries are not cached in the current implementation.

8. Adapter tests use mocked external services. Live provider retrieval still requires deployment-specific verification.

## DemandCap

The DemandCap provider uses Kepler CPU-package power metrics through Prometheus.

It:

- Retrieves current CPU-package power.
- Converts watts to kW.
- Calculates utilization against the configured cap.
- Reports `WITHIN_CAP` or `EXCEEDED`.
- Validates source-data freshness.
- Preserves both source observation time and collection time.

The handler remains responsible for caching; the DemandCap provider does not maintain a second cache.

## Verification

Run from the `controller` directory:

```text
go test ./...
go test -race ./...
```

These commands should be run against the current branch before merging.

## Separate Work

Cloud spot-price signal integration is being handled separately.  