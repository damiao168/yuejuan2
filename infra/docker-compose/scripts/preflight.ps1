param(
  [string]$ComposeFile = "..\docker-compose.yml",
  [string]$EnvFile = "..\.env",
  [switch]$RequireApplicationBaseImages
)

$ErrorActionPreference = "Stop"
$scriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path

function Resolve-DeploymentPath([string]$Path) {
  $candidate = if ([IO.Path]::IsPathRooted($Path)) { $Path } else { Join-Path $scriptRoot $Path }
  return (Resolve-Path -LiteralPath $candidate).Path
}

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

function Test-SecureOrLoopbackUrl([string]$Value) {
  try { $uri = [Uri]$Value } catch { return $false }
  if (-not $uri.IsAbsoluteUri) { return $false }
  if ($uri.Scheme -eq "https") { return $true }
  if ($uri.Scheme -ne "http") { return $false }
  if ($uri.Host -eq "localhost") { return $true }
  $address = $null
  if ([Net.IPAddress]::TryParse($uri.Host, [ref]$address)) { return [Net.IPAddress]::IsLoopback($address) }
  return $false
}

$composePath = Resolve-DeploymentPath $ComposeFile
$envPath = Resolve-DeploymentPath $EnvFile
$composeDir = Split-Path -Parent $composePath
$envValues = Read-EnvFile $envPath

$requiredKeys = @(
  "EDUGRADE_ENV",
  "EDUGRADE_SESSION_COOKIE_SECURE",
  "EDUGRADE_CORS_ALLOWED_ORIGINS",
  "EDUGRADE_POSTGRES_DB",
  "EDUGRADE_POSTGRES_USER",
  "EDUGRADE_POSTGRES_PASSWORD",
  "EDUGRADE_POSTGRES_APP_USER",
  "EDUGRADE_POSTGRES_APP_PASSWORD",
  "EDUGRADE_POSTGRES_ADMIN_DSN",
  "EDUGRADE_POSTGRES_DSN",
  "EDUGRADE_POSTGRES_TENANT_RLS",
  "EDUGRADE_REDIS_USERNAME",
  "EDUGRADE_REDIS_PASSWORD",
  "EDUGRADE_REDIS_TLS_ENABLED",
  "EDUGRADE_AUTH_LIMITER_FAIL_CLOSED",
  "EDUGRADE_INTERNAL_API_BASE_URL",
  "EDUGRADE_INTERNAL_MATH_VERIFY_BASE_URL",
  "EDUGRADE_INTERNAL_TLS_PORT",
  "EDUGRADE_GRADING_AGENT_HEALTH_URL",
  "EDUGRADE_MATH_VERIFY_HEALTH_URL",
  "EDUGRADE_MINIO_ROOT_USER",
  "EDUGRADE_MINIO_ROOT_PASSWORD",
  "EDUGRADE_MINIO_APP_ACCESS_KEY",
  "EDUGRADE_MINIO_APP_SECRET_KEY",
  "EDUGRADE_MINIO_USE_SSL",
  "EDUGRADE_FILE_BUCKET",
  "EDUGRADE_BARCODE_HMAC_KEYS",
  "EDUGRADE_QDRANT_API_KEY",
  "EDUGRADE_AI_SERVICE_URL",
  "EDUGRADE_GRADING_AGENT_TOKEN",
  "EDUGRADE_MATH_VERIFY_TOKEN",
  "EDUGRADE_AI_MODEL_VERSION",
  "EDUGRADE_AI_PROMPT_VERSION",
  "EDUGRADE_GRADING_MODEL_BASE_URL",
  "EDUGRADE_GRADING_MODEL_API_KEY"
)
foreach ($key in $requiredKeys) {
  if (-not $envValues.ContainsKey($key) -or [string]::IsNullOrWhiteSpace($envValues[$key])) {
    throw "Required deployment setting is missing: $key"
  }
}
# The Compose application login is NOINHERIT even in local deployments; its
# grants are activated by the tenant connector's SET ROLE on each connection.
if ($envValues["EDUGRADE_POSTGRES_TENANT_RLS"] -ne "true") {
  throw "PostgreSQL tenant RLS must be enabled for the Compose application login. Set EDUGRADE_POSTGRES_TENANT_RLS=true in the deployment env file before starting or upgrading."
}
$promptManifestPath = Resolve-DeploymentPath "../../../ai-services/prompts/manifest.json"
$promptManifest = Get-Content -LiteralPath $promptManifestPath -Raw -Encoding utf8 | ConvertFrom-Json
$manifestPromptVersion = [string]$promptManifest.prompt_version
if ([string]::IsNullOrWhiteSpace($manifestPromptVersion)) {
  throw "Governed prompt manifest is missing prompt_version."
}
if ($envValues["EDUGRADE_AI_PROMPT_VERSION"] -ne $manifestPromptVersion) {
  throw "EDUGRADE_AI_PROMPT_VERSION '$($envValues["EDUGRADE_AI_PROMPT_VERSION"])' does not match governed prompt manifest version '$manifestPromptVersion'."
}
if ($envValues["EDUGRADE_GRADING_AGENT_TOKEN"].Length -lt 32) {
  throw "EDUGRADE_GRADING_AGENT_TOKEN must contain at least 32 characters."
}
if ($envValues["EDUGRADE_MATH_VERIFY_TOKEN"].Length -lt 32) {
  throw "EDUGRADE_MATH_VERIFY_TOKEN must contain at least 32 characters."
}
if ($envValues["EDUGRADE_GRADING_AGENT_TOKEN"] -eq $envValues["EDUGRADE_MATH_VERIFY_TOKEN"]) {
  throw "Grading Agent and math verification tokens must be different."
}
if ($envValues["EDUGRADE_QDRANT_API_KEY"].Length -lt 32) {
  throw "EDUGRADE_QDRANT_API_KEY must contain at least 32 characters."
}

