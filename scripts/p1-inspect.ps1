param(
    [string]$Namespace = "sistem-informasi-kebencanaan-bnpb"
)

function Invoke-Checked {
    param([scriptblock]$Command, [string]$Description)
    & $Command
    if ($LASTEXITCODE -ne 0) {
        throw "$Description failed with exit code $LASTEXITCODE"
    }
}

Write-Output "=== Kubernetes nodes and P1 placement ==="
Invoke-Checked { kubectl get nodes -L bnpb.io/physical-host,bnpb.io/mongo-shard } "get nodes"
Invoke-Checked { kubectl -n $Namespace get pods -l role=store -o wide } "get store pods"

$router = kubectl -n $Namespace get pods -l app=mongo-router -o jsonpath='{.items[0].metadata.name}'
if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($router)) {
    throw "no mongo-router pod is available"
}

$distribution = @'
const dbName = "bnpb";
const collectionName = "hazard_events";
print("--- sharded collection distribution ---");
db.getSiblingDB(dbName)[collectionName].getShardDistribution();
print("--- chunks per shard ---");
printjson(db.getSiblingDB("config").chunks.aggregate([
  {$group: {_id: "$shard", chunks: {$sum: 1}}},
  {$sort: {_id: 1}}
]).toArray());
print("--- hazard counts ---");
printjson(db.getSiblingDB(dbName)[collectionName].aggregate([
  {$group: {_id: "$hazard_type", count: {$sum: 1}}},
  {$sort: {_id: 1}}
]).toArray());
print("--- balancer ---");
printjson(db.adminCommand({balancerStatus: 1}));
'@

Write-Output "=== Canonical Store distribution ==="
Invoke-Checked {
    kubectl -n $Namespace exec $router -- sh -ceu `
        'mongosh --quiet -u "$MONGO_ROOT_USER" -p "$MONGO_ROOT_PASSWORD" --authenticationDatabase admin --eval "$1"' `
        sh $distribution
} "read sharded distribution"

Write-Output "=== Replica roles and operation counters per data-bearing pod ==="
$podsRaw = kubectl -n $Namespace get pods -l storage-unit -o jsonpath='{.items[*].metadata.name}'
if ($LASTEXITCODE -ne 0) {
    throw "list data-bearing pods failed"
}
$pods = $podsRaw -split '\s+' | Where-Object { -not [string]::IsNullOrWhiteSpace($_) }
$memberStatus = @'
const hello = db.hello();
const server = db.serverStatus();
printjson({
  host: hello.me,
  role: hello.isWritablePrimary ? "primary" : (hello.secondary ? "replica" : "other"),
  client_operations: server.opcounters,
  replication_operations: server.opcountersRepl
});
'@
foreach ($pod in $pods) {
    Write-Output "--- $pod ---"
    Invoke-Checked {
        kubectl -n $Namespace exec $pod -- sh -ceu `
            'mongosh --quiet -u "$MONGO_ROOT_USER" -p "$MONGO_ROOT_PASSWORD" --authenticationDatabase admin --eval "$1"' `
            sh $memberStatus
    } "read member status for $pod"
}

Write-Output "=== CPU and memory snapshot (requires Metrics Server) ==="
kubectl -n $Namespace top pod -l role=store --containers
if ($LASTEXITCODE -ne 0) {
    Write-Warning "kubectl top is unavailable; install or wait for Metrics Server before collecting load evidence"
}
