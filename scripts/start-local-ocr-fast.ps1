param(
    [switch]$Build
)

$ErrorActionPreference = "Stop"
$repositoryRoot = Resolve-Path (Join-Path $PSScriptRoot "..")
$composeDirectory = Join-Path $repositoryRoot "infra\docker-compose"
$composeFile = Join-Path $composeDirectory "docker-compose.yml"
$envFile = Join-Path $composeDirectory ".env"
$ocrModelCache = Join-Path $repositoryRoot ".cache\ocr-models"

New-Item -ItemType Directory -Force -Path $ocrModelCache | Out-Null

$composeArguments = @(
    "compose",
    "--env-file", $envFile,
    "-f", $composeFile,
    "--profile", "ocr",
    "up", "-d"
)
if ($Build) {
    $composeArguments += "--build"
}
$composeArguments += @("postgres", "redis", "minio", "api-gateway", "ocr-worker")

# 保持控制服务运行，但只运行一个 OCR 工作进程，省略占用较大的数学工作进程。
& docker @composeArguments
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
