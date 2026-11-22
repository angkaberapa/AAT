# Cluster demo multi-laptop (k3s di VM)

Panduan dari nol sampai sistem BNPB berjalan di cluster demo: 6 node VM di 3 laptop, satu node
sebagai control-plane. Ditulis dengan bantuan LLM (Claude).

> Spek: node wajib punya **kernel terpisah**. Beberapa distro WSL2 di satu laptop berbagi satu
> kernel, jadi **WSL2 tidak boleh dipakai sebagai node**. kind/k3d/minikube hanya untuk
> pengembangan.

## 1. Topologi

Overlay `k8s/overlays/demo` menempatkan MongoDB dengan aturan berikut: tiap shard (2 shard) adalah
replica set 3 anggota, **satu anggota per host fisik**, dan tiap node hanya untuk satu shard.
Karena itu dibutuhkan **3 laptop × 2 VM = 6 node**:

| Laptop | VM | Peran | Label | vCPU / RAM / disk |
|---|---|---|---|---|
| **A (paling kuat)** | `node-a1` | **control-plane** + worker | `host-a`, `shard-0` | 4 / 6 GB / 30 GB |
| A | `node-a2` | worker | `host-a`, `shard-1` | 2 / 3 GB / 20 GB |
| B | `node-b1` | worker | `host-b`, `shard-0` | 2 / 3 GB / 20 GB |
| B | `node-b2` | worker | `host-b`, `shard-1` | 2 / 3 GB / 20 GB |
| C | `node-c1` | worker | `host-c`, `shard-0` | 2 / 3 GB / 20 GB |
| C | `node-c2` | worker | `host-c`, `shard-1` | 2 / 3 GB / 20 GB |

Control-plane butuh lebih besar karena Kafka, consumer, satellite, coastal, dan sensor-pantai
di-pin ke sana (state di disk lokal, dan skenario mematikan node hanya dikenakan pada worker).
RAM yang dibutuhkan untuk VM: laptop A ±9 GB, laptop B/C ±6 GB.

Catat untuk README (Deklarasi Topologi): merek/tipe, CPU, dan RAM tiap laptop, serta IP tiap VM.

## 2. Prasyarat

Di **setiap laptop**:
- VirtualBox 7.x (atau Hyper-V/UTM; langkah di bawah memakai VirtualBox).
- ISO **Ubuntu Server 24.04 LTS** (`ubuntu-24.04-live-server-amd64.iso`).
- Virtualisasi (VT-x/AMD-V) aktif di BIOS. Di Windows, bila VirtualBox lambat atau gagal start
  VM, matikan "Memory integrity" (Core Isolation) atau pakai Hyper-V.

**Jaringan**: semua VM harus bisa saling menjangkau langsung. Wi-Fi kampus sering memblokir
komunikasi antar-perangkat (client isolation), jadi pakai **router sendiri atau hotspot HP**.
Jika tetap tidak bisa, gunakan rencana B (Tailscale) di bagian 9.

Di **laptop operator** (yang menjalankan `deploy.sh`): `git`, `bash`, `kubectl`, `python3`.
Bisa dari `node-a1` langsung (sudah ada `kubectl` dari k3s).

## 3. Membuat VM (ulangi untuk tiap VM)

1. VirtualBox → **New** → nama `node-a1` (dst.), ISO Ubuntu Server, *Skip Unattended
   Installation*; atur vCPU/RAM/disk sesuai tabel.
2. **Settings → Network → Adapter 1: Bridged Adapter**, pilih adapter Wi-Fi/LAN laptop yang
   sedang terhubung. Jangan memakai NAT: VM di laptop lain tidak akan bisa menjangkaunya.
3. Install Ubuntu: hostname **sama dengan nama node** (`node-a1`, …), centang **Install OpenSSH
   server**, sisanya bawaan.
4. Setelah boot, catat IP: `ip -4 addr show` (biasanya interface `enp0s3`). Agar IP tidak
   berubah, set statis lewat netplan atau reservasi DHCP di router:
   ```bash
   sudo tee /etc/netplan/60-static.yaml >/dev/null <<'EOF'
   network:
     version: 2
     ethernets:
       enp0s3:
         dhcp4: false
         addresses: [192.168.1.11/24]      # IP VM ini
         routes: [{to: default, via: 192.168.1.1}]
         nameservers: {addresses: [1.1.1.1, 8.8.8.8]}
   EOF
   sudo chmod 600 /etc/netplan/60-static.yaml && sudo netplan apply
   ```
