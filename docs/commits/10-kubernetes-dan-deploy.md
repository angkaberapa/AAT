# Commit 10: feat(coastal): add Kubernetes manifests and deploy wiring

## Tujuan

Commit ini membuat P5 bisa dijalankan di cluster, bukan hanya di laptop. Sensor
Pantai dan `bnpb-coastal` dideploy sebagai workload terpisah dengan role label
yang dikenali Tester Agent.

## File

- `k8s/base/coastal.yaml`
- `k8s/base/kustomization.yaml`
- `scripts/deploy.sh`
- `scripts/k3d-up.sh`
- `.env.example`

## Alur end-to-end

`coastal.yaml` memasang PVC, Deployment, Service, dan ConfigMap untuk sensor dan
coastal. Sensor memakai image digest yang diberikan asisten, port 8080, dan PVC
untuk state profile. Coastal memakai image `aat/bnpb-coastal:dev`, port 8000, PVC
untuk SQLite, dan ConfigMap untuk alamat sensor, alamat Aggregator, interval
poll, batas halaman, batas stale, serta konfigurasi circuit breaker.

Kedua Deployment memakai `role` yang berbeda. Sensor memakai `role=sensor` dan
coastal memakai `role=consumer`. Resource request dan limit mengikuti Lampiran B.
Sensor memakai 128Mi/256Mi, coastal memakai 128Mi/256Mi. Keduanya memakai
`Recreate` karena PVC ReadWriteOnce hanya boleh dipasang satu pod.

`scripts/deploy.sh` menambah dua secret baru, yaitu `PANTAI_AUTH_TOKEN` dan
`COASTAL_INGEST_TOKEN`. `.env.example` menyediakan placeholder. `scripts/k3d-up.sh`
membangun image `bnpb-coastal` bersama service lain.

## Keputusan

- Sensor memakai `PANTAI_PROFILE=practice` untuk pengembangan. Profile exam
  membutuhkan kunci dan diubah saat demo ujian.
- Tidak ada NetworkPolicy yang membatasi sensor karena Tester Agent harus bisa
  menjalankan skenario tanpa dihalangi.
- Service coastal hanya ClusterIP karena yang diakses dari luar hanyalah API dan
  dashboard. Metrics dibaca Prometheus dari dalam cluster.
- PVC memakai `local-path` sesuai ketentuan storage lokal. Konsekuensinya, pod
  tidak bisa pindah node dengan data yang sama. Strategi rebuild atau pin node
  harus diselesaikan di level platform.

## Verifikasi

```powershell
kubectl kustomize k8s/base
docker build -t aat/bnpb-coastal:dev services/bnpb-coastal
```

Kalau cluster tersedia:

```bash
scripts/k3d-up.sh
scripts/deploy.sh
kubectl -n sistem-informasi-kebencanaan-bnpb get pods -l app=bnpb-coastal
```

Hasil yang diharapkan: pod sensor dan coastal Ready, `/health` hijau, dan
`/metrics` bisa di-scrape.

## Yang tidak termasuk

- Dashboard dan alert Prometheus.
- Load shedding dan prioritas di `bnpb-api`.
- Overlay demo dua node.
