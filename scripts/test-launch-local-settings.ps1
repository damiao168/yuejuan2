$ErrorActionPreference = 'Stop'
Set-StrictMode -Version 3.0
$repository = Split-Path -Parent $PSScriptRoot
$fixture = Join-Path ([IO.Path]::GetTempPath()) ('yuejuan-launch-settings-' + [guid]::NewGuid().ToString('N'))
$previousBootstrapPassword = $env:EDUGRADE_BOOTSTRAP_PASSWORD
$launcherBrowser = @{ Url = '' }

# 全程只运行隔离副本；Docker、初始化、TLS、HTTP 与浏览器启动都在替身边界内。
function global:docker { $global:LASTEXITCODE = 0; if ($args[0] -eq 'info') { 'linux' } }
function Start-Process([string]$FilePath) { $launcherBrowser.Url = $FilePath }
try {
    foreach ($directory in @('scripts', 'infra/docker-compose/scripts', 'lab/scripts', 'lab/config')) {
        New-Item -ItemType Directory -Path (Join-Path $fixture $directory) -Force | Out-Null
    }
    Copy-Item -LiteralPath (Join-Path $repository 'scripts/launch-local.ps1') -Destination (Join-Path $fixture 'scripts/launch-local.ps1')
    foreach ($path in @('infra/docker-compose/docker-compose.yml', 'infra/docker-compose/.env.example', 'lab/config/local-runtime.json')) {
        Set-Content -LiteralPath (Join-Path $fixture $path) -Value 'synthetic fixture'
    }
    foreach ($path in @('infra/docker-compose/scripts/init.ps1', 'infra/docker-compose/scripts/prepare-internal-tls.ps1', 'infra/docker-compose/scripts/sync-local-grading-model-key.ps1', 'lab/scripts/prepare-local-runtime.ps1', 'lab/scripts/start-local-server.ps1')) {
        Set-Content -LiteralPath (Join-Path $fixture $path) -Value '$global:LASTEXITCODE = 0'
    }
    @'
param([string]$ApiUrl, [string]$PublicUrl, [string]$SessionCookieName)
[pscustomobject]@{ api=$ApiUrl; public=$PublicUrl; cookie=$SessionCookieName; tenant=$env:EDUGRADE_SMOKE_TENANT_CODE; username=$env:EDUGRADE_SMOKE_USERNAME } | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $PSScriptRoot '../../../observed.json')
$global:LASTEXITCODE = 0
'@ | Set-Content -LiteralPath (Join-Path $fixture 'infra/docker-compose/scripts/smoke-test.ps1')
    foreach ($case in @(
        @{ Name='custom'; Env=@('EDUGRADE_API_PORT=18080', 'EDUGRADE_NGINX_HTTP_PORT="18088"', "EDUGRADE_SESSION_COOKIE_NAME='custom_session'"); Api='http://127.0.0.1:18080'; Public='http://127.0.0.1:18088'; Cookie='custom_session' },
        @{ Name='defaults'; Env=@('# optional settings omitted'); Api='http://127.0.0.1:8080'; Public='http://127.0.0.1:8088'; Cookie='edugrade_session' }
    )) {
        Set-Content -LiteralPath (Join-Path $fixture 'infra/docker-compose/.env') -Value $case.Env
        $marker = Join-Path $fixture 'infra/docker-compose/.env.local-install-complete'
        if (Test-Path -LiteralPath $marker) { Remove-Item -LiteralPath $marker }
        $env:EDUGRADE_BOOTSTRAP_PASSWORD = 'synthetic-launcher-password-2026'
        $output = & (Join-Path $fixture 'scripts/launch-local.ps1') -SkipLocalModel 6>&1 | Out-String
        $observed = Get-Content -LiteralPath (Join-Path $fixture 'observed.json') -Raw | ConvertFrom-Json
        if ($observed.api -ne $case.Api -or $observed.public -ne $case.Public -or $observed.cookie -ne $case.Cookie -or $observed.tenant -ne 'platform' -or $observed.username -ne 'platform_admin') {
            throw "Smoke settings mismatch in case $($case.Name): $($observed | ConvertTo-Json -Compress)"
        }
        if ($launcherBrowser.Url -ne $case.Public -or -not $output.Contains("Web: $($case.Public)")) {
            throw "Browser or displayed URL mismatch in case $($case.Name)"
        }
        if (-not (Test-Path -LiteralPath $marker)) { throw 'Installation boundary was not persisted before smoke.' }
        Write-Host "Launcher settings regression passed: $($case.Name)"
    }
} finally {
    $env:EDUGRADE_BOOTSTRAP_PASSWORD = $previousBootstrapPassword
    Remove-Item Function:\docker -ErrorAction SilentlyContinue
    # 只清理本测试生成且位于系统临时目录内的副本。
    $resolvedFixture = [IO.Path]::GetFullPath($fixture)
    $resolvedTemp = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\') + '\'
    if (-not $resolvedFixture.StartsWith($resolvedTemp, [StringComparison]::OrdinalIgnoreCase) -or (Split-Path -Leaf $resolvedFixture) -notlike 'yuejuan-launch-settings-*') { throw 'Unsafe fixture cleanup path.' }
    if (Test-Path -LiteralPath $resolvedFixture) { Remove-Item -LiteralPath $resolvedFixture -Recurse -Force }
}
