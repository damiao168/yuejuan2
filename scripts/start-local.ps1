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

# Bind mounts do not reliably create a writable leaf directory on every Docker
# Desktop version, so create the project-local model cache before Compose runs.
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
        # Daily application changes do not need the large PaddleOCR image.
        # Rebuild it only with -BuildWorkers or an explicit -BuildService.
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
