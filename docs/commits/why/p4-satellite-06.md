# P4 Satellite 06: internal ingest client

## Dasar

Kontrak bagian 3 dan 11 menetapkan bahwa producer mengirim `UPSERT` dan `DELETE`
ke Internal Ingest API dengan token, `Idempotency-Key`, dan `source_position`.
P4 membutuhkan jalur tulis yang sama dengan P5, tetapi tidak boleh mengimpor
package milik service P5.

Repo ini memakai pola salin `internal/core` ke setiap service karena spesifikasi
melarang service saling mengimpor kode. Client ingest mengikuti pola yang sama:
DTO dan client disalin ke module P4, dengan import `core` milik module sendiri.

## Alternatif

Pilihan pertama adalah membuat satu package bersama untuk semua producer. Ini
ditolak karena melanggar aturan service isolation dan membuat P4/P5 terikat pada
satu versi DTO.

Pilihan kedua adalah mengimpor package `bnpb-coastal/internal/ingest`. Ini
ditolak karena Go akan menolak import internal dari module lain, dan secara
desain service tidak boleh bergantung pada service lain.

Pilihan ketiga adalah menulis langsung ke Mongo. Ini ditolak karena melanggar
kepemilikan canonical storage.

## Batas

Commit ini hanya client. Processor yang memakai client, menyusun command dari
fire, dan mengatur outbox belum ada. Fake Aggregator untuk P4 juga belum
disalin; test client memakai `httptest`.

## Konsekuensi

DTO dan aturan error sekarang ada di dua tempat, P4 dan P5. Kalau kontrak
berubah, keduanya harus diperbarui bersama. Ini konsekuensi yang diterima demi
service isolation.

## Bukti

`go test ./internal/ingest/...` di module P4 mencakup payload UPSERT/DELETE,
header, idempotency, `409`, `422`, `429`, `503`, timeout, dan correlation ID.
