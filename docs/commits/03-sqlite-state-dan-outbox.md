# Commit 03: feat(coastal): add SQLite state, cursor, and outbox

## Tujuan

Commit ini memberi service memori lokal yang tahan restart. Cursor, observation,
outbox, quarantine, dan cache disimpan di SQLite supaya service bisa melanjutkan
pekerjaan setelah pod mati tanpa kehilangan event yang belum diterima
Aggregator.

## File

- `services/bnpb-coastal/internal/domain/observation.go`
- `services/bnpb-coastal/internal/store/sqlite.go`
- `services/bnpb-coastal/internal/store/sqlite_test.go`

## Alur end-to-end

Saat satu halaman sensor diproses, service membuka transaksi SQLite. Observation
yang valid dimasukkan ke tabel `observations` dengan `INSERT ... ON CONFLICT DO
NOTHING`, jadi `observation_id` yang sama tidak menjadi duplikat. Command untuk
Aggregator dimasukkan ke `outbox` dengan `producer_event_id` unik. Cache per
station diperbarui di transaksi yang sama.

Cursor tidak disimpan di luar transaksi. Fungsi `SetCursorTx` hanya dipanggil
setelah tidak ada outbox dan quarantine yang belum terkirim. Pemeriksaan ini
memakai `HasUnsentOutbox` dan `HasUnsentQuarantine`, bukan `PendingOutbox`,
karena outbox yang gagal dan dijadwalkan ulang tetap harus menahan cursor.

Tabel `quarantine` menyimpan pesan yang tidak bisa menjadi `HazardEvent`.
Payload mentah disimpan sebagai Base64, alasan disimpan sebagai teks, dan
`status` membedakan `retrying` dari `parked`. Tabel `cache` menyimpan observation
terakhir per station untuk keadaan darurat saat sensor tidak bisa dihubungi.

## Keputusan

- SQLite memakai WAL, `busy_timeout`, dan satu koneksi. Ini menghindari
  `SQLITE_BUSY` dan membuat urutan tulis deterministik.
- Waktu disimpan sebagai RFC3339Nano UTC supaya perbandingan string tetap
  kronologis.
- `outbox` punya `parked_at` dan `park_reason`. Baris yang diparkir tidak
  menahan cursor, tetapi payload-nya tetap tersimpan.
- `observations` menyimpan `raw_json` supaya bukti dan audit tidak bergantung
  pada hasil parsing.

## Verifikasi

```powershell
$env:GOWORK="off"
go test ./internal/store/...
```

Test mencakup cursor round-trip, insert observation idempoten, outbox lifecycle,
retry yang dijadwalkan, park outbox beserta quarantine, dan last event.

## Yang tidak termasuk

- Poller sensor.
- Pemetaan ke `HazardEvent`.
- Metrics dan circuit breaker.
