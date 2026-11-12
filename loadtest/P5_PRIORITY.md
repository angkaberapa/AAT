# P5 priority admission load test

Script `p5-priority.js` menguji kriteria P5 nomor 5: saat Media membanjiri API,
Tim Lapangan tetap dilayani dan Media ditolak secara terkontrol.

Jalankan dari root repo:

```bash
loadtest/run.sh p5-priority.js
```

Hasil Markdown dan JSON ditulis ke `loadtest/results/`.

Yang dibaca dari hasil:

- `api_ok` dan `api_rejected_429` untuk melihat pembagian 200 dan 429.
- `http_req_duration{endpoint:field}` p95, harus di bawah 2 detik.
- `api_unexpected_error` harus di bawah 1 persen.
- Kalau Media tidak pernah mendapat 429, batas `MEDIA_MAX_CONCURRENT_REQUESTS`
  belum bekerja atau beban belum cukup.

Script ini membutuhkan `bnpb-api` dan `bnpb-auth` yang berjalan, serta token
client dari `.env`. Token harus sudah membawa claim `role` dari commit
`feat(auth): add role claim and propagate it to API principal`.
