# Commit 07: feat(coastal): add Prometheus metrics

## Tujuan

Commit ini membuat kondisi service bisa dibaca dari luar. Metrics yang dipakai
mengikuti nama di kontrak §9.2 supaya dashboard dan alert platform tidak perlu
menebak nama metrik.

## File

- `services/bnpb-coastal/internal/metrics/metrics.go`
- `services/bnpb-coastal/internal/metrics/metrics_test.go`

## Alur end-to-end

Package metrics membuat registry Prometheus sendiri, lalu mendaftarkan sembilan
collector. Setiap request sensor menambah `coastal_request_total` dan mengamati
`coastal_request_duration_seconds`. Setiap observation invalid menambah
`coastal_invalid_observation_total` dengan label alasan. Jumlah outbox dan
quarantine yang belum terkirim menjadi gauge. State circuit breaker menjadi gauge
0, 1, atau 2. Umur cache, lag cursor, dan latency event kritis juga dicatat.

`Handler` mengembalikan handler HTTP untuk `/metrics`. Registry dibuat per
instance service, bukan registry global, supaya test bisa membuat metrics baru
tanpa bentrok registrasi.

## Keputusan

- Nama metrik persis mengikuti kontrak: `coastal_request_total`,
  `coastal_request_duration_seconds`, `coastal_cursor_lag`,
  `coastal_invalid_observation_total`, `coastal_quarantine_pending`,
  `coastal_ingest_outbox_pending`, `coastal_circuit_breaker_state`,
  `coastal_cache_age_seconds`, dan `coastal_critical_event_latency_seconds`.
- Label dijaga rendah kardinalitas. Tidak ada `observation_id` atau `station_id`
  sebagai label.
- Method metrics aman dipanggil pada nil receiver, sehingga test lama tidak
  wajib membuat registry baru.

## Verifikasi

```powershell
$env:GOWORK="off"
go test ./internal/metrics/...
```

Test memanggil semua method, menjalankan handler `/metrics`, lalu memastikan
sembilan nama metrik muncul di output. Uji manual: jalankan service dan buka
`http://localhost:18082/metrics`.

## Yang tidak termasuk

- Wiring ke processor dan `main.go`, yang masuk commit 08.
- Dashboard Grafana.
- Alert rules.
