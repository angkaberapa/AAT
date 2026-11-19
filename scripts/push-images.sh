#!/usr/bin/env bash
# Ditulis dengan bantuan LLM (Claude).
# Build semua image BNPB + mock, push ke registry, lalu pasang tag-nya di overlay demo supaya
# node di laptop mana pun bisa menarik image yang sama persis.
#
#   docker login ghcr.io -u <user-github>      # sekali, token classic dengan scope write:packages
#   scripts/push-images.sh                     # tag = commit saat ini
#   TAG=m2-final scripts/push-images.sh
#   REGISTRY=docker.io/<user> scripts/push-images.sh
#   PLATFORMS=linux/amd64,linux/arm64 scripts/push-images.sh   # bila ada node ARM (butuh buildx)
#
# Setelah push pertama, ubah visibility tiap package jadi Public (GitHub -> Packages ->
# Package settings) supaya node tidak butuh imagePullSecret.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
REGISTRY="${REGISTRY:-ghcr.io/angkaberapa}"
PREFIX="${PREFIX:-aat-}"
OVERLAY="$ROOT/k8s/overlays/demo/kustomization.yaml"
SERVICES=(bmkg-mock pvmbg-mock bnpb-aggregator bnpb-api bnpb-auth bnpb-consumer-dashboard
  bnpb-consumer-notifier bnpb-coastal bnpb-satellite)

if [[ -z "${TAG:-}" ]]; then
  TAG="$(git -C "$ROOT" rev-parse --short HEAD)"
  if [[ -n "$(git -C "$ROOT" status --porcelain -- services)" ]]; then
    echo "error: ada perubahan belum di-commit di services/; commit dulu atau set TAG=... secara eksplisit" >&2
    exit 1
  fi
fi

for svc in "${SERVICES[@]}"; do
  image="$REGISTRY/$PREFIX$svc:$TAG"
  target=()
  [[ "$svc" == pvmbg-mock ]] && target=(--target runtime)
  echo ">>> $image"
  if [[ -n "${PLATFORMS:-}" ]]; then
    docker buildx build --platform "$PLATFORMS" "${target[@]}" -t "$image" --push "$ROOT/services/$svc"
  else
    docker build "${target[@]}" -t "$image" "$ROOT/services/$svc"
    docker push "$image"
  fi
done

# Tulis ulang blok images: di overlay demo (aat/<svc>:dev -> registry/tag ini).
python3 - "$OVERLAY" "$REGISTRY" "$PREFIX" "$TAG" "${SERVICES[@]}" <<'EOF'
import re, sys
path, registry, prefix, tag, *services = sys.argv[1:]
text = open(path).read()
block = "images:\n" + "".join(
    f"  - name: aat/{s}\n    newName: {registry}/{prefix}{s}\n    newTag: \"{tag}\"\n" for s in services)
begin, end = "# >>> images (scripts/push-images.sh)\n", "# <<< images\n"
if begin in text:
    text = re.sub(re.escape(begin) + r".*?" + re.escape(end), begin + block + end, text, flags=re.S)
else:
    text = text.rstrip("\n") + "\n\n" + begin + block + end
open(path, "w").write(text)
EOF
echo "overlay demo sekarang memakai $REGISTRY/$PREFIX*:$TAG (commit file $OVERLAY)"
