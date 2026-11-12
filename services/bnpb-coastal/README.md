# bnpb-coastal

Service P5 untuk Milestone 2. Saat ini berisi client Internal Ingest API,
fake Aggregator, dan SQLite state/outbox. Belum ada poller sensor atau circuit
breaker.

Kontrak integrasi: `docs/KONTRAK_INTERNAL_INGEST.md`.
Rencana kerja: `docs/M2_P4_P5_PLAN.md`.

## Isi

- `internal/ingest`: DTO, `Client` interface, HTTP client, klasifikasi error.
- `internal/store`: SQLite schema, cursor, observations, outbox, quarantine, cache.
- `internal/core`: correlation ID, logger, env, HTTP server. Salinan dari service lain.
- `cmd/fake-aggregator`: fake Aggregator untuk development dan test.

## Menjalankan fake Aggregator

```powershell
$env:GOWORK="off"
go run ./cmd/fake-aggregator -addr :18081
```

Endpoint yang tersedia:

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

## Catatan

- Module ini memakai Go 1.26 karena `modernc.org/sqlite`. Dockerfile memakai
  `golang:1.27-alpine` seperti service lain.
- Module ini belum masuk `go.work`. Tambahkan setelah koordinasi dengan platform.
- Tidak ada akses langsung ke MongoDB. Semua tulisan lewat Internal Ingest API.
- `hazard_id` harus memakai formula yang sama dengan Aggregator:
  `HZ-` + 16 byte pertama SHA-256 dari `source:source_ref_id`.
