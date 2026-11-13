# P5 Coastal 06: circuit breaker

## Dasar

Kriteria P5 nomor 4 meminta jumlah request ke Sensor Pantai tidak melonjak saat
sensor tidak sehat, dan sistem kembali memakainya tanpa intervensi. Kontrak
Internal Ingest bagian 4 juga menyediakan `reason_code=circuit_open` untuk
status sumber. Jadi circuit breaker bukan tambahan opsional; ia bagian dari
kriteria yang dinilai.

## Alternatif

Pilihan pertama adalah retry terus-menerus. Ini ditolak karena saat sensor mati,
retry hanya menambah beban dan memperlambat pemulihan. Ini pola retry storm yang
justru ingin dihindari.

Pilihan kedua adalah tidak memakai breaker dan mengandalkan timeout. Ini ditolak
karena timeout tetap memanggil sensor setiap siklus dan tidak memberi state
yang bisa dibaca dashboard.

Pilihan ketiga adalah membuka breaker untuk semua error, termasuk `400` dan
`401`. Ini ditolak karena error terminal berarti sensor masih bisa dihubungi;
yang bermasalah adalah request atau kredensial, bukan koneksi.

## Batas

Commit ini hanya state machine dan wrapper client. Wiring ke processor dan
metric state ada di commit berikutnya. Tidak ada cache response di sini.

## Konsekuensi

Breaker default membuka setelah lima kegagalan retryable, menunggu 30 detik,
lalu mengizinkan satu probe. Selama open, request ke sensor tidak dilakukan.
Kalau probe sukses, breaker menutup. Kalau probe gagal, breaker membuka lagi.

## Bukti

`go test ./internal/sensor/...` mencakup closed ke open, penolakan saat open,
half-open setelah timeout, probe sukses, probe gagal, dan error terminal yang
tidak membuka breaker. Uji manual dengan mematikan container sensor menunjukkan
state berubah menjadi `2`.
