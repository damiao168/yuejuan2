[CmdletBinding()]
param(
  [string]$ProjectName
)

$ErrorActionPreference = "Stop"

# 这个运行器保持独立，不引用常规部署的 Compose 文件、
# 固定容器名或数据卷。
# 它只测试 OMR 页面处理工作进程；临时测试数据
# 有意模拟已经完成的采集和登记边界。
$scriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$composePath = Join-Path (Split-Path -Parent $scriptRoot) "docker-compose.story056-e2e.yml"
if (-not (Test-Path -LiteralPath $composePath)) {
  throw "STORY-056 E2E Compose file was not found: $composePath"
}

if ([string]::IsNullOrWhiteSpace($ProjectName)) {
  $ProjectName = "edugrade-story056-e2e-$PID-$([Guid]::NewGuid().ToString('N').Substring(0, 8))"
}
if ($ProjectName -notmatch '^edugrade-story056-e2e-[a-z0-9-]+$') {
  throw "ProjectName must match ^edugrade-story056-e2e-[a-z0-9-]+$ to protect unrelated Docker resources."
}

function Get-DockerProjectResources {
  param([Parameter(Mandatory = $true)][string]$Name)

  $label = "label=com.docker.compose.project=$Name"
  $commands = @(
    @{ Kind = "container"; Args = @("ps", "-a", "--filter", $label, "--format", "{{.ID}}") },
    @{ Kind = "volume"; Args = @("volume", "ls", "--filter", $label, "--format", "{{.Name}}") },
    @{ Kind = "network"; Args = @("network", "ls", "--filter", $label, "--format", "{{.ID}}") }
  )
  $resources = @()
  foreach ($command in $commands) {
    $items = @(& docker @($command.Args))
    $exitCode = $LASTEXITCODE
    if ($exitCode -ne 0) {
      throw "Unable to inspect existing Docker $($command.Kind) resources for project '$Name' (exit $exitCode)."
    }
    foreach ($item in $items) {
      $item = [string]$item
      if (-not [string]::IsNullOrWhiteSpace($item)) {
        $resources += "$($command.Kind):$($item.Trim())"
      }
    }
  }
  return @($resources)
}

# 只有项目名尚未对应任何 Docker 资源时，传入的项目名才安全。
# 这样可防止复用匹配的前缀，让 `down --volumes`
# 误删先前运行的容器、网络或数据卷。
$existingResources = @(Get-DockerProjectResources -Name $ProjectName)
if ($existingResources.Count -gt 0) {
  throw "Refusing to reuse Docker Compose project '$ProjectName'; existing resources were found: $($existingResources -join ', ')"
}
Write-Host "Verified Docker Compose project $ProjectName has no pre-existing resources."

$createdAdminPassword = [string]::IsNullOrWhiteSpace($env:EDUGRADE_E2E_ADMIN_PASSWORD)
$createdWorkerPassword = [string]::IsNullOrWhiteSpace($env:EDUGRADE_E2E_WORKER_PASSWORD)
$createdUnprivilegedPassword = [string]::IsNullOrWhiteSpace($env:EDUGRADE_E2E_UNPRIVILEGED_PASSWORD)
$createdPostgresPassword = [string]::IsNullOrWhiteSpace($env:EDUGRADE_STORY056_POSTGRES_PASSWORD)
$createdMinioSecret = [string]::IsNullOrWhiteSpace($env:EDUGRADE_STORY056_MINIO_SECRET)
if ($createdAdminPassword) {
  $env:EDUGRADE_E2E_ADMIN_PASSWORD = "S056e2e!A9$([Guid]::NewGuid().ToString('N'))"
}
if ($createdWorkerPassword) {
  $env:EDUGRADE_E2E_WORKER_PASSWORD = "S056e2e!W8$([Guid]::NewGuid().ToString('N'))"
}
if ($createdUnprivilegedPassword) {
  $env:EDUGRADE_E2E_UNPRIVILEGED_PASSWORD = "S056e2e!U7$([Guid]::NewGuid().ToString('N'))"
}
if ($createdPostgresPassword) {
  $env:EDUGRADE_STORY056_POSTGRES_PASSWORD = "S056db!P9$([Guid]::NewGuid().ToString('N'))"
}
if ($createdMinioSecret) {
  $env:EDUGRADE_STORY056_MINIO_SECRET = "S056minio!M6$([Guid]::NewGuid().ToString('N'))"
}

function Invoke-E2ECompose {
  param([Parameter(Mandatory = $true)][string[]]$ComposeArgs)
  & docker compose --project-name $ProjectName --profile e2e -f $composePath @ComposeArgs
  if ($LASTEXITCODE -ne 0) {
    throw "STORY-056 real-worker E2E compose command failed: $($ComposeArgs -join ' ')"
  }
}

