param(
  [string]$ComposeFile = "..\docker-compose.yml",
  [string]$EnvFile = "..\.env",
  [switch]$SkipBuild,
  [switch]$BuildWorkers,
  [switch]$SkipSmoke,
  [switch]$BootstrapAdmin,
  [ValidatePattern('^000020$')][string]$MigrationBaselineVersion,
  [switch]$EnableOcr,
  [switch]$EnableQuality,
  [switch]$EnableProcessing,
  [switch]$EnableObservability
)

$ErrorActionPreference = "Stop"
$scriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$composeCandidate = if ([IO.Path]::IsPathRooted($ComposeFile)) { $ComposeFile } else { Join-Path $scriptRoot $ComposeFile }
$envCandidate = if ([IO.Path]::IsPathRooted($EnvFile)) { $EnvFile } else { Join-Path $scriptRoot $EnvFile }
$composePath = (Resolve-Path -LiteralPath $composeCandidate).Path
$envPath = (Resolve-Path -LiteralPath $envCandidate).Path
$composeDir = Split-Path -Parent $composePath

function Read-EnvFile([string]$Path) {
  $values = @{}
  foreach ($line in Get-Content -LiteralPath $Path -Encoding utf8) {
    $trimmed = $line.Trim()
    if ($trimmed -eq "" -or $trimmed.StartsWith("#")) { continue }
    $parts = $trimmed.Split("=", 2)
    if ($parts.Count -ne 2) { throw "Invalid env line for key parsing." }
    $values[$parts[0].Trim()] = $parts[1].Trim().Trim('"').Trim("'")
  }
  return $values
}

$deploymentSettings = Read-EnvFile $envPath
foreach ($entry in Get-ChildItem Env:) { if ($entry.Name.StartsWith('EDUGRADE_')) { $deploymentSettings[$entry.Name] = $entry.Value } }

function Assert-WorkerCredentials([string]$Name, [string]$Prefix) {
  foreach ($suffix in @("TENANT_CODE", "USERNAME", "PASSWORD")) {
    $key = "${Prefix}_${suffix}"
    if (-not $deploymentSettings.ContainsKey($key) -or [string]::IsNullOrWhiteSpace($deploymentSettings[$key])) {
      throw "$Name requires a non-empty $key in the deployment env file."
    }
  }
}

function Invoke-Compose {
  param([string[]]$Arguments)
  & docker compose --env-file $envPath -f $composePath @Arguments
  if ($LASTEXITCODE -ne 0) { throw "docker compose command failed: $($Arguments -join ' ')" }
}

function Wait-ComposeService([string]$Service, [int]$TimeoutSeconds = 180) {
  $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
  do {
    $containerId = (& docker compose --env-file $envPath -f $composePath ps -q $Service).Trim()
    if ($containerId) {
      $state = docker inspect --format '{{.State.Status}}|{{if .State.Health}}{{.State.Health.Status}}{{end}}' $containerId
      if ($LASTEXITCODE -eq 0) {
        $parts = $state.Trim().Split('|', 2)
        if ($parts[0] -eq 'running' -and ($parts.Count -eq 1 -or $parts[1] -eq '' -or $parts[1] -eq 'healthy')) {
          Write-Host "$Service ready."
          return
        }
        if ($parts[0] -eq 'exited' -or $parts[1] -eq 'unhealthy') { throw "$Service failed readiness with state '$state'." }
      }
    }
    Start-Sleep -Seconds 3
  } while ((Get-Date) -lt $deadline)
  throw "Timed out waiting for $Service."
}