$localEnvironments = @("local", "development", "dev", "test")
$environment = $envValues["EDUGRADE_ENV"].ToLowerInvariant()

# 只有明确列出的本地环境可以放宽要求，未知环境名按生产级约束检查。
$productionLike = $localEnvironments -notcontains $environment
if ($productionLike) {
  $problems = @()
  if ($envValues["EDUGRADE_SESSION_COOKIE_SECURE"] -ne "true") { $problems += "secure session cookie is required" }
  if ($envValues["EDUGRADE_POSTGRES_PASSWORD"] -match "change_me|edugrade_dev") { $problems += "PostgreSQL example password must be replaced" }
  if ($envValues["EDUGRADE_POSTGRES_APP_PASSWORD"] -match "change_me|edugrade_dev" -or $envValues["EDUGRADE_POSTGRES_APP_PASSWORD"].Length -lt 32) { $problems += "PostgreSQL application password must be replaced and contain at least 32 characters" }
  if ($envValues["EDUGRADE_POSTGRES_APP_USER"] -eq $envValues["EDUGRADE_POSTGRES_USER"]) { $problems += "PostgreSQL migration and application users must be different" }
  if ($envValues["EDUGRADE_POSTGRES_ADMIN_DSN"] -eq $envValues["EDUGRADE_POSTGRES_DSN"]) { $problems += "PostgreSQL admin and application DSNs must be different" }
  if ($envValues["EDUGRADE_POSTGRES_ADMIN_DSN"] -match "sslmode=disable|change_me|edugrade_dev") { $problems += "PostgreSQL admin DSN is not production-safe" }
  if ($envValues["EDUGRADE_POSTGRES_DSN"] -match "sslmode=disable|change_me|edugrade_dev") { $problems += "PostgreSQL DSN is not production-safe" }
  if ($envValues["EDUGRADE_REDIS_PASSWORD"] -match "change_me|edugrade_dev") { $problems += "Redis example password must be replaced" }
  if ($envValues["EDUGRADE_REDIS_TLS_ENABLED"] -ne "true") { $problems += "Redis TLS must be enabled" }
  if ($envValues["EDUGRADE_AUTH_LIMITER_FAIL_CLOSED"] -ne "true") { $problems += "authentication rate limiting must fail closed when Redis is degraded" }
  if ($envValues["EDUGRADE_INTERNAL_API_BASE_URL"] -notmatch "^https://") { $problems += "worker-to-API transport must use HTTPS" }
  if ($envValues["EDUGRADE_INTERNAL_MATH_VERIFY_BASE_URL"] -notmatch "^https://") { $problems += "OCR-to-math-verifier transport must use HTTPS" }
  if ($envValues["EDUGRADE_AI_SERVICE_URL"] -notmatch "^https://") { $problems += "API-to-grading-agent transport must use HTTPS" }
  if ($envValues["EDUGRADE_GRADING_AGENT_HEALTH_URL"] -notmatch "^https://") { $problems += "grading-agent health probe must use HTTPS" }
  if ($envValues["EDUGRADE_MATH_VERIFY_HEALTH_URL"] -notmatch "^https://") { $problems += "math-verifier health probe must use HTTPS" }
  if (-not (Test-SecureOrLoopbackUrl $envValues["EDUGRADE_GRADING_MODEL_BASE_URL"])) { $problems += "grading-agent-to-model transport must use HTTPS outside loopback" }
  if ($envValues["EDUGRADE_INTERNAL_TLS_PORT"] -eq "0") { $problems += "internal API TLS listener must be enabled" }
  foreach ($key in @("EDUGRADE_INTERNAL_CA_FILE", "EDUGRADE_INTERNAL_TLS_CERT_FILE", "EDUGRADE_INTERNAL_TLS_KEY_FILE", "EDUGRADE_GRADING_AGENT_TLS_CERT_FILE", "EDUGRADE_GRADING_AGENT_TLS_KEY_FILE", "EDUGRADE_MATH_VERIFY_TLS_CERT_FILE", "EDUGRADE_MATH_VERIFY_TLS_KEY_FILE")) {
    if (-not $envValues.ContainsKey($key) -or [string]::IsNullOrWhiteSpace($envValues[$key])) { $problems += "$key must be configured for internal TLS" }
  }
  if ($envValues["EDUGRADE_MINIO_ROOT_USER"] -match "edugrade-root" -or $envValues["EDUGRADE_MINIO_ROOT_PASSWORD"] -match "change_me|edugrade_dev") { $problems += "MinIO Root example credentials must be replaced" }
  if ($envValues["EDUGRADE_MINIO_APP_ACCESS_KEY"] -match "edugrade-app" -or $envValues["EDUGRADE_MINIO_APP_SECRET_KEY"] -match "change_me|edugrade_dev") { $problems += "MinIO application example credentials must be replaced" }
  if ($envValues["EDUGRADE_MINIO_ROOT_USER"] -eq $envValues["EDUGRADE_MINIO_APP_ACCESS_KEY"] -or $envValues["EDUGRADE_MINIO_ROOT_PASSWORD"] -eq $envValues["EDUGRADE_MINIO_APP_SECRET_KEY"]) { $problems += "MinIO Root and application credentials must be different" }
  if ($envValues["EDUGRADE_MINIO_USE_SSL"] -ne "true") { $problems += "MinIO TLS must be enabled" }
  if ($envValues["EDUGRADE_CORS_ALLOWED_ORIGINS"] -match "localhost|127\.0\.0\.1") { $problems += "local CORS origins are not allowed" }
  if ($envValues["EDUGRADE_QDRANT_API_KEY"] -match "change_me") { $problems += "Qdrant example API key must be replaced" }
  if ($envValues["EDUGRADE_GRADING_AGENT_TOKEN"] -match "replace_with|change_me") { $problems += "Grading Agent example token must be replaced" }
  if ($envValues["EDUGRADE_MATH_VERIFY_TOKEN"] -match "replace_with|change_me") { $problems += "math verification example token must be replaced" }
  if ($envValues["EDUGRADE_GRADING_MODEL_API_KEY"] -match "replace_with|change_me") { $problems += "grading model example API key must be replaced" }
  if ($envValues["EDUGRADE_BARCODE_HMAC_KEYS"] -match "replace_with|change_me") { $problems += "barcode example HMAC key must be replaced" }
  if ($envValues["EDUGRADE_GRAFANA_ADMIN_PASSWORD"] -match "change_me" -or $envValues["EDUGRADE_GRAFANA_ADMIN_PASSWORD"] -eq "admin") { $problems += "Grafana example password must be replaced" }
  if ($envValues.ContainsKey("EDUGRADE_INTERNAL_BIND_HOST") -and $envValues["EDUGRADE_INTERNAL_BIND_HOST"] -eq "0.0.0.0") { $problems += "internal service ports must not bind to 0.0.0.0 in production" }

  $immutableImageKeys = @(
    "EDUGRADE_POSTGRES_IMAGE",
    "EDUGRADE_REDIS_IMAGE",
    "EDUGRADE_MINIO_IMAGE",
    "EDUGRADE_MINIO_MC_IMAGE",
    "EDUGRADE_QDRANT_IMAGE",
    "EDUGRADE_API_GATEWAY_IMAGE",
    "EDUGRADE_WEB_ADMIN_IMAGE",
    "EDUGRADE_GRADING_AGENT_IMAGE",
    "EDUGRADE_OCR_WORKER_IMAGE",
    "EDUGRADE_IMAGE_QUALITY_WORKER_IMAGE",
    "EDUGRADE_SUBJECTIVE_GRADING_WORKER_IMAGE",
    "EDUGRADE_MATH_VERIFICATION_WORKER_IMAGE",
    "EDUGRADE_PAGE_PROCESSING_WORKER_IMAGE",
    "EDUGRADE_NGINX_IMAGE",
    "EDUGRADE_PROMETHEUS_IMAGE",
    "EDUGRADE_GRAFANA_IMAGE",
    "EDUGRADE_GO_BUILD_IMAGE",
    "EDUGRADE_API_RUNTIME_IMAGE",
    "EDUGRADE_NODE_BUILD_IMAGE",
    "EDUGRADE_WEB_RUNTIME_IMAGE",
    "EDUGRADE_AI_PYTHON_IMAGE",
    "EDUGRADE_OCR_PYTHON_IMAGE",
    "EDUGRADE_QUALITY_PYTHON_IMAGE",
    "EDUGRADE_SUBJECTIVE_PYTHON_IMAGE",
    "EDUGRADE_MATH_PYTHON_IMAGE",
    "EDUGRADE_PAGE_PROCESSING_PYTHON_IMAGE"
  )
  foreach ($key in $immutableImageKeys) {
    if (-not $envValues.ContainsKey($key) -or $envValues[$key] -notmatch "^[^\s@]+@sha256:[0-9a-fA-F]{64}$") {
      $problems += "$key must be pinned to an immutable image digest"
    }
  }
  if ($envValues.ContainsKey("EDUGRADE_PAPER_FORMULA_WORKER_IMAGE") -and
      -not [string]::IsNullOrWhiteSpace($envValues["EDUGRADE_PAPER_FORMULA_WORKER_IMAGE"]) -and
      $envValues["EDUGRADE_PAPER_FORMULA_WORKER_IMAGE"] -notmatch "^[^\s@]+@sha256:[0-9a-fA-F]{64}$") {
    $problems += "EDUGRADE_PAPER_FORMULA_WORKER_IMAGE must be empty or pinned to an immutable image digest"
  }
  if ($problems.Count -gt 0) { throw "Production preflight rejected unsafe configuration: $($problems -join '; ')" }
} elseif (
  $envValues["EDUGRADE_POSTGRES_PASSWORD"] -match "change_me" -or
  $envValues["EDUGRADE_POSTGRES_APP_PASSWORD"] -match "change_me" -or
  $envValues["EDUGRADE_REDIS_PASSWORD"] -match "change_me" -or
  $envValues["EDUGRADE_MINIO_ROOT_PASSWORD"] -match "change_me" -or
  $envValues["EDUGRADE_MINIO_APP_SECRET_KEY"] -match "change_me"
) {
  Write-Warning "Local example credentials are still configured. Use only on an isolated development host."
}

