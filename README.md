# IF4031 – Platform Koordinasi Kebencanaan (BMKG · PVMBG · BNPB)

Tugas Besar IF4031 Arsitektur Aplikasi Terdistribusi – Milestone 1.

## Arsitektur singkat

```
                    ┌─────────────┐      ┌──────────────┐
  client ──8090───▶ │  bnpb-auth  │      │  bmkg-mock   │◀─ X-BMKG-Key ─┐
  (media/field/ops) └─────────────┘      └──────────────┘               │  polling
          │                                                             │  (REST)
          └──8080─▶ ┌─────────────┐ REST ┌──────────────────┐           │
                    │  bnpb-api   │────▶ │ bnpb-aggregator  │───────────┤
                    └─────────────┘      └──────────────────┘           │
                                           │           │                │
                                           ▼           ▼                │
                                       ┌───────┐   ┌───────┐   ┌──────────────┐
                                       │ mongo │   │ kafka │   │  pvmbg-mock  │◀─ Bearer
                                       └───────┘   └───────┘   └──────────────┘
                                       (store net)     │ topic hazard-events
                                                ┌──────┴───────┐
                                                ▼              ▼
                                  consumer-dashboard   consumer-notifier
```

| Service | Folder | Port (host) | Peran | Problem |
|---|---|---|---|---|
| `bmkg-mock` | `services/bmkg-mock` | 8081 | Mock BMKG (seismik + tsunami). Kontrak: [`CONTRACT.md`](services/bmkg-mock/CONTRACT.md) | – |
| `pvmbg-mock` | `services/pvmbg-mock` | 8082 | Mock PVMBG (vulkanik, outage, schema switch). Kontrak: [`CONTRACT.md`](services/pvmbg-mock/CONTRACT.md) | – |
| `bnpb-aggregator` | `services/bnpb-aggregator` | – (internal 8000) | Polling, mapping → HazardEvent, pemilik Canonical Store, producer Kafka | P1, P2, P4, P5 |
| `bnpb-api` | `services/bnpb-api` | 8080 | Client-Facing API | P2, P3 |
| `bnpb-auth` | `services/bnpb-auth` | 8090 | Penerbit token (JWT) untuk client downstream | P3 |
| `bnpb-consumer-dashboard` | `services/bnpb-consumer-dashboard` | – | Consumer Kafka #1 | P5 |
| `bnpb-consumer-notifier` | `services/bnpb-consumer-notifier` | – | Consumer Kafka #2 | P5 |
| `mongo` | – | – | Canonical Store (MongoDB 7) | P4 |
| `kafka` | – | – | Message broker (Kafka 3.9, KRaft, 1 node) | P5 |

Keputusan solusi kelompok:
P1 JSON additive-only · P2 async I/O + rate limiter + graceful degradation ·
P3 JWT · P4 MongoDB · P5 Kafka.

## Menjalankan

Prasyarat: Docker + Docker Compose v2.

```bash
cp .env.example .env        # lalu ganti nilai rahasia
docker compose up -d --build
docker compose ps           # semua service harus (healthy)
```

Cek health:

```bash
curl localhost:8080/health   # bnpb-api
curl localhost:8081/health   # bmkg-mock
curl localhost:8082/health   # pvmbg-mock
curl localhost:8090/health   # bnpb-auth
```

Selama `Dockerfile` mock belum ada, jalankan bagian BNPB saja:

```bash
docker compose up -d --build bnpb-aggregator bnpb-api bnpb-auth bnpb-consumer-dashboard bnpb-consumer-notifier
```

Port host bisa diganti lewat `.env` (`API_PORT`, `BMKG_PORT`, `PVMBG_PORT`, `AUTH_PORT`)
bila bentrok dengan aplikasi lain.

Menjalankan / mematikan satu service secara independen:

```bash
docker compose stop bnpb-consumer-notifier
docker compose up -d --build --no-deps bnpb-consumer-notifier
```

## Konfigurasi

