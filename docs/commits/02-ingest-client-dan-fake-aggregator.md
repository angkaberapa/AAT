# Commit 02: feat(coastal): scaffold service and internal ingest client

## Tujuan

Commit ini membuat kerangka service `bnpb-coastal` dan client yang bisa
berbicara dengan Internal Ingest API. Fake Aggregator disertakan supaya client
bisa diuji sebelum endpoint asli dari platform tersedia.

## File

- `services/bnpb-coastal/go.mod`, `go.sum`
- `services/bnpb-coastal/internal/core/*`
- `services/bnpb-coastal/internal/ingest/types.go`
- `services/bnpb-coastal/internal/ingest/client.go`
- `services/bnpb-coastal/internal/ingest/client_test.go`
- `services/bnpb-coastal/cmd/fake-aggregator/main.go`
- `services/bnpb-coastal/README.md` versi awal

## Alur end-to-end

Service membentuk command `UPSERT` atau `DELETE`, lalu `HTTPClient` mengirim
request `POST` ke Aggregator. Client menempelkan `Authorization: Bearer`,
`Idempotency-Key` yang sama dengan `producer_event_id`, `Content-Type`, dan
`X-Correlation-ID` dari context. Untuk source status dan unprocessed, client
memakai endpoint yang berbeda sesuai kontrak.

Fake Aggregator menerima command, memeriksa idempotency key, lalu menyimpan
hasilnya di memori. Key yang sama dengan payload yang sama mengembalikan hasil
sebelumnya. Key yang sama dengan payload berbeda mengembalikan `409`. Fake ini
juga menyediakan `/debug/state` supaya isi event, status, dan unprocessed bisa
diperiksa saat development.

Client mengklasifikasikan error menjadi dua kelompok. `429`, `5xx`, dan kegagalan
transport dianggap retryable. `400`, `401`, `403`, `404`, `409`, `413`, dan `422`
dianggap terminal. Pemisahan ini penting karena producer harus tahu kapan harus
mengulang dan kapan harus berhenti atau memarkir event.

## Keputusan

- Satu request membawa satu command. Ini mengikuti kontrak dan membuat
  idempotency per event.
- `TransportError` dipakai untuk kegagalan jaringan supaya `IsRetryable` tidak
  salah menganggap semua error sebagai error API.
- `Retry-After` dibaca dari header, baik dalam detik maupun HTTP date.
- Fake Aggregator menyimpan hash payload canonical, bukan byte mentah, supaya
  urutan field JSON tidak menyebabkan `409` palsu.

## Verifikasi

```powershell
$env:GOWORK="off"
go test ./internal/ingest/...
go run ./cmd/fake-aggregator -addr :18081
```

Kirim satu `UPSERT`, ulangi dengan payload yang sama, lalu ulangi dengan payload
berbeda. Hasil yang diharapkan: `inserted`, `inserted` lagi tanpa versi baru,
lalu `409`.

## Yang tidak termasuk

- SQLite, cursor, dan outbox.
- Sensor client dan validator.
- Circuit breaker dan metrics.
