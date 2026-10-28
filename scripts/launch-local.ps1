[CmdletBinding()]
param(
    [switch]$Build,
    [switch]$SkipLocalModel,
    [ValidateSet('Auto', 'Manual', 'External', 'Local')][string]$ModelMode = 'Auto',
    [ValidateSet('Auto', 'Enabled', 'Disabled')][string]$ProcessingMode = 'Auto',
    [ValidateRange(1,3650)][int]$LocalCertificateDays = 365,
    [switch]$RotateLocalCertificate,
    [switch]$SkipBootstrap,
    [switch]$EnableOcr,
    [switch]$EnableQuality,
    [switch]$EnableProcessing,
    [switch]$EnableObservability,
    [switch]$NoBrowser,
    [switch]$DryRun
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version 3.0

$repositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$composeDirectory = Join-Path $repositoryRoot "infra\docker-compose"
$composeFile = Join-Path $composeDirectory "docker-compose.yml"
$envExampleFile = Join-Path $composeDirectory ".env.example"
$envFile = Join-Path $composeDirectory ".env"
$installMarker = Join-Path $composeDirectory ".env.local-install-complete"
$modelModeFile = Join-Path $composeDirectory ".env.local-model-mode"
$processingModeFile = Join-Path $composeDirectory '.env.local-processing-mode'
$buildVersionFile = Join-Path $composeDirectory '.env.local-build-version.json'
. (Join-Path $PSScriptRoot 'application-source-version.ps1')
. (Join-Path $PSScriptRoot 'local-processing-mode.ps1')
if ($ProcessingMode -eq 'Disabled' -and ($EnableOcr -or $EnableQuality -or $EnableProcessing)) {
    throw 'ProcessingMode Disabled conflicts with explicit Enable* processing switches.'
}
$initScript = Join-Path $composeDirectory "scripts\init.ps1"
$prepareTlsScript = Join-Path $composeDirectory "scripts\prepare-internal-tls.ps1"
$smokeScript = Join-Path $composeDirectory "scripts\smoke-test.ps1"
$syncModelKeyScript = Join-Path $composeDirectory "scripts\sync-local-grading-model-key.ps1"
$prepareModelScript = Join-Path $repositoryRoot "lab\scripts\prepare-local-runtime.ps1"
$startModelScript = Join-Path $repositoryRoot "lab\scripts\start-local-server.ps1"
$modelManifestFile = Join-Path $repositoryRoot "lab\config\local-runtime.json"

function Write-Step([string]$Message) {
    Write-Host ""
    Write-Host "==> $Message" -ForegroundColor Cyan
}

function Assert-File([string]$Path) {
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        throw "Required repository file is missing: $Path"
    }
}

function New-HexSecret([int]$Bytes = 32) {
    $buffer = New-Object byte[] $Bytes
    $generator = [Security.Cryptography.RandomNumberGenerator]::Create()
    try {
        $generator.GetBytes($buffer)
    } finally {
        $generator.Dispose()
    }
    return -join ($buffer | ForEach-Object { $_.ToString("x2") })
}

function Set-EnvValue([string]$Path, [string]$Key, [string]$Value) {
    $lines = [System.Collections.Generic.List[string]]::new()
    foreach ($line in Get-Content -LiteralPath $Path -Encoding UTF8) {
        $lines.Add($line)
    }

    $pattern = "^\s*$([regex]::Escape($Key))="
    $updated = $false
    for ($index = 0; $index -lt $lines.Count; $index++) {
        if ($lines[$index] -match $pattern) {
            $lines[$index] = "$Key=$Value"
            $updated = $true
            break
        }
    }
    if (-not $updated) {
        $lines.Add("$Key=$Value")
    }
    [IO.File]::WriteAllLines($Path, $lines, [Text.UTF8Encoding]::new($false))
}

