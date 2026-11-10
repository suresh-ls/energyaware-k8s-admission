#!/usr/bin/env bash
# Read-only verification of scraped node, pod and container CPU power.
set -euo pipefail
context="${1:-kind-carbongate}"
service="${PROMETHEUS_SERVICE:-prometheus-kube-prometheus-prometheus}"
namespace="${PROMETHEUS_NAMESPACE:-monitoring}"
for scope in node pod container; do
  query="count(kepler_${scope}_cpu_watts{zone=\"package\"})"
  encoded="$(python3 -c 'import sys,urllib.parse; print(urllib.parse.quote(sys.argv[1],safe=""))' "$query")"
  kubectl --context "$context" --request-timeout=20s get --raw "/api/v1/namespaces/${namespace}/services/${service}:9090/proxy/api/v1/query?query=${encoded}" |
    python3 -c 'import json,sys; d=json.load(sys.stdin); r=d.get("data",{}).get("result",[]); n=float(r[0]["value"][1]) if len(r)==1 else 0; print(sys.argv[1]+": "+str(n)+" scraped package series"); sys.exit(0 if d.get("status")=="success" and n>0 and not d.get("warnings") else 1)' "$scope"
done
