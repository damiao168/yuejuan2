$ErrorActionPreference = 'Stop'
Set-StrictMode -Version 3.0
$repo = Split-Path -Parent $PSScriptRoot
$compose = Join-Path $repo 'infra/docker-compose/docker-compose.yml'
$preflight = Join-Path $repo 'infra/docker-compose/scripts/preflight.ps1'
$sample = Get-Content -LiteralPath (Join-Path $repo 'infra/docker-compose/.env.example') -Raw
# 操作人员必须提供模型密钥；这里使用合成值进入
# 数据库配置检查，不读取本地部署密钥。
$sample = $sample -replace '(?m)^EDUGRADE_GRADING_MODEL_API_KEY=.*$', 'EDUGRADE_GRADING_MODEL_API_KEY=synthetic-deployment-test-key'
$sample = $sample -replace '(?m)^EDUGRADE_MODEL_CREDENTIAL_MASTER_KEY=.*$', 'EDUGRADE_MODEL_CREDENTIAL_MASTER_KEY=synthetic-model-encryption-key-32-chars'
$sample = $sample -replace '(?m)^EDUGRADE_REVIEW_EVIDENCE_HMAC_KEY=.*$', 'EDUGRADE_REVIEW_EVIDENCE_HMAC_KEY=synthetic-independent-evidence-key-32-chars'
$testEnv = [IO.Path]::GetTempFileName()
$deploymentDockerState = @{ Calls = 0 }

# 本套件测试真实预检，不启动或更改服务。
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
        throw "Unexpected preflight result: expected='$ExpectedError', error='$failure', dockerCalls=$($deploymentDockerState.Calls)"
    }
}

try {
    Assert-Preflight ($sample -replace '(?m)^EDUGRADE_TRUSTED_PROXY_CIDRS=.*$', 'EDUGRADE_TRUSTED_PROXY_CIDRS=172.30.24.0/24') 'must match the fixed nginx IPv4 address' 0
    Assert-Preflight ($sample -replace '(?m)^EDUGRADE_COMPOSE_SUBNET=.*$', 'EDUGRADE_COMPOSE_SUBNET=172.31.24.0/24') 'nginx IPv4 address must be inside' 0
    Assert-Preflight ($sample -replace '(?m)^EDUGRADE_MODEL_CREDENTIAL_MASTER_KEY=.*$', 'EDUGRADE_MODEL_CREDENTIAL_MASTER_KEY=edugrade-local-model-credential-key-change-before-production') 'must be a unique secret' 0
    Assert-Preflight ($sample -replace '(?m)^EDUGRADE_REVIEW_EVIDENCE_HMAC_KEY=.*$', 'EDUGRADE_REVIEW_EVIDENCE_HMAC_KEY=synthetic-model-encryption-key-32-chars') 'must use independent secrets' 0
    $previousProxyOverride = [Environment]::GetEnvironmentVariable('EDUGRADE_TRUSTED_PROXY_CIDRS', 'Process')
    try {
        $env:EDUGRADE_TRUSTED_PROXY_CIDRS = '0.0.0.0/0'
        Assert-Preflight $sample 'must match the fixed nginx IPv4 address' 0
    } finally {
        if ([string]::IsNullOrEmpty($previousProxyOverride)) { Remove-Item Env:EDUGRADE_TRUSTED_PROXY_CIDRS -ErrorAction SilentlyContinue }
        else { $env:EDUGRADE_TRUSTED_PROXY_CIDRS = $previousProxyOverride }
    }
    Assert-Preflight ($sample -replace '(?m)^EDUGRADE_POSTGRES_TENANT_RLS=.*$', 'EDUGRADE_POSTGRES_TENANT_RLS=false') 'PostgreSQL tenant RLS must be enabled' 0
    Assert-Preflight ($sample -replace '(?m)^EDUGRADE_POSTGRES_TENANT_RLS=.*\r?\n?', '') 'Required deployment setting is missing: EDUGRADE_POSTGRES_TENANT_RLS' 0
    Assert-Preflight $sample 'TEST_DOCKER_BOUNDARY' 1
    & (Join-Path $PSScriptRoot 'launch-local.ps1') -DryRun
    if ($deploymentDockerState.Calls -ne 1) { throw 'Launcher dry run unexpectedly invoked Docker.' }
    Write-Host 'Compose deployment regressions passed: proxy, secret and RLS failures rejected before Docker; synthetic and generated settings accepted.'
} finally {
    Remove-Item -LiteralPath $testEnv -Force
}
