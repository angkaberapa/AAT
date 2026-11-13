# Commit 06: feat(coastal): add circuit breaker

## Tujuan

Commit ini menghentikan panggilan ke Sensor Pantai saat sumber itu terus gagal.
Tanpa circuit breaker, service akan terus mengirim request ke sensor yang mati,
membakar waktu timeout, dan membuat log serta koneksi menumpuk.

## File

- `services/bnpb-coastal/internal/sensor/breaker.go`
- `services/bnpb-coastal/internal/sensor/breaker_test.go`

## Alur end-to-end

Breaker mulai dalam keadaan closed. Setiap panggilan yang diizinkan akan
menjalankan fetch. Kalau hasilnya sukses, breaker kembali ke closed dan
hitungan gagal direset. Kalau hasilnya retryable, hitungan gagal bertambah.
Setelah jumlah gagal mencapai threshold, breaker masuk open dan menolak
panggilan berikutnya.

Saat open, `Allow` memeriksa apakah `OpenTimeout` sudah lewat. Kalau sudah,
breaker masuk half-open dan mengizinkan satu probe. Probe yang sukses menutup
breaker. Probe yang gagal membuka breaker lagi dan memulai timeout baru.
`BreakerClient` membungkus fetcher sensor dan hanya menghitung error retryable
sebagai kegagalan. Error terminal seperti `400` atau `401` tidak membuka
breaker karena sensor masih bisa dihubungi.

## Keputusan

- Default threshold lima kegagalan, open 30 detik, dan satu probe half-open.
  Nilainya bisa diubah lewat environment.
- Breaker tidak melakukan retry. Retry dan jadwal backoff tetap di processor.
- State breaker diekspos sebagai method `State` supaya bisa diteruskan ke
  metric `coastal_circuit_breaker_state`.

## Verifikasi

```powershell
$env:GOWORK="off"
go test ./internal/sensor/...
```

Test mencakup transisi closed ke open, penolakan saat open, half-open setelah
timeout, probe sukses yang menutup breaker, probe gagal yang membuka lagi, dan
error terminal yang tidak membuka breaker.

Uji end-to-end: jalankan sensor practice, lalu matikan container sensor. Setelah
lima kegagalan, `coastal_circuit_breaker_state` harus bernilai `2` dan log
menampilkan `sensor circuit breaker is open`.

## Yang tidak termasuk

- Wiring breaker ke `main.go`, yang masuk commit 08.
- Cache response terakhir.
- Metrics, yang masuk commit 07.
