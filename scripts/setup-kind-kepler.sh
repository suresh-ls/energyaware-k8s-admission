#!/usr/bin/env bash
# setup-kind-kepler.sh
#
# Creates a Kind cluster, installs Prometheus (kube-prometheus-stack) and
# Kepler, wires Prometheus to scrape Kepler, and verifies that
# kepler_node_cpu_watts is queryable.
#
# Usage:
#   ./scripts/setup-kind-kepler.sh                 # real RAPL (bare metal Linux)
#   ./scripts/setup-kind-kepler.sh --fake-meter    # WSL2 / VMs without RAPL
#   ./scripts/setup-kind-kepler.sh --teardown      # delete the cluster
#
# Environment overrides:
#   CLUSTER_NAME (default: energygate)

set -euo pipefail

CLUSTER_NAME="${CLUSTER_NAME:-energygate}"
PROM_NS="monitoring"
KEPLER_NS="kepler"
PROM_LOCAL_PORT="${PROM_LOCAL_PORT:-9090}"
FAKE_METER="false"
PF_PID=""

log()  { printf '\n\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33mWARN:\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31mERROR:\033[0m %s\n' "$*" >&2; exit 1; }

cleanup() {
  if [[ -n "${PF_PID}" ]]; then
    kill "${PF_PID}" 2>/dev/null || true
  fi
}
trap cleanup EXIT

usage() {
  sed -n '2,15p' "$0" | sed 's/^# \{0,1\}//'
  exit 0
}

# ---------------------------------------------------------------- arguments
TEARDOWN="false"
for arg in "$@"; do
  case "$arg" in
    --fake-meter) FAKE_METER="true" ;;
    --teardown)   TEARDOWN="true" ;;
    -h|--help)    usage ;;
    *)            die "unknown argument: $arg (try --help)" ;;
  esac
done

if [[ "$TEARDOWN" == "true" ]]; then
  log "Deleting Kind cluster '${CLUSTER_NAME}'"
  kind delete cluster --name "${CLUSTER_NAME}"
  exit 0
fi

# ---------------------------------------------------------------- preflight
log "Checking prerequisites"
for bin in docker kind kubectl helm curl; do
  command -v "$bin" >/dev/null 2>&1 || die "'$bin' is not installed (see docs/kepler-setup.md)"
done
docker info >/dev/null 2>&1 || die "Docker is not running or you lack permission (add yourself to the docker group, then log out and in)"

log "Checking for RAPL on the host"
if compgen -G "/sys/class/powercap/intel-rapl*" >/dev/null; then
  echo "RAPL found on host:"
  ls /sys/class/powercap/ | sed 's/^/  /'
  if [[ "$FAKE_METER" == "true" ]]; then
    warn "RAPL exists but --fake-meter was given. Values will be synthetic."
  fi
else
  warn "No /sys/class/powercap/intel-rapl* on this host."
  if [[ "$FAKE_METER" != "true" ]]; then
    warn "Kepler will crash with 'no RAPL zones found'."
    warn "Re-run with --fake-meter to use synthetic values (WSL2 / VMs)."
    die "Aborting. Use --fake-meter or run on bare-metal Linux."
  fi
fi

# ---------------------------------------------------------------- cluster
if kind get clusters 2>/dev/null | grep -qx "${CLUSTER_NAME}"; then
  log "Kind cluster '${CLUSTER_NAME}' already exists, reusing it"
else
  log "Creating Kind cluster '${CLUSTER_NAME}'"
  kind create cluster --name "${CLUSTER_NAME}"
fi
kubectl config use-context "kind-${CLUSTER_NAME}" >/dev/null
kubectl wait --for=condition=Ready node --all --timeout=120s

if [[ "$FAKE_METER" != "true" ]]; then
  log "Checking RAPL is visible inside the Kind node"
  if docker exec "${CLUSTER_NAME}-control-plane" ls /sys/class/powercap 2>/dev/null | grep -q rapl; then
    echo "OK: RAPL visible inside the node"
  else
    warn "RAPL is NOT visible inside the Kind node. Kepler may fail."
  fi
fi

# ---------------------------------------------------------------- prometheus
log "Installing kube-prometheus-stack (this can take several minutes)"
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts >/dev/null 2>&1 || true
helm repo update >/dev/null
helm upgrade --install prometheus prometheus-community/kube-prometheus-stack \
  --namespace "${PROM_NS}" --create-namespace \
  --set prometheus.prometheusSpec.serviceMonitorSelectorNilUsesHelmValues=false \
  --wait --timeout 10m

