# Updated Generic Signal Handler

## Included files
- `signal.go`: shared signal and signal-set structures.
- `provider.go`: current-signal contract plus optional forecast capability.
- `handler.go`: cache, validation orchestration, combined signal retrieval, and forecast retrieval.
- `router.go`: normalized routing, duplicate-key rejection, and optional forecast routing.
- `validator.go`: shared signal validation and freshness rules.
- `handler_test.go`, `router_test.go`: unit tests for the changes.

## Important integration notes
1. `NewProviderRouter` now returns `(*ProviderRouter, error)`. Update call sites:
   `router, err := signals.NewProviderRouter(providers)`.
2. Forecast support is an optional interface:
   `GetForecastSignals(ctx, request, horizon) ([]Signal, error)`.
   A carbon adapter must implement it to supply the 24-hour forecast. The adapter implementation was not included in the uploaded files, so forecast API integration is not implemented or verified here.
3. Demand signals are normalized from `signal_type: "demand"` to the shared `demand_cap` type. Demand provider results must preserve `Cap`, `Utilization`, and `Status`; the handler rejects demand signals with a non-positive cap, invalid utilization, or empty status.
4. `Timestamp` remains the source observation/market-period timestamp. `CollectedAt` records retrieval time. Cost adapters should set `CollectedAt` so the max-age policy checks freshness without rejecting future market delivery timestamps.
5. The default maximum signal age is one hour. Configure per-type limits using `NewHandlerWithOptions`.
6. Quality values are currently required to be non-empty, but are not checked against a fixed vocabulary. The team must agree on the allowed values before enforcement is safe.
7. Forecast entries are not cached in this version; forecast-specific cache semantics should be agreed before adding that behavior.
8. This package cannot verify external adapter integration because only the generic signal-handler files and their tests were provided.

## Verification
Tested with:
- `go test ./...`
- `go test -race ./...`

Both passed for the included package.
