# P5 Coastal 04: Sensor Pantai HTTP client

## Dasar

Image Sensor Pantai yang dijalankan dengan profile practice menunjukkan bahwa
sumbernya pull-based. Endpoint-nya `GET /v1/observations?limit=&cursor=`, dengan
`Authorization: Bearer`, `generated_at`, `items`, dan `next_cursor`. Ini berbeda
dari Feed Satelit yang SSE, sehingga P5 tidak bisa memakai client yang sama
dengan P4.

Spesifikasi P5 meminta data yang melanggar kontrak disisihkan. Karena itu client
tidak boleh menggagalkan seluruh halaman hanya karena satu item rusak. `items`
disimpan sebagai `json.RawMessage`, lalu validator memeriksa satu per satu.

## Alternatif

Pilihan pertama adalah memakai SSE untuk Sensor Pantai. Ini ditolak karena
image-nya tidak menyediakan stream; ia memberi halaman berdasarkan cursor.

Pilihan kedua adalah langsung melakukan unmarshal ke struct observation. Ini
ditolak karena satu field dengan tipe salah akan membuat seluruh halaman gagal,
padahal kriteria P5 meminta item rusak disisihkan, bukan seluruh respons
dibuang.

Pilihan ketiga adalah menaruh retry di client. Ini ditolak karena retry dan
circuit breaker perlu satu tempat bersama proses cursor dan outbox.

## Batas

Commit ini hanya transport dan parsing halaman. Validator, station table, dan
mapper ada di commit berikutnya. Client juga belum menyimpan apa pun.

## Konsekuensi

`generated_at` dipakai sebagai waktu acuan untuk memeriksa data stale, bukan jam
dinding, karena profile practice memakai waktu virtual. Limit dibatasi 1 sampai
1000 mengikuti endpoint. `next_cursor` boleh kosong; pemanggil yang memutuskan
apakah cursor perlu dipertahankan.

## Bukti

`go test ./internal/sensor/...` mencakup query limit dan cursor, header
autentikasi, correlation ID, respons kosong, `401`, `429`, `503`,
`generated_at` invalid, timeout, dan limit di luar rentang.
