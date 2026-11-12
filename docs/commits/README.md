# Breakdown Commit P5

Folder ini memecah pekerjaan P5 menjadi commit kecil. Satu file menjelaskan satu
commit secara end-to-end: tujuan, file, alur, keputusan, verifikasi, dan batas.

Rencana besar ada di `docs/M2_P4_P5_BONUS_PLAN.md`. Kontrak integrasi ada di
`docs/KONTRAK_INTERNAL_INGEST.md`.

| # | Commit | File |
|---|---|---|
| 1 | `docs: add M2 internal ingest contract and P5 plan` | `01-docs-kontrak-dan-plan.md` |
| 2 | `feat(coastal): scaffold service and internal ingest client` | `02-ingest-client-dan-fake-aggregator.md` |
| 3 | `feat(coastal): add SQLite state, cursor, and outbox` | `03-sqlite-state-dan-outbox.md` |
| 4 | `feat(coastal): add Sensor Pantai HTTP client` | `04-sensor-http-client.md` |
| 5 | `feat(coastal): validate sensor contract and map COASTAL events` | `05-validator-dan-mapper-coastal.md` |
| 6 | `feat(coastal): add circuit breaker` | `06-circuit-breaker.md` |
| 7 | `feat(coastal): add Prometheus metrics` | `07-metrics-prometheus.md` |
| 8 | `feat(coastal): poll, validate, and forward observations` | `08-processor-dan-service-wiring.md` |
| 9 | `fix(coastal): park terminal 4xx instead of stalling cursor` | `09-park-terminal-4xx.md` |
| 10 | `feat(coastal): add Kubernetes manifests and deploy wiring` | `10-kubernetes-dan-deploy.md` |
| 11 | `docs(coastal): document operation, limitations, and AI declaration` | `11-dokumentasi-operasional-dan-ai.md` |

## Cara memakai

Baca satu file sebelum mengerjakan commit yang bersangkutan. File menjelaskan
batas commit supaya perubahan tidak melebar ke pekerjaan lain. Setelah commit
selesai, jalankan bagian verifikasi di file itu, lalu baru lanjut ke nomor
berikutnya.

## Aturan umum

Setiap commit harus bisa dibangun dan dites sendiri. Test ikut commit bersama
kode yang dites. Dokumen yang menjelaskan fitur boleh menyusul di commit
terpisah. Jangan mencampur perubahan `k8s/base` dengan perubahan kode service.
Jangan commit token, database, binary, atau file hasil generate.
