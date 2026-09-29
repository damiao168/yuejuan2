$ErrorActionPreference = 'Stop'
Set-StrictMode -Version 3.0
$repo = Split-Path -Parent $PSScriptRoot
$compose = Join-Path $repo 'infra/docker-compose/docker-compose.yml'
$preflight = Join-Path $repo 'infra/docker-compose/scripts/preflight.ps1'
$sample = Get-Content -LiteralPath (Join-Path $repo 'infra/docker-compose/.env.example') -Raw
# The operator must supply a model key; use a synthetic value to reach the
# database-configuration checks without reading the local deployment secrets.
$sample = $sample -replace '(?m)^EDUGRADE_GRADING_MODEL_API_KEY=.*$', 'EDUGRADE_GRADING_MODEL_API_KEY=synthetic-deployment-test-key'
$testEnv = [IO.Path]::GetTempFileName()
$deploymentDockerState = @{ Calls = 0 }

# This suite exercises the real preflight without starting or changing services.
function docker {
    $deploymentDockerState.Calls++
    throw 'TEST_DOCKER_BOUNDARY'
}

function Assert-Preflight([string]$Content, [string]$ExpectedError, [int]$ExpectedDockerCalls) {
    [IO.File]::WriteAllText($testEnv, $Content, [Text.UTF8Encoding]::new($false))
    $deploymentDockerState.Calls = 0
    $failure = ''
    try { & $preflight -ComposeFile $compose -EnvFile $testEnv }
    catch { $failure = $_.Exception.Message }
    if (-not $failure.Contains($ExpectedError) -or $deploymentDockerState.Calls -ne $ExpectedDockerCalls) {
        throw "Unexpected preflight result: error='$failure', dockerCalls=$($deploymentDockerState.Calls)"
    }
}

try {
    Assert-Preflight ($sample -replace '(?m)^EDUGRADE_POSTGRES_TENANT_RLS=.*$', 'EDUGRADE_POSTGRES_TENANT_RLS=false') 'PostgreSQL tenant RLS must be enabled' 0
    Assert-Preflight ($sample -replace '(?m)^EDUGRADE_POSTGRES_TENANT_RLS=.*\r?\n?', '') 'Required deployment setting is missing: EDUGRADE_POSTGRES_TENANT_RLS' 0
    Assert-Preflight $sample 'TEST_DOCKER_BOUNDARY' 1
    & (Join-Path $PSScriptRoot 'launch-local.ps1') -DryRun
    if ($deploymentDockerState.Calls -ne 1) { throw 'Launcher dry run unexpectedly invoked Docker.' }
    Write-Host 'Compose deployment regressions passed: false/missing RLS rejected before Docker; shipped and generated settings accepted.'
} finally {
    Remove-Item -LiteralPath $testEnv -Force
}
