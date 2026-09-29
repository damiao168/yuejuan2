# EduGrade Enterprise 预生产部署 Runbook

适用版本：当前 `main`

适用拓扑：Windows 主机 + Docker Desktop/Engine + Docker Compose 单机私有化部署

## 1. 能力边界

本 Runbook 用于单机内网部署、受控试点和预生产验收。它覆盖 PostgreSQL、Redis、MinIO、Qdrant、API Gateway、Web Admin、Nginx、内部 `grading-agent`，以及按 profile 启用的 OCR、图像质量、页面处理和可观测性服务的启动、迁移、初始化、登录、备份与恢复验证。

该拓扑不提供多机高可用、自动故障转移或跨地域灾备。CI 已提供依赖检查、SBOM 和 High/Critical 漏洞门禁，但不能替代学校环境的网络、主机与镜像仓库安全评估。`grading-agent` 可调用受控的本地 llama.cpp 模型，但只产生必须由教师复核的建议；Lab 当前仍为 `Pilot NOT_READY`。OCR、图像质量和页面处理 worker 通过 profile 单独启用；扫描仪驱动、无人工最终定分等未实现能力不因部署成功而变成生产能力。

## 2. 主机要求

- Windows 10/11 或 Windows Server，启用 Docker Desktop/Engine Linux containers。
- Docker Engine 与 Docker Compose v2 可用。
- 建议至少 8 核 CPU、16 GB 内存、100 GB 可用磁盘；启用 PaddleOCR 时按模型资源额外预留。
- 宿主机端口默认使用：8088、8080、5432、6379、9000、9001、6333、6334。`grading-agent:8100` 仅在 Compose 内网监听，不映射到宿主机。
- 正式环境必须有受控 DNS、TLS 证书、备份介质和限制访问的运维账号。

## 3. 配置准备

```powershell
Set-Location D:\project\yuejuan\infra\docker-compose
Copy-Item .env.example .env
```

本地/受控试点保持：

```text
EDUGRADE_ENV=local
```

首次启动前至少修改 PostgreSQL、Redis、MinIO 和 Grafana 密码。`.env` 不得提交仓库。

production-like 环境必须同时满足：

- `EDUGRADE_SESSION_COOKIE_SECURE=true`
- `EDUGRADE_POSTGRES_TENANT_RLS=true`
- API 使用独立的 `EDUGRADE_POSTGRES_APP_USER` 与 `EDUGRADE_POSTGRES_DSN`；迁移专用账号只出现在 `EDUGRADE_POSTGRES_ADMIN_DSN`，两者不得相同；迁移账号必须具备角色管理能力，或由托管数据库管理员预先完成等价授权
- PostgreSQL 使用非示例凭据，DSN 不得包含 `sslmode=disable`
- Redis 使用独立 ACL 用户与非示例密码，并设置 `EDUGRADE_REDIS_TLS_ENABLED=true`；服务证书和 API 信任的 CA 按 Compose 部署说明挂载
- API Gateway 到 Worker/Grading Agent/数学验证及远端模型的非 loopback 链路使用内部 CA 验证的 HTTPS；API 的 Worker 专用 TLS 监听、三个服务端证书和内部健康 URL 均已配置
- MinIO 使用非示例 access key/secret
- CORS 不包含 localhost/127.0.0.1
- `.env.example` 列出的运行时镜像与构建基础镜像全部使用 `repository@sha256:<digest>`，应用镜像 digest 与发布证据一致
- Web/API 经 TLS 反向代理暴露

API 会再次执行相同安全校验；不安全配置会拒绝启动。

## 4. 部署预检

```powershell
.\scripts\preflight.ps1
```

预检内容：

- Docker daemon 和 Compose 可用。
- Compose 所有 profile 可以解析。
- 必填环境变量存在。
- production-like 配置满足安全门禁。
- production-like 的 RLS 开关已启用，所有镜像引用均为不可变 digest。
- 应用基础镜像是否已缓存。

严格要求本机已有构建基础镜像：

```powershell
.\scripts\preflight.ps1 -RequireApplicationBaseImages
```

## 5. 首次初始化

```powershell
.\scripts\init.ps1
```

执行顺序：依赖健康检查、migration、MinIO private bucket、应用构建、应用健康检查、HTTP smoke test。

初始化脚本可重复执行：已记录的 migration 会按 SHA-256 校验后跳过，bucket 会保持 private，现有 volume 和管理员不会被删除或覆盖。

### 首个管理员

在当前 PowerShell 会话安全输入一次性 bootstrap 密码：