function Initialize-DeploymentEnv([string]$Path) {
    $postgresAdminPassword = New-HexSecret
    $postgresAppPassword = New-HexSecret

    Set-EnvValue $Path "EDUGRADE_POSTGRES_PASSWORD" $postgresAdminPassword
    Set-EnvValue $Path "EDUGRADE_POSTGRES_APP_PASSWORD" $postgresAppPassword
    Set-EnvValue $Path "EDUGRADE_POSTGRES_ADMIN_DSN" "postgres://edugrade:$postgresAdminPassword@postgres:5432/edugrade?sslmode=disable"
    Set-EnvValue $Path "EDUGRADE_POSTGRES_DSN" "postgres://edugrade_app:$postgresAppPassword@postgres:5432/edugrade?sslmode=disable"
    Set-EnvValue $Path "EDUGRADE_POSTGRES_TENANT_RLS" "true"
    Set-EnvValue $Path "EDUGRADE_REDIS_PASSWORD" (New-HexSecret)
    Set-EnvValue $Path "EDUGRADE_MINIO_ROOT_PASSWORD" (New-HexSecret)
    Set-EnvValue $Path "EDUGRADE_MINIO_APP_SECRET_KEY" (New-HexSecret)
    Set-EnvValue $Path "EDUGRADE_QDRANT_API_KEY" (New-HexSecret)
    Set-EnvValue $Path "EDUGRADE_BARCODE_HMAC_KEYS" "local-v1:$(New-HexSecret)"
    Set-EnvValue $Path "EDUGRADE_GRADING_AGENT_TOKEN" (New-HexSecret)
    Set-EnvValue $Path "EDUGRADE_MATH_VERIFY_TOKEN" (New-HexSecret)
    Set-EnvValue $Path "EDUGRADE_GRAFANA_ADMIN_PASSWORD" (New-HexSecret)
    Set-EnvValue $Path "EDUGRADE_OCR_WORKER_PASSWORD" (New-HexSecret)
    Set-EnvValue $Path "EDUGRADE_IMAGE_QUALITY_PASSWORD" (New-HexSecret)
    Set-EnvValue $Path "EDUGRADE_PAGE_PROCESSING_PASSWORD" (New-HexSecret)
    Set-EnvValue $Path "EDUGRADE_SUBJECTIVE_WORKER_PASSWORD" (New-HexSecret)
}

function Read-DeploymentSettings([string]$Path, [switch]$Effective) {
    $values = @{}
    foreach ($line in Get-Content -LiteralPath $Path -Encoding UTF8) {
        $trimmed = $line.Trim()
        if ($trimmed -eq "" -or $trimmed.StartsWith("#") -or -not $trimmed.Contains("=")) {
            continue
        }
        $parts = $trimmed.Split("=", 2)
        $values[$parts[0].Trim()] = $parts[1].Trim().Trim('"').Trim("'")
    }
    if ($Effective) {
        # 与 Compose 和密钥同步一致，宿主机的显式覆盖优先；读取不打印任何配置值。
        foreach ($entry in Get-ChildItem Env:) {
            if ($entry.Name.StartsWith('EDUGRADE_')) { $values[$entry.Name] = $entry.Value }
        }
    }
    return $values
}

function Assert-GeneratedEnv([string]$Path) {
    $values = Read-DeploymentSettings $Path

    $secretKeys = @(
        "EDUGRADE_POSTGRES_PASSWORD",
        "EDUGRADE_POSTGRES_APP_PASSWORD",
        "EDUGRADE_REDIS_PASSWORD",
        "EDUGRADE_MINIO_ROOT_PASSWORD",
        "EDUGRADE_MINIO_APP_SECRET_KEY",
        "EDUGRADE_QDRANT_API_KEY",
        "EDUGRADE_GRADING_AGENT_TOKEN",
        "EDUGRADE_MATH_VERIFY_TOKEN",
        "EDUGRADE_GRAFANA_ADMIN_PASSWORD",
        "EDUGRADE_OCR_WORKER_PASSWORD",
        "EDUGRADE_IMAGE_QUALITY_PASSWORD",
        "EDUGRADE_PAGE_PROCESSING_PASSWORD",
        "EDUGRADE_SUBJECTIVE_WORKER_PASSWORD"
    )
    foreach ($key in $secretKeys) {
        if (-not $values.ContainsKey($key) -or $values[$key].Length -lt 32) {
            throw "Generated deployment secret is missing or too short: $key"
        }
    }
    if ($values["EDUGRADE_GRADING_AGENT_TOKEN"] -eq $values["EDUGRADE_MATH_VERIFY_TOKEN"]) {
        throw "Generated internal service tokens must be different."
    }
    if ($values["EDUGRADE_BARCODE_HMAC_KEYS"] -notmatch "^local-v1:[0-9a-f]{64}$") {
        throw "Generated barcode HMAC key has an invalid format."
    }
    if ($values["EDUGRADE_POSTGRES_ADMIN_DSN"] -notlike "*:$($values['EDUGRADE_POSTGRES_PASSWORD'])@postgres:*") {
        throw "Generated PostgreSQL admin DSN does not match its password."
    }
    if ($values["EDUGRADE_POSTGRES_DSN"] -notlike "*:$($values['EDUGRADE_POSTGRES_APP_PASSWORD'])@postgres:*") {
        throw "Generated PostgreSQL application DSN does not match its password."
    }
    if ($values["EDUGRADE_POSTGRES_TENANT_RLS"] -ne "true") {
        throw "Generated PostgreSQL application connection must enable tenant RLS."
    }
}

