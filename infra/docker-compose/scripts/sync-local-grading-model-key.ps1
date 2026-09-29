param(
  [string]$EnvFile = "..\.env",
  [string]$KeyFile = "..\..\..\lab\.runtime\llama-server.api-key"
)

$ErrorActionPreference = "Stop"
$scriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$envPath = if ([IO.Path]::IsPathRooted($EnvFile)) { $EnvFile } else { Join-Path $scriptRoot $EnvFile }
$keyPath = if ([IO.Path]::IsPathRooted($KeyFile)) { $KeyFile } else { Join-Path $scriptRoot $KeyFile }
$envPath = (Resolve-Path -LiteralPath $envPath).Path
$keyPath = (Resolve-Path -LiteralPath $keyPath).Path


# 仅把密钥同步到配置文件，不输出值；运行中的容器需由部署流程重新加载配置。
$key = (Get-Content -Raw -LiteralPath $keyPath).Trim()
if ([string]::IsNullOrWhiteSpace($key)) {
  throw "The local llama.cpp API key file is empty: $keyPath"
}

$lines = [System.Collections.Generic.List[string]]::new()
foreach ($line in Get-Content -LiteralPath $envPath) { $lines.Add($line) }
$updated = $false
for ($index = 0; $index -lt $lines.Count; $index++) {
  if ($lines[$index] -match '^\s*EDUGRADE_GRADING_MODEL_API_KEY=') {
    $lines[$index] = "EDUGRADE_GRADING_MODEL_API_KEY=$key"
    $updated = $true
    break
  }
}
if (-not $updated) {
  $lines.Add("EDUGRADE_GRADING_MODEL_API_KEY=$key")
}
[System.IO.File]::WriteAllLines($envPath, $lines, [System.Text.UTF8Encoding]::new($false))
Write-Host "Updated EDUGRADE_GRADING_MODEL_API_KEY in $envPath from $keyPath."