5. Persiapan dasar:
   ```bash
   sudo apt update && sudo apt install -y curl git python3 chrony
   sudo swapoff -a && sudo sed -i '/ swap / s/^/#/' /etc/fstab
   timedatectl          # "System clock synchronized: yes"
   ```
6. Uji konektivitas dari setiap VM ke semua VM lain: `ping -c2 <ip-vm-lain>`.

## 4. Memasang k3s

**Control-plane (`node-a1`)**:
```bash
curl -sfL https://get.k3s.io | sh -s - server \
  --node-ip <IP_node-a1> --flannel-iface enp0s3 --write-kubeconfig-mode 644
sudo cat /var/lib/rancher/k3s/server/node-token       # token untuk node lain
kubectl get nodes                                       # node-a1 Ready
```

**Lima node lainnya**:
```bash
curl -sfL https://get.k3s.io | K3S_URL=https://<IP_node-a1>:6443 K3S_TOKEN=<token> \
  sh -s - agent --node-ip <IP_VM_ini> --flannel-iface enp0s3
```

Port antar-VM yang harus terbuka: **6443/tcp** (API), **8472/udp** (jaringan pod flannel),
**10250/tcp** (kubelet). Ubuntu Server tidak mengaktifkan firewall secara bawaan; bila `ufw`
aktif: `sudo ufw allow 6443/tcp && sudo ufw allow 8472/udp && sudo ufw allow 10250/tcp`.

Cek di `node-a1`: `kubectl get nodes -o wide` → 6 node `Ready`.

## 5. Label node

```bash
kubectl label node node-a1 bnpb.io/physical-host=host-a bnpb.io/mongo-shard=shard-0
kubectl label node node-a2 bnpb.io/physical-host=host-a bnpb.io/mongo-shard=shard-1
kubectl label node node-b1 bnpb.io/physical-host=host-b bnpb.io/mongo-shard=shard-0
kubectl label node node-b2 bnpb.io/physical-host=host-b bnpb.io/mongo-shard=shard-1
kubectl label node node-c1 bnpb.io/physical-host=host-c bnpb.io/mongo-shard=shard-0
kubectl label node node-c2 bnpb.io/physical-host=host-c bnpb.io/mongo-shard=shard-1

kubectl get nodes -L bnpb.io/physical-host,bnpb.io/mongo-shard
kubectl get nodes -l node-role.kubernetes.io/control-plane=true   # harus: node-a1
```

## 6. Deploy

Di `node-a1` (atau laptop operator dengan kubeconfig, lihat bagian 8):
```bash
git clone https://github.com/angkaberapa/AAT.git && cd AAT
cp .env.example .env
nano .env
```
Isi `.env`:
- semua nilai `change-me` diganti nilai sendiri;
- `MONGO_CLUSTER_KEY`: `openssl rand -base64 756 | tr -d '\n'`, **satu nilai yang sama** dipakai
  di mana pun `.env` ini dipakai;
- token dari asisten: `AUDIT_TOKEN`, `SATELLITE_TOKEN`, `PANTAI_AUTH_TOKEN`.

```bash
OVERLAY=demo bash scripts/deploy.sh
```
Image ditarik setiap node dari `ghcr.io/angkaberapa/aat-*` (public), tidak perlu build. Cek:
```bash
NS=sistem-informasi-kebencanaan-bnpb
kubectl -n $NS get pods -o wide                 # semua Running; Mongo tersebar per host
kubectl -n $NS logs job/mongo-bootstrap-v1      # inisialisasi shard selesai
kubectl -n $NS get lease bnpb-aggregator-ingest # leader aggregator
```

## 7. Akses

ServiceLB k3s membuka port di **setiap node**, jadi pakai IP node mana saja:
`http://<IP>:8080` (API), `:8090` (auth), `:8091/analytics` (dashboard analitik),
`:8092/notifications`, `:3000` (Grafana), `:16686` (Jaeger), `:9090` (Prometheus).
Contoh request ada di README bagian "Memicu skenario pengujian" (ganti `localhost` dengan IP node).

