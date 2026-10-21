# Generic Signal Handler

This package defines the shared signal contract, validates provider responses, routes requests by signal type, and caches valid signals by normalized signal type and region.

## Files
- `signal.go`: request and signal structures.
- `provider.go`: `SignalProvider` interface.
- `validator.go`: validation rules.
- `handler.go`: request normalization, provider retrieval, validation, and cache.
- `router.go`: provider routing.
- `handler_test.go`, `router_test.go`: unit tests.

## Test
From the existing `controller` module root, run:

```powershell
go test -count=1 ./internal/signals/...
go test -race -count=1 ./internal/signals/...
```

This package does not execute or communicate with adapter programs itself. A concrete `SignalProvider` implementation must connect this interface to the team's chosen adapter transport.

## Concurrency
The cache is safe for concurrent access. Simultaneous cache misses can still trigger multiple provider calls for the same key; preventing that would require per-key in-flight request coordination and is an optional optimization.
