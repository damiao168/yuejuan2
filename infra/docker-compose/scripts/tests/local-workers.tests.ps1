$ErrorActionPreference = 'Stop'
$testRoot = Join-Path ([IO.Path]::GetTempPath()) ('edugrade-worker-provision-tests-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path (Join-Path $testRoot 'scripts') | Out-Null
$source = Join-Path (Split-Path -Parent $PSScriptRoot) 'init.ps1'
Copy-Item -LiteralPath $source -Destination (Join-Path $testRoot 'scripts/init.ps1')
foreach ($dependency in @('prepare-internal-tls.ps1','preflight.ps1','smoke-test.ps1')) { 'param(); $global:LASTEXITCODE=0' | Set-Content -LiteralPath (Join-Path $testRoot "scripts/$dependency") }
'services: {}' | Set-Content -LiteralPath (Join-Path $testRoot 'docker-compose.yml')
$fixturePassword = 'fixture-local-worker-password-000000000000'
$fixtureLines = @('EDUGRADE_ENV=local','EDUGRADE_API_PORT=19999')
foreach ($prefix in @('EDUGRADE_OCR_WORKER','EDUGRADE_IMAGE_QUALITY','EDUGRADE_PAGE_PROCESSING')) {
    $fixtureLines += "${prefix}_TENANT_CODE=platform", "${prefix}_USERNAME=$($prefix.ToLowerInvariant())", "${prefix}_PASSWORD=$fixturePassword"
}
$fixtureLines | Set-Content -LiteralPath (Join-Path $testRoot '.env')
$global:LocalWorkerProvisionFixture = @{ Calls = [System.Collections.Generic.List[string]]::new(); LoginCount=0; ProvisionFailure=$false; Selection=''; Password=$fixturePassword }
function global:docker {
    $arguments = @($args)
    $global:LocalWorkerProvisionFixture.Calls.Add(($arguments -join ' '))
    if (($arguments -join ' ').Contains($global:LocalWorkerProvisionFixture.Password)) { throw 'Password leaked into Docker arguments.' }
    $global:LASTEXITCODE = 0
    if ($arguments -contains 'provision-local-workers') {
        $global:LocalWorkerProvisionFixture.Selection = $env:EDUGRADE_PROVISION_WORKERS
        if ($global:LocalWorkerProvisionFixture.ProvisionFailure) { $global:LASTEXITCODE = 1 }
    } elseif ($arguments -contains 'inspect') { 'running|healthy' }
    elseif ($arguments -contains '-q') { 'fixture-container' }
}
function global:Invoke-RestMethod {
    param($Method,$Uri,$ContentType,$Body,$TimeoutSec,$Headers)
    if ($Uri -like '*/auth/token') {
        $global:LocalWorkerProvisionFixture.LoginCount++
        $request = $Body | ConvertFrom-Json
        if ($request.client_type -ne 'service' -or $request.password -ne $global:LocalWorkerProvisionFixture.Password) { throw 'Wrong service login payload.' }
        return @{ access_token='fixture-token'; user=@{ roles=@('page_processing_worker') } }
    }
    return @{}
}
function Assert([bool]$Condition,[string]$Message) { if (-not $Condition) { throw $Message } }
$priorSelection = [Environment]::GetEnvironmentVariable('EDUGRADE_PROVISION_WORKERS','Process')
try {
    $env:EDUGRADE_PROVISION_WORKERS = 'prior-value'
    & (Join-Path $testRoot 'scripts/init.ps1') -SkipBuild -SkipSmoke -EnableOcr -EnableQuality -EnableProcessing | Out-Null
    Assert ($global:LocalWorkerProvisionFixture.Selection -eq 'ocr,quality,processing') 'Selected capabilities were not passed as a single explicit batch.'
    Assert ($global:LocalWorkerProvisionFixture.LoginCount -eq 3) 'Each account must pass a service login check.'
    Assert ($env:EDUGRADE_PROVISION_WORKERS -eq 'prior-value') 'Process environment was not restored.'
    Assert (@($global:LocalWorkerProvisionFixture.Calls | Where-Object { $_ -match 'up -d (ocr-worker|image-quality-worker|page-processing-worker)$' }).Count -eq 3) 'Only requested workers must start after verification.'
    $global:LocalWorkerProvisionFixture.Calls.Clear(); $global:LocalWorkerProvisionFixture.ProvisionFailure = $true
    $failed = $false
    try { & (Join-Path $testRoot 'scripts/init.ps1') -SkipBuild -SkipSmoke -EnableOcr | Out-Null } catch { $failed = $true }
    Assert $failed 'Failed provisioning must stop startup.'
    Assert (@($global:LocalWorkerProvisionFixture.Calls | Where-Object { $_ -match 'up -d ocr-worker$' }).Count -eq 0) 'Worker started after provisioning failed.'
    Assert ($env:EDUGRADE_PROVISION_WORKERS -eq 'prior-value') 'Failed provisioning leaked process overrides.'
    Write-Host 'Local Worker isolated tests passed: selection, credential inheritance, login, startup ordering, failure and environment recovery.'
} finally {
    [Environment]::SetEnvironmentVariable('EDUGRADE_PROVISION_WORKERS',$priorSelection,'Process')
    Remove-Item Function:\docker -ErrorAction SilentlyContinue
    Remove-Item Function:\Invoke-RestMethod -ErrorAction SilentlyContinue
    Remove-Variable LocalWorkerProvisionFixture -Scope Global -ErrorAction SilentlyContinue
    $resolvedTestRoot = (Resolve-Path -LiteralPath $testRoot).Path
    $resolvedTemporaryRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath())
    if (-not $resolvedTestRoot.StartsWith($resolvedTemporaryRoot,[StringComparison]::OrdinalIgnoreCase)) { throw 'Refusing cleanup outside temporary directory.' }
    Remove-Item -LiteralPath $resolvedTestRoot -Recurse -Force
}
