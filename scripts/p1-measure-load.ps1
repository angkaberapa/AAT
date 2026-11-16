param(
    [string]$Namespace = "sistem-informasi-kebencanaan-bnpb",
    [ValidateRange(10, 3600)][int]$DurationSeconds = 120,
    [ValidateRange(2, 60)][int]$SampleEverySeconds = 5,
    [string]$OutputDirectory = "evidence/p1-metrics"
)

$ErrorActionPreference = "Stop"
$startedAt = Get-Date
$deadline = $startedAt.AddSeconds($DurationSeconds)
$samples = [System.Collections.Generic.List[object]]::new()
New-Item -ItemType Directory -Force -Path $OutputDirectory | Out-Null

$mongoScript = @'
const hello = db.hello();
const status = db.serverStatus();
print(JSON.stringify({
  role: hello.isWritablePrimary ? "primary" : (hello.secondary ? "replica" : "other"),
  insert: status.opcounters.insert,
  query: status.opcounters.query,
  update: status.opcounters.update,
  delete: status.opcounters.delete,
  getmore: status.opcounters.getmore,
  command: status.opcounters.command,
  repl_insert: status.opcountersRepl.insert,
  repl_update: status.opcountersRepl.update,
  repl_delete: status.opcountersRepl.delete
}));
'@

Write-Output "Sampling every $SampleEverySeconds seconds for $DurationSeconds seconds. Run the catalogue loader or burst tool in another terminal now."
do {
    $podList = kubectl -n $Namespace get pods -l storage-unit -o json | ConvertFrom-Json
    if ($LASTEXITCODE -ne 0) { throw "unable to list data-bearing pods" }
    foreach ($pod in $podList.items) {
        $name = $pod.metadata.name
        $raw = kubectl -n $Namespace exec $name -- sh -ceu `
            'mongosh --quiet -u "$MONGO_ROOT_USER" -p "$MONGO_ROOT_PASSWORD" --authenticationDatabase admin --eval "$1"' `
            sh $mongoScript
        if ($LASTEXITCODE -ne 0) {
            Write-Warning "Cannot sample MongoDB counters from $name"
            continue
        }
        $counter = $raw | Select-Object -Last 1 | ConvertFrom-Json
        $top = kubectl -n $Namespace top pod $name --no-headers 2>$null
        $cpu = "unavailable"
        $memory = "unavailable"
        if ($LASTEXITCODE -eq 0 -and $top) {
            $columns = $top -split '\s+'
            if ($columns.Count -ge 3) { $cpu, $memory = $columns[1], $columns[2] }
        }
        $samples.Add([pscustomobject]@{
            timestamp = (Get-Date).ToUniversalTime().ToString("o")
            pod = $name
            node = $pod.spec.nodeName
            storage_unit = $pod.metadata.labels.'storage-unit'
            role = $counter.role
            cpu = $cpu
            memory = $memory
            insert = $counter.insert
            query = $counter.query
            update = $counter.update
            delete = $counter.delete
            getmore = $counter.getmore
            command = $counter.command
            repl_insert = $counter.repl_insert
            repl_update = $counter.repl_update
            repl_delete = $counter.repl_delete
        })
    }
    if ((Get-Date) -lt $deadline) { Start-Sleep -Seconds $SampleEverySeconds }
} while ((Get-Date) -lt $deadline)

$stamp = $startedAt.ToUniversalTime().ToString("yyyyMMddTHHmmssZ")
$samplesPath = Join-Path $OutputDirectory "samples-$stamp.csv"
$deltaPath = Join-Path $OutputDirectory "deltas-$stamp.csv"
$samples | Export-Csv -NoTypeInformation -Encoding utf8 -Path $samplesPath

$deltas = foreach ($group in ($samples | Group-Object pod)) {
    $ordered = @($group.Group | Sort-Object timestamp)
    if ($ordered.Count -lt 2) { continue }
    $first, $last = $ordered[0], $ordered[-1]
    [pscustomobject]@{
        pod = $group.Name
        node = $last.node
        storage_unit = $last.storage_unit
        final_role = $last.role
        insert_delta = [int64]$last.insert - [int64]$first.insert
        query_delta = [int64]$last.query - [int64]$first.query
        update_delta = [int64]$last.update - [int64]$first.update
        delete_delta = [int64]$last.delete - [int64]$first.delete
        command_delta = [int64]$last.command - [int64]$first.command
        repl_insert_delta = [int64]$last.repl_insert - [int64]$first.repl_insert
        repl_update_delta = [int64]$last.repl_update - [int64]$first.repl_update
        repl_delete_delta = [int64]$last.repl_delete - [int64]$first.repl_delete
    }
}
$deltas | Export-Csv -NoTypeInformation -Encoding utf8 -Path $deltaPath

Write-Output "Raw samples: $samplesPath"
Write-Output "Per-node operation deltas: $deltaPath"
$deltas | Format-Table -AutoSize
