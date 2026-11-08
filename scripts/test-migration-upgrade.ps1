$ErrorActionPreference = 'Stop'
Set-StrictMode -Version 3.0
$repo = Split-Path -Parent $PSScriptRoot
$fixtureRoot = Join-Path ([IO.Path]::GetTempPath()) ('edugrade-upgrade-test-' + [guid]::NewGuid().ToString('N'))
$fixtureRoot = [IO.Path]::GetFullPath($fixtureRoot)
$fixtureScripts = Join-Path $fixtureRoot 'scripts'
$upgradeState = @{ Calls = [Collections.Generic.List[string]]::new(); Failure = '' }

# 运行真实升级脚本，只有 Docker 和 TLS/配置预检替换为隔离桩；不会连接本机部署。
function docker {
  $command = $args -join ' '
  $upgradeState.Calls.Add($command)
  $global:LASTEXITCODE = 0
  if ($command -match 'ps --services --status running') { return @('api-gateway', 'page-processing-worker') }
  if ($command -match 'ps -q ') { return 'synthetic-container' }
  if ($command -match '^inspect ') { return 'running|healthy' }
  if ($upgradeState.Failure -and $command.Contains($upgradeState.Failure)) { $global:LASTEXITCODE = 1 }
}

function Invoke-UpgradeCase([string]$Failure) {
  $upgradeState.Calls.Clear()
  $upgradeState.Failure = $Failure
  $caught = $false
  try { & (Join-Path $fixtureScripts 'init.ps1') -SkipSmoke }
  catch { $caught = $true }
  if ($caught -ne [bool]$Failure) { throw "Unexpected upgrade result for failure '$Failure'." }
  return @($upgradeState.Calls)
}

try {
  New-Item -ItemType Directory -Path $fixtureScripts -Force | Out-Null
  Copy-Item -LiteralPath (Join-Path $repo 'infra/docker-compose/scripts/init.ps1') -Destination (Join-Path $fixtureScripts 'init.ps1')
  foreach ($name in @('prepare-internal-tls.ps1', 'preflight.ps1')) {
    [IO.File]::WriteAllText((Join-Path $fixtureScripts $name), 'param($EnvFile, $ComposeDirectory, $ComposeFile)')
  }
  [IO.File]::WriteAllText((Join-Path $fixtureRoot '.env'), "EDUGRADE_ENV=test`n")
  [IO.File]::WriteAllText((Join-Path $fixtureRoot 'docker-compose.yml'), 'services: {}')
  $calls = @(Invoke-UpgradeCase '')
  $build = [Array]::FindIndex($calls, [Predicate[object]]{ param($line) $line -match ' build ' })
  $stop = [Array]::FindIndex($calls, [Predicate[object]]{ param($line) $line -match ' stop ' })
  $migration = [Array]::FindIndex($calls, [Predicate[object]]{ param($line) $line -match 'run --rm db-migrate' })
  $app = [Array]::FindIndex($calls, [Predicate[object]]{ param($line) $line -match 'up -d --no-build grading-agent' })
  if (-not ($build -ge 0 -and $build -lt $stop -and $stop -lt $migration -and $migration -lt $app)) { throw 'Upgrade must build, stop, migrate, then start the matching release.' }
  if (-not ($calls -match 'stop api-gateway grading-agent ocr-worker image-quality-worker page-processing-worker math-recognition-worker paper-formula-worker subjective-grading-worker')) { throw 'All application writers must stop before migration.' }
  if (-not ($calls -match 'up -d --no-build page-processing-worker')) { throw 'Previously running optional worker was not restored.' }
  foreach ($failure in @(' build ', ' stop ', 'run --rm db-migrate')) {
    $calls = @(Invoke-UpgradeCase $failure)
    if ($failure -ne 'run --rm db-migrate' -and ($calls -match 'run --rm db-migrate')) { throw "Migration executed after '$failure' failed." }
    if ($calls -match 'up -d --no-build grading-agent') { throw "Applications restarted after '$failure' failed." }
  }
  Write-Host 'Upgrade regressions passed: build before downtime, stop all writers, restore prior worker, and fail closed without restarting old images.'
} finally {
  $expectedParent = [IO.Path]::GetFullPath([IO.Path]::GetTempPath())
  if (-not $fixtureRoot.StartsWith($expectedParent, [StringComparison]::OrdinalIgnoreCase) -or (Split-Path $fixtureRoot -Leaf) -notlike 'edugrade-upgrade-test-*') { throw 'Refusing to clean an unexpected test path.' }
  Remove-Item -LiteralPath $fixtureRoot -Recurse -Force -ErrorAction SilentlyContinue
}

# 最后一个用例故意让 Docker 桩失败。断言与清理通过后，向 CI 返回本脚本的结果。
exit 0
