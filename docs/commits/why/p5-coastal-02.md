# P5 Coastal 02: scaffold dan ingest client

## Dasar

Kontrak bagian 3 dan 6 menetapkan bahwa producer menulis lewat
`POST /internal/ingest/hazards` dengan token terpisah, `Idempotency-Key` yang
sama dengan `producer_event_id`, dan empat operasi: `UPSERT`, `DELETE`, source
status, dan unprocessed. Kontrak bagian 11 juga menyebut bahwa P4/P5 boleh mulai
dengan fake Aggregator selama endpoint asli belum ada.

Struktur service mengikuti konvensi repo. Setiap service Go punya salinan
`internal/core` sendiri, memakai `log/slog`, `X-Correlation-ID`, dan image
distroless. Salinan ini sengaja tidak dijadikan library bersama karena
spesifikasi melarang service saling mengimpor kode.

## Alternatif

Pilihan pertama adalah menaruh DTO dan client di package bersama yang dipakai
`bnpb-coastal` dan `bnpb-satellite`. Ini ditolak karena melanggar aturan
komunikasi antar-service dan membuat kedua service terikat pada versi library
yang sama.

Pilihan kedua adalah mengirim banyak `HazardEvent` dalam satu request. Ini
ditolak karena kontrak memakai satu command per request dengan
`producer_event_id` sendiri. Satu command membuat retry dan idempotency per
event lebih jelas.

Pilihan ketiga adalah membiarkan client melakukan retry sendiri. Ini ditolak
karena keputusan retry, backoff, dan park sebaiknya ada di processor, bersama
aturan cursor dan outbox.

## Batas

Commit ini belum menyentuh SQLite, sensor, atau processor. Fokusnya hanya
kontrak HTTP dan fake Aggregator. Dengan batas ini, client bisa diuji tanpa
database dan tanpa service lain.

## Konsekuensi

Error transport dibedakan dari error API. `429`, `5xx`, dan kegagalan koneksi
retryable; `4xx` lain terminal. Fake Aggregator menyimpan hasil idempotency di
memori, jadi ia hanya alat development, bukan pengganti integration test dengan
Aggregator asli.

## Bukti

`go test ./internal/ingest/...` mencakup header autentikasi, correlation ID,
idempotency key, payload UPSERT dan DELETE, `409`, `422`, `429`, `503`, timeout,
dan klasifikasi retryable.
