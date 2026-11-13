# Rencana End-to-End P4, P5, dan Bonus

Status: 2026-11-13
Branch P5: `feat/m2-p5-coastal-ingest`
Backup: `backup/m2-p5-coastal-ingest-2026-11-13`
Kontrak: `docs/KONTRAK_INTERNAL_INGEST.md`

Dokumen ini menggantikan rencana singkat `M2_P4_P5_PLAN.md`. Isinya memetakan
setiap kriteria ke pekerjaan, bukti, dan urutan commit. Rincian tiap commit ada
di `docs/commits/`, satu file per commit.

## 1. Definisi selesai

Satu problem dianggap selesai kalau tiga hal ada:

1. Kode yang menjalankan kriteria.
2. Bukti dari sistem yang berjalan, bukan klaim.
3. Bagian laporan yang menjelaskan masalah, desain, trade-off, batasan, dan bukti.

Status kode saja tidak cukup. Status laporan saja juga tidak cukup.

## 2. Matriks P4

| # | Kriteria | Pekerjaan | Bukti | Status |
|---|---|---|---|---|
| 1 | Hotspot menjadi WILDFIRE sesuai definisi, satu kebakaran satu event | Parser detection, skip confidence low, detection key, union-find 1 km/360 menit, fire ID stabil, severity, area provinsi | Unit test, Audit API events, diff referensi | Belum |
| 2 | Kelompokkan berdasarkan waktu deteksi, batas keterlambatan, late data dihitung | Event time dari `acq_date`+`acq_time`, watermark, late counter, allowed lateness | Metric late, log, fixture data terlambat | Belum |
| 3 | Update/retract memperbarui event yang ada, bukan event baru | Tandai detection inactive, rebuild komponen sekitar, pecah komponen, `DELETE` bila kurang dari 2 | Test retract, Audit API, dashboard | Belum |
| 4 | 60x, tidak ada data hilang, pulih ≤60 detik, p95 dan lag di dashboard | Bounded queue, batch SQLite, histogram p95, gauge lag, dashboard | Replay 60x, screenshot dashboard | Belum |
| 5 | Pemroses mati saat fire setengah jadi, hasil akhir sama | Checkpoint detection, komponen, offset, outbox; kirim ulang idempoten | Kill pod, bandingkan checksum | Belum |
| 6 | Bandingkan dengan hasil referensi | Export `source_ref_id` + `detection_keys`, diff, klasifikasi selisih | Laporan diff dan penyebab | Belum |
| 7 | Feed live beberapa menit saat demo | SSE client, token, dashboard | Demo live, log | Belum |
| 8 | Putus dan sambung tanpa hilang atau ganda | `Last-Event-ID`/`from`, offset setelah commit, upsert idempoten | Test reconnect, Audit API | Belum |

Laporan P4 yang harus ditulis:

1. Nama persoalan dan konsep literatur untuk kriteria 2, 4, dan 5.
2. Alasan polling M1 dipertahankan atau ditinggalkan untuk Feed Satelit.
3. Cara menentukan kebakaran selesai dikenali, penanganan keterlambatan, dan tempat state.
4. Jaminan pemrosesan dan buktinya.

## 3. Matriks P5

| # | Kriteria | Pekerjaan | Bukti | Status |
|---|---|---|---|---|
| 1 | 0 event kritis hilang, p95 ≤5 detik | Cursor, outbox, dedup, notifier, histogram latency | Hitung critical di feed vs notifier, p95 | Sebagian |
| 2 | Data melanggar kontrak disisihkan dengan alasan | Validator, quarantine, `unprocessed` | Jumlah quarantine per alasan, Audit API | Sebagian |
| 3 | Sumber lain tetap memenuhi latensi M1 saat sensor bermasalah | Service terpisah, resource limit, load test | p95 BMKG/PVMBG saat sensor down | Sebagian |
| 4 | Request ke sensor tidak melonjak, client cepat, pulih sendiri | Circuit breaker, backoff, cache, status stale, half-open | Request rate, breaker state, waktu pulih | Sebagian |
| 5 | Overload: Tim Lapangan dilayani, Media ditolak terkontrol | Role claim, priority admission, media cap, `field-reports` prioritas | Load test, 429 Media, p95 Field | Belum |
| 6 | Kombinasi kegagalan, tanpa intervensi | Probe, restart, state tahan node mati, game day | Log, metric, postmortem | Belum |
| 7 | Semua anggota bisa menjelaskan dari dashboard/metrik/trace | Metrics, trace, dashboard, runbook | Screenshot, trace satu request | Sebagian |
| 8 | SLO dan pemakaian selama game day | SLO 99,9% dalam 5 detik, error budget, laporan | SLO report | Belum |

Laporan P5 yang harus ditulis:

1. Nama persoalan dan konsep literatur untuk kriteria 2 sampai 5.
2. Mekanisme ketahanan, konfigurasi, dan alasan nilainya.
3. Perilaku menyimpang Sensor Pantai yang ditemukan, cara menemukan, dan penanganannya.
4. Postmortem game day: deteksi, dampak, mitigasi, perbaikan.

