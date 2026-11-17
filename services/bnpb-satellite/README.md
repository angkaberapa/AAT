# bnpb-satellite

Service P4 untuk Milestone 2. Service ini membaca Feed Satelit lewat SSE,
menyimpan deteksi hotspot dan offset di SQLite, membentuk kebakaran sesuai
Definisi Kebakaran, lalu mengirim HazardEvent WILDFIRE ke Aggregator.

## Isi

- `internal/sse`: client `GET /v1/stream` dengan `from` dan `Last-Event-ID`.
- `internal/feed`: parser frame detection, update, retract, dan heartbeat.
- `internal/store`: SQLite untuk detection, offset, fire, dan outbox.
- `internal/wildfire`: grouping 1 km/360 menit, severity, area, fire ID stabil,
  dan reconcile saat retract.
- `internal/processor`: loop stream, commit offset, dan pengiriman outbox.
- `internal/metrics`: metrik Prometheus.

## Perilaku

- Offset hanya maju setelah detection, fire, outbox, dan offset tersimpan dalam
  satu transaksi SQLite.
- `confidence=low` tidak disimpan. Deteksi yang lebih tua dari
  `SATELLITE_ALLOWED_LATENESS_SECONDS` tetap diproses dan dihitung sebagai late.
- `update` memperbarui detection yang sama; `retract` menonaktifkan detection
  lalu menghitung ulang seluruh komponen aktif. Fire yang turun di bawah dua
  detection menghasilkan `DELETE`.
- `producer_event_id` memakai versi fire, sehingga pengiriman ulang tidak
  menambah versi baru.
- Koneksi yang putus disambung ulang dari offset terakhir dengan backoff.

## Konfigurasi

| Variabel | Default | Arti |
|---|---|---|
| `PORT` | `8000` | Port HTTP health, status, dan metrics |
| `SATELLITE_DB_PATH` | `/data/satellite.db` | Lokasi SQLite |
| `SATELLITE_BASE_URL` | `https://satellite.naufarrel.dev` | Basis Feed Satelit |
| `SATELLITE_TOKEN` | wajib | Token feed dari asisten |
| `SATELLITE_INGEST_TOKEN` | wajib | Token ke Internal Ingest API |
| `SATELLITE_ALLOWED_LATENESS_SECONDS` | `21600` | Batas keterlambatan (360 menit) |
| `SATELLITE_RECONNECT_MIN_SECONDS` | `1` | Backoff minimum |
| `SATELLITE_RECONNECT_MAX_SECONDS` | `30` | Backoff maksimum |
| `AGGREGATOR_BASE_URL` | `http://bnpb-aggregator:8000` | Aggregator |
| `AGGREGATOR_TIMEOUT_MS` | `5000` | Timeout request ingest |

## Menjalankan lokal

```bash
cd services/bnpb-satellite
SATELLITE_TOKEN=... SATELLITE_INGEST_TOKEN=... \
SATELLITE_DB_PATH=./satellite.db go run .
```

Endpoint: `GET /health`, `GET /status`, `GET /metrics`.

## Deploy

Manifest ada di `k8s/base/satellite.yaml`. Secret `bnpb-secrets` harus memuat
`SATELLITE_TOKEN` dan `SATELLITE_INGEST_TOKEN`.

```bash
kubectl apply -k k8s/base
```

## Batasan

- Satu replika. SQLite dan offset tidak dibagi antar pod.
- Internal Ingest API harus menyediakan `POST /internal/ingest/hazards`.
- Perbandingan dengan hasil referensi asisten belum dijalankan karena token
  Feed Satelit belum tersedia saat implementasi.

## Deklarasi AI

Kode ini ditulis dengan bantuan LLM (OpenCode, DeepSeek V4.1 Flash) untuk
Milestone 2 IF4031. Keputusan desain dan hasil uji ditinjau oleh anggota
kelompok.
