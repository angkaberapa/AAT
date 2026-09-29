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
- **Latensi panggilan keluar**: gunakan `core.NewHTTPClient(...)` (`internal/core/httpclient.go`),
  jangan `http.DefaultClient`, supaya correlation ID ikut terkirim dan latensi tiap panggilan tercatat.
- **Background job** (polling, consumer Kafka): buat context baru per siklus/event dengan
  `core.WithCorrelationID(ctx, core.NewCorrelationID())`, lalu log dengan varian `...Context`
  (`logger.InfoContext(ctx, ...)`) supaya `correlation_id` ikut tercetak.

Tiap service boleh memakai bahasa/framework berbeda; yang mengikat hanya kontrak jaringan.
Service mock menjelaskan kewajibannya di `CONTRACT.md` masing-masing.

Paket `internal/core/` (correlation ID, logger, server + `/health`, HTTP client, pembaca env)
sengaja **disalin** ke setiap service BNPB, bukan dibagikan sebagai library, sesuai larangan
berbagi library lintas service. Isinya tidak mengandung business logic. Kalau diubah, samakan
di semua service.

## Stack BNPB

| Kebutuhan | Pilihan |
|---|---|
| Bahasa | Go (`go 1.24` di `go.mod`; image build memakai Go 1.27) |
| HTTP server & client | `net/http` bawaan |
| Logging | `log/slog` bawaan (JSON), correlation ID lewat `context.Context` |
| Config | `os` bawaan + `core.Env` (semua error dilaporkan sekaligus, lalu exit) |
| Image runtime | `distroless/static:nonroot` (±16 MB, tanpa shell; health check via `/app healthcheck`) |

Base saat ini **hanya memakai standard library**. Library yang direncanakan saat problem terkait
dikerjakan: MongoDB → `go.mongodb.org/mongo-driver/v2`, Kafka → `github.com/twmb/franz-go`
(Go murni, tanpa cgo), JWT → `github.com/golang-jwt/jwt/v5` (+ JWKS), rate limit →
`golang.org/x/time/rate`.

Pengembangan lokal satu service (tanpa Docker):

```bash
cd services/bnpb-api
go run .           # set env yang dibutuhkan (lihat internal/config/config.go)
go vet ./...
go test ./...
```

`go.work` di root menggabungkan kelima modul supaya editor (gopls) bisa dibuka dari root repo.
Tiap service tetap modul Go tersendiri dan di-build terpisah oleh Docker.

## Struktur repo

```
.
├── docker-compose.yml
├── .env.example
├── go.work                  # workspace untuk editor; tidak dipakai Docker
├── docs/                    # backlog, diagram & bahan laporan
├── loadtest/                # skrip load test (P2)
├── scripts/                 # skrip demo tiap skenario
└── services/
    ├── bmkg-mock/           # tech bebas, lihat CONTRACT.md
    ├── pvmbg-mock/          # tech bebas, lihat CONTRACT.md
    └── bnpb-*/              # Go
        ├── Dockerfile       # multi-stage: go build -> distroless
        ├── go.mod
        ├── main.go          # entrypoint
        └── internal/
            ├── config/      # env -> Config tervalidasi
            └── core/        # correlation ID, logger, server (+/health), HTTP client, env
```

`bnpb-aggregator/internal` juga punya `ingest/`, `mapping/`, `store/`, `publisher/`, `api/`, dan
`reference/` (tabel gunung api milik BNPB, di-embed ke binary lewat `go:embed`).

## Skenario pengujian

_Diisi seiring implementasi tiap problem (P1–P5)._
