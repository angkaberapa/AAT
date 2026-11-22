#!/usr/bin/env bash
# Demo bonus AI-native: baseline normal lalu burst, tanpa menyentuh jalur ingest.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

AREA="AI-DEMO-$(date +%s)"
echo "area demo: $AREA"
echo "mengirim 25 event baseline (1 detik) lalu 25 event burst (10 ms) ke Kafka..."

docker compose exec -T -e AREA="$AREA" kafka bash -c '
set -euo pipefail
producer=/opt/kafka/bin/kafka-console-producer.sh
now() { date -u +%Y-%m-%dT%H:%M:%SZ; }

{
  for i in $(seq 1 25); do
    ts=$(now)
    printf "{\"event_id\":\"ai-demo-%s:v1\",\"type\":\"hazard.created\",\"version\":1,\"published_at\":\"%s\",\"hazard\":{\"hazard_id\":\"HZ-ai-demo-%s\",\"source\":\"BMKG\",\"source_ref_id\":\"ai-demo-%s\",\"hazard_type\":\"EARTHQUAKE\",\"severity\":\"NORMAL\",\"area_name\":\"%s\",\"latitude\":-0.9,\"longitude\":119.87,\"occurred_at\":\"%s\"}}\n" \
      "$i" "$ts" "$i" "$i" "$AREA" "$ts"
    sleep 1
  done
  for i in $(seq 26 50); do
    ts=$(now)
    printf "{\"event_id\":\"ai-demo-%s:v1\",\"type\":\"hazard.created\",\"version\":1,\"published_at\":\"%s\",\"hazard\":{\"hazard_id\":\"HZ-ai-demo-%s\",\"source\":\"BMKG\",\"source_ref_id\":\"ai-demo-%s\",\"hazard_type\":\"EARTHQUAKE\",\"severity\":\"AWAS\",\"area_name\":\"%s\",\"latitude\":-0.9,\"longitude\":119.87,\"occurred_at\":\"%s\"}}\n" \
      "$i" "$ts" "$i" "$i" "$AREA" "$ts"
    sleep 0.01
  done
} | "$producer" --bootstrap-server kafka:9092 --topic hazard-events
'

sleep 1
echo
echo "GET /advisories"
curl -fsS "http://localhost:8094/advisories?limit=5"

echo
echo "GET /summary"
curl -fsS "http://localhost:8094/summary"
echo
