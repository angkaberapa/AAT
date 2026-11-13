# P5 priority admission load test

Script `p5-priority.js` menguji kriteria P5 nomor 5: saat Media membanjiri API,
Tim Lapangan tetap dilayani dan Media ditolak secara terkontrol.

Jalankan dari root repo:

```bash
loadtest/run.sh p5-priority.js
```

Hasil Markdown dan JSON ditulis ke `loadtest/results/`.

Yang dibaca dari hasil:

- `p5_field_ok` dan `p5_field_rejected`: Field harus selalu 200.
- `p5_media_ok` dan `p5_media_rejected`: Media harus mendapat 200 dan 429.
- `http_req_duration{endpoint:field}` p95, harus di bawah 2 detik.
- `api_unexpected_error` harus di bawah 1 persen.
- Kalau Media tidak pernah mendapat 429, batas `MEDIA_MAX_CONCURRENT_REQUESTS`
  belum bekerja atau beban belum cukup.

Script ini membutuhkan `bnpb-api` dan `bnpb-auth` yang berjalan, serta token
client dari `.env`. Token harus sudah membawa claim `role` dari commit
`feat(auth): add role claim and propagate it to API principal`.

## Hasil 2026-11-13

Dijalankan dengan Docker Compose, 150 VU Media dan 10 request/detik Field
selama 60 detik.

| Metrik | Hasil |
|---|---|
| Request data | 85.792 |
| Throughput | 1.418,1 req/s |
| 200 | 18.559 (306,8 req/s) |
| 429 terkontrol | 67.233 (78,37%) |
| Error di luar 429 | 0 |
| 5xx | 0 |
| Field 200 | 601 |
| Field 429 | 0 |
| Field p95 | 13,30 ms |
| Media 200 | 17.958 |
| Media 429 | 67.233 |
| Media p95 | 14,22 ms |

Semua threshold lulus. Field tidak pernah ditolak, Media ditolak dengan 429
setelah batas in-flight-nya tercapai. Media p95 mencakup respons 429 yang
memang cepat, jadi angka itu bukan latency jalur peringatan. Latency Field
adalah yang dipakai untuk kriteria P5.