**Load test** (k6 lewat Docker, dijalankan di luar pod aplikasi): dari mesin Linux yang punya
Docker dan menjangkau IP node, mis. VM tambahan atau laptop operator,
`K6_NETWORK=host API_HOST=<IP_node> loadtest/p2m2-scale.sh`.

## 8. kubectl dari laptop lain
```bash
scp <user>@<IP_node-a1>:/etc/rancher/k3s/k3s.yaml ~/.kube/config
sed -i 's/127.0.0.1/<IP_node-a1>/' ~/.kube/config
kubectl get nodes
```

## 9. Rencana B: VM antar-laptop tidak bisa saling menjangkau

Pasang Tailscale di setiap VM (akun gratis, satu tailnet untuk semua anggota):
```bash
curl -fsSL https://tailscale.com/install.sh | sh
sudo tailscale up            # login lewat URL yang muncul
tailscale ip -4              # IP 100.x.y.z
```
Lalu di bagian 4 pakai IP Tailscale untuk `--node-ip` dan `K3S_URL`, serta
`--flannel-iface tailscale0`. Jalan di jaringan mana pun, termasuk Wi-Fi kampus.

## 10. Pengujian yang butuh akses ke node

| Skenario | Cara |
|---|---|
| Node worker mati + data hilang (P1, P5) | VirtualBox → *Close → Power off* VM worker, atau skenario Tester Agent |
| Freeze node (P2 kriteria 5) | VirtualBox → *Pause* VM tempat leader berjalan > 15 s, lalu *Resume*; atau di VM: `sudo kill -STOP <pid>` / `-CONT` |
| Jam bergeser (P2 kriteria 4) | Di VM worker: `sudo timedatectl set-ntp false; sudo date -s '+45 seconds'`, kemudian `sudo date -s '-75 seconds'`; kembalikan dengan `sudo timedatectl set-ntp true` |
| Node baru | Buat VM baru (bagian 3–4), beri label shard/host yang kosong, lalu `pwsh scripts/p1-replace-member.ps1 ...` |

Mencari PID leader: `kubectl -n $NS get pod <leader> -o wide` (node), lalu di VM itu
`sudo crictl ps --name bnpb-aggregator` → `sudo crictl inspect <id> | grep '"pid"'`.

Tester Agent: lihat README bagian "Tester Agent". Skenario node dan jam **hanya** dijalankan di
cluster ini, jangan di k3d (node k3d berbagi kernel dan jam dengan Docker).

## 11. Troubleshooting

| Gejala | Penyebab & solusi |
|---|---|
| Node `NotReady`, pod tidak bisa saling menjangkau | `--flannel-iface` salah atau 8472/udp diblokir. Cek `ip a`, pasang ulang agent dengan interface yang benar, atau pakai Tailscale |
| Pod Mongo `Pending` | Label `bnpb.io/physical-host`/`mongo-shard` belum lengkap: `kubectl describe pod <pod>` |
| Kafka/consumer/satellite/coastal `Pending` | Node control-plane tidak berlabel `node-role.kubernetes.io/control-plane=true` atau kehabisan RAM |
| `ErrImagePull` | Package GHCR belum public atau tag di `k8s/overlays/demo/kustomization.yaml` belum di-push (`scripts/push-images.sh`) |
| Pod lama di `ContainerCreating` / `Init:0/1`, event `Pulling image "mongo:7"` | Node baru menarik image besar dari Docker Hub (Mongo ±800 MB) dan bisa lambat/macet. Tarik dulu di setiap VM sebelum deploy: `sudo k3s ctr images pull docker.io/library/mongo:7 && sudo k3s ctr images pull docker.io/apache/kafka:3.9.0`, lalu hapus pod yang tertahan |
| Aggregator restart terus | Lihat `kubectl -n $NS logs deploy/bnpb-aggregator --previous`; biasanya kunci `.env` kosong |
| Jam antar-VM berbeda jauh | `timedatectl` → pastikan NTP aktif (kecuali saat uji clock skew) |

Reset node: `/usr/local/bin/k3s-uninstall.sh` (server) atau `/usr/local/bin/k3s-agent-uninstall.sh`
(agent), lalu ulangi bagian 4.
