# P5 Load 03: load test media flood

## Dasar

Kriteria P5 nomor 5 tidak cukup dibuktikan dengan unit test middleware. Yang
dinilai adalah perilaku saat beban nyata: Media membanjiri API, Field tetap
dilayani, dan penolakan Media terkontrol. Karena itu perlu skenario k6 yang
menjalankan dua pola beban sekaligus.

Skenario Media memakai 150 VU konstan selama 60 detik. Skenario Field memakai
10 request per detik. Dengan batas Media 96 dan total 128, Media harus mulai
menerima `429`, sementara Field harus tetap `200`.

## Alternatif

Pilihan pertama adalah menguji manual dengan Postman. Ini ditolak karena sulit
mengulang beban yang sama dan tidak menghasilkan p95 yang bisa dibandingkan.

Pilihan kedua adalah hanya menguji middleware di unit test. Ini ditolak karena
tidak membuktikan interaksi dengan auth, token bucket, dan handler HTTP.

Pilihan ketiga adalah menjalankan load test dari dalam pod aplikasi. Ini
ditolak karena mengganggu resource yang sedang diukur dan melanggar catatan
bahwa alat load test berjalan di luar pod komponen.

## Batas

Script belum dijalankan karena stack `bnpb-api` dan `bnpb-auth` belum hidup di
laptop ini. Script juga belum menguji `POST /field-reports` karena endpoint itu
belum ada.

## Konsekuensi

Threshold Field p95 di bawah dua detik dan error di luar 429 di bawah satu
persen. Kalau Media tidak pernah mendapat 429, beban belum cukup atau batas
Media belum aktif. Hasil ditulis ke `loadtest/results/` dalam Markdown dan JSON.

## Bukti

Script memakai helper `loadtest/lib/common.js` yang sudah dipakai P2, sehingga
token, metrik, dan format hasil konsisten. Setelah stack hidup, jalankan
`loadtest/run.sh p5-priority.js`.
