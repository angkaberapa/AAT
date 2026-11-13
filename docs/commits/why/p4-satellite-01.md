# P4 Satellite 01: scaffold dan SSE client

## Dasar

Kriteria P4 nomor 7 meminta feed live saat demo, dan nomor 8 meminta reconnect
tanpa kehilangan atau menggandakan data. Spesifikasi Feed Satelit menyediakan
`GET /v1/session` dan `GET /v1/stream?from=<offset>` dengan `Last-Event-ID`,
jenis event `detection`, `update`, `retract`, dan `heartbeat`, serta offset yang
naik monoton.

Pola polling M1 tidak cocok untuk sumber ini. Feed mendorong event saat ada
deteksi baru, dan offset adalah posisi yang harus dilanjutkan. Kalau sistem
polling, ia harus menebak kapan menarik data dan tetap membutuhkan offset yang
sama.

## Alternatif

Pilihan pertama adalah polling endpoint session berulang kali. Ini ditolak
karena tidak memanfaatkan aliran event dan membuat latency serta beban request
lebih buruk.

Pilihan kedua adalah menaruh reconnect otomatis di dalam client. Ini ditolak
karena keputusan backoff dan kapan offset boleh maju sebaiknya ada di processor,
bersama checkpoint dan outbox.

Pilihan ketiga adalah memakai Kafka Connect atau library streaming besar. Ini
ditolak karena feed-nya SSE, bukan Kafka, dan menambah dependensi yang tidak
perlu.

## Batas

Commit ini hanya transport dan parser SSE. Belum ada penyimpanan, grouping,
atau pengiriman ke Aggregator. Client berhenti saat stream tertutup; pemanggil
yang menyambung ulang.

## Konsekuensi

Client tidak melakukan deduplikasi. Kalau server mengirim event yang sama dua
kali, processor yang harus menanganinya lewat detection key dan offset. `retry:`
dari server diabaikan karena kebijakan backoff milik service.

## Bukti

`go test ./internal/sse/...` mencakup parsing event, multiline data, heartbeat
comment, event tanpa data, event yang belum ditutup blank line, `Last-Event-ID`,
`401`, `429`, `503`, handler error, dan error transport.
