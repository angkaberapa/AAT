# Rencana End-to-End P5 dan Aturan Commit

Status: 2026-11-13
Branch: `feat/m2-p5-coastal-ingest`
Backup: `backup/m2-p5-coastal-ingest-2026-11-13`

## Tujuan

- P5 Sensor Pantai selesai dan bisa dibuktikan per kriteria.
- Setiap perubahan bisa dilacak ke satu alasan.
- Commit kecil, bisa direview, dan tidak mencampur dokumen, refactor, dan fitur.
- P4 dan bonus punya urutan yang jelas setelah P5.

## Prinsip commit

1. Satu commit satu alasan. Kalau judulnya butuh kata "dan", pecah.
2. Setiap commit harus bisa dibangun dan dites.
3. Test ikut commit bersama kode yang dites.
4. Dokumen yang menjelaskan fitur boleh commit terpisah setelah kode.
5. Jangan commit binary, database, token, atau file hasil generate.
6. Stage per kelompok file, review `git diff --cached`, baru commit.
7. Jangan amend atau force-push commit yang sudah direview orang lain.
8. Push setelah satu slice selesai, bukan setelah setiap commit.
9. Buka MR draft lebih awal supaya kontrak dan antarmuka bisa direview.
10. Pesan commit memakai bentuk `type(scope): ringkasan imperatif`.

## Arsitektur yang sedang dibangun

```text
Sensor Pantai --GET /v1/observations--> bnpb-coastal
                                           |
                                           +--> SQLite: cursor, observations, outbox, quarantine, cache
                                           |
                                           +--> POST /internal/ingest/hazards
                                           +--> POST /internal/ingest/source-status
                                           +--> POST /internal/ingest/unprocessed
                                                    |
                                                    v
                                              bnpb-aggregator --> Mongo --> outbox --> Kafka
```

`bnpb-coastal` tidak mengakses Mongo. Semua tulisan canonical lewat Internal Ingest API.

## Status saat ini

Sudah ada di branch:

| Commit | Isi | Ukuran | Catatan |
|---|---|---|---|
| `99565b9` | Kontrak dan rencana P4/P5 | 2 file, 569 baris | Baik |
| `8eb46ab` | Ingest client dan fake Aggregator | 10 file, 1237 baris | Cukup fokus |
| `53d9f76` | SQLite state dan outbox | 6 file, 780 baris | Cukup fokus |
| `b7e14c2` | Sensor client, validator, processor, main, Dockerfile | 16 file, 1669 baris | Terlalu lebar |
| `c6f185c` | Batasan dan deklarasi AI | 2 file, 18 baris | Baik |
| `7a5f950` | Park terminal 4xx | 5 file, 201 baris | Baik |
| `d32e33a` | Circuit breaker, metrics, manifest Kubernetes | 16 file, 814 baris | Terlalu lebar |

Yang sudah selesai:

- Kontrak Internal Ingest dan rencana.
- Ingest client, fake Aggregator, klasifikasi error.
- SQLite cursor, observations, outbox, quarantine, cache.
- Poller Sensor Pantai, validator, mapper COASTAL, quarantine.
- Aturan cursor: hanya maju setelah outbox dan quarantine terkirim.
- `409/413/422` diparkir ke `unprocessed`; `400/401/403/404` menghentikan pipeline.
- Circuit breaker closed/open/half-open.
- Metrics Prometheus sesuai kontrak §9.2.
- Manifest Kubernetes sensor dan coastal.
- Dockerfile dan build k3d.
- Unit test, race test, vet, build Docker, kustomize, dan end-to-end dengan sensor practice.

Yang belum selesai:

- Load shedding dan prioritas Tim Lapangan vs Media. Ini di `bnpb-api` dan `POST /field-reports`.
- Game day, SLO, dan postmortem P5.
- Integrasi quarantine ke Audit API `unprocessed`.
- Uji profile exam.
- Integrasi ke Aggregator asli.
- P4 dan bonus.

## Rencana commit yang lebih kecil

Kalau history dirapikan, urutan yang disarankan:

