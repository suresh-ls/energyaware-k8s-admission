# EnergyGate

Energy-aware Kubernetes workload admission using carbon, cost and demand signals.

- [Cluster and Kueue setup](cluster/README.md)
- [Kepler + Prometheus setup](docs/kepler-setup.md)
- [Demand signal contract, configuration and failure policy](docs/demand-signal.md)
- [Historical workload energy export](docs/energy-export.md)
- [Kepler demand and history logging](docs/logging.md)
- [Controller](controller/README.md)

Production signal adapters live in `controller/internal/signals/adapters`.
The real demand adapter replaces the dummy on this branch. The controller's
Workload reconciler still needs its separately owned Decision Engine wiring;
provider integration alone does not admit jobs.

The root module (Go 1.24+) contains the legacy standalone demand probe and the
history exporter. The controller module requires Go 1.27. Run checks in both:

```sh
go test -race ./...
go vet ./...
cd controller
go test -race ./internal/signals/... ./cmd/demand-probe
go vet ./internal/signals/... ./cmd/demand-probe
```
