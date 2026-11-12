# kontrak

Dokumen ini mendefinisikan kontrak antara `bnpb-satellite`, `bnpb-coastal`, dan BNPB
Aggregator. Tujuannya agar implementasi P4 dan P5 dapat dimulai tanpa bergantung pada
topologi penyimpanan P1. Perubahan yang memutus kompatibilitas terhadap kontrak ini harus
dibicarakan sebelum digabungkan.

## 1. Prinsip Integrasi

1. `bnpb-satellite` dan `bnpb-coastal` tidak mengakses MongoDB atau storage P1 secara langsung.
2. Seluruh perubahan canonical `HazardEvent` dikirim melalui Internal Ingest API milik Aggregator.
3. Respons sukses dari Internal Ingest API berarti perubahan canonical beserta outbox-nya
   telah tersimpan secara durable. Respons tersebut tidak menjamin event sudah diterbitkan
   ke Kafka.
4. Setiap operasi wajib idempoten. Retry request yang sama tidak boleh membuat event atau
   versi baru.
5. Offset/cursor lokal hanya boleh di-commit setelah Internal Ingest API mengembalikan
   respons `2xx`.
6. Token Satellite, Coastal, dan Audit dipisahkan dan memiliki izin minimum.

## 2. Perluasan Canonical Model M2

Nilai baru pada `HazardEvent.source`:

```text
SATELIT
SENSOR_PANTAI
```

Nilai baru pada `HazardEvent.hazard_type`:

```text
WILDFIRE
COASTAL
```

Bentuk canonical event yang dikirim producer:

```json
{
  "hazard_id": "HZ-...",
  "source": "SATELIT",
  "source_ref_id": "fire-...",
  "hazard_type": "WILDFIRE",
  "severity": "SIAGA",
  "area_name": "Kalimantan Tengah",
  "latitude": -2.51234,
  "longitude": 113.8045,
  "occurred_at": "2026-11-12T02:43:00Z",
  "attributes": {}
}
```

Ketentuan:

- `hazard_id` dan `source_ref_id` wajib stabil. Pemrosesan ulang data yang sama harus
  menghasilkan identifier yang sama.
- `occurred_at` menggunakan waktu kejadian pada sumber, bukan waktu data tiba di BNPB.
- Producer tidak mengirim `ingested_at`. Aggregator mengisinya menggunakan waktu server.
- `severity` menggunakan salah satu nilai `NORMAL`, `WASPADA`, `SIAGA`, atau `AWAS`.

## 3. Internal Hazard Ingest

### 3.1 Endpoint

```http
POST /internal/ingest/hazards
Authorization: Bearer <service-token>
Content-Type: application/json
Idempotency-Key: <producer-event-id>
X-Correlation-ID: <optional-correlation-id>
```

`Idempotency-Key` wajib sama dengan `producer_event_id` pada body.

### 3.2 Operasi UPSERT

```json
{
  "operation": "UPSERT",
  "producer": "bnpb-satellite",
  "producer_event_id": "satellite-offset-1872",
  "source_position": {
    "type": "sse_offset",
    "value": "1872"
  },
  "event": {
    "hazard_id": "HZ-...",
    "source": "SATELIT",
    "source_ref_id": "fire-...",
    "hazard_type": "WILDFIRE",
    "severity": "SIAGA",
    "area_name": "Kalimantan Tengah",
    "latitude": -2.51234,
    "longitude": 113.8045,
    "occurred_at": "2026-11-12T02:43:00Z",
    "attributes": {
      "detection_count": 6,
      "satellites": ["N20", "N21"],
      "max_confidence": "h",
      "total_frp_mw": 41.45,
      "detection_keys": [
        "N20|2026-11-12|243|-2.51230|113.80440"
      ]
    }
  }
}
```

Untuk Coastal, nilai berikut digunakan:

```text
producer             = bnpb-coastal
source               = SENSOR_PANTAI
hazard_type          = COASTAL
source_position.type = coastal_cursor
```

### 3.3 Operasi DELETE

Operasi `DELETE` digunakan ketika update/retract membuat sebuah kebakaran tidak lagi
memenuhi syarat sebagai `HazardEvent`, misalnya jumlah deteksi aktif turun menjadi kurang
dari dua.

```json
{
  "operation": "DELETE",
  "producer": "bnpb-satellite",
  "producer_event_id": "satellite-offset-1901",
  "source_position": {
    "type": "sse_offset",
    "value": "1901"
  },
  "hazard_id": "HZ-...",
  "source": "SATELIT",
  "source_ref_id": "fire-..."
}
```

Aggregator menyimpan tombstone dan menerbitkan perubahan tersebut melalui outbox agar
consumer dapat menghapus atau mengurangi agregasi yang sebelumnya sudah dihitung.

### 3.4 Respons Sukses

```json
{
  "hazard_id": "HZ-...",
  "result": "inserted",
  "version": 1,
  "producer_event_id": "satellite-offset-1872"
}
```

Nilai `result`:

```text
inserted
updated
unchanged
deleted
already_applied
```

