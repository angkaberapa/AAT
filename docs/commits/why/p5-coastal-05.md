# P5 Coastal 05: validator dan mapper

## Dasar

Kriteria P5 nomor 2 meminta data yang melanggar kontrak dasar disisihkan beserta
alasannya, bukan disimpan sebagai `HazardEvent`. Kontrak Internal Ingest bagian 2
juga menetapkan bentuk canonical event, termasuk `occurred_at` dari waktu sumber
dan `source_ref_id` yang stabil.

Payload Sensor Pantai tidak membawa latitude dan longitude. Yang ada hanya
`station_id`, sehingga mapper membutuhkan tabel station untuk mendapatkan
koordinat dan provinsi. Tabel itu belum diberikan secara formal, jadi commit ini
memakai tujuh station yang ditemukan di image dan menandainya provisional.

## Alternatif

Pilihan pertama adalah mempercayai sensor dan menyimpan semua item. Ini ditolak
karena kriteria 2 justru menguji perilaku saat sensor mengirim data rusak.

Pilihan kedua adalah memakai nama kota sebagai `area_name`. Ini ditolak karena
kontrak canonical meminta nama provinsi, dan tabel station sudah menyediakan
provinsi.

Pilihan ketiga adalah menolak seluruh halaman saat satu item salah. Ini ditolak
karena satu pesan rusak tidak boleh menghentikan data lain.

## Batas

Commit ini hanya parsing, validasi, station table, dan mapping. Poller dan
pengiriman ke Aggregator ada di commit berikutnya. Severity COASTAL masih
provisional karena kontrak resmi Sensor Pantai belum tersedia.

## Konsekuensi

Aturan `critical` menjadi AWAS, anomali atau gelombang besar menjadi SIAGA, dan
sisanya WASPADA. Ini asumsi yang harus dicocokkan ke kontrak resmi. Station yang
tidak dikenal akan dikarantina, bukan ditebak. `hazard_id` memakai formula
Aggregator supaya event yang sama tidak menjadi dua dokumen.

## Bukti

`go test ./internal/coastal/...` mencakup field hilang, tipe salah, kualitas
buruk, station tidak dikenal, data stale, waktu masa depan, nilai di luar
rentang, critical invalid, dan pemetaan severity.
