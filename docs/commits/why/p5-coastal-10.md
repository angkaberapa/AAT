# P5 Coastal 10: manifest Kubernetes dan deploy

## Dasar

Ketentuan Platform M2 mewajibkan seluruh komponen berjalan di Kubernetes,
minimal dua node, deploy satu perintah, Secret dan ConfigMap, resource limit
sesuai Lampiran B, dan label `role` yang dikenali Tester Agent. Spesifikasi P5
juga membutuhkan image Sensor Pantai berjalan sebagai sumber, bukan sebagai
library di dalam service.

Manifest ini memisahkan dua peran. Sensor memakai `role=sensor` dan coastal
memakai `role=consumer`. Pemisahan itu membuat Tester Agent bisa menemukan
sasaran yang benar tanpa menebak nama pod.

## Alternatif

Pilihan pertama adalah tetap memakai Docker Compose. Ini ditolak karena demo M2
wajib Kubernetes, dan skenario node mati tidak bisa dijalankan di Compose.

Pilihan kedua adalah menaruh sensor dan coastal dalam satu pod. Ini ditolak
karena kegagalan sensor akan ikut menjatuhkan coastal, padahal P5 justru menguji
isolasi sumber.

Pilihan ketiga adalah menambahkan NetworkPolicy yang hanya mengizinkan coastal
menghubungi sensor. Ini ditolak karena Tester Agent tidak boleh dihalangi, dan
policy pada port sensor bisa memblokir skenario ujian.

## Batas

Commit ini belum menaruh dashboard, alert, atau overlay demo dua node. Sensor
masih memakai `PANTAI_PROFILE=practice`; profile exam membutuhkan kunci.

## Konsekuensi

Kedua Deployment memakai `Recreate` karena PVC ReadWriteOnce hanya bisa dipasang
satu pod. PVC memakai `local-path`, jadi pod tidak bisa pindah node dengan data
yang sama. Ini risiko yang harus diselesaikan lewat pin node atau rebuild, dan
sudah dicatat sebagai batasan.

## Bukti

`kubectl kustomize k8s/base` lolos. `docker build` untuk image coastal lolos.
`scripts/k3d-up.sh` sudah memasukkan service coastal ke daftar build.
