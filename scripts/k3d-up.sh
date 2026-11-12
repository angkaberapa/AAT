#!/usr/bin/env bash
# Cluster PENGEMBANGAN saja (k3d = node berupa container, tidak sah untuk demo).
# Membuat cluster 1 server + 2 agent, membangun semua image, lalu meng-import-nya ke cluster.
#   scripts/k3d-up.sh            # buat cluster (kalau belum ada) + build + import
#   SKIP_CLUSTER=1 scripts/k3d-up.sh   # hanya build + import ulang image
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CLUSTER="${CLUSTER:-bnpb}"
SERVICES=(bmkg-mock pvmbg-mock bnpb-coastal bnpb-aggregator bnpb-auth bnpb-api bnpb-consumer-dashboard bnpb-consumer-notifier)

if [[ -z "${SKIP_CLUSTER:-}" ]] && ! k3d cluster get "$CLUSTER" >/dev/null 2>&1; then
  # Port host sama dengan M1; diteruskan ke Service LoadBalancer lewat loadbalancer k3d.
  k3d cluster create "$CLUSTER" --agents 2 \
    -p "8080:8080@loadbalancer" -p "8081:8081@loadbalancer" -p "8082:8082@loadbalancer" \
    -p "8090:8090@loadbalancer" -p "8091:8091@loadbalancer" -p "8092:8092@loadbalancer"
fi

images=()
for svc in "${SERVICES[@]}"; do
  target=()
  [[ "$svc" == pvmbg-mock ]] && target=(--target runtime)
  docker build "${target[@]}" -t "aat/${svc}:dev" "$ROOT/services/$svc"
  images+=("aat/${svc}:dev")
done
k3d image import -c "$CLUSTER" "${images[@]}"

# Pod lama tetap memakai image lama dengan tag yang sama; restart supaya mengambil hasil import.
if kubectl get ns sistem-informasi-kebencanaan-bnpb >/dev/null 2>&1; then
  kubectl -n sistem-informasi-kebencanaan-bnpb rollout restart deployment
fi