Jika request dengan `producer_event_id` yang sama dikirim kembali menggunakan payload
yang sama, Aggregator mengembalikan hasil sebelumnya tanpa membuat versi baru. Jika key
yang sama digunakan untuk payload berbeda, Aggregator mengembalikan `409 Conflict`.

### 3.5 Error

Seluruh error menggunakan bentuk:

```json
{
  "error": {
    "code": "validation_failed",
    "message": "source_ref_id is required"
  }
}
```

Status yang digunakan:

| Status | Makna |
|---|---|
| `400` | Request atau JSON envelope tidak valid |
| `401` | Token tidak ada atau tidak valid |
| `403` | Token tidak boleh menulis source tersebut |
| `409` | Idempotency key digunakan untuk payload berbeda |
| `413` | Payload melebihi batas ukuran |
| `422` | Canonical `HazardEvent` tidak valid |
| `503` | Canonical storage sementara tidak tersedia |

Untuk timeout, kegagalan koneksi, `429`, atau `5xx`, producer mengirim ulang request yang
sama menggunakan idempotency key yang sama. Error validasi `4xx` selain `429` tidak
dicoba ulang tanpa perubahan data atau kode.

## 4. Internal Source Status

### 4.1 Endpoint

```http
POST /internal/ingest/source-status
Authorization: Bearer <service-token>
Content-Type: application/json
X-Correlation-ID: <optional-correlation-id>
```

### 4.2 Body

```json
{
  "source": "SENSOR_PANTAI",
  "status": "degraded",
  "last_event_at": "2026-11-12T10:30:00Z",
  "lag_seconds": 14.7,
  "reason_code": "circuit_open",
  "details": {
    "cursor": "coastal-1837"
  }
}
```

Nilai `status`:

```text
ok
degraded
unavailable
```

Aggregator menggunakan waktu penerimaan server sebagai dasar freshness. Nilai
`last_event_at` dipakai sebagai informasi waktu data terakhir, bukan sebagai sumber
kebenaran lease atau waktu sistem.

`reason_code` minimal untuk Satellite:

```text
disconnected
reconnecting
lagging
upstream_unavailable
```

`reason_code` minimal untuk Coastal:

```text
upstream_timeout
circuit_open
stale_cache
invalid_payload
upstream_unavailable
```

## 5. Internal Unprocessed Ingest

Endpoint ini menghubungkan quarantine P5 dan kegagalan P4 dengan failed-message store P3.

### 5.1 Endpoint

```http
POST /internal/ingest/unprocessed
Authorization: Bearer <service-token>
Content-Type: application/json
Idempotency-Key: <message-id>
X-Correlation-ID: <optional-correlation-id>
```

### 5.2 Body

```json
{
  "message_id": "coastal-observation-392",
  "source": "SENSOR_PANTAI",
  "source_position": {
    "type": "coastal_cursor",
    "value": "392"
  },
  "received_at": "2026-11-12T10:30:00Z",
  "reason": "contract_violation: observed_at is missing",
  "retryable": false,
  "status": "parked",
  "raw_payload_base64": "eyJpbnZhbGlkIjoidHJ1ZSJ9",
  "content_type": "application/json"
}
```

Nilai status:

```text
retrying
parked
```

Ketentuan:

- `message_id` wajib deterministik agar retry tidak membuat record baru.
- Payload mentah dikirim sebagai Base64 agar JSON terpotong atau UTF-8 tidak valid tetap
  dapat disimpan secara utuh.
- Jika endpoint unavailable, producer menyimpan record di SQLite local outbox dan
  mengirimkannya kembali sampai memperoleh respons `2xx`.
- Pesan yang belum pulih ditampilkan melalui `GET /internal/audit/unprocessed`.
- Daftar Audit API tidak menampilkan raw payload. Akses raw payload menggunakan endpoint
  Ops terproteksi yang disediakan P3.

## 6. Token dan Otorisasi

Secret yang digunakan:

```text
SATELLITE_INGEST_TOKEN
COASTAL_INGEST_TOKEN
AUDIT_API_TOKEN
```

Aturan izin:

| Token | Izin |
|---|---|
| Satellite | Menulis `source=SATELIT`, `hazard_type=WILDFIRE`, status Satellite, dan unprocessed Satellite |
| Coastal | Menulis `source=SENSOR_PANTAI`, `hazard_type=COASTAL`, status Coastal, dan unprocessed Coastal |
| Audit | Membaca endpoint `/internal/audit/*`; tidak memiliki izin ingest |

Token diambil dari environment/Kubernetes Secret dan tidak boleh di-hardcode atau
di-commit. Untuk development, gunakan placeholder lokal yang tidak disimpan di Git.

## 7. Aturan Commit Offset dan Cursor

Urutan pemrosesan producer:

1. Terima atau baca data sumber.
2. Simpan raw input dan perubahan state ke SQLite.
3. Bentuk operasi UPSERT, DELETE, atau unprocessed.
4. Simpan operasi ke local outbox dalam transaksi SQLite yang sama dengan perubahan
   state.
