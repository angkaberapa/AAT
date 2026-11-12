# Rencana Kerja P4/P5 M2

Branch: `feat/m2-p4-p5`
Kontrak integrasi: `docs/KONTRAK_INTERNAL_INGEST.md`

## Prinsip

- P5 dulu, P4 kedua, bonus terakhir.
- Service P4/P5 tidak mengakses Mongo. Semua tulisan lewat Internal Ingest API.
- Aggregator, Audit API, observability, dan `POST /field-reports` bukan milik branch ini.
- Setiap slice harus bisa dijalankan dan diuji dengan fake Aggregator sebelum menyentuh cluster.
- Commit kecil, push setelah slice bisa diuji. Jangan menumpuk semua pekerjaan dalam satu commit.

## Urutan P5 (`bnpb-coastal`)

1. **Ingest client + fake Aggregator**
   - DTO `UpsertHazardCommand`, `DeleteHazardCommand`, `SourceStatus`, `UnprocessedMessage`.
   - HTTP client dengan `Authorization`, `Idempotency-Key`, `X-Correlation-ID`.
   - Klasifikasi error: retryable (`429`, `5xx`, timeout) dan terminal (`4xx` lain, `409`).
   - Fake Aggregator untuk test dan demo lokal.
2. **SQLite state**
   - Schema `state`, `observations`, `outbox`, `cache`.
   - Cursor hanya commit setelah semua outbox untuk halaman itu terkirim.
   - Outbox pending dan retry.
3. **Sensor client + validator + quarantine**
   - Poll `GET /v1/observations?limit=&cursor=`.
   - Validasi field, tipe, station, `quality=ok`, umur data, dan range.
   - Data invalid masuk quarantine dan dikirim ke `/internal/ingest/unprocessed`.
4. **Circuit breaker + cache + metrics**
   - Buka setelah 5 kegagalan atau error rate >50% dalam 10 detik.
   - Open 30 detik, half-open satu request.
   - Cache observasi terakhir per station.
   - Metrics sesuai kontrak §9.2.
5. **Test**
   - Validator table-driven.
   - Cursor/outbox dengan SQLite temporary.
   - Fake sensor dan fake Aggregator.
   - `go test -race ./...`, `go vet ./...`.

## Urutan P4 (`bnpb-satellite`)

1. SSE client + fake SSE server.
2. SQLite schema dan outbox per command.
3. Detection key, union-find 1 km/360 menit, watermark, late count.
4. Rebuild komponen saat retract; `DELETE` saat fire turun di bawah 2.
5. Metrics sesuai kontrak §9.1.
6. Test replay, reconnect, dan kill pod.

## Commit dan push

| Commit | Isi | Push |
|---|---|---|
| 1 | Kontrak + rencana ini | Ya, supaya teman bisa review dan mulai |
| 2 | P5 ingest client + fake Aggregator | Ya |
| 3 | P5 SQLite + poller + validator | Ya |
| 4 | P5 breaker + cache + metrics + test | Ya, PR siap review |
| 5 | P4 SSE + grouping | Ya |
| 6 | P4 retract/delete + metrics + test | Ya |

Commit 1 dipush lebih dulu karena kontrak membuka pekerjaan A dan C. Commit berikutnya menyusul per slice, bukan satu commit besar.

## Yang tidak dikerjakan di branch ini

- Endpoint internal di Aggregator.
- Audit API dan failed-message store.
- Prometheus, Grafana, dan tracing platform.
- `POST /field-reports`.
- Perubahan `k8s/base`, `go.work`, dan `docker-compose.yml` sebelum dikoordinasikan.

## Definition of done P5 slice

- `go test -race ./...` dan `go vet ./...` lolos.
- Service jalan dengan fake sensor dan fake Aggregator.
- Cursor tidak maju saat Aggregator mengembalikan `503`.
- Data invalid tidak pernah menjadi `HazardEvent`.
- Metrics muncul di `/metrics`.
- Tidak ada akses langsung ke Mongo.
