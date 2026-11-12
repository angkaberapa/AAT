# Commit 08: feat(coastal): poll, validate, and forward observations

## Tujuan

Commit ini menyambungkan semua bagian menjadi service yang berjalan. Service
mengambil halaman dari Sensor Pantai, memvalidasi setiap observation, menyimpan
state dan outbox, lalu mengirim command ke Aggregator. Commit ini juga menaruh
konfigurasi, entrypoint, dan Dockerfile.

## File

- `services/bnpb-coastal/internal/config/config.go`
- `services/bnpb-coastal/internal/processor/processor.go`
- `services/bnpb-coastal/internal/processor/processor_test.go`
- `services/bnpb-coastal/main.go`
- `services/bnpb-coastal/Dockerfile`
- `services/bnpb-coastal/.dockerignore`

## Alur end-to-end

`main.go` membaca konfigurasi, membuka SQLite, membuat sensor client, ingest
client, circuit breaker, metrics, dan processor. HTTP server menyediakan
`/health`, `/status`, dan `/metrics`. Processor berjalan di goroutine terpisah
dan berhenti saat context dibatalkan.

Setiap siklus membaca cursor dari SQLite, mengambil satu halaman sensor, lalu
membuka transaksi. Observation valid dipetakan menjadi `HazardEvent`, command
`UPSERT` dimasukkan ke outbox, observation disimpan, dan cache diperbarui.
Observation invalid dimasukkan ke quarantine dengan alasan yang bisa dibaca.
Setelah transaksi commit, processor mengirim outbox dan quarantine. Cursor baru
maju kalau tidak ada outbox dan quarantine yang belum terkirim.

Processor juga melaporkan source status ke Aggregator. Saat sensor gagal,
status menjadi `degraded` dengan `reason_code` yang sesuai. Saat sukses, status
menjadi `ok` beserta `last_event_at` dan `lag_seconds`.

Pada commit ini, error terminal `4xx` belum diparkir. Kalau Aggregator menolak
satu event dengan `409`, `413`, atau `422`, outbox tetap pending dan cursor
berhenti. Perbaikan perilaku itu masuk commit 09.

## Keputusan

- Satu processor dan satu penulis SQLite. Ini cukup untuk volume Sensor Pantai
  dan membuat urutan cursor deterministik.
- Pengiriman outbox dilakukan setelah transaksi, bukan di dalam transaksi.
  Transaksi tidak boleh menunggu jaringan.
- `TerminationGracePeriodSeconds` disiapkan 30 detik. Processor berhenti lewat
  context, bukan dengan mematikan proses secara paksa.
- `/status` menampilkan cursor, jumlah observation, outbox pending, outbox
  parked, dan quarantine pending untuk debugging.

## Verifikasi

```powershell
$env:GOWORK="off"
go test -race ./...
go vet ./...
```

Uji end-to-end dengan sensor practice dan fake Aggregator. Hasil yang diharapkan:
observation bertambah, cursor maju, outbox pending kembali 0, dan
`/metrics` menampilkan request sukses.

## Yang tidak termasuk

- Park terminal 4xx, yang masuk commit 09.
- Manifest Kubernetes, yang masuk commit 10.
- Load shedding dan game day.
