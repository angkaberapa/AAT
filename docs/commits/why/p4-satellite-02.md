# P4 Satellite 02: SQLite detection, offset, fire, outbox

## Dasar

Kontrak bagian 8 menyebut SQLite Satellite menyimpan offset terakhir, raw
detection, state update/retract, clustering, watermark, dan outbox. Kriteria P4
nomor 5 meminta hasil yang sama setelah pemroses mati di tengah pengenalan
kebakaran. Tanpa state lokal yang durable, offset dan komponen akan hilang saat
restart.

Detection disimpan dengan detection key sebagai primary key. Latitude dan
longitude disimpan sebagai string karena key harus mempertahankan presisi dari
feed, bukan hasil pembulatan float. Tabel `fires` menyimpan proyeksi canonical
per fire supaya update dan delete bisa dibandingkan tanpa membangun ulang semua
event dari nol.

## Alternatif

Pilihan pertama adalah menyimpan semua state di memori. Ini ditolak karena
restart pod akan kehilangan offset dan komponen, sehingga hasil setelah mati
tidak sama dengan pemrosesan tanpa gangguan.

Pilihan kedua adalah menyimpan detection langsung di MongoDB. Ini ditolak karena
Mongo milik Aggregator dan service P4 tidak boleh membuka koneksi ke sana.

Pilihan ketiga adalah menyimpan satu file JSON besar. Ini ditolak karena update
detection, fire, dan outbox harus atomik dan bisa di-query per key.

## Batas

Commit ini hanya skema dan operasi store. Grouping, severity, dan processor
belum ada. Tabel `fires` sudah ada supaya commit reconcile bisa membandingkan
event lama dan baru.

## Konsekuensi

Satu koneksi SQLite dipakai untuk menghindari `SQLITE_BUSY` dan menjaga urutan
tulis. Local PV bisa hilang bersama node, jadi offset dan state harus bisa
direbuild dari feed. Ini belum diimplementasikan pada commit ini dan dicatat
sebagai batasan.

## Bukti

`go test ./internal/store/...` mencakup offset, insert detection idempoten,
deactivate detection, upsert fire dengan versi naik, delete fire, outbox
lifecycle, dan pemeriksaan bahwa outbox yang gagal tetap menahan cursor.
