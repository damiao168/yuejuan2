param(
    [switch]$SkipModel
)

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"
$LabRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$Manifest = Get-Content -Raw (Join-Path $LabRoot "config/local-runtime.json") | ConvertFrom-Json
$Downloads = Join-Path $LabRoot ".downloads"
$RuntimeDir = Join-Path $LabRoot $Manifest.runtime.install_dir
$RuntimeArchive = Join-Path $Downloads $Manifest.runtime.archive
$ModelPath = Join-Path $LabRoot $Manifest.model.model_path

New-Item -ItemType Directory -Force $Downloads | Out-Null
New-Item -ItemType Directory -Force (Split-Path $ModelPath) | Out-Null

# 这里仅跳过大小相符的重复下载；调用方随后仍需验证 SHA256，大小一致不等于内容可信。
function Download-VerifiedFile($Url, $Destination, $ExpectedBytes) {
    if ((Test-Path $Destination) -and ((Get-Item $Destination).Length -eq $ExpectedBytes)) {
        return
    }
    & curl.exe -L --fail --retry 5 --retry-delay 2 -C - --output $Destination $Url
    if ($LASTEXITCODE -ne 0) { throw "download failed: $Url" }
    if ((Get-Item $Destination).Length -ne $ExpectedBytes) {
        throw "download size mismatch: $Destination"
    }
}

Download-VerifiedFile $Manifest.runtime.url $RuntimeArchive $Manifest.runtime.expected_bytes
$RuntimeHash = (Get-FileHash -Algorithm SHA256 $RuntimeArchive).Hash.ToLowerInvariant()
if ($RuntimeHash -ne $Manifest.runtime.expected_sha256) {
    throw "runtime SHA256 mismatch: expected $($Manifest.runtime.expected_sha256), got $RuntimeHash"
}
if (-not (Test-Path (Join-Path $RuntimeDir $Manifest.runtime.server_binary))) {
    New-Item -ItemType Directory -Force $RuntimeDir | Out-Null
    Expand-Archive -Path $RuntimeArchive -DestinationPath $RuntimeDir -Force
}

if (-not $SkipModel) {
    Download-VerifiedFile $Manifest.model.url $ModelPath $Manifest.model.expected_bytes
    $Hash = (Get-FileHash -Algorithm SHA256 $ModelPath).Hash.ToLowerInvariant()
    if ($Hash -ne $Manifest.model.expected_sha256) {
        throw "model SHA256 mismatch: expected $($Manifest.model.expected_sha256), got $Hash"
    }
}

Write-Output "Local runtime prepared under $LabRoot"
