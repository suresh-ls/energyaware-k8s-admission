
# Carbon Signal Adapter

Fetches carbon intensity data from the Electricity Maps API and converts it into the normalized signal format used by EnergyGate.

## Implemented

- Fetches the latest carbon intensity for a configured electricity zone.
- Validates the API response, including required fields and non-negative carbon intensity.
- Handles HTTP errors, invalid JSON, and missing API tokens.
- Converts the response into a normalized JSON signal with timestamp, validity, and freshness status.
- Includes 10 unit tests covering success and error cases.

## Configuration

Set these environment variables before running:

- `ELECTRICITY_MAPS_API_TOKEN` — Electricity Maps API token.
- `ELECTRICITY_MAPS_ZONE` — Electricity zone (defaults to `IN-SO`).

## Run

```bash
go run .
```

## Tests

```bash
go test -v ./...
```

## Future Issues

- Add support for carbon-intensity forecasts.
- Integrate with the Generic Signal Handler.
- Improve handling of stale data and coordinate caching/fallback with the handler.
- Add further validation and integration tests as the interface is finalized.
