# P4 Satellite 03: grouping event time dan jarak

## Dasar

Definisi Kebakaran di spesifikasi M2 menetapkan lima aturan. Detection
diidentifikasi oleh satellite, acq_date, acq_time, latitude, dan longitude.
Waktu deteksi adalah acq_date digabung acq_time UTC, bukan waktu tiba. Dua
detection terhubung bila jarak ≤1,0 km dan selisih waktu ≤360 menit. Hubungan
bersifat berantai. Komponen dengan ≥2 detection adalah satu kebakaran.

Aturan berantai membuat clustering biasa tidak cukup. Dua detection yang tidak
saling dekat tetap bisa berada di kebakaran yang sama kalau ada detection lain
yang menghubungkan keduanya. Karena itu dipakai union-find.

## Alternatif

Pilihan pertama adalah DBSCAN. Ini ditolak karena DBSCAN memakai kepadatan dan
radius, bukan aturan chain yang eksplisit. Parameter DBSCAN juga sulit
dijelaskan sebagai aturan yang sama dengan definisi kebakaran.

Pilihan kedua adalah PostGIS. Ini ditolak karena menambah service database baru,
sementara data detection sudah ada di SQLite dan volume P4 masih bisa ditangani
grid lokal.

Pilihan ketiga adalah membandingkan semua pasangan detection. Ini ditolak karena
O(n²) tidak akan bertahan saat feed 60x mengirim ribuan detection. Grid 0,01
derajat membatasi kandidat sebelum jarak dihitung.

## Batas

Commit ini hanya clustering. Severity, area, dan pembentukan event ada di commit
berikutnya. Retract dan split belum ditangani di sini.

## Konsekuensi

Grid 0,01 derajat cukup untuk Indonesia yang berada di lintang rendah. Di
lintang sangat tinggi, satu sel longitude menjadi lebih pendek dan radius dua
sel bisa kurang dari 1 km; asumsi ini dicatat. Union-find statis tidak mendukung
penghapusan detection, sehingga retract ditangani dengan recompute di commit
berikutnya.

## Bukti

`go test ./internal/wildfire/...` mencakup jarak dalam batas, jarak luar batas,
selisih waktu luar batas, rantai A-B-C, single detection, ID stabil saat urutan
input diubah, dan input kosong.
