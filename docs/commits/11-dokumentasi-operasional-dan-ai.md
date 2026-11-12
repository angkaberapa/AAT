# Commit 11: docs(coastal): document operation, limitations, and AI declaration

## Tujuan

Commit ini membuat service bisa dijalankan dan diperiksa orang lain tanpa
membaca kode. Isinya cara menjalankan lokal, cara membaca metrics, perilaku
circuit breaker, cara deploy, batasan, dan deklarasi penggunaan AI.

## File

- `services/bnpb-coastal/README.md`

## Alur end-to-end

README mulai dari status service dan daftar package. Setelah itu ada perintah
untuk menjalankan sensor practice, fake Aggregator, dan service lokal. Bagian
berikutnya menjelaskan endpoint `/status` dan `/metrics`, arti state circuit
breaker, dan cara membaca outbox serta quarantine.

Bagian Kubernetes menjelaskan manifest yang dipakai, dua secret yang harus diisi,
dan perintah build serta deploy. Bagian batasan menyebut apa yang belum ada:
load shedding, game day, Audit API, profile exam, dan station table final.
Deklarasi AI menyebut bahwa kode ditulis dengan bantuan LLM dan keputusan desain
serta hasil uji ditinjau anggota kelompok.

## Keputusan

- README menyebut angka dan nama metric yang benar, bukan nama umum.
- Batasan ditulis terbuka supaya reviewer tidak menganggap P5 sudah selesai.
- Perintah lokal memakai `GOWORK=off` karena module belum masuk `go.work`.

## Verifikasi

Baca README sambil menjalankan perintahnya. Periksa bahwa:

- `go test -race ./...` dan `go vet ./...` disebut.
- `GET /metrics` dan `GET /status` disebut.
- Dua token di `.env` disebut.
- Batasan load shedding, game day, Audit API, dan exam disebut.
- Deklarasi AI ada.

## Yang tidak termasuk

- Laporan PDF.
- Video demo.
- Dokumentasi P4 dan bonus.
