# P5 Coastal 03: SQLite state dan outbox

## Dasar

Kontrak bagian 7 menetapkan urutan yang tidak boleh dibalik. Producer menyimpan
raw input dan perubahan state, menyimpan command ke outbox, mengirim command,
lalu commit cursor hanya setelah menerima `2xx`. Kontrak bagian 8 juga menyebut
SQLite sebagai tempat cursor, observation, quarantine, cache, dan outbox.

Ketentuan itu muncul dari sifat Sensor Pantai. Feed bisa diambil ulang, tetapi
cursor yang sudah maju tidak boleh kembali. Kalau cursor maju sebelum command
diterima Aggregator, event bisa hilang. Karena itu cursor dan outbox harus
berada di transaksi yang sama.

## Alternatif

Pilihan pertama adalah menyimpan cursor di memori. Ini ditolak karena restart
pod akan mengulang dari awal atau kehilangan posisi, tergantung cara
implementasinya.

Pilihan kedua adalah menyimpan state di MongoDB. Ini ditolak karena MongoDB
milik Aggregator, dan service P5 tidak boleh membuka koneksi ke sana.

Pilihan ketiga adalah memakai satu file JSON. Ini ditolak karena update cursor,
outbox, dan quarantine harus atomik. SQLite memberi transaksi dan index tanpa
menambah server baru.

## Batas

Commit ini hanya store dan test. Belum ada poller, validator, atau pemanggilan
Aggregator. Dukungan `parked_at` sudah dimasukkan ke skema karena kolom itu
dibutuhkan commit perbaikan `4xx`, tetapi perilakunya belum dipakai di sini.

## Konsekuensi

SQLite dikonfigurasi WAL, `busy_timeout`, dan satu koneksi. Satu koneksi
menghindari `SQLITE_BUSY` dan membuat urutan tulis deterministik, dengan
konsekuensi throughput tulis dibatasi satu writer. Untuk volume Sensor Pantai
ini cukup. `HasUnsentOutbox` sengaja tidak memakai filter `next_attempt_at`,
supaya command yang gagal dan dijadwalkan ulang tetap menahan cursor.

## Bukti

`go test ./internal/store/...` mencakup cursor, insert observation idempoten,
outbox lifecycle, retry terjadwal, park outbox beserta quarantine, dan
pemeriksaan bahwa baris gagal tetap menahan cursor.
