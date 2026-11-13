# P4 Satellite 04: severity, area, dan fire ID

## Dasar

Tabel Aturan Pemetaan Tambahan menetapkan `source=SATELIT`,
`hazard_type=WILDFIRE`, `source_ref_id` yang stabil, `area_name` dari provinsi
terdekat, `latitude`/`longitude` titik pusat, `occurred_at` detection paling
awal, severity berdasarkan jumlah detection dan total FRP, serta attributes
berisi jumlah detection, satelit, confidence tertinggi, total FRP, dan daftar
detection key.

Severity memakai ambang yang eksplisit: AWAS bila ≥20 detection atau total FRP
≥100 MW; SIAGA bila ≥6 atau ≥30 MW; selain itu WASPADA. Fire ID harus stabil
supaya memproses ulang data yang sama menghasilkan event yang sama.

## Alternatif

Pilihan pertama adalah memakai nama kota sebagai area. Ini ditolak karena
spesifikasi meminta provinsi, dan tabel provinsi bisa dipakai untuk semua titik
di Indonesia.

Pilihan kedua adalah memakai ID acak atau timestamp. Ini ditolak karena
pemrosesan ulang akan membuat event baru, melanggar syarat `source_ref_id`
stabil.

Pilihan ketiga adalah membiarkan Aggregator menghitung `hazard_id` dari
`source_ref_id`. Ini ditolak karena kontrak bagian 2 mengirim `hazard_id` dari
producer; Aggregator hanya memvalidasi. Formula harus sama di kedua sisi.

## Batas

Commit ini belum menangani retract, delete, atau processor. Tabel provinsi masih
statis dan bisa diganti tabel resmi BNPB. Severity memakai total FRP dari
detection yang sudah lolos filter confidence di processor nanti.

## Konsekuensi

Fire ID memakai detection key terkecil di komponen. Kalau detection terkecil
di-retract dan komponen masih ada, ID bisa berubah. Ini trade-off yang
didokumentasikan dan akan terlihat saat reconcile. Centroid dihitung dari semua
detection valid di komponen, bukan dari detection pertama.

## Bukti

`go test ./internal/wildfire/...` mencakup ambang severity, attributes, area,
dan ID stabil. `go test ./internal/reference/...` mencakup provinsi terdekat
untuk Padang, Kalimantan Tengah, dan Papua.
