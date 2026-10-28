param(
  [string]$EnvFile = "..\.env",
  [string]$KeyFile = "..\..\..\lab\.runtime\llama-server.api-key",
  [switch]$ManagedLocal
)

$ErrorActionPreference = "Stop"
$scriptRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$envPath = if ([IO.Path]::IsPathRooted($EnvFile)) { $EnvFile } else { Join-Path $scriptRoot $EnvFile }
$keyPath = if ([IO.Path]::IsPathRooted($KeyFile)) { $KeyFile } else { Join-Path $scriptRoot $KeyFile }
$envPath = (Resolve-Path -LiteralPath $envPath).Path
$settings = @{}
foreach ($line in Get-Content -LiteralPath $envPath) {
  if ($line -match '^\s*(EDUGRADE_[A-Z0-9_]+)=(.*)$') { $settings[$matches[1]] = $matches[2].Trim().Trim('"').Trim("'") }
}
# 显式本地模式与实际连接同时成立才可同步；外部连接及宿主机覆盖值不能被本地密钥替换。
$modelUrl = if ($env:EDUGRADE_GRADING_MODEL_BASE_URL) { $env:EDUGRADE_GRADING_MODEL_BASE_URL } else { $settings['EDUGRADE_GRADING_MODEL_BASE_URL'] }
$adapter = if ($env:EDUGRADE_AI_ADAPTER_TYPE) { $env:EDUGRADE_AI_ADAPTER_TYPE } else { $settings['EDUGRADE_AI_ADAPTER_TYPE'] }
if (-not $ManagedLocal -or ([string]$modelUrl).TrimEnd('/') -ne 'http://host.docker.internal:8087/v1' -or $adapter -ne 'local_llama_cpp') {
  Write-Host 'Local model key synchronization skipped: this deployment is not the explicitly selected managed local connection.'
  return
}
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