function Test-DockerDaemon {
    $null = & docker info --format "{{.OSType}}" 2>$null
    return $LASTEXITCODE -eq 0
}

function Wait-DockerDaemon([int]$TimeoutSeconds = 180) {
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    do {
        if (Test-DockerDaemon) {
            return
        }
        Start-Sleep -Seconds 3
    } while ((Get-Date) -lt $deadline)
    throw "Docker Desktop did not become ready within $TimeoutSeconds seconds."
}

function Ensure-Docker {
    if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
        throw "Docker CLI was not found. Install Docker Desktop and reopen PowerShell."
    }

    if (-not (Test-DockerDaemon)) {
        $programFiles = [Environment]::GetFolderPath("ProgramFiles")
        $dockerDesktop = Join-Path $programFiles "Docker\Docker\Docker Desktop.exe"
        if (-not (Test-Path -LiteralPath $dockerDesktop -PathType Leaf)) {
            throw "Docker daemon is not running and Docker Desktop was not found at $dockerDesktop."
        }
        Write-Step "Starting Docker Desktop"
        Start-Process -FilePath $dockerDesktop -WindowStyle Hidden
        Wait-DockerDaemon
    }

    $dockerOs = (& docker info --format "{{.OSType}}").Trim()
    if ($LASTEXITCODE -ne 0 -or $dockerOs -ne "linux") {
        throw "Docker must be running Linux containers. Current OSType: '$dockerOs'."
    }

    & docker compose version | Out-Host
    if ($LASTEXITCODE -ne 0) {
        throw "Docker Compose v2 is not available."
    }
}

function Read-BootstrapPassword {
    if (-not [string]::IsNullOrWhiteSpace($env:EDUGRADE_BOOTSTRAP_PASSWORD)) {
        return $env:EDUGRADE_BOOTSTRAP_PASSWORD
    }

    $secure = Read-Host "Create the platform_admin password (at least 15 characters)" -AsSecureString
    $pointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secure)
    try {
        return [Runtime.InteropServices.Marshal]::PtrToStringBSTR($pointer)
    } finally {
        [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($pointer)
    }
}

function Assert-BootstrapPassword([string]$Password) {
    if ([string]::IsNullOrWhiteSpace($Password) -or $Password.Length -lt 15) {
        throw "The platform_admin password must contain at least 15 characters."
    }
    $normalized = $Password.Trim().ToLowerInvariant()
    if ($normalized -in @("123456789012345", "passwordpassword", "qwertyuiopasdfgh", "adminadminadmin", "changemechangeme")) {
        throw "The platform_admin password is blocked because it is too common."
    }
}

function Write-InstallMarker {
    $stateLines = @(
        "installed_at=$((Get-Date).ToUniversalTime().ToString('o'))",
        "repository=$repositoryRoot"
    )
    [IO.File]::WriteAllLines($installMarker, $stateLines, [Text.UTF8Encoding]::new($false))
}

foreach ($requiredFile in @(
    $composeFile,
    $envExampleFile,
    $initScript,
    $prepareTlsScript,
    $smokeScript,
    $syncModelKeyScript,
    $prepareModelScript,
    $startModelScript,
    $modelManifestFile
)) {
    Assert-File $requiredFile
}

$firstInstall = -not (Test-Path -LiteralPath $installMarker -PathType Leaf)
$envExists = Test-Path -LiteralPath $envFile -PathType Leaf
$envCreatedThisRun = $false