# ---------------------------------------------------------------- kepler
log "Installing Kepler"
helm upgrade --install kepler oci://quay.io/sustainable_computing_io/charts/kepler \
  --namespace "${KEPLER_NS}" --create-namespace

if [[ "$FAKE_METER" == "true" ]]; then
  # Best effort: enable dev.fake-cpu-meter in the kepler ConfigMap.
  # Not verified against every chart version. If it fails, edit the
  # ConfigMap by hand (see docs/kepler-setup.md, "Fake meter").
  log "Enabling Kepler fake CPU meter (synthetic values)"
  CFG="$(kubectl get cm kepler -n "${KEPLER_NS}" -o jsonpath='{.data.config\.yaml}')"
  [[ -n "$CFG" ]] || die "ConfigMap 'kepler' has no config.yaml key; enable dev.fake-cpu-meter manually"
  if grep -q 'fake-cpu-meter' <<<"$CFG"; then
    NEW="$(sed '/fake-cpu-meter:/,/enabled:/ s/enabled: *false/enabled: true/' <<<"$CFG")"
  else
    NEW="${CFG}"$'\n'"dev:"$'\n'"  fake-cpu-meter:"$'\n'"    enabled: true"
  fi
  kubectl create cm kepler -n "${KEPLER_NS}" --from-literal=config.yaml="${NEW}" \
    --dry-run=client -o yaml | kubectl replace -f -
  kubectl rollout restart ds/kepler -n "${KEPLER_NS}"
fi

log "Waiting for Kepler DaemonSet"
kubectl rollout status ds/kepler -n "${KEPLER_NS}" --timeout=180s || {
  warn "Kepler did not become ready. Last log lines:"
  kubectl logs -n "${KEPLER_NS}" -l app.kubernetes.io/name=kepler --tail=30 || true
  die "Kepler is not running (see docs/kepler-setup.md, Troubleshooting)"
}

# ---------------------------------------------------------------- servicemonitor
log "Creating ServiceMonitor so Prometheus scrapes Kepler"
SVC_LABEL="$(kubectl get svc kepler -n "${KEPLER_NS}" -o jsonpath='{.metadata.labels.app\.kubernetes\.io/name}')"
PORT_NAME="$(kubectl get svc kepler -n "${KEPLER_NS}" -o jsonpath='{.spec.ports[0].name}')"
PORT_NUM="$(kubectl get svc kepler -n "${KEPLER_NS}" -o jsonpath='{.spec.ports[0].port}')"
[[ -n "$SVC_LABEL" ]] || SVC_LABEL="kepler"
echo "Service label app.kubernetes.io/name=${SVC_LABEL}, port name='${PORT_NAME}', port=${PORT_NUM}"

if [[ -n "$PORT_NAME" ]]; then
  ENDPOINT_PORT="    - port: ${PORT_NAME}"
else
  ENDPOINT_PORT="    - targetPort: ${PORT_NUM}"
fi

kubectl apply -f - <<EOF
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: kepler
  namespace: ${KEPLER_NS}
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: ${SVC_LABEL}
  endpoints:
${ENDPOINT_PORT}
      interval: 15s
EOF

# ---------------------------------------------------------------- verify
log "Verifying that Prometheus can query kepler_node_cpu_watts"
kubectl port-forward -n "${PROM_NS}" svc/prometheus-kube-prometheus-prometheus \
  "${PROM_LOCAL_PORT}:9090" >/dev/null 2>&1 &
PF_PID=$!
sleep 5

QUERY='kepler_node_cpu_watts'
FOUND="false"
for i in $(seq 1 12); do
  RESP="$(curl -s "http://localhost:${PROM_LOCAL_PORT}/api/v1/query" --data-urlencode "query=${QUERY}" || true)"
  if grep -q '"result":\[{' <<<"$RESP"; then
    FOUND="true"
    break
  fi
  echo "  waiting for first scrape (${i}/12)..."
  sleep 10
done

if [[ "$FOUND" == "true" ]]; then
  log "SUCCESS: Prometheus is scraping Kepler"
  echo "$RESP" | grep -o '"zone":"[^"]*"' | sort -u | sed 's/^/  found /'
  cat <<MSG

Next steps:
  kubectl port-forward -n ${PROM_NS} svc/prometheus-kube-prometheus-prometheus 9090:9090
  open http://localhost:9090/targets          (kepler should be UP)
  query: kepler_node_cpu_watts
  load test: kubectl run stress --image=polinux/stress --restart=Never -- stress --cpu 4 --timeout 120
MSG
else
  warn "No data for ${QUERY} after 2 minutes."
  warn "Check http://localhost:${PROM_LOCAL_PORT}/targets and docs/kepler-setup.md (Troubleshooting)."
  exit 1
fi