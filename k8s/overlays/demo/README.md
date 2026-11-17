# P1 demo cluster

This overlay replaces the development singleton with two MongoDB shards. Each shard is a
three-member replica set; the config server and `mongos` router also have three members/instances.
It requires six separate-kernel Kubernetes worker nodes on three physical hosts. Apply the labels
below after replacing the example node names with `kubectl get nodes` output:

```powershell
kubectl label node node-a bnpb.io/physical-host=host-a bnpb.io/mongo-shard=shard-0
kubectl label node node-b bnpb.io/physical-host=host-a bnpb.io/mongo-shard=shard-1
kubectl label node node-c bnpb.io/physical-host=host-b bnpb.io/mongo-shard=shard-0
kubectl label node node-d bnpb.io/physical-host=host-b bnpb.io/mongo-shard=shard-1
kubectl label node node-e bnpb.io/physical-host=host-c bnpb.io/mongo-shard=shard-0
kubectl label node node-f bnpb.io/physical-host=host-c bnpb.io/mongo-shard=shard-1
```

Deploy with:

```powershell
$env:OVERLAY = "demo"
bash scripts/deploy.sh
```

Every shard has one replica on each physical host. Do not reuse a physical-host label for VMs
on different laptops: the label is the failure domain used by hard anti-affinity.

Before deployment, `.env` must contain `AUDIT_TOKEN` and one shared `MONGO_CLUSTER_KEY`. Generate
the key once and copy the same value to every host; never generate a different key per node:

```powershell
openssl rand -base64 756
```

Validate placement and bootstrap status:

```powershell
kubectl -n sistem-informasi-kebencanaan-bnpb get pods -o wide
kubectl -n sistem-informasi-kebencanaan-bnpb get job mongo-bootstrap-v1
kubectl -n sistem-informasi-kebencanaan-bnpb logs job/mongo-bootstrap-v1
```

The initial chunks are split at `HZ-8`: values below the boundary start on `shard-0`, and values
at or above it start on `shard-1`. MongoDB's balancer remains enabled so later chunks can move
without stopping reads or writes.
