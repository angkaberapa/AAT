#!/usr/bin/env bash
# Menjalankan satu skrip k6 di dalam jaringan Docker sistem.
#
#   loadtest/run.sh p2-k1-hol.js
#   loadtest/run.sh p2-k2-load.js -e VUS=60 -e DURATION=75s -e LABEL=p2-k2-default
#
# Kredensial client dibaca dari .env di root repo. Hasil ditulis ke loadtest/results/.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SCRIPT="${1:?usage: loadtest/run.sh <script.js> [k6 run options...]}"
shift

NETWORK="${K6_NETWORK:-bnpb-platform_backend}"
IMAGE="${K6_IMAGE:-grafana/k6:1.3.0}"

if [[ ! -f "$ROOT/.env" ]]; then
  echo "file .env tidak ditemukan di $ROOT (jalankan: cp .env.example .env)" >&2
  exit 1
fi
mkdir -p "$ROOT/loadtest/results"

docker run --rm -i \
  --user "$(id -u):$(id -g)" \
  --network "$NETWORK" \
  --env-file "$ROOT/.env" \
  -v "$ROOT/loadtest:/scripts:ro" \
  -v "$ROOT/loadtest/results:/results" \
  "$IMAGE" run --quiet "$@" "/scripts/$SCRIPT"
