#!/usr/bin/env bash
# Cluster PENGEMBANGAN saja (k3d = node berupa container, tidak sah untuk demo).
# Membuat cluster 1 server + agent yang dapat dikonfigurasi, membangun semua image,
# lalu meng-import-nya ke cluster.
#   scripts/k3d-up.sh            # buat cluster (kalau belum ada) + build + import
#   SKIP_CLUSTER=1 scripts/k3d-up.sh   # hanya build + import ulang image
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CLUSTER="${CLUSTER:-bnpb}"
AGENTS="${AGENTS:-2}"
K3S_IMAGE="${K3S_IMAGE:-rancher/k3s:v1.34.13-k3s1}"
K3D_API_PORT="${K3D_API_PORT:-6445}"
SERVICES=(bmkg-mock pvmbg-mock bnpb-coastal bnpb-satellite bnpb-aggregator bnpb-auth bnpb-api bnpb-consumer-dashboard bnpb-consumer-notifier)

if [[ -z "${SKIP_CLUSTER:-}" ]] && ! k3d cluster get "$CLUSTER" >/dev/null 2>&1; then
  # Port host sama dengan M1; diteruskan ke Service LoadBalancer lewat loadbalancer k3d.
  k3d cluster create "$CLUSTER" --agents "$AGENTS" --image "$K3S_IMAGE" \
    --api-port "127.0.0.1:${K3D_API_PORT}" \
    -p "8080:8080@loadbalancer" -p "8081:8081@loadbalancer" -p "8082:8082@loadbalancer" \
    -p "8090:8090@loadbalancer" -p "8091:8091@loadbalancer" -p "8092:8092@loadbalancer" \
    -p "3000:3000@loadbalancer" -p "9090:9090@loadbalancer" -p "16686:16686@loadbalancer"
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