# DryRun 只在临时文件中检验生成配置，结束后删除；不会调用 Docker 或写部署 .env。
if ($DryRun) {
    $testEnvFile = [IO.Path]::GetTempFileName()
    try {
        Copy-Item -LiteralPath $envExampleFile -Destination $testEnvFile -Force
        Initialize-DeploymentEnv $testEnvFile
        Assert-GeneratedEnv $testEnvFile
    } finally {
        Remove-Item -LiteralPath $testEnvFile -Force -ErrorAction SilentlyContinue
    }
    Write-Host "Repository: $repositoryRoot"
    Write-Host "Mode: $(if ($firstInstall) { 'first install' } else { 'daily start' })"
    Write-Host "Deployment env exists: $envExists"
    Write-Host "Build requested: $Build"
    Write-Host "Model mode requested: $ModelMode (SkipLocalModel: $SkipLocalModel)"
    Write-Host "Answer-sheet processing requested: $ProcessingMode (explicit worker switches preserved)"
    Write-Host "Controlled local TLS rotation requested: $RotateLocalCertificate"
    Write-Host "Generated configuration self-test: passed"
    Write-Host "No files or services were changed because -DryRun was used."
    return
}

$bootstrapPassword = $null
if ($firstInstall -and -not $SkipBootstrap) {
    # 在下载和构建前完成交互与规则校验；密码仅留进程内，不写模式文件或日志。
    $bootstrapPassword = Read-BootstrapPassword
    Assert-BootstrapPassword $bootstrapPassword
}

Ensure-Docker


# 只为首次创建的配置生成随机密钥；已有配置必须保留，避免数据库密码与持久化数据脱节。
if (-not $envExists) {
    Write-Step "Creating a secure local deployment configuration"
    Copy-Item -LiteralPath $envExampleFile -Destination $envFile
    Initialize-DeploymentEnv $envFile
    Assert-GeneratedEnv $envFile
    $envExists = $true
    $envCreatedThisRun = $true
    Write-Host "Created $envFile with randomly generated local secrets."
} elseif ($firstInstall) {
    Write-Warning "An existing .env was found. It will be preserved; preflight will reject missing required values."
}

$savedProcessingMode = if (Test-Path -LiteralPath $processingModeFile -PathType Leaf) { (Get-Content -Raw -LiteralPath $processingModeFile).Trim() } else { '' }
$processingChoice = Resolve-LocalProcessingMode -Requested $ProcessingMode -Saved $savedProcessingMode -ExplicitWorkers ([bool]($EnableOcr -or $EnableQuality -or $EnableProcessing))
if ($processingChoice -eq 'Enabled') { $EnableOcr = $true; $EnableQuality = $true; $EnableProcessing = $true }
if ($processingChoice -ne 'Explicit') {
    # 只保存能力选择，不保存凭据或“已就绪”结论；中途失败后仍按原选择重试账号核验与健康检查。
    [IO.File]::WriteAllText($processingModeFile, $processingChoice, [Text.UTF8Encoding]::new($false))
}
Write-Host "Answer-sheet processing mode: $processingChoice"

Write-Step "Preparing and validating internal TLS certificates"
if ($RotateLocalCertificate) {
    # 轮换工具先验证新目录，再切换已运行服务；失败会恢复旧配置，不删除旧证书或私钥。
    & python -B (Join-Path $composeDirectory 'scripts/rotate-local-tls.py') --env-file $envFile --compose-directory $composeDirectory --days $LocalCertificateDays --apply
    if ($LASTEXITCODE -ne 0) { throw 'Controlled local TLS rotation failed. Review the recovery instructions before continuing.' }
}
& $prepareTlsScript -EnvFile $envFile -ComposeDirectory $composeDirectory -LocalValidDays $LocalCertificateDays

