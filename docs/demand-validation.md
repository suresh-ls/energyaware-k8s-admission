# Demand implementation validation — 10 November 2026

Branch: `feature/kepler-demand-signal`. Locally merged generic-handler commit
`393062d` and main commit `a036d15`; no remote branch was updated.

## Issue coverage

- #5 / #7 / #8: real Kepler adapter inside the controller, shared provider contract,
  deployment-region checking, kW conversion and canonical demand fields.
- #6: setup reuses/defaults to carbongate and creates the shared three-node Kind
  configuration if absent. Read-only metric verification script added. Live
  validation used the existing kind-energygate cluster; carbongate was not present.
- #9: under/equal/over-cap and invalid numeric inputs covered.
- #10: production handler owns caching, expiry is never extended, CACHED is
  explicit, expired/unavailable measurements return errors. The original stale
  return policy is replaced by the integration plan's error/WAIT contract; record
  this interpretation when closing the issue. Legacy cache expiry is fixed too.
- #11: mocked provider/router/handler tests, expiry/outage/recovery, region/schema
  failures, and root-level CI for both Go modules.
- #12: historical JSON export with workload/resource metadata, runtime, Wh and
  measurement mode. Strict coverage by default; optional bounded edge estimates.
- #13: demand contract/configuration/failure policy, JSON example and pipeline
  diagram in docs/demand-signal.md, plus setup and exporter guides.

## Checks performed

- Root `go test -race -count=1 ./...` and `go vet ./...`: passed.
- Controller `go test -race -count=1 ./internal/signals/... ./cmd/demand-probe`
  and the matching `go vet`: passed with Go 1.27 downloaded into /tmp.
- Controller `make test`: passed, including envtest and signal adapters.
- Shell syntax checks and `git diff --check`: passed.
- `make lint-fix`: blocked before analysis by the inherited missing custom
  `logcheck` plugin. The repository linter configuration was retained. The new adapter/probe
  passed the remaining configured linters using a temporary configuration that
  omitted only that unavailable plugin.

## Live checks

On the existing kind-energygate development cluster:

- Prometheus had 1 node, 16 pod and 36 container CPU package-power series when
  scripts/verify-kepler.sh was run. Counts vary as workloads change.
- The production demand probe returned a FRESH, WITHIN_CAP measurement through
  the real adapter, router and shared handler. It used the measured source time,
  separate collection time and source-bounded expiry.
- Historical export of the completed 120-second stress workload retrieved 8
  samples. Strict mode rejected its missing initial scrape. Explicit
  `-allow-edge-estimates` produced approximately 0.4984 Wh, marked estimated,
  with 15 estimated boundary seconds. This is a validation example, not a
  benchmark or evidence of carbon savings.

## Remaining team integration

Construct these providers in manager startup and connect the Decision Engine to
Workload reconciliation; those components remain separately owned scaffolds.
Apply/verify monitoring on the shared carbongate cluster when the team deploys
there. No full run/wait admission demo or GPU-energy measurement is claimed.
CI has been added but has not run on GitHub. No issues were closed or PR published.