docker version --format "Docker server {{.Server.Version}}" | Out-Host
if ($LASTEXITCODE -ne 0) { throw "Docker daemon is not available." }
docker compose version | Out-Host
if ($LASTEXITCODE -ne 0) { throw "Docker Compose is not available." }

Push-Location $composeDir
try {
  # 只校验配置并隐藏展开结果，避免将解析后的凭据输出到终端。
  docker compose --env-file $envPath -f $composePath --profile tools --profile ocr --profile quality --profile processing --profile observability config --quiet
  if ($LASTEXITCODE -ne 0) { throw "Docker Compose configuration is invalid." }
} finally {
  Pop-Location
}

$baseImages = @(
  $envValues["EDUGRADE_GO_BUILD_IMAGE"],
  $envValues["EDUGRADE_API_RUNTIME_IMAGE"],
  $envValues["EDUGRADE_NODE_BUILD_IMAGE"],
  $envValues["EDUGRADE_WEB_RUNTIME_IMAGE"],
  $envValues["EDUGRADE_AI_PYTHON_IMAGE"],
  $envValues["EDUGRADE_OCR_PYTHON_IMAGE"],
  $envValues["EDUGRADE_QUALITY_PYTHON_IMAGE"],
  $envValues["EDUGRADE_SUBJECTIVE_PYTHON_IMAGE"],
  $envValues["EDUGRADE_MATH_PYTHON_IMAGE"],
  $envValues["EDUGRADE_PAGE_PROCESSING_PYTHON_IMAGE"]
) | Where-Object { -not [string]::IsNullOrWhiteSpace($_) } | Select-Object -Unique
$missingImages = @()
foreach ($image in $baseImages) {
  $previousErrorAction = $ErrorActionPreference
  $ErrorActionPreference = "SilentlyContinue"
  docker image inspect $image *> $null
  $inspectExitCode = $LASTEXITCODE
  $ErrorActionPreference = $previousErrorAction
  if ($inspectExitCode -ne 0) { $missingImages += $image }
}
if ($missingImages.Count -gt 0) {
  $message = "Application base images are not cached: $($missingImages -join ', '). Pull them, configure the EDUGRADE_*_IMAGE mirror variables, or import them with docker load."
  if ($RequireApplicationBaseImages) { throw $message }
  Write-Warning $message
}

Write-Host "EduGrade deployment preflight passed for environment '$environment'."
