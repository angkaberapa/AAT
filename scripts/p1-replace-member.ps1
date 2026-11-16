param(
    [Parameter(Mandatory = $true)]
    [ValidateSet("mongo-shard-0", "mongo-shard-1")]
    [string]$StatefulSet,

    [Parameter(Mandatory = $true)]
    [string]$ReplacementNode,

    [string]$Namespace = "sistem-informasi-kebencanaan-bnpb",

    [switch]$ConfirmDataLoss
)

if (-not $ConfirmDataLoss) {
    throw "This intentionally deletes one replica volume. Re-run with -ConfirmDataLoss after verifying the exact StatefulSet and replacement node."
}

function Invoke-Checked {
    param([scriptblock]$Command, [string]$Description)
    & $Command
    if ($LASTEXITCODE -ne 0) {
        throw "$Description failed with exit code $LASTEXITCODE"
    }
}

$expectedShard = $StatefulSet -replace '^mongo-', ''
$nodeShard = kubectl get node $ReplacementNode -o jsonpath='{.metadata.labels.bnpb\.io/mongo-shard}'
if ($LASTEXITCODE -ne 0) {
    throw "replacement node $ReplacementNode does not exist"
}
$physicalHost = kubectl get node $ReplacementNode -o jsonpath='{.metadata.labels.bnpb\.io/physical-host}'
if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($physicalHost)) {
    throw "replacement node $ReplacementNode has no bnpb.io/physical-host label"
}
if ($nodeShard -ne $expectedShard) {
    throw "replacement node $ReplacementNode is labelled for '$nodeShard', expected '$expectedShard'"
}

$pod = "$StatefulSet-2"
$claim = "data-$pod"
$oldNode = kubectl -n $Namespace get pod $pod -o jsonpath='{.spec.nodeName}' 2>$null
Write-Output "Replacing replica $pod (previous node: $oldNode, PVC: $claim) on node $ReplacementNode"

Invoke-Checked { kubectl -n $Namespace scale statefulset $StatefulSet --replicas=2 } "scale down $StatefulSet"
kubectl -n $Namespace get pod $pod *> $null
if ($LASTEXITCODE -eq 0) {
    Invoke-Checked { kubectl -n $Namespace wait --for=delete pod/$pod --timeout=180s } "wait for $pod deletion"
}
Invoke-Checked { kubectl -n $Namespace delete pvc $claim --wait=true } "delete empty/lost replica PVC $claim"
Invoke-Checked { kubectl -n $Namespace scale statefulset $StatefulSet --replicas=3 } "scale up $StatefulSet"
Invoke-Checked { kubectl -n $Namespace wait --for=condition=Ready pod/$pod --timeout=900s } "wait for initial sync of $pod"

$actualNode = kubectl -n $Namespace get pod $pod -o jsonpath='{.spec.nodeName}'
if ($LASTEXITCODE -ne 0) {
    throw "cannot read replacement placement"
}
if ($actualNode -ne $ReplacementNode) {
    throw "replica became Ready on '$actualNode', not requested node '$ReplacementNode'; review node labels and affinity"
}

$replicaStatus = @'
const status = rs.status();
printjson({
  set: status.set,
  members: status.members.map(member => ({name: member.name, state: member.stateStr, health: member.health}))
});
if (status.members.length !== 3 || status.members.some(member => member.health !== 1)) quit(1);
'@
Invoke-Checked {
    kubectl -n $Namespace exec $pod -- sh -ceu `
        'mongosh --quiet -u "$MONGO_ROOT_USER" -p "$MONGO_ROOT_PASSWORD" --authenticationDatabase admin --eval "$1"' `
        sh $replicaStatus
} "verify restored replica set"

Write-Output "Replacement complete. Capture a new Audit API checksum before testing the next node loss."