Semua konfigurasi & kredensial dibaca dari environment variable. Daftar lengkap beserta
nilai contoh ada di [`.env.example`](.env.example). File `.env` tidak di-commit.

`docker-compose.yml` hanya meneruskan variabel yang dibutuhkan tiap service (mis. `bnpb-api`
tidak menerima kredensial BMKG/PVMBG sama sekali).

## Konvensi lintas service

Berlaku untuk semua service (Ketentuan Implementasi no. 7):

- **Health check**: `GET /health` → `{"status": "ok", "service": "<nama>"}`.
- **Log terstruktur**: JSON satu baris per event ke stdout (`docker compose logs -f <service>`).
- **Correlation ID**: header `X-Correlation-ID`. Diterima dari request masuk (atau dibuat
  baru), disimpan di context, ikut tercetak di setiap log, dan diteruskan ke setiap panggilan
  keluar. Untuk alur tanpa request masuk (mis. satu siklus polling), buat ID baru per siklus.
- **Latensi panggilan keluar**: gunakan `createHttpClient(logger)` dari `src/core/http.ts`,
  jangan panggil `fetch` langsung, supaya latensi dan status tiap panggilan tercatat.
- **Background job** (polling, consumer Kafka): bungkus tiap siklus/event dengan
  `withCorrelationId(newCorrelationId(), () => ...)` dari `src/core/context.ts`.

Tiap service boleh memakai bahasa/framework berbeda; yang mengikat hanya kontrak jaringan.
Service mock menjelaskan kewajibannya di `CONTRACT.md` masing-masing.

Folder `src/core/` (context, logger, server, http) sengaja **disalin** ke setiap service BNPB,
bukan dibagikan sebagai library, sesuai larangan berbagi library lintas service. Isinya tidak
mengandung business logic. Kalau diubah, samakan di semua service.

## Stack BNPB

| Kebutuhan | Pilihan |
|---|---|
| Runtime / bahasa | Node.js 24 + TypeScript 6 (ESM) |
| HTTP server | Fastify 5 |
| Logging | pino (JSON) + `AsyncLocalStorage` untuk correlation ID |
| Validasi config | zod |
| HTTP client | `fetch` bawaan Node |
| MongoDB | driver resmi `mongodb` |
| Kafka | `@confluentinc/kafka-javascript` (API kompatibel KafkaJS; KafkaJS sendiri sudah tidak dirawat) |
| JWT | `jose` |

Pengembangan lokal satu service (tanpa Docker):

```bash
cd services/bnpb-api
npm install
npm run dev        # tsx watch; set env yang dibutuhkan (lihat src/config.ts)
npm run typecheck
```

Catatan npm 11: install script diblokir secara default. `@confluentinc/kafka-javascript` sudah
di-approve di `package.json` (`allowScripts`). Kalau versinya di-upgrade, jalankan ulang
`npm approve-scripts @confluentinc/kafka-javascript`.

## Struktur repo

```
.
├── docker-compose.yml
├── .env.example
├── docs/                    # diagram & bahan laporan
├── loadtest/                # skrip load test (P2)
├── scripts/                 # skrip demo tiap skenario
└── services/
    ├── bmkg-mock/           # tech bebas, lihat CONTRACT.md
    ├── pvmbg-mock/          # tech bebas, lihat CONTRACT.md
    └── bnpb-*/              # TypeScript + Fastify
        ├── Dockerfile       # multi-stage: build tsc -> image produksi
        ├── package.json
        ├── tsconfig.json
        └── src/
            ├── main.ts      # entrypoint
            ├── config.ts    # env -> config tervalidasi (zod)
            └── core/        # context (correlation ID), logger, server (+/health), http
```

`bnpb-aggregator` juga punya `src/ingest/`, `src/mapping/`, `src/store/`, `src/publisher/`,
`src/api/`, dan `reference/volcanoes.json` (tabel referensi gunung api milik BNPB).

## Skenario pengujian

_Diisi seiring implementasi tiap problem (P1–P5)._
