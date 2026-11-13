# P5 Load 01: role claim di auth dan principal API

## Dasar

Kriteria P5 nomor 5 meminta permintaan Tim Lapangan tetap dilayani saat Media
ditolak terkontrol. API sudah punya tiga client yang berbeda, tetapi token hanya
membawa `scope`, bukan peran. Tanpa peran, admission control tidak bisa
membedakan Media dari Field tanpa menebak dari client ID.

Claim `role` membuat peran menjadi bagian dari token yang sudah ditandatangani.
API memverifikasi tanda tangan, lalu membaca peran dari claim yang sama. Ini
lebih kuat daripada memetakan client ID di API, karena client ID adalah string
yang bisa berubah dan tidak punya kontrak tipe.

## Alternatif

Pilihan pertama adalah memakai `scope` yang sudah ada. Ini ditolak karena Field
dan Ops sama-sama memakai `hazards:read`; scope tidak membedakan keduanya.

Pilihan kedua adalah memakai client ID di API. Ini ditolak karena menambah daftar
string yang harus disinkronkan setiap kali identitas berubah.

Pilihan ketiga adalah menambah service auth baru. Ini ditolak karena auth yang
ada sudah cukup; yang kurang hanya satu field di token.

## Batas

Commit ini hanya menambah dan meneruskan claim. Middleware prioritas belum
memakainya sampai commit berikutnya. Token lama tanpa role tetap diterima dan
dianggap Media di lapisan prioritas, supaya deployment tidak langsung memutus
token yang belum kedaluwarsa.

## Konsekuensi

`NewService` sekarang menolak role yang tidak dikenal. Tiga client di
`main.go` dan test harus mengisi role. Kalau ada client keempat di masa depan,
daftar role harus diperbarui.

## Bukti

`go test ./...` di `bnpb-auth` dan `bnpb-api` lolos. Test auth memeriksa claim
role per identitas dan menolak role tidak dikenal. Test verifier memastikan role
sampai ke `Principal`.