## 4. Matriks bonus

| Bonus | Pekerjaan | Bukti | Status |
|---|---|---|---|
| AI-native | Consumer async dari Kafka, ringkasan atau anomali, timeout, fallback, dashboard | AI dimatikan, jalur peringatan tetap jalan | Belum |
| Autoscaling | HPA atau KEDA pada komponen yang cocok | Jumlah pod naik/turun saat beban berubah | Belum |
| Host fisik berbeda | Dua host nyata, ukur latensi antar-node | Screenshot host, pengukuran, cabut jaringan | Belum |

Bonus AI-native prioritas Faqih. Autoscaling dan host berbeda hanya kalau waktu tersisa.

## 5. Pekerjaan P5

### 5.1 Sudah ada

- Ingest client dan fake Aggregator.
- SQLite cursor, observations, outbox, quarantine, cache.
- Poller Sensor Pantai dan validator kontrak.
- Mapper COASTAL dan station table 7 station dari image.
- Aturan commit cursor: outbox dan quarantine harus terkirim.
- Park `409/413/422` ke `unprocessed`; stop pada `400/401/403/404`.
- Circuit breaker closed/open/half-open.
- Metrics Prometheus sesuai §9.2.
- Manifest Kubernetes sensor dan coastal.
- Unit test, race test, vet, Docker build, kustomize, end-to-end practice.

### 5.2 Belum

1. **Load shedding dan prioritas**
   - Tambah claim `role` di token.
   - Priority admission di `bnpb-api`.
   - Media cap dan `Retry-After`.
   - `POST /field-reports` memakai jalur prioritas yang sama.
   - Aggregator tidak boleh tenggelam oleh Media.
2. **Game day dan SLO**
   - Skenario sensor mati, 429/500/timeout, data rusak, node mati, network partition, PVMBG outage, media flood.
   - Setiap eksperimen punya steady state, blast radius, abort criteria, postmortem.
   - SLO 99,9% event kritis sampai notifier dalam 5 detik.
3. **Audit dan exam**
   - Quarantine muncul di `GET /internal/audit/unprocessed`.
   - Uji profile exam dengan kunci asisten.
   - Bandingkan critical count dengan referensi.
4. **State saat node mati**
   - Pin coastal dan notifier ke Node A, atau replikasi cursor/outbox.
   - Uji node B dimatikan dan disk dihapus.
5. **Bukti laporan**
   - Screenshot dashboard, log correlation ID, hasil load test, postmortem.

### 5.3 Commit P5 berikutnya

| # | Commit | File | Verifikasi |
|---|---|---|---|
| 1 | `feat(api): add role claim and priority admission control` | `bnpb-auth`, `bnpb-api` | Load test Media vs Field |
| 2 | `feat(api): protect field-reports with field priority` | `bnpb-api`, P3 handler | 202 cepat saat Media flood |
| 3 | `feat(observability): add P5 dashboard and alerts` | k8s monitoring, dashboard JSON | Screenshot |
| 4 | `test(p5): add outage and media flood scenarios` | loadtest, game day script | Hasil test |
| 5 | `docs(p5): add SLO and game day postmortem` | docs | Laporan |

## 6. Pekerjaan P4

Branch: `feat/m2-p4-satellite`

### 6.1 Alur

```text
Feed Satelit --SSE--> bnpb-satellite
                         |
                         +--> SQLite: raw detection, offset, komponen, watermark, outbox
                         |
                         +--> POST /internal/ingest/hazards (UPSERT/DELETE)
```

### 6.2 Keputusan algoritma

- Detection key: `satellite|acq_date|acq_time|latitude|longitude`, lat/lon disimpan sebagai string asli.
- Waktu deteksi: `acq_date` + `acq_time` UTC.
- `confidence=low` dibuang.
- Dua detection terhubung bila jarak ≤1,0 km dan selisih waktu ≤360 menit.
- Hubungan berantai memakai union-find.
- Komponen ≥2 detection adalah satu fire; detection tunggal dihitung terpisah.
- Fire ID stabil: hash SHA-256 dari detection key terkecil di komponen.
- Area: provinsi dengan centroid terdekat. Tambah `internal/reference/provinces.json`.
- Severity: AWAS ≥20 detection atau total FRP ≥100 MW; SIAGA ≥6 atau ≥30 MW; selain itu WASPADA.
- Watermark: `max(event_time) - 360 menit`.
- Detection di bawah watermark tetap diproses, ditandai late, dan dihitung.
- Fire ditutup setelah watermark melewati detection terakhir + 360 menit.
- Retract: tandai detection inactive, rebuild komponen di grid sekitar dengan BFS.
- Bila komponen turun di bawah 2 detection, kirim `DELETE`.
- Merge: pertahankan komponen dengan detection paling awal, kirim `DELETE` untuk yang terserap.

