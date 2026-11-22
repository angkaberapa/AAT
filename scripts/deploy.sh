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
  AUDIT_TOKEN
  MONGO_ROOT_USER MONGO_ROOT_PASSWORD MONGO_CLUSTER_KEY
  JWT_SIGNING_KEY
  CLIENT_MEDIA_ID CLIENT_MEDIA_SECRET
  CLIENT_FIELD_ID CLIENT_FIELD_SECRET
  CLIENT_OPS_ID CLIENT_OPS_SECRET
  PANTAI_AUTH_TOKEN
  SATELLITE_TOKEN
  COASTAL_INGEST_TOKEN
  FIELD_REPORT_INGEST_TOKEN
  GRAFANA_ADMIN_PASSWORD
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

# LLM_API_KEY opsional: tanpa key, /summary memakai fallback.
llm_line="$(grep -E '^LLM_API_KEY=' "$ENV_FILE" | tail -n1 || true)"
llm_value="${llm_line#*=}"
if [[ -n "$llm_line" && -n "$llm_value" ]]; then
  secret_args+=("--from-literal=LLM_API_KEY=${llm_value}")
fi

echo "deploy overlay '$OVERLAY' ke context: $(kubectl config current-context)"
kubectl apply -f "$ROOT/k8s/base/namespace.yaml"
kubectl -n "$NS" create secret generic bnpb-secrets "${secret_args[@]}" \
  --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -k "$ROOT/k8s/overlays/$OVERLAY"

echo "menunggu semua workload siap..."
kubectl -n "$NS" rollout status statefulset -l app --timeout=300s
kubectl -n "$NS" rollout status deployment -l app --timeout=300s
if kubectl -n "$NS" get job mongo-bootstrap-v1 >/dev/null 2>&1; then
  kubectl -n "$NS" wait --for=condition=complete job/mongo-bootstrap-v1 --timeout=600s
fi
kubectl -n "$NS" get pods -o wide