```powershell
$secure = Read-Host "Bootstrap password" -AsSecureString
$ptr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secure)
try {
  $env:EDUGRADE_BOOTSTRAP_PASSWORD = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($ptr)
  .\scripts\init.ps1 -SkipBuild -BootstrapAdmin
} finally {
  [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($ptr)
  Remove-Item Env:EDUGRADE_BOOTSTRAP_PASSWORD -ErrorAction SilentlyContinue
}
```

如果 active 平台管理员已经存在，bootstrap 会拒绝覆盖。

## 6. 已有数据库采用 migration tracking

STORY-052 之前部署的数据库可能已有表但没有 `schema_migration`。必须先确认该库确实已经应用仓库当前全部 migration，再执行一次：

```powershell
.\scripts\init.ps1 -SkipBuild -SkipSmoke -MigrationBaselineVersion 000020
```

该参数只允许在确认旧库确实对应 000020 时使用一次。迁移器会先校验表、字段类型、非空约束、默认值、索引和租户复合外键指纹，只登记到 000020，再执行后续 migration；不会再把全部文件直接标记为已执行。没有显式版本或指纹不匹配时会拒绝启动迁移。

已应用 migration 文件不得原地修改；校验和变化会阻止继续部署。修正数据库结构必须新增 migration。

## 7. 访问与冒烟

