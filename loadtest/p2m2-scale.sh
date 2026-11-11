#!/usr/bin/env bash
# P2 M2 kriteria 6: load test >= 10.000 VU >= 120 s sambil Aggregator di-scale 3 -> 5 -> 2,
# lalu cek kriteria 2 (tidak ada HazardEvent ganda, satu leader per periode).
#
#   loadtest/p2m2-scale.sh                        # k3d: k6 langsung ke ServiceLB node
#   API_HOST=<ip-node> loadtest/p2m2-scale.sh     # cluster demo
#   MODE=rollout loadtest/p2m2-scale.sh           # kriteria 8: ganti versi di tengah tes
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
NS=sistem-informasi-kebencanaan-bnpb
API_HOST="${API_HOST:-k3d-bnpb-server-0}"
VUS="${VUS:-10000}"
THINK="${THINK_SECONDS:-3}"
MODE="${MODE:-scale}"
LABEL="${LABEL:-p2m2-k6-$MODE}"
# Jadwal (detik sejak k6 mulai). Ramp 30 s lalu tahan 150 s.
T1="${T1:-70}"
T2="${T2:-130}"

kubectl -n "$NS" scale deploy/bnpb-aggregator --replicas=3 >/dev/null
kubectl -n "$NS" rollout status deploy/bnpb-aggregator --timeout=180s >/dev/null
epoch_before=$(kubectl -n "$NS" get lease bnpb-aggregator-ingest -o jsonpath='{.spec.leaseTransitions}')
start=$(date -u +%Y-%m-%dT%H:%M:%SZ)

K6_NETWORK="${K6_NETWORK:-k3d-bnpb}" "$ROOT/loadtest/run.sh" p2m2-scale.js \
  -e API_URL="http://$API_HOST:8080" -e AUTH_URL="http://$API_HOST:8090" \
  -e VUS="$VUS" -e THINK_SECONDS="$THINK" -e PHASES="$T1,$T2" -e LABEL="$LABEL" &
k6_pid=$!

at() { # tunggu sampai detik ke-$1 sejak k6 mulai
  local target=$(( $(date -d "$start" +%s) + $1 ))
  while (( $(date +%s) < target )); do sleep 1; done
}

if [[ "$MODE" == scale ]]; then
  at "$T1"; echo ">>> $(date +%T) scale aggregator 3 -> 5"; kubectl -n "$NS" scale deploy/bnpb-aggregator --replicas=5
  at "$T2"; echo ">>> $(date +%T) scale aggregator 5 -> 2"; kubectl -n "$NS" scale deploy/bnpb-aggregator --replicas=2
else
  at "$T1"; echo ">>> $(date +%T) rolling update aggregator (versi baru)"
  kubectl -n "$NS" rollout restart deploy/bnpb-aggregator
fi
wait "$k6_pid" || true

echo; echo "=== kriteria 2 setelah tes"
kubectl -n "$NS" get pods -l app=bnpb-aggregator -o wide
echo "lease transitions: $epoch_before -> $(kubectl -n "$NS" get lease bnpb-aggregator-ingest -o jsonpath='{.spec.leaseTransitions}') (holder $(kubectl -n "$NS" get lease bnpb-aggregator-ingest -o jsonpath='{.spec.holderIdentity}'))"
kubectl -n "$NS" exec mongo-0 -c mongo -- sh -c 'mongosh --quiet -u "$MONGO_INITDB_ROOT_USERNAME" -p "$MONGO_INITDB_ROOT_PASSWORD" --authenticationDatabase admin "$0" --eval "
const total = db.hazard_events.countDocuments();
const uniq = db.hazard_events.aggregate([{\$group:{_id:{s:\"\$source\",r:\"\$source_ref_id\"}}},{\$count:\"n\"}]).toArray()[0].n;
print(\"HazardEvent total=\" + total + \" unik=\" + uniq + (total === uniq ? \" -> tidak ada duplikat\" : \" -> ADA DUPLIKAT\"));" ' "${MONGO_DB:-bnpb}"
echo "leader term selama tes:"
for p in $(kubectl -n "$NS" get pods -l app=bnpb-aggregator -o name); do
  kubectl -n "$NS" logs "$p" --since-time="$start" 2>/dev/null | grep -E '"msg":"(leader term started|write rejected by fencing token, stepping down)"' \
    | sed -E 's/.*"ts":"([^"]+)".*"msg":"([^"]+)".*"instance":"([^"]+)".*"epoch":([0-9]+).*/\1 \3 epoch=\4 \2/' || true
done | sort | grep . || echo "(tidak ada pergantian leader selama tes)"