5. Kirim operasi ke Aggregator.
6. Setelah menerima `2xx`, tandai outbox sebagai terkirim dan commit offset/cursor.
7. Jika koneksi putus sebelum respons diterima, kirim ulang operasi dengan idempotency key
   yang sama.

Dengan aturan ini, pengiriman bersifat at-least-once, sedangkan efek penyimpanan tetap
effectively-once melalui idempotency dan deduplikasi Aggregator.

## 8. Persistence Lokal

SQLite Satellite disimpan pada:

```text
/data/satellite.db
```

Minimal menyimpan:

- SSE offset terakhir yang sudah durable di Aggregator;
- raw detection;
- update/retract state;
- clustering/intermediate state;
- watermark/checkpoint;
- pending ingest/unprocessed outbox.

SQLite Coastal disimpan pada:

```text
/data/coastal.db
```

Minimal menyimpan:

- cursor terakhir yang sudah durable;
- pending observation;
- pending hazard ingest;
- pending quarantine;
- cache terakhir;
- local outbox;
- state circuit breaker jika diperlukan untuk recovery.

Kedua direktori `/data` akan dipasang ke PersistentVolumeClaim. Implementasi juga harus
memiliki strategi rebuild/replay karena local PV dapat hilang jika node beserta disk-nya
hilang.

## 9. Metrics dan Tracing

Kedua service mengekspos:

```http
GET /metrics
```

Platform menyediakan Prometheus, Grafana, OpenTelemetry Collector, dan tracing backend.
Instrumentation di dalam P4/P5 tetap menjadi tanggung jawab masing-masing service.

### 9.1 Satellite Metrics

Minimal:

```text
satellite_received_offset
satellite_committed_offset
satellite_stream_lag
satellite_reconnect_total
satellite_detection_total
satellite_late_detection_total
satellite_processing_duration_seconds
satellite_ingest_outbox_pending
wildfire_active_total
```

### 9.2 Coastal Metrics

Minimal:

```text
coastal_request_total
coastal_request_duration_seconds
coastal_cursor_lag
coastal_invalid_observation_total
coastal_quarantine_pending
coastal_ingest_outbox_pending
coastal_circuit_breaker_state
coastal_cache_age_seconds
coastal_critical_event_latency_seconds
```

Setiap outbound request dan processing cycle membawa correlation ID serta trace context.
Correlation ID yang sama harus diteruskan ke Internal Ingest API dan dicatat pada log
terstruktur.

## 10. NetworkPolicy dan Deployment Contract

- `bnpb-satellite` dan `bnpb-coastal` hanya membutuhkan akses keluar ke sumbernya,
  Aggregator Internal Ingest API, tracing collector, dan DNS.
- Keduanya tidak memperoleh akses jaringan atau credential ke canonical storage.
- Port metrics hanya dapat diakses Prometheus.
- Endpoint internal Aggregator tidak diekspos melalui public Ingress.
- Kedua pod menggunakan label `role=consumer`.
- SQLite dipasang melalui PVC pada `/data`.

## 11. Interface yang Dapat Dibuat Sekarang

P4/P5 dapat langsung mengimplementasikan interface setara berikut:

```go
type IngestClient interface {
	UpsertHazard(ctx context.Context, command UpsertHazardCommand) (IngestResult, error)
	DeleteHazard(ctx context.Context, command DeleteHazardCommand) (IngestResult, error)
	ReportSourceStatus(ctx context.Context, status SourceStatus) error
	StoreUnprocessed(ctx context.Context, message UnprocessedMessage) error
}
```

Implementasi sementara dapat menggunakan fake HTTP Aggregator dengan kontrak yang sama.
Saat endpoint Aggregator tersedia, hanya konfigurasi base URL dan token yang berubah.

Komponen berikut sudah dapat dikerjakan tanpa menunggu bagian P1 atau P3:

- DTO dan validasi client;
- fake Aggregator;
- SQLite schema dan migration;
- local outbox worker;
- retry dan idempotency behavior;
- SSE client dan wildfire grouping;
- Coastal pull client dan cursor;
- Coastal validator dan quarantine;
- cache dan circuit breaker;
- metrics dan tracing;
- unit serta integration test terhadap fake Aggregator.

## 12. Pembagian Tanggung Jawab

### P1/P3

- Perluasan canonical enum dan validasi Aggregator.
- Storage abstraction dan durable canonical write.
- Tombstone serta transactional outbox.
- Idempotency registry/deduplikasi command.
- Failed-message store.
- Audit API.
- `POST /field-reports` dan prioritas Tim Lapangan.

### P2/Platform

- Implementasi dan deployment endpoint internal Aggregator.
- Kubernetes Secret dan NetworkPolicy.
- Prometheus, Grafana, dan distributed tracing platform.
- Deployment/PVC untuk Satellite dan Coastal berdasarkan kebutuhan service.

### P4/P5

- Satellite dan Coastal client.
- SQLite state, checkpoint, cursor, dan local outbox.
- Wildfire processing dan Coastal validation.
- Retry, circuit breaker, cache, dan quarantine forwarding.
- Metrics, spans, dan structured logs service.
