#!/usr/bin/env bash
# Satu perintah deploy seluruh sistem BNPB ke cluster yang aktif di kubeconfig:
#   scripts/deploy.sh            # overlay dev (k3d)
#   OVERLAY=demo scripts/deploy.sh
# Kredensial dibaca dari .env (salin dari .env.example) dan dijadikan Secret `bnpb-secrets`;
# hanya kunci rahasia yang dimasukkan, konfigurasi lain ada di ConfigMap (k8s/base/config.yaml).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OVERLAY="${OVERLAY:-dev}"
ENV_FILE="${ENV_FILE:-$ROOT/.env}"
NS=sistem-informasi-kebencanaan-bnpb

SECRET_KEYS=(
  BMKG_API_KEY PVMBG_TOKEN
  MONGO_ROOT_USER MONGO_ROOT_PASSWORD
  JWT_SIGNING_KEY
  CLIENT_MEDIA_ID CLIENT_MEDIA_SECRET
  CLIENT_FIELD_ID CLIENT_FIELD_SECRET
  CLIENT_OPS_ID CLIENT_OPS_SECRET
)

if [[ ! -f "$ENV_FILE" ]]; then
  echo "error: $ENV_FILE tidak ada. Jalankan: cp .env.example .env" >&2
  exit 1
fi

secret_args=()
missing=()
for key in "${SECRET_KEYS[@]}"; do
  line="$(grep -E "^${key}=" "$ENV_FILE" | tail -n1 || true)"
  value="${line#*=}"
  if [[ -z "$line" || -z "$value" ]]; then
    missing+=("$key")
  else
    secret_args+=("--from-literal=${key}=${value}")
  fi
done
if ((${#missing[@]})); then
  echo "error: kunci berikut kosong/tidak ada di $ENV_FILE: ${missing[*]}" >&2
  exit 1
fi

echo "deploy overlay '$OVERLAY' ke context: $(kubectl config current-context)"
kubectl apply -f "$ROOT/k8s/base/namespace.yaml"
kubectl -n "$NS" create secret generic bnpb-secrets "${secret_args[@]}" \
  --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -k "$ROOT/k8s/overlays/$OVERLAY"

echo "menunggu semua workload siap..."
kubectl -n "$NS" rollout status statefulset --timeout=300s
kubectl -n "$NS" rollout status deployment --timeout=300s
kubectl -n "$NS" get pods -o wide
