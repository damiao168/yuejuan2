[CmdletBinding()]
param(
  [ValidateSet("compatibility", "latency")]
  [string]$LifecycleGoal = "compatibility",
  [string]$SampleDirectory = "",
  [ValidateRange(1, 20)]
  [int]$Repeats = 3
)

$ErrorActionPreference = "Stop"
$composeRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$composeFile = Join-Path $composeRoot "docker-compose.yml"
$envFile = Join-Path $composeRoot ".env"
if (-not (Test-Path -LiteralPath $envFile -PathType Leaf)) {
  throw "Missing $envFile. Copy .env.example to .env and finish the normal deployment setup first."
}

$runArgs = @(
  "compose", "--env-file", $envFile, "-f", $composeFile, "--profile", "ocr",
  "run", "--rm", "--no-deps"
)
$calibrationArgs = @(
  "paper-formula-worker", "python", "-m", "ocr_worker.formula_calibration",
  "--batch-sizes", "1,2,4,8", "--repeats", $Repeats,
  "--lifecycle-goal", $LifecycleGoal
)
if ($SampleDirectory) {
  $resolvedSamples = (Resolve-Path -LiteralPath $SampleDirectory).Path
  if (-not (Test-Path -LiteralPath $resolvedSamples -PathType Container)) {
    throw "Formula sample path is not a directory: $resolvedSamples"
  }
  $runArgs += @("--volume", "${resolvedSamples}:/calibration-input:ro")
  $calibrationArgs += @("--sample-dir", "/calibration-input")
}

Write-Host "Measuring formula batches on this deployment. No model is selected from RAM capacity alone."
& docker @runArgs @calibrationArgs
if ($LASTEXITCODE -ne 0) {
  throw "Formula calibration failed with exit code $LASTEXITCODE. The existing runtime profile was not replaced."
}


# 测量已成功写入配置后重建 worker 使其生效；重启失败不等于配置未写入。
& docker compose --env-file $envFile -f $composeFile --profile ocr up -d --no-deps --force-recreate paper-formula-worker
if ($LASTEXITCODE -ne 0) {
  throw "The profile was written, but paper-formula-worker could not be restarted."
}

Write-Host "Formula deployment profile applied. Inspect the worker startup log for source=measured_profile."
