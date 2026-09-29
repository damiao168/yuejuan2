param(
    [string]$Destination = ".downloads/jorgpt-18981627/dataset_en.csv"
)

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"
$LabRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$Lock = Get-Content -Raw (Join-Path $LabRoot "config/dataset-source-locks/jorgpt-zenodo-18981627.json") | ConvertFrom-Json
$DestinationPath = [IO.Path]::GetFullPath((Join-Path $LabRoot $Destination))
if (-not $DestinationPath.StartsWith($LabRoot + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
    throw "destination must remain under lab: $DestinationPath"
}
New-Item -ItemType Directory -Force (Split-Path $DestinationPath) | Out-Null

function Test-VerifiedFile([string]$Path) {
    if (-not (Test-Path -LiteralPath $Path)) { return $false }
    if ((Get-Item -LiteralPath $Path).Length -ne $Lock.file.expected_bytes) { return $false }
    $Md5 = (Get-FileHash -Algorithm MD5 -LiteralPath $Path).Hash.ToLowerInvariant()
    if ($Md5 -ne $Lock.file.expected_md5) { return $false }
    $Sha256 = (Get-FileHash -Algorithm SHA256 -LiteralPath $Path).Hash.ToLowerInvariant()
    return $Sha256 -eq $Lock.file.expected_sha256
}

if (-not (Test-VerifiedFile $DestinationPath)) {
    $PartialPath = "$DestinationPath.partial"
    & curl.exe -L --fail --silent --show-error --retry 5 --retry-delay 2 -C - --output $PartialPath $Lock.file.url
    if ($LASTEXITCODE -ne 0) { throw "JorGPT dataset download failed" }
    if (-not (Test-VerifiedFile $PartialPath)) { throw "JorGPT dataset integrity verification failed" }
    # 断点下载只写 partial，大小和两种摘要均通过后才替换正式文件。
    Move-Item -LiteralPath $PartialPath -Destination $DestinationPath -Force
}

[pscustomobject]@{
    source_id = $Lock.source_id
    record_doi = $Lock.record_doi
    path = $DestinationPath
    bytes = (Get-Item -LiteralPath $DestinationPath).Length
    md5 = (Get-FileHash -Algorithm MD5 -LiteralPath $DestinationPath).Hash.ToLowerInvariant()
    sha256 = (Get-FileHash -Algorithm SHA256 -LiteralPath $DestinationPath).Hash.ToLowerInvariant()
} | ConvertTo-Json