- Web/Nginx：[http://127.0.0.1:8088](http://127.0.0.1:8088)
- API liveness：[http://127.0.0.1:8080/health/live](http://127.0.0.1:8080/health/live)
- API readiness：[http://127.0.0.1:8080/health/ready](http://127.0.0.1:8080/health/ready)
- MinIO Console：[http://127.0.0.1:9001](http://127.0.0.1:9001)
- Qdrant：[http://127.0.0.1:6333/dashboard](http://127.0.0.1:6333/dashboard)

匿名 smoke：

```powershell
.\scripts\smoke-test.ps1
```

登录与 readiness smoke：

```powershell
$env:EDUGRADE_SMOKE_TENANT_CODE="platform"
$env:EDUGRADE_SMOKE_USERNAME="platform_admin"
$env:EDUGRADE_SMOKE_PASSWORD="<temporary-shell-secret>"
try {
  .\scripts\smoke-test.ps1
} finally {
  Remove-Item Env:EDUGRADE_SMOKE_PASSWORD -ErrorAction SilentlyContinue
}
```

`/health/live` 是匿名存活探针；`/health/ready` 是匿名但脱敏的依赖就绪探针。详细依赖错误、Worker 影响和构建版本只通过受 `system:read` 保护的 `/api/v1/system/status` 提供。旧 `/ready` 仅兼容保留。

## 8. OCR、图像质量与页面处理 Worker

先创建具备最小权限的 worker 账号，并在 `.env` 配置对应 tenant、username、password。

```powershell
.\scripts\init.ps1 -SkipBuild -EnableQuality
.\scripts\init.ps1 -SkipBuild -EnableOcr
.\scripts\init.ps1 -SkipBuild -EnableProcessing
```

OCR 首次构建会下载 PaddleOCR/PaddlePaddle 依赖和模型，耗时及磁盘占用显著增加。没有配置 worker 凭据时不得启用 profile。

Ryzen 7 5800H / 16 GB 本地压测可执行 `scripts/start-local-ocr-fast.ps1`（或 `.sh`）。
该脚本保留 PostgreSQL、Redis、MinIO、API Gateway，只请求启动一个 `ocr-worker`，不主动启动
`math-recognition-worker`；已运行的其他 worker 不会被脚本停止。压测时需记录其他负载。
本机实际 CPU 为 Ryzen 5 5500U，5800H 仍需在目标机复测。当前候选配置为 `EDUGRADE_OCR_CPU_THREADS=4`、
`EDUGRADE_OCR_WORKER_CPU_LIMIT=4.0`、`EDUGRADE_OCR_BATCH_SIZE=1`；先运行
`benchmark_local.py --run-id local-baseline-v1` 再比较优化 profile。HPI 依赖通过
`EDUGRADE_OCR_INSTALL_PADDLEOCR_HPI` 独立 build arg 控制，默认关闭。

## 9. 镜像仓库与离线部署

基础镜像可通过以下变量替换为企业镜像仓库：

```text
EDUGRADE_GO_BUILD_IMAGE
EDUGRADE_API_RUNTIME_IMAGE
EDUGRADE_NODE_BUILD_IMAGE
EDUGRADE_WEB_RUNTIME_IMAGE
EDUGRADE_AI_PYTHON_IMAGE
EDUGRADE_OCR_PYTHON_IMAGE
EDUGRADE_QUALITY_PYTHON_IMAGE
EDUGRADE_PAGE_PROCESSING_PYTHON_IMAGE
```

联网机器准备离线包：

```powershell
docker pull golang:1.26.6-alpine
docker pull alpine:3.22
docker pull node:24-alpine
docker pull nginx:1.29-alpine
docker pull python:3.12.13-alpine3.24
docker pull python:3.11.15-slim-trixie
docker save -o edugrade-base-images.tar golang:1.26.6-alpine alpine:3.22 node:24-alpine nginx:1.29-alpine python:3.12.13-alpine3.24 python:3.11.15-slim-trixie
```

离线机器导入：

```powershell
docker load -i edugrade-base-images.tar
.\scripts\preflight.ps1 -RequireApplicationBaseImages
```

镜像拉取失败时先检查 DNS、代理、IPv4/IPv6 路由和企业 registry；不要通过关闭 API 安全配置解决网络问题。

## 10. 备份

```powershell
.\scripts\backup.ps1
```

脚本进入维护窗口：识别当前 Compose 项目实际运行的容器，停止除 PostgreSQL、Redis、MinIO、Qdrant 外的服务（包括各 profile 的 worker 和后续新增服务），再执行数据库快照和对象复制。成功或失败后，`finally` 都只重启原先运行的容器；原先停止的服务不会被启动。维护期间业务会暂时不可用，不得另行运行 `compose up/start` 或同时执行另一份备份/恢复。

该维护边界仅覆盖当前 Compose 项目。备份前须停止项目外的数据库/S3 写入任务、直接连接数据库的工具、外部 worker 和对象生命周期删除规则；脚本不能冻结这些外部写入方。若部署存在这些来源，需先在其运行平台停写后再进入此流程。

每次备份生成含时间戳和随机 ID 的独立目录，包含：

- PostgreSQL custom dump
- MinIO mirror 目录
- 版本 3 的 `manifest-*.json`，绑定唯一 dump、MinIO 目录与引用清单
- `file-references-*.json`，记录未删除且为 `active` / `orphan_recovered` 的文件引用、bucket、key、字节数和 SHA-256
- 文件 SHA-256
- 已应用 migration 数量
- 当前 Compose 镜像 ID（包含为备份暂时停止的应用容器）和维护停写证据

脚本在容器内生成并用 `pg_restore --list` 校验 dump，再通过 `docker cp` 复制到主机，避免二进制数据经过 PowerShell 文本管道。随后逐条校验数据库有效文件引用对应的实际对象大小和 SHA-256；缺失、损坏或指向其他 bucket 时拒绝生成可用清单。进行中的上传、删除和隔离记录保留在数据库中，由恢复后的业务流程继续处理，不纳入已持久化对象承诺。

旧版备份没有维护停写和引用证据，本流程会拒绝其清单；升级后应重新备份。不要手工把旧清单版本改成 3。

备份目录包含敏感数据，必须限制 ACL，并在复制到外部介质时加密。

## 11. 隔离恢复验证

不要先覆盖主数据库。优先选择一份已通过校验的备份，恢复到隔离验证库和隔离 bucket：

```powershell
$backup = Get-ChildItem .\backups -Directory | Sort-Object LastWriteTime -Descending | Select-Object -First 1
$verified = .\scripts\verify-backup.ps1 -BackupDirectory $backup.FullName | ConvertFrom-Json
.\scripts\restore.ps1 `
  -PostgresDump $verified.postgres_path `
  -TargetDatabase edugrade_restore_verify `
  -TargetBucket edugrade-restore-verify `
  -CreateTargetDatabase `
  -ConfirmRestore
```

验证：

```powershell
docker compose --env-file .env -f docker-compose.yml exec -T postgres `
  psql -U edugrade -d edugrade_restore_verify -c "select count(*) from schema_migration"
```

脚本在任何停服、删库或建库操作前验证完整清单、所有文件的大小/哈希，并确认传入 dump 和对象目录正是该清单绑定的路径。`-MinioBackupDirectory` 可省略，由清单选择；显式指定其他目录会失败。

恢复时同样进入 Compose 维护窗口。对象复制完成后，在事务中将数据库中源 bucket 的文件引用重映射到目标 bucket；只暂时停用题库文件不可变保护触发器，并保留、恢复其原始启用模式，其他约束和触发器仍生效。随后读取恢复库的实际引用，并重新下载目标 bucket 中的对象验证大小与 SHA-256，全部通过才报告成功。

覆盖主数据库需额外提供 `-AllowPrimaryDatabase`，覆盖主 bucket 需 `-AllowPrimaryBucket`；生产环境还需 `-AllowProductionRestore`。保留升级前备份并确认回退窗口。数据库与对象存储之间没有跨系统事务；若主库/主 bucket 恢复已开始改动数据却未完成验证，脚本保留维护停服状态，并输出原运行容器的 `docker start` 命令。检查错误并重新执行完整恢复，在完整验证通过后再人工启动这些容器。若目标库和 bucket 均隔离，或失败发生在改动数据前，则自动恢复原运行集合。

完整自动演练优先使用：

```powershell
.\scripts\verify-backup.ps1 -BackupDirectory $backup.FullName
.\scripts\restore-drill.ps1 -BackupDirectory $backup.FullName
```

演练恢复到随机隔离数据库和 bucket，验证 migration、租户关系、数据库文件引用与实际对象字节/哈希后默认清理。`-KeepDrillData` 可保留演练数据供人工检查。建议预生产 RPO 不超过 24 小时、RTO 不超过 4 小时；正式考试窗口应缩短备份周期，并以演练实测值替代建议值。

脚本回归测试（PowerShell 7）：

```powershell
.\scripts\tests\recovery.tests.ps1
# 使用可创建临时数据库的独立 PostgreSQL 测试实例：
.\scripts\tests\recovery-postgres.tests.ps1 -PostgresTestURL $env:EDUGRADE_RECOVERY_TEST_DATABASE_URL
# 可选：使用真实 Docker，自动创建和清理唯一项目、临时卷与合成数据，不接触已有部署。
.\scripts\tests\recovery-docker.tests.ps1
```

## 12. 升级与回滚

升级前：

1. 运行 preflight。
2. 执行 backup 并完成隔离恢复验证。
3. 记录当前 `.env`、镜像 ID 和 manifest。
4. 构建或导入目标镜像。
5. 执行 init，让 migration runner 只应用新增 SQL。
6. 执行 authenticated smoke 和核心业务验收。

当前仓库没有 down migration。升级失败时的回滚方式是：停止新镜像、恢复升级前 PostgreSQL/MinIO 备份、恢复原 `.env` 和镜像 tag、重新执行 smoke。不能只回退应用镜像而保留不兼容的新 schema。

## 13. TLS 与正式环境

正式环境不得直接暴露本 Runbook 的 HTTP 端口。由企业 Nginx、负载均衡或零信任网关终止 TLS，并验证：

- TLS 1.2/1.3 和完整证书链。
- HTTP 到 HTTPS 重定向。
- `X-Forwarded-Proto=https`。
- session cookie 包含 Secure、HttpOnly 和适当 SameSite。
- API 与 Web 同源，CORS 只允许真实域名，并允许 Web 必需的 `X-EduGrade-CSRF` 请求头。
- PostgreSQL、MinIO 和备份链路满足组织内部加密要求。

真实证书与私钥不进入仓库。

## 14. 故障定位

查看状态：

```powershell
docker compose --env-file .env -f docker-compose.yml ps
```

查看单服务日志：

```powershell
docker compose --env-file .env -f docker-compose.yml logs --tail 200 api-gateway
```

常见问题：

- Qdrant unhealthy：确认当前 Compose 已使用 `/proc/net/tcp` 监听检查；旧配置依赖不存在的 `wget`。
- MinIO init 输出 `mc` help：确认 `command` 是单元素 shell script，运行 `config --format json` 检查。
- migration 提示 baseline：只在已核实旧 schema 后使用一次 baseline 参数。
- migration checksum mismatch：已应用 SQL 被修改，恢复原文件并新增 migration。
- API 拒绝 unsafe production configuration：按错误逐项修复 cookie、DSN、MinIO 和 CORS，不要改代码绕过。
- Docker Hub timeout：使用基础镜像变量切换企业 mirror，或 `docker save/load` 离线导入。
- `8088` 不通：先验证 nginx、web-admin 和 api-gateway 都为 healthy。

## 15. 预生产验收记录

每次发布至少留存：

- preflight 输出。
- Compose `ps` 状态。
- migration apply/skip 输出和 migration 数量。
- MinIO private bucket 初始化输出。
- anonymous + authenticated smoke 输出。
- backup manifest/hash。
- 隔离 restore 的 migration、tenant、user 数量验证。
- 已知风险、未执行项和发布审批人。

生产控制、能力降级、指标告警、供应链、错误模型和安全测试边界见 [生产安全与可靠性控制](../architecture/production-security-and-reliability.md)。
