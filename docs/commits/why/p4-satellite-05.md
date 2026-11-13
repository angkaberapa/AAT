# P4 Satellite 05: reconcile retract dan split

## Dasar

Kriteria P4 nomor 3 meminta detection yang diperbarui atau ditarik memperbarui
`HazardEvent` yang sudah ada, bukan membuat event baru. Feed bisa mengirim
`retract`, dan satu detection yang hilang bisa memecah komponen menjadi dua
atau membuat komponen turun di bawah dua detection.

Union-find statis tidak bisa menghapus node. Karena itu commit ini memakai
recompute dari seluruh detection aktif setiap kali ada retract, lalu
membandingkan hasilnya dengan fire yang tersimpan. Hasil yang hilang menjadi
`DELETE`, hasil baru atau berubah menjadi `UPSERT`.

## Alternatif

Pilihan pertama adalah incremental union-find dengan rebuild komponen lokal.
Ini ditolak untuk tahap ini karena implementasinya lebih rumit dan retract
jauh lebih jarang daripada detection. Recompute penuh lebih mudah dibuktikan.

Pilihan kedua adalah membiarkan fire lama tetap ada dan membuat fire baru.
Ini ditolak karena satu kebakaran bisa menjadi dua event dan melanggar
kriteria 3.

Pilihan ketiga adalah menghapus detection dari tabel tanpa recompute. Ini
ditolak karena komponen yang tersisa tidak dihitung ulang, sehingga hasilnya
tidak mencerminkan definisi kebakaran.

## Batas

Commit ini hanya fungsi reconcile dan query detection/fire aktif. Processor yang
memanggil reconcile, membentuk command `DELETE`, dan mengirim ke Aggregator
belum ada.

## Konsekuensi

Recompute penuh membaca semua detection aktif. Untuk volume P4 saat ini masih
masuk akal, tetapi perlu diukur saat feed 60x. Fire yang detection terkecilnya
di-retract bisa mendapat ID baru; event lama dihapus dan event baru dibuat.
Trade-off ini harus dijelaskan di laporan.

## Bukti

`go test ./internal/wildfire/...` mencakup fire yang hilang, fire yang tetap,
split setelah detection tengah di-retract, dan tidak ada detection aktif.
`go test ./internal/store/...` mencakup daftar detection dan fire aktif.
