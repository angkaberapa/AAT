# P5 Load 02: priority admission control

## Dasar

Kriteria P5 nomor 5 meminta Tim Lapangan tetap dilayani saat beban melebihi
kapasitas, sementara Media ditolak secara terkontrol. Middleware yang ada hanya
punya batas total dan token bucket per client. Media bisa memakai seluruh slot
total, sehingga Field ikut tertolak sebelum sempat dilayani.

Priority admission memberi Media batas in-flight yang lebih rendah dari total.
Dengan total 128 dan Media 96, tersisa 32 slot yang tidak bisa diambil Media.
Field dan Ops memakai slot cadangan itu.

## Alternatif

Pilihan pertama adalah menaikkan rate limit Field. Ini ditolak karena rate limit
tidak menjamin slot saat Media sudah memenuhi batas total.

Pilihan kedua adalah membuat endpoint atau Service terpisah untuk Field. Ini
ditolak karena menambah jalur deployment dan load balancer, sementara masalahnya
bisa diselesaikan di admission control.

Pilihan ketiga adalah priority queue. Ini ditolak karena request yang sudah
diterima akan menunggu, dan kriteria meminta penolakan terkontrol dengan
`Retry-After`, bukan antrean yang menggantung.

## Batas

Commit ini hanya `bnpb-api`. `POST /field-reports` belum ada, jadi prioritas
untuk jalur tulis belum bisa diuji. Middleware sudah generik, sehingga jalur itu
bisa memakai wrapper yang sama saat P3 menambahkannya.

## Konsekuensi

Media di atas 96 in-flight mendapat `429` dan `Retry-After`. Field dan Ops tetap
tunduk pada batas total 128. Kalau total penuh oleh Field, Media dan Field
sama-sama ditolak; reservasi hanya menjamin Media tidak bisa mengambil slot
cadangan.

## Bukti

`go test ./internal/limit/...` mencakup Media yang ditolak saat cap tercapai,
Field yang tetap mendapat 200, batas total, dan token lama tanpa role yang
diperlakukan sebagai Media. Script `loadtest/p5-priority.js` disiapkan untuk uji
end-to-end saat stack hidup.
