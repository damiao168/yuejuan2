param(
    [switch]$Build,
    [switch]$BuildWorkers,
    [string[]]$BuildService = @()
)

$ErrorActionPreference = "Stop"
$repositoryRoot = Resolve-Path (Join-Path $PSScriptRoot "..")
$composeDirectory = Join-Path $repositoryRoot "infra\docker-compose"
$composeFile = Join-Path $composeDirectory "docker-compose.yml"
$envFile = Join-Path $composeDirectory ".env"
$ocrModelCache = Join-Path $repositoryRoot ".cache\ocr-models"

if (-not (Test-Path -LiteralPath $envFile -PathType Leaf)) {
    throw "Missing $envFile. Copy infra/docker-compose/.env.example to .env and configure it first."
}

# 部分 Docker Desktop 版本的绑定挂载不能可靠地创建可写的末级目录，
# 因此在 Compose 运行前先创建项目内的模型缓存目录。
New-Item -ItemType Directory -Force -Path $ocrModelCache | Out-Null

$composeArguments = @(
    "compose",
    "--env-file", $envFile,
    "-f", $composeFile,
    "--profile", "*"
)

$dailyBuildServices = @("api-gateway", "grading-agent", "web-admin")
$workerBuildServices = @(
    "image-quality-worker",
    "subjective-grading-worker",
    "math-verification-worker",
    "page-processing-worker",
    "ocr-worker"
)
$knownBuildServices = $dailyBuildServices + $workerBuildServices

Write-Host "OCR model cache: $ocrModelCache"
if ($Build) {
    if ($BuildService.Count -gt 0) {
        $buildTargets = @($BuildService | Select-Object -Unique)
    } elseif ($BuildWorkers) {
        $buildTargets = @($knownBuildServices | Select-Object -Unique)
    } else {
        # 日常应用修改不需要重建大型 PaddleOCR 镜像；
        # 只在使用 -BuildWorkers 或显式 -BuildService 时重建。
        $buildTargets = $dailyBuildServices
    }
    $unknownTargets = @($buildTargets | Where-Object { $_ -notin $knownBuildServices })
    if ($unknownTargets.Count -gt 0) {
        throw "Unknown build service(s): $($unknownTargets -join ', '). Allowed: $($knownBuildServices -join ', ')"
    }
    Write-Host "Rebuilding: $($buildTargets -join ', ')"
    & docker @composeArguments "build" @buildTargets
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
} else {
    Write-Host "Starting services with existing images (use -Build after application source changes)..."
}

Write-Host "Starting all configured services without rebuilding unrelated worker images..."
& docker @composeArguments "up" "-d"
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
