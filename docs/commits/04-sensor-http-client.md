# Commit 04: feat(coastal): add Sensor Pantai HTTP client

## Tujuan

Commit ini menambahkan client untuk mengambil halaman observation dari Sensor
Pantai. Client hanya mengurus transport, autentikasi, cursor, dan klasifikasi
error. Validasi kontrak dan pemetaan ke `HazardEvent` sengaja dipisah ke commit
berikutnya.

## File

- `services/bnpb-coastal/internal/sensor/types.go`
- `services/bnpb-coastal/internal/sensor/client.go`
- `services/bnpb-coastal/internal/sensor/client_test.go`

## Alur end-to-end

Client mengirim `GET /v1/observations?limit=<n>&cursor=<cursor>` dengan header
`Authorization: Bearer <PANTAI_AUTH_TOKEN>`. Cursor hanya dikirim kalau tidak
kosong. Response dibaca sampai batas 8 MB, lalu `generated_at`, `items`, dan
`next_cursor` diambil. `generated_at` wajib ada dan harus bisa diparse sebagai
RFC3339 karena nilainya dipakai sebagai waktu acuan untuk memeriksa data stale.

`items` sengaja disimpan sebagai `[]json.RawMessage`. Dengan begitu satu item
yang rusak tidak menggagalkan seluruh halaman. Parser item dan aturan kontrak
ada di package `coastal`, bukan di client. Client juga meneruskan
`X-Correlation-ID` supaya log sensor dan log service bisa disambungkan.

Error dipisahkan seperti client ingest. `429`, `5xx`, dan kegagalan transport
retryable. `400`, `401`, `403`, `404`, dan error validasi response terminal.
`Retry-After` dibaca dari header dan disimpan di `APIError`.

## Keputusan

- Limit dibatasi 1 sampai 1000, mengikuti batas endpoint.
- `generated_at` dipakai sebagai waktu virtual sensor. Ini penting karena profile
  practice memakai waktu sendiri, bukan jam dinding.
- `next_cursor` boleh kosong. Pemanggil yang memutuskan apakah cursor perlu
  dipertahankan.
- Tidak ada retry di dalam client. Retry dan circuit breaker ada di lapisan
  processor supaya kebijakannya satu tempat.

## Verifikasi

```powershell
$env:GOWORK="off"
go test ./internal/sensor/...
```

Test mencakup query `limit` dan `cursor`, header autentikasi, correlation ID,
response kosong, `401`, `429`, `503`, `generated_at` invalid, timeout, dan limit
di luar rentang.

## Yang tidak termasuk

- Validator kontrak.
- Circuit breaker.
- Penyimpanan hasil ke SQLite.
