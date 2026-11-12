# Commit 09: fix(coastal): park terminal 4xx instead of stalling cursor

## Tujuan

Commit ini memperbaiki satu kegagalan yang bisa menghentikan seluruh pipeline.
Sebelumnya, satu event yang ditolak Aggregator dengan `4xx` terminal membuat
outbox tetap pending dan cursor tidak pernah maju. Satu event buruk bisa
menahan semua event berikutnya.

## File

- `services/bnpb-coastal/internal/store/sqlite.go`
- `services/bnpb-coastal/internal/store/sqlite_test.go`
- `services/bnpb-coastal/internal/processor/processor.go`
- `services/bnpb-coastal/internal/processor/processor_test.go`
- `services/bnpb-coastal/README.md`

## Alur end-to-end

Saat outbox dikirim dan Aggregator mengembalikan error, processor memeriksa
statusnya. `429`, `5xx`, dan kegagalan transport tetap dianggap retryable dan
dijadwalkan ulang dengan backoff. `409`, `413`, dan `422` dianggap masalah satu
event, sehingga command diparkir dan payload-nya dikirim ke endpoint
`unprocessed`. `400`, `401`, `403`, dan `404` dianggap masalah konfigurasi,
sehingga pipeline berhenti sampai token atau endpoint diperbaiki.

Parking dilakukan dalam satu transaksi. Baris outbox mendapat `parked_at` dan
`park_reason`, lalu record quarantine dimasukkan dengan `message_id` yang
deterministik. Setelah transaksi commit, quarantine dikirim ke Aggregator.
Cursor baru bisa maju setelah quarantine itu diterima. Dengan begitu event tidak
hilang, tetapi juga tidak menahan seluruh pipeline.

## Keputusan

- `409`, `413`, dan `422` diparkir karena hanya satu event yang bermasalah.
- `400`, `401`, `403`, dan `404` menghentikan pipeline karena biasanya itu
  masalah token, route, atau format envelope yang berlaku untuk semua event.
- `HasUnsentOutbox` tidak menghitung baris yang sudah diparkir. Kalau baris
  diparkir masih dihitung, cursor tetap akan tertahan.
- Payload mentah disimpan Base64 supaya event yang ditolak bisa diperiksa lagi
  setelah sistem diperbaiki.

## Verifikasi

```powershell
$env:GOWORK="off"
go test ./internal/store/...
go test ./internal/processor/...
```

Test yang harus ada:

- `422` memarkir event, mengirim unprocessed, dan memajukan cursor.
- `401` tidak memarkir event, tidak memajukan cursor, dan outbox tetap unsent.
- `503` dijadwalkan ulang dan cursor tidak maju.
- Baris yang diparkir tidak lagi muncul di `PendingOutbox`.

## Yang tidak termasuk

- Load shedding dan prioritas.
- Audit API yang membaca `unprocessed`.
- Retry manual setelah sistem diperbaiki.
