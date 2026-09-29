param(
  [string]$ComposeFile = "..\docker-compose.yml",
  [string]$EnvFile = "..\.env",
  [switch]$SkipBuild,
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
try {
  & (Join-Path $scriptRoot "preflight.ps1") -ComposeFile $composePath -EnvFile $envPath

  Invoke-Compose -Arguments @("up", "-d", "postgres", "redis", "minio", "qdrant")
  foreach ($service in @("postgres", "redis", "minio", "qdrant")) { Wait-ComposeService $service }

  # 先完成迁移和对象桶初始化再启动应用，防止服务连接到尚未准备好的存储结构。
  $migrationArgs = @("--profile", "tools", "run", "--rm")
  if ($MigrationBaselineVersion) { $migrationArgs += @("-e", "EDUGRADE_MIGRATION_BASELINE_VERSION=$MigrationBaselineVersion") }
  $migrationArgs += "db-migrate"
  Invoke-Compose -Arguments $migrationArgs
  Invoke-Compose -Arguments @("--profile", "tools", "run", "--rm", "minio-init")

  $appArgs = @("up", "-d")
  if (-not $SkipBuild) { $appArgs += "--build" }
  $appArgs += @("grading-agent", "api-gateway", "web-admin", "nginx")
  Invoke-Compose -Arguments $appArgs
  foreach ($service in @("grading-agent", "api-gateway", "web-admin", "nginx")) { Wait-ComposeService $service 300 }

  if ($BootstrapAdmin) {
    if ([string]::IsNullOrWhiteSpace($env:EDUGRADE_BOOTSTRAP_PASSWORD)) {
      throw "Set EDUGRADE_BOOTSTRAP_PASSWORD in the current shell before using -BootstrapAdmin."
    }
    Invoke-Compose -Arguments @("run", "--rm", "-e", "EDUGRADE_BOOTSTRAP_PASSWORD", "api-gateway", "bootstrap-admin")
  }

  if ($EnableOcr) {
    Assert-WorkerCredentials "OCR worker" "EDUGRADE_OCR_WORKER"
    $workerArgs = @("--profile", "ocr", "up", "-d")
    if (-not $SkipBuild) { $workerArgs += "--build" }
    $workerArgs += "ocr-worker"
    Invoke-Compose -Arguments $workerArgs
    Wait-ComposeService "ocr-worker"
  }
  if ($EnableQuality) {
    Assert-WorkerCredentials "image-quality worker" "EDUGRADE_IMAGE_QUALITY"
    $workerArgs = @("--profile", "quality", "up", "-d")
    if (-not $SkipBuild) { $workerArgs += "--build" }
    $workerArgs += "image-quality-worker"
    Invoke-Compose -Arguments $workerArgs
    Wait-ComposeService "image-quality-worker"
  }
  if ($EnableProcessing) {
    Assert-WorkerCredentials "page-processing worker" "EDUGRADE_PAGE_PROCESSING"
    $workerArgs = @("--profile", "processing", "up", "-d")
    if (-not $SkipBuild) { $workerArgs += "--build" }
    $workerArgs += "page-processing-worker"
    Invoke-Compose -Arguments $workerArgs
    Wait-ComposeService "page-processing-worker"
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
} finally {
  Pop-Location
}
