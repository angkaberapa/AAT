# P5 Coastal 08: processor dan service wiring

## Dasar

Kontrak bagian 7 mengatur urutan commit cursor, dan bagian 8 menyebut SQLite
sebagai tempat state. Spesifikasi P5 nomor 1 meminta event kritis tidak hilang
dengan p95 di bawah lima detik. Keduanya menuntut satu loop yang mengambil
halaman, menyimpan state, mengirim command, lalu memajukan cursor hanya setelah
command diterima.

Service ini juga harus punya entrypoint, konfigurasi, health check, dan
Dockerfile. Tanpa itu, kode hanya kumpulan package yang tidak bisa dijalankan.

## Alternatif

Pilihan pertama adalah memproses satu observation per request tanpa halaman.
Ini ditolak karena endpoint sensor memakai cursor halaman; satu request per item
akan membuang cursor dan menambah round trip.

Pilihan kedua adalah commit cursor sebelum outbox terkirim. Ini ditolak karena
event bisa hilang kalau Aggregator down setelah cursor maju.

Pilihan ketiga adalah banyak writer SQLite. Ini ditolak karena SQLite punya satu
writer dan urutan cursor harus deterministik untuk volume ini.

## Batas

Commit ini belum memarkir error terminal `4xx`; itu perbaikan di commit
berikutnya. Manifest Kubernetes juga belum ada. Fokusnya hanya service yang
bisa jalan dengan sensor practice dan fake Aggregator.

## Konsekuensi

Processor berjalan satu goroutine. Pengiriman outbox dilakukan setelah transaksi
commit, bukan di dalamnya, supaya transaksi tidak menunggu jaringan. Kalau
outbox belum kosong, cursor tidak maju dan event akan diambil ulang saat
reconnect; idempotency mencegah duplikat.

## Bukti

`go test -race ./...` dan `go vet ./...` lolos. Uji end-to-end dengan sensor
practice dan fake Aggregator menunjukkan observation bertambah, cursor maju,
outbox pending nol, dan `/status` serta `/metrics` hidup.
