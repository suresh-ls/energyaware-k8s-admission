
# Carbon Signal Adapter

The Carbon Signal Adapter fetches the latest carbon intensity from the Electricity Maps API and converts it into the normalized JSON signal format used by EnergyGate.

## Requirements

- Go installed
- An Electricity Maps API token

## Configuration

Set the following environment variables:

- `ELECTRICITY_MAPS_API_TOKEN` — Your Electricity Maps API token.
- `ELECTRICITY_MAPS_ZONE` — The electricity zone to query. Defaults to `IN-SO`.

For Git Bash on Windows:

```bash
export ELECTRICITY_MAPS_API_TOKEN="your_api_token"
export ELECTRICITY_MAPS_ZONE="IN-SO"
```

Replace `your_api_token` with your actual token. Never commit your API token to GitHub.

## Run the Adapter

From the `carbon-adapter` directory:

```bash
go run .
```

The adapter prints a normalized JSON signal containing the signal type, provider, region, carbon intensity, unit, timestamp, validity, forecast status, and quality.

## Run Tests

Run all unit tests using:

```bash
go test -v ./...
```

The following 10 unit tests are implemented:

1. `TestFetchCarbonIntensitySuccess` — Successful API response.
2. `TestFetchCarbonIntensityHTTPError` — HTTP error response.
3. `TestFetchCarbonIntensityMissingValue` — Missing carbon intensity.
4. `TestFetchCarbonIntensityNegativeValue` — Negative carbon intensity.
5. `TestFetchCarbonIntensityMissingTimestamp` — Missing timestamp.
6. `TestFetchCarbonIntensityInvalidJSON` — Invalid JSON response.
7. `TestFetchCarbonIntensityMissingZone` — Missing zone.
8. `TestFetchCarbonIntensityMissingGranularity` — Missing temporal granularity.
9. `TestFetchCarbonIntensityZeroTimestamp` — Zero timestamp.
10. `TestFetchCarbonIntensityMissingToken` — Missing API token.

All 10 tests passed in the latest test run.

## Future Issues

- Add support for carbon-intensity forecasts.
- Integrate the adapter with the Generic Signal Handler.
- Coordinate caching, stale-data handling, and fallback behavior with the handler.
- Add integration tests after the shared interface is finalized.
