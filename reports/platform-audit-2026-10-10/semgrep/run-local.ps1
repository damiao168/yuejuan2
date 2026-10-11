# 重新运行时读取当前本地工作区；只用本地 Git 清单，不拉取远端。
# ZIP 放在临时目录，真实扫描容器禁网、只读挂载副本；结果只写本目录。
$ErrorActionPreference = 'Stop'
$semgrepRepository = (Resolve-Path (Join-Path $PSScriptRoot '../../..')).Path
Push-Location $semgrepRepository
try {
    python (Join-Path $PSScriptRoot 'prepare_snapshot.py')
    if ($LASTEXITCODE -ne 0) { throw '本地快照生成失败。' }
    $semgrepSnapshot = Join-Path $env:TEMP 'yuejuan2-semgrep-snapshot-20261011'
    docker run --rm --name yuejuan2-semgrep-local-rescan --network none --cpus 2 --memory 2g `
        -e SEMGREP_SEND_METRICS=off -e SEMGREP_ENABLE_VERSION_CHECK=0 -e SEMGREP_APP_TOKEN= `
        --mount "type=bind,source=$semgrepSnapshot,target=/snapshot,readonly" `
        --mount "type=bind,source=$PSScriptRoot,target=/audit" `
        --entrypoint python `
        semgrep/semgrep@sha256:93963d9295a366f59e4850127b1550400ee7b388f04fe144e4a1f6325d96e01b `
        /audit/local_scan.py
    if ($LASTEXITCODE -ne 0) { throw 'Semgrep 执行失败，查看 scan.stderr.log 和扫描元数据。' }
    python (Join-Path $PSScriptRoot 'summarize_results.py')
    if ($LASTEXITCODE -ne 0) { throw '扫描结果汇总失败。' }
} finally {
    Pop-Location
}
