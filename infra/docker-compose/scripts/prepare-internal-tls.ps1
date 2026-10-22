param(
  [Parameter(Mandatory = $true)][string]$EnvFile,
  [Parameter(Mandatory = $true)][string]$ComposeDirectory
)

$ErrorActionPreference = "Stop"
$python = Get-Command python -CommandType Application -ErrorAction SilentlyContinue
if (-not $python) { throw "Internal TLS preparation requires Python 3.11+ and OpenSSL; see infra/docker-compose/README.md." }
# 在下载模型、构建镜像或启动服务前验证TLS；已有证书不覆盖，失败现场留给部署人员处理。
& $python.Source -B (Join-Path $PSScriptRoot "ensure-internal-tls.py") --env-file $EnvFile --compose-directory $ComposeDirectory --generate-local-if-missing
if ($LASTEXITCODE -ne 0) { throw "Internal TLS preparation failed. No deployment services were started by this step." }
