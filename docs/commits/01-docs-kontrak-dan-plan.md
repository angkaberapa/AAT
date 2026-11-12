# Commit 01: docs: add M2 internal ingest contract and P5 plan

## Tujuan

Commit ini menaruh kesepakatan antarmuka antara `bnpb-coastal`, `bnpb-satellite`,
dan Aggregator. Tanpa kontrak ini, service P4/P5 tidak punya cara menulis
`HazardEvent` tanpa membuka akses langsung ke MongoDB. Commit ini juga menaruh
rencana besar dan breakdown commit supaya pekerjaan berikutnya tidak melebar.

## File

- `docs/KONTRAK_INTERNAL_INGEST.md`
- `docs/M2_P4_P5_BONUS_PLAN.md`
- `docs/commits/README.md`
- `docs/commits/01-docs-kontrak-dan-plan.md` sampai `11-...md`

## Alur end-to-end

Kontrak mulai dari prinsip bahwa producer tidak menyentuh canonical storage.
Producer mengirim command ke Internal Ingest API, lalu Aggregator menyimpan
perubahan canonical dan outbox dalam satu transaksi. Respons `2xx` berarti
perubahan sudah durable, tetapi belum tentu sudah terbit ke Kafka. Producer baru
boleh memajukan cursor setelah respons itu diterima.

Command `UPSERT` membawa `producer_event_id`, `source_position`, dan
`HazardEvent`. Command `DELETE` dipakai saat fire tidak lagi memenuhi syarat,
misalnya jumlah detection aktif turun di bawah dua. Aggregator menyimpan
tombstone dan menerbitkannya lewat outbox supaya consumer bisa mengurangi
agregasi. Idempotency key harus sama dengan `producer_event_id`, sehingga retry
tidak membuat versi baru.

Kontrak juga mendefinisikan source status, unprocessed, token, dan aturan commit
cursor. Source status memakai `ok`, `degraded`, atau `unavailable` beserta
`reason_code`. Unprocessed membawa payload mentah dalam Base64 supaya JSON yang
rusak tetap bisa disimpan. Token Satellite, Coastal, dan Audit dipisahkan agar
satu producer tidak bisa menulis source lain.

## Keputusan

- Satu command per request, bukan batch. Ini membuat idempotency dan retry per
  event lebih jelas.
- `hazard_id` dikirim producer dan harus stabil. Nilainya memakai formula
  Aggregator, yaitu `HZ-` ditambah 16 byte pertama SHA-256 dari
  `source:source_ref_id`.
- `ingested_at` tidak dikirim producer. Aggregator mengisinya dengan waktu server.
- Producer menyimpan state dan outbox di SQLite lokal. Local PV boleh hilang,
  jadi implementasi harus punya strategi rebuild atau replay.

## Verifikasi

Tidak ada kode yang dijalankan. Verifikasi dilakukan dengan membaca kontrak dan
mencocokkan setiap endpoint dengan rencana. Periksa bahwa:

- `POST /internal/ingest/hazards` punya `UPSERT` dan `DELETE`.
- `POST /internal/ingest/source-status` punya status dan reason code.
- `POST /internal/ingest/unprocessed` punya payload Base64 dan status
  `retrying`/`parked`.
- Aturan commit cursor menyebut respons `2xx`.

## Yang tidak termasuk

- Implementasi endpoint di Aggregator. Itu milik P2/platform.
- Audit API dan failed-message store. Itu milik P1/P3.
- Kode service P4/P5.
