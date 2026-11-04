# EnergyGate Cost Signal Provider

A standalone Go provider that retrieves the latest Indian Electricity Exchange (IEX) Day-Ahead Market (DAM) electricity price from EnergyMap and converts it into INR/kWh.

## Features

- Retrieves the latest IEX DAM electricity price.
- Authenticates using an EnergyMap API key.
- Converts prices from INR/MWh to INR/kWh.
- Validates timestamps, regions, market type, and price values.
- Handles HTTP errors, malformed responses, and missing records.
- Marks potentially stale data using the API's freshness metadata.
- Includes automated tests using Go's `httptest` package.

## Requirements

- Go 1.26 or a compatible version.
- An EnergyMap API key with access to the IEX market endpoint.

## Configuration

Set your API key using the `ENERGYMAP_API_KEY` environment variable.

### Windows CMD

```cmd
set ENERGYMAP_API_KEY=your_api_key_here
```

This sets the variable for the current CMD session.

### Linux/macOS

```bash
export ENERGYMAP_API_KEY=your_api_key_here
```

Do not commit your real API key to the repository.

## API Endpoint

The provider requests:

```text
GET https://api.energymap.in/developer/v1/market/iex/latest?limit=1&market_type=DAM
```

The request includes the following headers:

```text
X-API-Key: your_api_key_here
Accept: application/json
```

### Expected Response Fields

The current implementation expects a JSON response containing a `data` array and a `meta` object.

Each price record is expected to contain:

- `timestamp`: timestamp of the market record.
- `market_type`: expected to be `DAM`.
- `region`: market region.
- `mcp_rs_mwh`: electricity price in INR/MWh.
- `source`: source identifier.

The metadata is expected to contain:

- `freshness_lag_minutes`: reported data freshness lag in minutes.

**Note:** Verify the expected response fields against the actual EnergyMap API documentation and an authorized API response before production integration.

## Unit Conversion

The source price is expected in INR/MWh. The provider converts it to INR/kWh using:

```text
Price (INR/kWh) = Price (INR/MWh) / 1000
```

For example:

```text
5000 INR/MWh = 5 INR/kWh
```

This is a unit conversion, not a cost-normalization or scaling policy.

## Running the Provider

Open CMD in the `cost-adapter` directory and run:

```cmd
go run .
```

If the request succeeds, the provider prints a JSON cost signal to standard output. Errors are printed to standard error.

### Example Output

The following is an illustrative example, not live market data:

```json
{
  "signal_type": "electricity_price",
  "provider": "EnergyMap-IEX",
  "region": "India",
  "value": 5,
  "unit": "INR/kWh",
  "timestamp": "2026-10-14T08:00:00Z",
  "valid_until": "2026-10-14T08:15:00Z",
  "is_forecast": false,
  "quality": "reported"
}
```

## Running Tests

Run the automated tests from the `cost-adapter` directory:

```cmd
go test -v ./...
```

The tests use a local mock HTTP server. They do not require a live EnergyMap API key and do not make requests to the actual service.

The current test suite covers:

- Successful retrieval and unit conversion.
- Missing API key.
- HTTP errors.
- Malformed JSON.
- Empty price records.
- Invalid price values.
- Stale-data quality marking.

## Freshness and Signal Contract

The current implementation marks data as `possibly_stale` when `freshness_lag_minutes` exceeds 15.

It currently sets `valid_until` to 15 minutes after the source timestamp.

These freshness and validity policies are provisional and must be reviewed with the EnergyGate team before integration.

The `CostSignal` structure is an initial representation. Its fields and semantics should be aligned with the shared EnergyGate signal contract before integration with the decision engine.

## Current Scope

This component retrieves and validates an electricity-market cost signal. It does not implement workload scheduling decisions, admission control, or the final cost-normalization policy.

## Limitations

- Requires valid API credentials and network access to EnergyMap.
- Depends on the actual API response matching the expected schema.
- Freshness thresholds and signal validity policies require confirmation.
- The provider has not yet been verified against a live API response.
