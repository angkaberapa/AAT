# P5 Coastal 07: metrics Prometheus

## Dasar

Kontrak Internal Ingest bagian 9.2 menetapkan nama metric yang harus diekspos
service Coastal. Spesifikasi P5 nomor 7 meminta setiap anggota bisa menjelaskan
kegagalan dari dashboard, metric, dan trace, bukan dugaan. Tanpa metric, kondisi
breaker, lag cursor, quarantine, dan latency kritis tidak bisa dibuktikan.

## Alternatif

Pilihan pertama adalah mengandalkan log saja. Ini ditolak karena log bagus untuk
satu kejadian, tetapi tidak bisa menjawab p95, laju error, atau jumlah antrean
sepanjang waktu.

Pilihan kedua adalah memakai label `observation_id` atau `station_id`. Ini
ditolak karena kardinalitasnya tidak terbatas dan akan membebani Prometheus.

Pilihan ketiga adalah memakai registry global. Ini ditolak karena test yang
membuat beberapa instance akan bentrok saat mendaftarkan collector yang sama.

## Batas

Commit ini hanya definisi metric dan handler `/metrics`. Wiring ke processor,
dashboard, dan alert ada di commit berikutnya atau di platform.

## Konsekuensi

Nama metric harus sama dengan kontrak supaya dashboard platform tidak perlu
menebak. Gauge outbox dan quarantine diisi dari `Stats` setelah satu siklus,
jadi nilainya adalah potret akhir siklus, bukan angka real-time per request.

## Bukti

`go test ./internal/metrics/...` memanggil semua method, menjalankan handler,
dan memastikan sembilan nama metric muncul di output. Uji manual membuka
`/metrics` saat service berjalan.