Push-Location $composeDir
$applicationsStoppedForMigration = $false
try {
  & (Join-Path $scriptRoot "prepare-internal-tls.ps1") -EnvFile $envPath -ComposeDirectory $composeDir
  & (Join-Path $scriptRoot "preflight.ps1") -ComposeFile $composePath -EnvFile $envPath

  Invoke-Compose -Arguments @("up", "-d", "postgres", "redis", "minio", "qdrant")
  foreach ($service in @("postgres", "redis", "minio", "qdrant")) { Wait-ComposeService $service }

  $workerServices = @('ocr-worker', 'image-quality-worker', 'page-processing-worker', 'math-recognition-worker', 'paper-formula-worker', 'subjective-grading-worker')
  $runningServices = @(& docker compose --env-file $envPath -f $composePath --profile '*' ps --services --status running)
  if ($LASTEXITCODE -ne 0) { throw 'Cannot determine running services before migration; no migration was started.' }
  $previousWorkers = @($runningServices | Where-Object { $_ -in $workerServices })
  $buildServices = @('grading-agent', 'api-gateway', 'web-admin') + $previousWorkers
  if ($EnableOcr) { $buildServices += 'ocr-worker' }
  if ($EnableQuality) { $buildServices += 'image-quality-worker' }
  if ($EnableProcessing) { $buildServices += 'page-processing-worker' }
  $buildServices = @($buildServices | Select-Object -Unique)
  # 构建失败时旧服务仍可用；停机后不再构建，缩短结构迁移期间的不可用窗口。
  if (-not $SkipBuild) { Invoke-Compose -Arguments (@('--profile', '*', 'build') + $buildServices) }
  elseif ($BuildWorkers) {
    $buildWorkersOnly = @($buildServices | Where-Object { $_ -in $workerServices })
    if ($buildWorkersOnly.Count) { Invoke-Compose -Arguments (@('--profile', '*', 'build') + $buildWorkersOnly) }
  }
  # 删列迁移不能与旧网关、worker 或后台作业并行；停止失败时禁止进入迁移。
  Invoke-Compose -Arguments (@('--profile', '*', 'stop', 'api-gateway', 'grading-agent') + $workerServices)
  $applicationsStoppedForMigration = $true
  $migrationArgs = @("--profile", "tools", "run", "--rm")
  if ($MigrationBaselineVersion) { $migrationArgs += @("-e", "EDUGRADE_MIGRATION_BASELINE_VERSION=$MigrationBaselineVersion") }
  $migrationArgs += "db-migrate"
  Invoke-Compose -Arguments $migrationArgs
  Invoke-Compose -Arguments @("--profile", "tools", "run", "--rm", "minio-init")

  $appArgs = @("up", "-d", "--no-build")
  $appArgs += @("grading-agent", "api-gateway", "web-admin", "nginx")
  Invoke-Compose -Arguments $appArgs
  foreach ($service in @("grading-agent", "api-gateway", "web-admin", "nginx")) { Wait-ComposeService $service 300 }

  if ($BootstrapAdmin) {
    if ([string]::IsNullOrWhiteSpace($env:EDUGRADE_BOOTSTRAP_PASSWORD)) {
      throw "Set EDUGRADE_BOOTSTRAP_PASSWORD in the current shell before using -BootstrapAdmin."
    }
    Invoke-Compose -Arguments @("run", "--rm", "-e", "EDUGRADE_BOOTSTRAP_PASSWORD", "api-gateway", "bootstrap-admin")
  }

  $requestedWorkers = @()
  if ($EnableOcr) { $requestedWorkers += ,@('ocr','EDUGRADE_OCR_WORKER') }
  if ($EnableQuality) { $requestedWorkers += ,@('quality','EDUGRADE_IMAGE_QUALITY') }
  if ($EnableProcessing) { $requestedWorkers += ,@('processing','EDUGRADE_PAGE_PROCESSING') }
  if ($requestedWorkers.Count) {
    foreach ($worker in $requestedWorkers) { Assert-WorkerCredentials $worker[0] $worker[1] }
    if ($deploymentSettings['EDUGRADE_ENV'] -in @('local','development','dev','test')) {
    # 凭据只通过子进程环境继承，不放 Docker 命令行，也不覆盖已有账号密码。正式环境仍由管理员管理账号。
    $priorWorkerEnvironment = @{}
    $workerEnvironment = @{ EDUGRADE_PROVISION_WORKERS = (($requestedWorkers | ForEach-Object { $_[0] }) -join ',') }
    foreach ($worker in $requestedWorkers) { foreach ($suffix in @('TENANT_CODE','USERNAME','PASSWORD')) { $key = "$($worker[1])_$suffix"; $workerEnvironment[$key] = $deploymentSettings[$key] } }
    $provisionArgs = @('run','--rm','--no-deps')
    try {
      foreach ($key in $workerEnvironment.Keys) { $priorWorkerEnvironment[$key] = [Environment]::GetEnvironmentVariable($key,'Process'); [Environment]::SetEnvironmentVariable($key,$workerEnvironment[$key],'Process'); $provisionArgs += @('-e',$key) }
      Invoke-Compose -Arguments ($provisionArgs + @('api-gateway','provision-local-workers'))
    } finally { foreach ($key in $priorWorkerEnvironment.Keys) { [Environment]::SetEnvironmentVariable($key,$priorWorkerEnvironment[$key],'Process') } }
    $workerApiPort = if ($deploymentSettings['EDUGRADE_API_PORT']) { $deploymentSettings['EDUGRADE_API_PORT'] } else { '8080' }
    foreach ($worker in $requestedWorkers) {
      $prefix = $worker[1]
      $payload = @{ tenant_code = $deploymentSettings["${prefix}_TENANT_CODE"]; username = $deploymentSettings["${prefix}_USERNAME"]; password = $deploymentSettings["${prefix}_PASSWORD"]; client_type = 'service'; device_name = 'Local deployment connection check' } | ConvertTo-Json -Compress
      try {
        $authentication = Invoke-RestMethod -Method Post -Uri "http://127.0.0.1:$workerApiPort/api/v1/auth/token" -ContentType 'application/json' -Body $payload -TimeoutSec 20
        if (-not $authentication.access_token -or @($authentication.user.roles).Count -ne 1 -or $authentication.user.roles[0] -ne 'page_processing_worker') { throw 'Restricted service identity was not verified.' }
        Invoke-RestMethod -Method Post -Uri "http://127.0.0.1:$workerApiPort/api/v1/auth/logout" -Headers @{ Authorization = "Bearer $($authentication.access_token)" } -ContentType 'application/json' -Body '{}' -TimeoutSec 20 | Out-Null
      } catch { throw "$($worker[0]) service login verification failed; worker was not started. Existing account credentials were preserved." }
      finally { $payload = $null; $authentication = $null }
    }
    }
  }

  if ($EnableOcr) {
    Assert-WorkerCredentials "OCR worker" "EDUGRADE_OCR_WORKER"
    $workerArgs = @("--profile", "ocr", "up", "-d")
    $workerArgs += "--no-build"
    $workerArgs += "ocr-worker"
    Invoke-Compose -Arguments $workerArgs
    Wait-ComposeService "ocr-worker"
  }
  if ($EnableQuality) {
    Assert-WorkerCredentials "image-quality worker" "EDUGRADE_IMAGE_QUALITY"
    $workerArgs = @("--profile", "quality", "up", "-d")
    $workerArgs += "--no-build"
    $workerArgs += "image-quality-worker"
    Invoke-Compose -Arguments $workerArgs
    Wait-ComposeService "image-quality-worker"
  }
  if ($EnableProcessing) {
    Assert-WorkerCredentials "page-processing worker" "EDUGRADE_PAGE_PROCESSING"
    $workerArgs = @("--profile", "processing", "up", "-d")
    $workerArgs += "--no-build"
    $workerArgs += "page-processing-worker"
    Invoke-Compose -Arguments $workerArgs
    Wait-ComposeService "page-processing-worker"
  }
  # 未重传开关的既有 worker 也恢复到本次镜像，不能迁移成功后一直留在停止状态。
  if ($previousWorkers.Count) {
    Invoke-Compose -Arguments (@('--profile', '*', 'up', '-d', '--no-build') + $previousWorkers)
    foreach ($service in $previousWorkers) { Wait-ComposeService $service }
  }
  if ($EnableObservability) {
    Invoke-Compose -Arguments @("--profile", "observability", "up", "-d", "prometheus", "grafana")
    foreach ($service in @("prometheus", "grafana")) { Wait-ComposeService $service }
  }

  if (-not $SkipSmoke) {
    $apiPort = if ([string]::IsNullOrWhiteSpace($deploymentSettings["EDUGRADE_API_PORT"])) { "8080" } else { $deploymentSettings["EDUGRADE_API_PORT"] }
    $nginxPort = if ([string]::IsNullOrWhiteSpace($deploymentSettings["EDUGRADE_NGINX_HTTP_PORT"])) { "8088" } else { $deploymentSettings["EDUGRADE_NGINX_HTTP_PORT"] }
    $cookieName = if ([string]::IsNullOrWhiteSpace($deploymentSettings["EDUGRADE_SESSION_COOKIE_NAME"])) { "edugrade_session" } else { $deploymentSettings["EDUGRADE_SESSION_COOKIE_NAME"] }
    & (Join-Path $scriptRoot "smoke-test.ps1") `
      -ApiUrl "http://127.0.0.1:$apiPort" `
      -PublicUrl "http://127.0.0.1:$nginxPort" `
      -SessionCookieName $cookieName
  }
  Invoke-Compose -Arguments @("ps")
  Write-Host "EduGrade private deployment initialized."
} catch {
  if ($applicationsStoppedForMigration) {
    Write-Warning 'Upgrade failed after application services were stopped. Do not restart old images against a changed schema; correct the matching release and rerun, or restore the verified pre-upgrade backup before starting the old release.'
  }
  throw
} finally {
  Pop-Location
}
