# bnpb-coastal

Service P5 untuk Milestone 2. Saat ini bisa poll Sensor Pantai, memvalidasi
kontrak, menyisihkan data invalid, mengirim HazardEvent COASTAL lewat Internal
Ingest API, dan menyimpan cursor/outbox di SQLite. Circuit breaker, cache
metrics, dan deployment Kubernetes belum masuk.

Kontrak integrasi: `docs/KONTRAK_INTERNAL_INGEST.md`.
Rencana kerja: `docs/M2_P4_P5_PLAN.md`.

## Isi

- `internal/ingest`: DTO, `Client` interface, HTTP client, klasifikasi error.
- `internal/sensor`: pull client `GET /v1/observations`.
- `internal/coastal`: validator kontrak, station table, mapper ke HazardEvent.
- `internal/store`: SQLite schema, cursor, observations, outbox, quarantine, cache.
- `internal/processor`: poll, validasi, quarantine, outbox, commit cursor.
- `internal/core`: correlation ID, logger, env, HTTP server. Salinan dari service lain.
- `cmd/fake-aggregator`: fake Aggregator untuk development dan test.
- `main.go`: wiring service, `GET /health`, `GET /status`.

## Menjalankan lokal

Sensor practice:

```powershell
docker run --rm -p 18080:8080 `
  -e PANTAI_AUTH_TOKEN=test-token `
  -e PANTAI_PROFILE=practice `
  -e PANTAI_ADDR=:8080 `
  sustuna/sensor-pantai@sha256:51fbd90d3891ad6e9f5768ad330ffb65205644cb29e50da65c7538f2e0b4397d
```

Fake Aggregator:

```powershell
$env:GOWORK="off"
go run ./cmd/fake-aggregator -addr :18081
```

Service:

```powershell
$env:GOWORK="off"
$env:PANTAI_AUTH_TOKEN="test-token"
$env:COASTAL_INGEST_TOKEN="test-ingest"
$env:SENSOR_BASE_URL="http://localhost:18080"
$env:AGGREGATOR_BASE_URL="http://localhost:18081"
$env:COASTAL_DB_PATH="$env:TEMP\coastal.db"
$env:PORT="18082"
go run .
```

Cek `http://localhost:18082/status` dan `http://localhost:18081/debug/state`.

Endpoint fake Aggregator:

- `POST /internal/ingest/hazards`
- `POST /internal/ingest/source-status`
- `POST /internal/ingest/unprocessed`
- `GET /health`
- `GET /debug/state`

## Test

```powershell
$env:GOWORK="off"
go test -race ./...
go vet ./...
```

## Batasan saat ini

- Circuit breaker, metrics Prometheus, dan manifest Kubernetes belum ada.
- `400`, `401`, `403`, dan `404` dari Aggregator dianggap masalah konfigurasi,
  jadi outbox berhenti dan cursor tidak maju sampai token/endpoint diperbaiki.
- `409`, `413`, dan `422` dianggap masalah satu event. Event itu diparkir ke
  `unprocessed` dan cursor tetap bisa maju.
- Station table dan mapping severity masih provisional sampai kontrak resmi
  Sensor Pantai tersedia.

## Deklarasi AI

Kode service ini ditulis dengan bantuan LLM (OpenCode, DeepSeek V4.1 Flash)
untuk Milestone 2 IF4031. Keputusan desain, kontrak, dan hasil uji ditinjau
oleh anggota kelompok.

## Catatan

- Module ini memakai Go 1.26 karena `modernc.org/sqlite`. Dockerfile memakai
  `golang:1.27-alpine` seperti service lain.
- Module ini belum masuk `go.work`. Tambahkan setelah koordinasi dengan platform.
- Tidak ada akses langsung ke MongoDB. Semua tulisan lewat Internal Ingest API.
- `hazard_id` harus memakai formula yang sama dengan Aggregator:
  `HZ-` + 16 byte pertama SHA-256 dari `source:source_ref_id`.