### 6.3 Commit P4

| # | Commit | File | Verifikasi |
|---|---|---|---|
| 1 | `feat(satellite): scaffold service and SSE client` | service baru, fake SSE | Reconnect test |
| 2 | `feat(satellite): add SQLite detection, offset, and outbox` | store | Unit test |
| 3 | `feat(satellite): group detections by event time and distance` | grouping | Fixture 1 km/360 menit |
| 4 | `feat(satellite): compute severity, area, and stable fire IDs` | mapper, provinces | Unit test |
| 5 | `feat(satellite): handle retract, split, and DELETE` | grouping, outbox | Test retract |
| 6 | `feat(satellite): add checkpoint, replay, and lag metrics` | checkpoint, metrics | Kill pod, 60x |
| 7 | `feat(satellite): add reference export and diff tool` | cmd/diff | Laporan diff |
| 8 | `feat(satellite): add Kubernetes manifests` | k8s/base | Kustomize |

### 6.4 Bukti P4

- Hasil grouping dibandingkan dengan referensi asisten.
- p95 dari deteksi masuk sampai HazardEvent tersimpan.
- Lag watermark terhadap event terakhir.
- Jumlah late detection dan single detection.
- Replay 60x dan waktu pemulihan.
- Kill pod saat fire setengah jadi dan checksum akhir.
- Reconnect `Last-Event-ID`.
- Live feed beberapa menit.

## 7. Bonus AI-native

Komponen: `bnpb-ai-summary`, consumer Kafka `hazard-events`.

- Membaca event terakhir, membuat ringkasan situasi atau skor anomali.
- Menulis ke topic atau collection sendiri.
- Tidak menulis ke canonical store.
- Tidak dipanggil notifier atau jalur peringatan.
- Timeout 2 sampai 3 detik, circuit breaker, cache, fallback template.
- Kalau LLM atau API mati, dashboard menampilkan `degraded:true`.

Commit:

1. `feat(ai): add async hazard summary consumer`
2. `feat(ai): add timeout, circuit breaker, and fallback`
3. `feat(ai): add dashboard endpoint and metrics`
4. `docs(ai): add failure demo and evidence`

## 8. Dependensi lintas tim

| Kebutuhan | Pemilik | Dipakai untuk |
|---|---|---|
| Endpoint Internal Ingest | A/P2 | P4 dan P5 menulis canonical |
| Audit API `events`, `checksum`, `unprocessed` | P1/P3 | Bukti P4/P5 |
| Observability Prometheus, Grafana, trace | A/P2 | Kriteria P4 lag dan P5 penjelasan |
| `POST /field-reports` dan prioritas | P3 | Kriteria P5 5 |
| Token Feed Satelit | Asisten/kelompok | P4 live |
| Token Sensor Pantai dan kunci exam | Asisten/kelompok | P5 exam |
| Cluster dua node | Kelompok | Demo |
| Tanggal rekaman video | Kelompok | Deliverable |

## 9. Urutan waktu

| Tanggal | Pekerjaan |
|---|---|
| 13 November | Rapikan commit P5, buka MR draft, minta review kontrak |
| 14 November | Load shedding dan prioritas P5 |
| 15 November | Game day P5, SLO, postmortem |
| 16 November | Mulai P4: SSE client, SQLite, grouping |
| 17 November | P4: severity, area, retract, DELETE |
| 18 November | P4: checkpoint, 60x, reconnect, reference diff |
| 19 November | Bonus kalau P4/P5 hijau, laporan, video |
| 20 November | Tag `milestone-2` dan kumpulkan |

Kalau molor: P5 diselesaikan dulu, P4 bagian inti, bonus dilepas.

## 10. Aturan commit

1. Satu commit satu alasan. Kalau judul butuh "dan", pecah.
2. Setiap commit harus build dan dites.
3. Test ikut commit bersama kode.
4. Jangan campur docs, refactor, dan fitur.
5. Stage per kelompok file, review `git diff --cached`.
6. Jangan commit binary, database, token, atau file generate.
7. Commit di atas 400 baris dipecah per lapisan.
8. Push setelah satu slice selesai, bukan setiap commit.
9. Buka MR draft lebih awal untuk kontrak atau antarmuka.
10. Jangan amend atau force-push commit yang sudah direview orang lain.

## 11. Status history dan keputusan

History sekarang punya beberapa commit. Dua di antaranya terlalu lebar:

- `b7e14c2`: sensor client, validator, processor, main, Dockerfile.
- `d32e33a`: circuit breaker, metrics, manifest, wiring.

Usulan: rewrite branch menjadi 11 commit kecil sebelum MR. Backup sudah ada di
`backup/m2-p5-coastal-ingest-2026-11-13`. Keputusan yang menunggu:

1. Rewrite history sekarang atau pertahankan dan pakai aturan ini untuk commit berikutnya?
2. Setelah P5 slice ini merge, lanjut load shedding/game day atau mulai P4?
