# P5 Coastal 01: kontrak dan rencana

## Dasar

Commit ini berdiri di atas tiga hal. Pertama, spesifikasi M2 mewajibkan
observability, Audit API, label `role`, storage milik satu service, dan resource
limit. Kedua, P4/P5 membutuhkan jalur tulis ke canonical store tanpa membuka
MongoDB ke service baru. Ketiga, topologi P1 belum final, sehingga P4/P5 tidak
bisa menunggu bentuk sharding atau replikasi selesai.

Kontrak Internal Ingest memisahkan producer dari perubahan topologi itu.
`bnpb-coastal` dan `bnpb-satellite` hanya perlu tahu endpoint, token, dan bentuk
command. Aggregator tetap menjadi satu-satunya pemilik canonical storage. Karena
itu kontrak ditulis lebih dulu, sebelum kode service, supaya antarmuka bisa
direview A dan C tanpa menunggu implementasi.

## Alternatif

Pilihan pertama adalah menunggu endpoint Aggregator selesai, lalu menulis service
P4/P5 langsung terhadap endpoint itu. Ini ditolak karena P4/P5 jadi idle dan
kontraknya baru ditemukan saat integrasi, ketika biaya perubahan sudah tinggi.

Pilihan kedua adalah memberi service P4/P5 akses langsung ke MongoDB. Ini ditolak
karena melanggar ketentuan storage milik satu service dan membuat P1 tidak bisa
mengganti topologi tanpa menyentuh dua service lain.

Pilihan ketiga adalah memakai Kafka sebagai satu-satunya jalur tulis. Ini ditolak
untuk tahap ini karena menambah offset management dan exactly-once antara Kafka
dan Mongo, sementara kontrak Internal Ingest sudah cukup untuk deadline.

## Batas

Commit ini hanya dokumen. Tidak ada kode Aggregator, Audit API, atau service
P4/P5. Batas ini sengaja supaya perubahan antarmuka bisa dibahas lebih dulu,
tanpa tercampur dengan implementasi yang bergantung padanya.

## Konsekuensi

Kontrak menjadi baseline. Kalau A atau C mengubah bentuk command, perubahan itu
harus dibicarakan dan versi dokumennya diperbarui. Kontrak juga belum menjawab
kontrak data Sensor Pantai, strategi rebuild setelah local PV hilang, atau
prioritas `field-reports`. Hal-hal itu tetap menjadi pekerjaan berikutnya.

## Bukti

Tidak ada test kode. Bukti commit ini adalah dokumen yang bisa dibaca dan
dicocokkan dengan spesifikasi: endpoint `hazards`, `source-status`, dan
`unprocessed`, aturan idempotency, serta aturan commit cursor.