| # | Pesan commit | File utama | Verifikasi |
|---|---|---|---|
| 1 | `docs: add M2 internal ingest contract and P5 plan` | `docs/KONTRAK_INTERNAL_INGEST.md`, `docs/M2_P4_P5_PLAN.md`, file ini | Dokumen terbaca |
| 2 | `feat(coastal): scaffold service and internal ingest client` | `go.mod`, `go.sum`, `internal/core`, `internal/ingest`, `cmd/fake-aggregator`, README awal | `go test ./internal/ingest/...` |
| 3 | `feat(coastal): add SQLite state, cursor, and outbox` | `internal/domain`, `internal/store` | `go test ./internal/store/...` |
| 4 | `feat(coastal): add Sensor Pantai HTTP client` | `internal/sensor/client.go`, `types.go`, test | `go test ./internal/sensor/...` |
| 5 | `feat(coastal): validate sensor contract and map COASTAL events` | `internal/coastal` | `go test ./internal/coastal/...` |
| 6 | `feat(coastal): add circuit breaker` | `internal/sensor/breaker.go`, test | `go test ./internal/sensor/...` |
| 7 | `feat(coastal): add Prometheus metrics` | `internal/metrics` | `go test ./internal/metrics/...` |
| 8 | `feat(coastal): poll, validate, and forward observations` | `internal/config`, `internal/processor`, `main.go`, Dockerfile | `go test -race ./...` |
| 9 | `fix(coastal): park terminal 4xx instead of stalling cursor` | `internal/processor`, `internal/store`, test | test park `422`, stop `401` |
| 10 | `feat(coastal): add Kubernetes manifests and deploy wiring` | `k8s/base/coastal.yaml`, kustomization, scripts, `.env.example` | `kubectl kustomize k8s/base` |
| 11 | `docs(coastal): document operation, limitations, and AI declaration` | README | README sesuai perilaku |

Catatan: kalau commit 9 terlalu sulit dipisahkan dari commit 8, gabungkan dan sebutkan di body commit. Jangan memaksakan pemisahan yang membuat commit tidak bisa dibangun.

## Rencana P5 setelah commit di atas

### P5 reliability

- Load shedding dan prioritas di `bnpb-api`.
- `POST /field-reports` mendapat prioritas yang sama.
- Uji Media flood vs Tim Lapangan.
- Metrics `api_rejected_total{role}`.

Verifikasi: load test, p95 Field Team, 429 Media, `Retry-After`.

### P5 game day

- Skenario: sensor mati, 429/500/timeout, data rusak, node mati, network partition, PVMBG outage, media flood.
- Setiap eksperimen punya steady state, blast radius, abort criteria, dan postmortem.
- SLO: 99,9% event kritis sampai notifier dalam 5 detik.

Verifikasi: log, metrics, dashboard, postmortem, hitungan critical lost.

### P5 audit dan exam

- Quarantine tampil di `GET /internal/audit/unprocessed`.
- Uji profile exam dengan kunci asisten.
- Bandingkan hasil dengan referensi.

## Rencana P4

Branch baru: `feat/m2-p4-satellite`.

Urutan commit:

1. `feat(satellite): add SSE client and fake SSE server`
2. `feat(satellite): add SQLite detection, offset, and outbox`
3. `feat(satellite): group detections with event time and union-find`
4. `feat(satellite): handle retract, split, and DELETE`
5. `feat(satellite): add metrics and checkpointing`
6. `feat(satellite): add Kubernetes manifests`
7. `test(satellite): replay, reconnect, and kill-pod checks`

Verifikasi: reference diff, p95, lag, 60x replay, reconnect `Last-Event-ID`, delete tombstone.

## Rencana bonus

Setelah P5 dan P4 hijau:

- `bnpb-ai-summary` consumer Kafka, jalur samping.
- Timeout, circuit breaker, fallback template.
- Metrics dan bukti bahwa jalur peringatan tidak terpengaruh.

## Aturan untuk commit berikutnya

- Stage dengan `git add -p` atau per folder.
- Jalankan `go test ./...` dan `go vet ./...` di module.
- Cek `git diff --cached --stat` sebelum commit.
- Jangan mencampur perubahan `k8s/base` dengan perubahan kode service dalam satu commit.
- Jangan mencampur perbaikan bug dengan fitur baru.
- Kalau commit lebih dari 400 baris, tanyakan apakah bisa dipecah per lapisan.
- Setelah push, buka MR draft dan minta review kontrak atau antarmuka, bukan seluruh kode.

## Keputusan yang menunggu

1. History dirapikan menjadi 10-11 commit, atau dipertahankan dan aturan ini dipakai untuk commit berikutnya?
2. Kalau dirapikan, force-push branch `feat/m2-p5-coastal-ingest` setelah backup branch aman.
3. Setelah P5 slice ini merge, lanjut load shedding/game day atau mulai P4?
