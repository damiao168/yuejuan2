[CmdletBinding()]
param(
    [switch]$Build,
    [switch]$SkipLocalModel,
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
$initScript = Join-Path $composeDirectory "scripts\init.ps1"
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

function Assert-GeneratedEnv([string]$Path) {
    $values = @{}
    foreach ($line in Get-Content -LiteralPath $Path -Encoding UTF8) {
        $trimmed = $line.Trim()
        if ($trimmed -eq "" -or $trimmed.StartsWith("#") -or -not $trimmed.Contains("=")) {
            continue
        }
        $parts = $trimmed.Split("=", 2)
        $values[$parts[0].Trim()] = $parts[1].Trim().Trim('"').Trim("'")
    }

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
    Write-Host "Local model enabled: $(-not $SkipLocalModel)"
    Write-Host "Generated configuration self-test: passed"
    Write-Host "No files or services were changed because -DryRun was used."
    return
}

Ensure-Docker

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

if (-not $SkipLocalModel) {
    $manifest = Get-Content -Raw -LiteralPath $modelManifestFile | ConvertFrom-Json
    $labRoot = Join-Path $repositoryRoot "lab"
    $runtimeBinary = Join-Path (Join-Path $labRoot $manifest.runtime.install_dir) $manifest.runtime.server_binary
    $modelFile = Join-Path $labRoot $manifest.model.model_path
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

    & $syncModelKeyScript -EnvFile $envFile
    if ($LASTEXITCODE -ne 0) {
        throw "Synchronizing the local model API key failed."
    }
}

Write-Step $(if ($firstInstall) { "Installing EduGrade" } else { "Starting EduGrade" })
$initParameters = @{}
$coreImagesReady = $true
foreach ($imageName in @("edugrade/api-gateway:local", "edugrade/grading-agent:local", "edugrade/web-admin:local")) {
    $null = & docker image inspect $imageName 2>$null
    if ($LASTEXITCODE -ne 0) {
        $coreImagesReady = $false
        break
    }
}
$workerBuildRequested = $EnableOcr -or $EnableQuality -or $EnableProcessing
if (-not $Build -and -not $envCreatedThisRun -and -not $workerBuildRequested -and $coreImagesReady) {
    $initParameters["SkipBuild"] = $true
}
if ($EnableOcr) { $initParameters["EnableOcr"] = $true }
if ($EnableQuality) { $initParameters["EnableQuality"] = $true }
if ($EnableProcessing) { $initParameters["EnableProcessing"] = $true }
if ($EnableObservability) { $initParameters["EnableObservability"] = $true }

& $initScript @initParameters
if ($LASTEXITCODE -ne 0) {
    throw "EduGrade initialization failed."
}

if ($firstInstall -and -not $SkipBootstrap) {
    Write-Step "Creating the first platform administrator"
    $bootstrapPassword = Read-BootstrapPassword
    Assert-BootstrapPassword $bootstrapPassword
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

        # Persist the bootstrap boundary before the authenticated smoke test so
        # a transient HTTP failure cannot cause a second bootstrap attempt.
        Write-InstallMarker
        $env:EDUGRADE_SMOKE_TENANT_CODE = "platform"
        $env:EDUGRADE_SMOKE_USERNAME = "platform_admin"
        $env:EDUGRADE_SMOKE_PASSWORD = $bootstrapPassword
        & $smokeScript
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
Write-Host "EduGrade is ready." -ForegroundColor Green
Write-Host "Web: http://127.0.0.1:8088"
Write-Host "Tenant: platform"
Write-Host "Username: platform_admin"
if (-not $NoBrowser) {
    Start-Process "http://127.0.0.1:8088"
}