function Wait-E2EService {
  param(
    [Parameter(Mandatory = $true)][string]$Service,
    [int]$TimeoutSeconds = 180
  )

  $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
  do {
    $containerId = (& docker compose --project-name $ProjectName --profile e2e -f $composePath ps -q $Service).Trim()
    if ($LASTEXITCODE -eq 0 -and $containerId) {
      $state = (& docker inspect --format '{{.State.Status}}|{{if .State.Health}}{{.State.Health.Status}}{{end}}' $containerId).Trim()
      if ($LASTEXITCODE -eq 0) {
        $parts = $state.Split('|', 2)
        $health = if ($parts.Count -gt 1) { $parts[1] } else { "" }
        if ($parts[0] -eq 'running' -and ($health -eq '' -or $health -eq 'healthy')) {
          Write-Host "$Service ready in isolated project $ProjectName."
          return
        }
        if ($parts[0] -eq 'exited' -or $health -eq 'unhealthy') {
          throw "$Service failed readiness in isolated project $ProjectName with state '$state'."
        }
      }
    }
    Start-Sleep -Seconds 2
  } while ((Get-Date) -lt $deadline)
  throw "Timed out waiting for $Service in isolated project $ProjectName."
}

$projectInvocationStarted = $false
$bodyFailure = $null
$cleanupFailure = $null
try {
  # 此值为真时，上面的预检表明只有本次调用
  # 可能在该项目标签下创建资源。
  $projectInvocationStarted = $true
  # Compose 参数保留在数组中。将 -d 直接传给 PowerShell 函数，
  # 会被 PowerShell 当作该函数的 -Debug 开关。
  Invoke-E2ECompose -ComposeArgs @("up", "-d", "postgres", "redis", "minio")
  foreach ($service in @('postgres', 'redis', 'minio')) { Wait-E2EService $service }

  # api-gateway 通过 depends_on 管理这些一次性依赖。
  # 单独运行它们会执行两次 bootstrap-admin，
  # 第二次会正确拒绝已经创建的管理员。
  Invoke-E2ECompose -ComposeArgs @("up", "-d", "--build", "api-gateway")
  Wait-E2EService api-gateway 300

  Invoke-E2ECompose -ComposeArgs @("run", "--rm", "--no-deps", "--build", "postgres-e2e-tests")
  Invoke-E2ECompose -ComposeArgs @("run", "--rm", "--no-deps", "--build", "e2e-runner", "--phase", "seed")
  Invoke-E2ECompose -ComposeArgs @("run", "--rm", "--no-deps", "--build", "page-processing-once")
  Invoke-E2ECompose -ComposeArgs @("run", "--rm", "--no-deps", "e2e-runner", "--phase", "verify")
  Write-Host "STORY-056 PostgreSQL and OMR page-processing Worker E2E passed in isolated project $ProjectName (capture/registration are deliberate direct-SQL fixture data)."
}
catch {
  $bodyFailure = $_
}
finally {
  # 首次执行 Compose 命令前，已校验项目名并确认没有已有资源；
  # 因此这里只能移除本次调用创建的资源。
  if ($projectInvocationStarted) {
    try {
      & docker compose --project-name $ProjectName --profile e2e -f $composePath down --volumes --remove-orphans
      $cleanupExitCode = $LASTEXITCODE
      if ($cleanupExitCode -ne 0) {
        $cleanupFailure = "STORY-056 E2E cleanup failed for verified project '$ProjectName' (exit $cleanupExitCode)."
      }
    }
    catch {
      $cleanupFailure = "STORY-056 E2E cleanup failed for verified project '$ProjectName': $($_.Exception.Message)"
    }
    if ($cleanupFailure) {
      [Console]::Error.WriteLine($cleanupFailure)
    }
  }
  if ($createdAdminPassword) { Remove-Item Env:EDUGRADE_E2E_ADMIN_PASSWORD -ErrorAction SilentlyContinue }
  if ($createdWorkerPassword) { Remove-Item Env:EDUGRADE_E2E_WORKER_PASSWORD -ErrorAction SilentlyContinue }
  if ($createdUnprivilegedPassword) { Remove-Item Env:EDUGRADE_E2E_UNPRIVILEGED_PASSWORD -ErrorAction SilentlyContinue }
  if ($createdPostgresPassword) { Remove-Item Env:EDUGRADE_STORY056_POSTGRES_PASSWORD -ErrorAction SilentlyContinue }
  if ($createdMinioSecret) { Remove-Item Env:EDUGRADE_STORY056_MINIO_SECRET -ErrorAction SilentlyContinue }
}

if ($bodyFailure -and $cleanupFailure) {
  throw "STORY-056 real-worker E2E failed: $($bodyFailure.Exception.Message) Cleanup also failed: $cleanupFailure"
}
if ($bodyFailure) {
  throw $bodyFailure
}
if ($cleanupFailure) {
  throw $cleanupFailure
}
