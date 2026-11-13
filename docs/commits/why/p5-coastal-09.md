# P5 Coastal 09: park terminal 4xx

## Dasar

Kontrak bagian 3.5 dan 5 membedakan error yang boleh diulang dari error yang
tidak akan berhasil tanpa perubahan data atau kode. Spesifikasi P5 nomor 2
meminta pesan yang tidak bisa diproses tetap tersimpan beserta alasannya. Kalau
satu `422` hanya didiamkan di outbox, cursor berhenti dan seluruh pipeline macet.

## Alternatif

Pilihan pertama adalah mengulang semua `4xx`. Ini ditolak karena `422` tidak akan
berubah tanpa perbaikan data atau kode, dan retry hanya mengulang kegagalan.

Pilihan kedua adalah membuang event yang ditolak. Ini ditolak karena menghilangkan
bukti dan melanggar semangat quarantine.

Pilihan ketiga adalah memarkir semua `4xx`, termasuk `401` dan `403`. Ini
ditolak karena token salah berlaku untuk semua event. Memarkir semuanya akan
menyembunyikan masalah konfigurasi dan mengosongkan canonical store.

## Batas

Commit ini hanya perilaku processor dan dukungan store yang sudah ada. Load
shedding, Audit API, dan replay manual belum termasuk.

## Konsekuensi

`409`, `413`, dan `422` diparkir ke `unprocessed`; `400`, `401`, `403`, dan `404`
menghentikan pipeline. Baris yang diparkir tidak menahan cursor, tetapi payload
mentahnya tetap disimpan. Kalau endpoint `unprocessed` down, cursor tetap
tertahan sampai quarantine terkirim.

## Bukti

Test `422` memastikan event diparkir, unprocessed terkirim, dan cursor maju.
Test `401` memastikan event tidak diparkir dan cursor tidak maju. Test `503`
memastikan outbox dijadwalkan ulang dan cursor tetap tertahan.
