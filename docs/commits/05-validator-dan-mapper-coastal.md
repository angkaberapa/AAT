# Commit 05: feat(coastal): validate sensor contract and map COASTAL events

## Tujuan

Commit ini mengubah satu observation mentah menjadi `HazardEvent` COASTAL yang
valid, atau menolaknya dengan alasan yang bisa diaudit. Validasi dipisah dari
client supaya data yang rusak tidak pernah masuk ke canonical store.

## File

- `services/bnpb-coastal/internal/coastal/stations.go`
- `services/bnpb-coastal/internal/coastal/validator.go`
- `services/bnpb-coastal/internal/coastal/mapper.go`
- `services/bnpb-coastal/internal/coastal/validator_test.go`

## Alur end-to-end

Validator menerima satu `json.RawMessage` dari halaman sensor. Objek diubah
menjadi map field, lalu setiap field wajib diperiksa satu per satu. Field yang
hilang menghasilkan `missing_field`, tipe yang salah menghasilkan `wrong_type`,
dan kualitas selain `ok` menghasilkan `bad_quality`. Station yang tidak ada di
tabel menghasilkan `unknown_station`. Waktu yang terlalu tua menghasilkan
`stale`, waktu di masa depan menghasilkan `future_timestamp`, dan nilai di luar
rentang menghasilkan `invalid_range`.

Setelah valid, mapper membentuk `HazardEvent`. `source_ref_id` memakai
`observation_id`, `source` memakai `SENSOR_PANTAI`, dan `hazard_type` memakai
`COASTAL`. `latitude` dan `longitude` diambil dari station table karena payload
sensor tidak membawa koordinat. `occurred_at` memakai `observed_at`, bukan waktu
data diterima. Severity sementara memakai aturan critical menjadi AWAS,
anomali atau gelombang besar menjadi SIAGA, dan sisanya WASPADA.

`hazard_id` memakai formula yang sama dengan Aggregator, yaitu `HZ-` ditambah 16
byte pertama SHA-256 dari `SENSOR_PANTAI:<observation_id>`. Kesamaan formula ini
penting supaya event yang sama tidak menjadi dua dokumen berbeda.

## Keputusan

- Station table berisi tujuh station yang ditemukan di image. Tabel ini
  provisional sampai kontrak resmi Sensor Pantai tersedia.
- Field numerik memakai pointer supaya nilai nol yang sah tidak dianggap field
  hilang.
- `critical=true` yang datanya invalid tetap menghasilkan error dengan penanda
  `Critical`, sehingga processor bisa memberi alasan `critical_invalid`.
- Severity dan area adalah asumsi yang harus dicocokkan ke kontrak resmi.

## Verifikasi

```powershell
$env:GOWORK="off"
go test ./internal/coastal/...
```

Test mencakup observation valid, field hilang, tipe salah, kualitas buruk,
station tidak dikenal, data stale, waktu masa depan, nilai di luar rentang,
critical invalid, dan pemetaan severity.

## Yang tidak termasuk

- Poller dan penyimpanan.
- Station table final dari kontrak resmi.
- Audit API.
