param(
    [string]$Candidate = "qwen3_4b"
)
$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"
$LabRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$Manifest = Get-Content -Raw (Join-Path $LabRoot "config/local-runtime.json") | ConvertFrom-Json
$CandidateRegistry = Get-Content -Raw (Join-Path $LabRoot "config/model-candidates.json") | ConvertFrom-Json
$CandidateEntry = $CandidateRegistry.candidates | Where-Object { $_.candidate_id -eq $Candidate }
if (-not $CandidateEntry) { throw "unknown model candidate: $Candidate" }
$RuntimeDir = Join-Path $LabRoot $Manifest.runtime.install_dir
$Server = Join-Path $RuntimeDir $Manifest.runtime.server_binary
$Model = Join-Path $LabRoot $CandidateEntry.model_path
$PidFile = Join-Path $LabRoot ".runtime/llama-server.pid"
$CandidateFile = Join-Path $LabRoot ".runtime/llama-server.candidate"
$ApiKeyFile = Join-Path $LabRoot ".runtime/llama-server.api-key"
$Stdout = Join-Path $LabRoot ".runtime/llama-server.stdout.log"
$Stderr = Join-Path $LabRoot ".runtime/llama-server.stderr.log"

if (Test-Path $PidFile) {
    $ExistingPid = [int](Get-Content -Raw $PidFile)
    if (Get-Process -Id $ExistingPid -ErrorAction SilentlyContinue) {
        $RunningCandidate = if (Test-Path $CandidateFile) { (Get-Content -Raw $CandidateFile).Trim() } else { "unknown" }
        if ($RunningCandidate -eq $Candidate) {
            Write-Output "llama-server already running with PID $ExistingPid for $Candidate"
            exit 0
        }
        throw "llama-server is already running candidate $RunningCandidate; stop it before switching"
    }
}
if (-not (Test-Path $Server)) { throw "llama-server is not prepared" }
if (-not (Test-Path $Model)) { throw "local model is not prepared" }
if (-not (Test-Path $ApiKeyFile)) {
    $Bytes = New-Object byte[] 32
    $Generator = [System.Security.Cryptography.RandomNumberGenerator]::Create()
    try { $Generator.GetBytes($Bytes) } finally { $Generator.Dispose() }
    Set-Content -Path $ApiKeyFile -Value ([Convert]::ToBase64String($Bytes))
}

# 本地服务只监听回环地址，密钥通过文件传入，避免把密钥正文放进进程命令行。
$Arguments = @(
    "--model", $Model,
    "--host", "127.0.0.1",
    "--port", "8087",
    "--ctx-size", [string]$Manifest.execution.context_tokens,
    "--threads", [string]$Manifest.execution.threads,
    "--threads-batch", [string]$Manifest.execution.threads,
    "--n-gpu-layers", [string]$Manifest.execution.gpu_layers,
    "--parallel", [string]$Manifest.execution.parallel_requests,
    "--seed", [string]$Manifest.execution.seed,
    "--jinja",
    "--no-webui",
    "--cors-origins", "localhost",
    "--no-cors-credentials",
    "--api-key-file", $ApiKeyFile
)
$Process = Start-Process -FilePath $Server -ArgumentList $Arguments -PassThru -WindowStyle Hidden -RedirectStandardOutput $Stdout -RedirectStandardError $Stderr
Set-Content -Path $PidFile -Value $Process.Id
Set-Content -Path $CandidateFile -Value $Candidate

for ($Attempt = 0; $Attempt -lt 120; $Attempt++) {
    Start-Sleep -Milliseconds 500
    try {
        $Health = Invoke-RestMethod -Uri "http://127.0.0.1:8087/health" -TimeoutSec 2
        if ($Health.status -eq "ok") {
            Write-Output "llama-server ready with PID $($Process.Id)"
            exit 0
        }
    } catch {
        if ($Process.HasExited) { throw "llama-server exited during startup; inspect $Stderr" }
    }
}
Stop-Process -Id $Process.Id -Force -ErrorAction SilentlyContinue
throw "llama-server did not become healthy"