$mode = $ModelMode
if ($SkipLocalModel) { $mode = 'Manual' }
if ($mode -eq 'Auto' -and (Test-Path -LiteralPath $modelModeFile -PathType Leaf)) {
    $savedMode = (Get-Content -Raw -LiteralPath $modelModeFile).Trim()
    if ($savedMode -in @('Manual', 'External', 'Local')) { $mode = $savedMode }
}
$modelSettings = Read-DeploymentSettings $envFile -Effective
if ($mode -eq 'Auto') {
    if (([string]$modelSettings['EDUGRADE_GRADING_MODEL_BASE_URL']).TrimEnd('/') -ne 'http://host.docker.internal:8087/v1' -or $modelSettings['EDUGRADE_AI_ADAPTER_TYPE'] -ne 'local_llama_cpp') {
        $mode = 'External'
    } else {
        $choice = Read-Host 'Model runtime: 1 = manual grading / no local download; 2 = existing external API; 3 = managed local model (~2.4 GiB). Choose 1, 2 or 3'
        $mode = switch ($choice) { '1' { 'Manual' } '2' { 'External' } '3' { 'Local' } default { throw 'Choose model mode 1, 2 or 3, or pass -ModelMode.' } }
    }
}
Write-Host "Model runtime mode: $mode"
if ($mode -eq 'Local' -and (([string]$modelSettings['EDUGRADE_GRADING_MODEL_BASE_URL']).TrimEnd('/') -ne 'http://host.docker.internal:8087/v1' -or $modelSettings['EDUGRADE_AI_ADAPTER_TYPE'] -ne 'local_llama_cpp')) {
    throw 'Local mode requires the managed local URL and local_llama_cpp adapter. Existing external settings were preserved.'
}
if (-not $SkipLocalModel) { [IO.File]::WriteAllText($modelModeFile, $mode, [Text.UTF8Encoding]::new($false)) }
$priorAIEnabled = [Environment]::GetEnvironmentVariable('EDUGRADE_AI_GRADING_ENABLED', 'Process')
$priorMathEnabled = [Environment]::GetEnvironmentVariable('EDUGRADE_MATH_GRADING_V2_ENABLED', 'Process')
try {
if ($mode -eq 'Manual') {
    # 人工模式不只是跳过下载，还要停止调度依赖模型的任务；只覆盖本次 Compose 进程，不改外部连接和密钥。
    $env:EDUGRADE_AI_GRADING_ENABLED = 'false'
    $env:EDUGRADE_MATH_GRADING_V2_ENABLED = 'false'
}
if ($mode -eq 'Local') {
    $manifest = Get-Content -Raw -LiteralPath $modelManifestFile -Encoding utf8 | ConvertFrom-Json
    $labRoot = Join-Path $repositoryRoot "lab"
    $runtimeBinary = Join-Path (Join-Path $labRoot $manifest.runtime.install_dir) $manifest.runtime.server_binary
    $modelFile = Join-Path $labRoot $manifest.model.model_path
    # 这里只做快速存在性和大小检查；下载准备及启动脚本负责各自完整性检查。
    $modelReady = (Test-Path -LiteralPath $runtimeBinary -PathType Leaf) -and
        (Test-Path -LiteralPath $modelFile -PathType Leaf) -and
        ((Get-Item -LiteralPath $modelFile).Length -eq [long]$manifest.model.expected_bytes)

    if (-not $modelReady) {
        Write-Step "Downloading and verifying the local llama.cpp runtime and Qwen model"
        & $prepareModelScript
        if ($LASTEXITCODE -ne 0) {
            throw "Preparing the local model runtime failed."
        }
    }

    Write-Step "Starting the local grading model"
    & $startModelScript -Candidate "qwen3_4b"
    if ($LASTEXITCODE -ne 0) {
        throw "Starting the local grading model failed."
    }

    & $syncModelKeyScript -EnvFile $envFile -ManagedLocal
    if ($LASTEXITCODE -ne 0) {
        throw "Synchronizing the local model API key failed."
    }
}

Write-Step $(if ($firstInstall) { "Installing EduGrade" } else { "Starting EduGrade" })
$initParameters = @{}
$sourceVersion = Get-ApplicationSourceVersion $repositoryRoot
Write-Host "Source revision: $($sourceVersion.commit) / $($sourceVersion.fingerprint.Substring(0,12))"
$builtVersion = $null
if (Test-Path -LiteralPath $buildVersionFile -PathType Leaf) {
    try {
        $builtVersion = Get-Content -Raw -LiteralPath $buildVersionFile | ConvertFrom-Json
        if (-not $builtVersion.PSObject.Properties['fingerprint'] -or ([string]$builtVersion.fingerprint) -notmatch '^[a-f0-9]{64}$' -or -not $builtVersion.PSObject.Properties['commit']) { $builtVersion = $null; throw 'Invalid build version record.' }
    }
    catch { Write-Warning 'The local build version record is unreadable. Use -Build to establish a verified source version.' }
}
if ($builtVersion) {
    Write-Host "Last local build: $($builtVersion.commit) / $(([string]$builtVersion.fingerprint).Substring(0,12))"
    if ($builtVersion.fingerprint -ne $sourceVersion.fingerprint -and -not $Build) {
        throw 'Application sources changed since the last local build. Run start-edugrade.cmd -Build to update core applications; existing data and credentials are preserved.'
    }
} elseif (-not $Build) { Write-Warning 'Existing image source version is unknown. Run start-edugrade.cmd -Build once to record the application source version.' }
$coreImagesReady = $true
$coreImageNames = @(
    $(if ($modelSettings['EDUGRADE_API_GATEWAY_IMAGE']) { $modelSettings['EDUGRADE_API_GATEWAY_IMAGE'] } else { 'edugrade/api-gateway:local' }),
    $(if ($modelSettings['EDUGRADE_GRADING_AGENT_IMAGE']) { $modelSettings['EDUGRADE_GRADING_AGENT_IMAGE'] } else { 'edugrade/grading-agent:local' }),
    $(if ($modelSettings['EDUGRADE_WEB_ADMIN_IMAGE']) { $modelSettings['EDUGRADE_WEB_ADMIN_IMAGE'] } else { 'edugrade/web-admin:local' })
)
$currentImageIDs = @()
foreach ($imageName in $coreImageNames) {
    $imageID = & docker image inspect --format '{{.Id}}' $imageName 2>$null
    if ($LASTEXITCODE -ne 0) {
        $coreImagesReady = $false
        break
    }
    $currentImageIDs += [string]$imageID
}
if ($builtVersion -and $builtVersion.PSObject.Properties['images'] -and -not $Build -and ($currentImageIDs -join ',') -ne ($builtVersion.images -join ',')) {
    throw 'Core images changed outside this launcher. Run start-edugrade.cmd -Build to verify the local source/image relationship.'
}
$workerBuildRequested = $EnableOcr -or $EnableQuality -or $EnableProcessing
if (-not $Build -and -not $envCreatedThisRun -and $coreImagesReady -and (-not $workerBuildRequested -or $builtVersion)) {
    # 启用 Worker 时只有已核验来源的核心镜像才能复用；旧镜像可能尚无受限账号配置命令。
    $initParameters["SkipBuild"] = $true
    if ($workerBuildRequested) { $initParameters['BuildWorkers'] = $true }
}
if ($EnableOcr) { $initParameters["EnableOcr"] = $true }
if ($EnableQuality) { $initParameters["EnableQuality"] = $true }
if ($EnableProcessing) { $initParameters["EnableProcessing"] = $true }
if ($EnableObservability) { $initParameters["EnableObservability"] = $true }

& $initScript @initParameters
if ($LASTEXITCODE -ne 0) {
    throw "EduGrade initialization failed."
}
Stop-DisabledLocalProcessingWorkers -Mode $processingChoice -EnvFile $envFile -ComposeFile $composeFile
if (-not $initParameters.ContainsKey('SkipBuild')) {
    $afterBuildVersion = Get-ApplicationSourceVersion $repositoryRoot
    if ($afterBuildVersion.fingerprint -ne $sourceVersion.fingerprint) {
        throw 'Services started, but application sources changed during the build. No verified build record was written. Run start-edugrade.cmd -Build again from a stable checkout.'
    }
    # 只在构建与服务健康检查成功后记录来源；失败不能把旧镜像标成已更新。
    $sourceVersion['images'] = @($coreImageNames | ForEach-Object {
        $builtImageID = & docker image inspect --format '{{.Id}}' $_
        if ($LASTEXITCODE -ne 0) { throw 'Cannot verify a newly built core image.' }
        [string]$builtImageID
    })
    [IO.File]::WriteAllText($buildVersionFile, ($sourceVersion | ConvertTo-Json), [Text.UTF8Encoding]::new($false))
}

# 初始化及本地模型配置可能更新 .env；认证冒烟和启动地址使用同一份最终部署设置。
$deploymentSettings = Read-DeploymentSettings $envFile -Effective
$apiPort = if ([string]::IsNullOrWhiteSpace($deploymentSettings["EDUGRADE_API_PORT"])) { "8080" } else { $deploymentSettings["EDUGRADE_API_PORT"] }
$publicPort = if ([string]::IsNullOrWhiteSpace($deploymentSettings["EDUGRADE_NGINX_HTTP_PORT"])) { "8088" } else { $deploymentSettings["EDUGRADE_NGINX_HTTP_PORT"] }
$sessionCookieName = if ([string]::IsNullOrWhiteSpace($deploymentSettings["EDUGRADE_SESSION_COOKIE_NAME"])) { "edugrade_session" } else { $deploymentSettings["EDUGRADE_SESSION_COOKIE_NAME"] }
$apiUrl = "http://127.0.0.1:$apiPort"
$publicUrl = "http://127.0.0.1:$publicPort"

if ($firstInstall -and -not $SkipBootstrap) {
    Write-Step "Creating the first platform administrator"
    try {
        $env:EDUGRADE_BOOTSTRAP_PASSWORD = $bootstrapPassword
        Push-Location $composeDirectory
        try {
            & docker compose --env-file $envFile -f $composeFile run --rm -e EDUGRADE_BOOTSTRAP_PASSWORD api-gateway bootstrap-admin
            if ($LASTEXITCODE -ne 0) {
                throw "Creating platform_admin failed."
            }
        } finally {
            Pop-Location
        }

        # 在带认证的冒烟测试前持久化初始化边界，
        # 避免暂时的 HTTP 失败导致第二次初始化。
        Write-InstallMarker
        $env:EDUGRADE_SMOKE_TENANT_CODE = "platform"
        $env:EDUGRADE_SMOKE_USERNAME = "platform_admin"
        $env:EDUGRADE_SMOKE_PASSWORD = $bootstrapPassword
        & $smokeScript -ApiUrl $apiUrl -PublicUrl $publicUrl -SessionCookieName $sessionCookieName
        if ($LASTEXITCODE -ne 0) {
            throw "Authenticated smoke testing failed."
        }
    } finally {
        Remove-Item Env:EDUGRADE_BOOTSTRAP_PASSWORD -ErrorAction SilentlyContinue
        Remove-Item Env:EDUGRADE_SMOKE_TENANT_CODE -ErrorAction SilentlyContinue
        Remove-Item Env:EDUGRADE_SMOKE_USERNAME -ErrorAction SilentlyContinue
        Remove-Item Env:EDUGRADE_SMOKE_PASSWORD -ErrorAction SilentlyContinue
        $bootstrapPassword = $null
    }
}

if (-not (Test-Path -LiteralPath $installMarker -PathType Leaf)) {
    Write-InstallMarker
}

Write-Host ""
Write-Host "EduGrade management is ready." -ForegroundColor Green
Write-Host "Web: $publicUrl"
Write-Host "Tenant: platform"
Write-Host "Username: platform_admin"
Write-Host "OCR worker requested: $EnableOcr; image quality worker requested: $EnableQuality; page processing worker requested: $EnableProcessing"
if ($processingChoice -eq 'Disabled') { Write-Host 'Answer-sheet processing is disabled. To enable the full local pipeline, run start-edugrade.cmd -ProcessingMode Enabled.' }
Write-Host 'Explicitly enabled local workers use separate restricted service accounts and a service-login check; existing passwords are never reset. See infra/docker-compose/README.md.'
$runningServices = @(& docker compose --env-file $envFile -f $composeFile ps --status running --services)
if ($LASTEXITCODE -eq 0) {
    foreach ($capability in @(@('OCR', 'ocr-worker'), @('Image quality', 'image-quality-worker'), @('Page processing', 'page-processing-worker'))) {
        Write-Host "$($capability[0]): $(if ($capability[1] -in $runningServices) { 'worker running; verify the first business task' } else { 'not enabled' })"
    }
}
if (-not $NoBrowser) {
    Start-Process $publicUrl
}
} finally {
    [Environment]::SetEnvironmentVariable('EDUGRADE_AI_GRADING_ENABLED', $priorAIEnabled, 'Process')
    [Environment]::SetEnvironmentVariable('EDUGRADE_MATH_GRADING_V2_ENABLED', $priorMathEnabled, 'Process')
}
