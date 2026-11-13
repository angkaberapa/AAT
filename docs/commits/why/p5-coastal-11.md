# P5 Coastal 11: dokumentasi operasional dan deklarasi AI

## Dasar

Aturan kuliah mewajibkan kode yang ditulis dengan bantuan LLM dideklarasikan.
Spesifikasi M2 juga mewajibkan README berisi cara menjalankan sistem, variabel
konfigurasi, dan cara memicu skenario pengujian. Tanpa README, service hanya bisa
dijalankan oleh orang yang menulisnya.

Dokumen ini juga mencatat batasan yang belum selesai. Tujuannya supaya reviewer
tidak membaca service ini sebagai P5 lengkap padahal load shedding, game day,
Audit API, dan exam belum ada.

## Alternatif

Pilihan pertama adalah menaruh semua penjelasan hanya di body MR. Ini ditolak
karena body MR tidak ikut ke repository dan hilang setelah merge.

Pilihan kedua adalah menulis README setelah semua P5 selesai. Ini ditolak karena
service sudah bisa dijalankan sekarang, dan orang lain butuh caranya lebih dulu.

Pilihan ketiga adalah tidak menulis deklarasi AI. Ini ditolak karena melanggar
aturan kuliah.

## Batas

Commit ini hanya README. Laporan PDF, video, dan dokumentasi P4/bonus tidak
termasuk.

## Konsekuensi

README menyebut perintah yang bergantung pada `GOWORK=off` karena module ini
belum masuk `go.work`. README juga menyebut dua token yang harus diisi di `.env`.
Kalau nama env berubah, README harus ikut diperbarui.

## Bukti

Perintah di README dicocokkan dengan kode: `go test -race`, `go vet`,
`/health`, `/status`, `/metrics`, `k3d-up.sh`, dan `deploy.sh`.
