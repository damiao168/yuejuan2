# EduGrade Docker Compose 私有化部署

本目录提供本地私有化与预生产验收环境。完整升级、备份和恢复门禁见 [`docs/deployment/preproduction-runbook.md`](../../docs/deployment/preproduction-runbook.md)。

默认长期服务：

- `api-gateway`、`web-admin`、`nginx`
- `postgres`、`redis`、`minio`、`qdrant`
- 内网 `grading-agent`

工具和可选服务：`db-migrate`、`minio-init`、OCR/图像质量/页面处理 Worker、Prometheus、Grafana。

## 智能阅卷边界

`grading-agent` 只监听 Compose 内网 `8100`，不映射宿主机端口，也不经过 Nginx 暴露。浏览器只能调用 API 网关。服务通过 `host.docker.internal` 访问宿主机 llama.cpp `8087`；`/health` 表示服务存活，`/ready` 表示模型运行时可用。

当前模型仍为影子建议：教师必须复核，作文和论述题仅保存影子结果，不允许自动发布最终成绩。

## 准备配置

```powershell
Set-Location infra\docker-compose
Copy-Item .env.example .env
```

至少替换：PostgreSQL、Redis、MinIO Root 与应用账号、Grafana 密码，`EDUGRADE_QDRANT_API_KEY`，以及长度不少于 32 字符且彼此不同的 `EDUGRADE_GRADING_AGENT_TOKEN`、`EDUGRADE_MATH_VERIFY_TOKEN`。同时通过下文的同步脚本写入本地模型 `EDUGRADE_GRADING_MODEL_API_KEY`。令牌只放在未提交的 `.env`，每个内部服务边界使用独立令牌。

### 从既有部署升级

`.env` 不随仓库更新，升级后需要手工补齐以下新增项，否则 `preflight.ps1` 会直接拒绝启动。预检还会核对 `EDUGRADE_AI_PROMPT_VERSION` 与受治理提示词清单的版本，避免旧配置让评分服务反复重启：

- `EDUGRADE_QDRANT_API_KEY`：**必填**。Qdrant 此前无鉴权，现在容器会读取该值；网关侧用同一个值发送 `api-key` 头，两端必须一致。
- `EDUGRADE_REDIS_USERNAME` / `EDUGRADE_REDIS_PASSWORD`：API 使用独立 ACL 用户；容器关闭 Redis 默认用户，并拒绝空用户名或空密码启动。应用 ACL 禁止管理类和危险命令。
- production-like 环境必须设置 `EDUGRADE_REDIS_TLS_ENABLED=true`。将 Redis 服务证书按 `server.crt` / `server.key` 与签发 CA 的 `ca.crt` 放入 `EDUGRADE_REDIS_CERTS_DIR`，另将 CA 的 `ca.crt` 放入 `EDUGRADE_REDIS_CA_DIR`；`EDUGRADE_REDIS_TLS_CA_FILE` 应指向容器内 `/run/secrets/redis-ca/ca.crt`。服务证书必须包含内部 DNS 名 `redis`（或同步调整 `EDUGRADE_REDIS_TLS_SERVER_NAME`）。
- `EDUGRADE_POSTGRES_APP_USER` / `EDUGRADE_POSTGRES_APP_PASSWORD`：API 专用低权限登录账号；不能与迁移账号相同，密码至少 32 字符。
- `EDUGRADE_POSTGRES_ADMIN_DSN`：仅供 `db-migrate` 使用的迁移账号 DSN。该账号必须能创建/修改角色并授予成员关系（自托管 PostgreSQL 通常需要 `CREATEROLE`）；托管数据库若限制角色管理，需由平台管理员预先创建同等账号与授权。`EDUGRADE_POSTGRES_DSN` 必须改为上述 API 账号的 DSN，不能继续让 API 以 PostgreSQL superuser 或 `BYPASSRLS` 身份连接。
- `EDUGRADE_MINIO_ROOT_USER` / `EDUGRADE_MINIO_ROOT_PASSWORD`：只注入 MinIO 和 `minio-init`/备份恢复工具，不注入 API。
- `EDUGRADE_MINIO_APP_ACCESS_KEY` / `EDUGRADE_MINIO_APP_SECRET_KEY`：由 `minio-init` 创建并绑定到 `EDUGRADE_FILE_BUCKET` 的读写删与列举策略，API 只使用这组凭据。升级既有部署后应立即轮换旧 Root 凭据。
- production-like 环境必须设置 `EDUGRADE_MINIO_USE_SSL=true`。将 MinIO 服务证书按 `public.crt` / `private.key` 放入 `EDUGRADE_MINIO_CERTS_DIR`，将签发 CA 的 PEM 文件放入 `EDUGRADE_MINIO_CA_DIR`，并让 `EDUGRADE_MINIO_TLS_CA_FILE` 指向容器内的 `/run/secrets/minio-ca/ca.crt`。证书必须包含内部 DNS 名 `minio`（或同步调整 `EDUGRADE_MINIO_TLS_SERVER_NAME`）。证书目录已被 Git 忽略，禁止提交私钥。
- production-like 环境的 Worker/AI 链路必须启用内部 TLS。把内部 CA 放到 `EDUGRADE_INTERNAL_CA_DIR/ca.crt`，并分别为 `api-gateway`、`grading-agent`、`math-verification-worker` 签发含对应 Docker DNS SAN 的证书。API 证书放在 `EDUGRADE_API_TLS_CERTS_DIR`，Grading Agent 与数学验证证书放在各自的 `*_TLS_CERTS_DIR`；容器内证书/私钥路径写入对应 `*_TLS_CERT_FILE` / `*_TLS_KEY_FILE`。设置 API 内部监听端口（建议 `EDUGRADE_INTERNAL_TLS_PORT=8443`），并将 `EDUGRADE_INTERNAL_API_BASE_URL`、`EDUGRADE_AI_SERVICE_URL`、`EDUGRADE_INTERNAL_MATH_VERIFY_BASE_URL` 和两个内部健康 URL 改为 `https://`。远端模型 URL 在 production-like 环境也必须使用 HTTPS；只有同一进程网络命名空间内的 loopback 模型例外。内部 CA 和服务私钥目录均已被 Git 忽略。

以下新增项都有默认值，不填也能启动：`EDUGRADE_INTERNAL_BIND_HOST`（默认 `127.0.0.1`，数据面端口只监听回环，仅 nginx 对外）、`EDUGRADE_CONTAINER_LOG_MAX_*`、各 `EDUGRADE_*_MEM_LIMIT`、`EDUGRADE_PAGE_PROCESSING_HEARTBEAT_*`。若需要从其他机器直连数据库或 MinIO 控制台，显式设置 `EDUGRADE_INTERNAL_BIND_HOST=0.0.0.0`（生产环境 preflight 会拒绝该值）。

production-like 部署还必须设置 `EDUGRADE_POSTGRES_TENANT_RLS=true`，并把 `.env.example` 中所有运行时镜像和 Dockerfile 基础镜像变量替换为 `registry/repository@sha256:<digest>`。启用 RLS 时，API 建立数据库连接会主动拒绝 `SUPERUSER` 或 `BYPASSRLS` 登录；迁移脚本创建无登录的 `edugrade_tenant_runtime` 权限角色，并把 API 账号加入该角色。本地开发仍可使用 tag；`preflight.ps1` 会在生产环境拒绝 tag、空值和格式不完整的 digest。应用镜像的正式 digest 来自 `Release immutable images` 工作流产物，不能从人类可读 tag 推断。

API 的 PostgreSQL 容量保护默认值为：最大连接 `10`、最大空闲连接 `5`、连接最长生命周期 `30m`、空闲回收 `5m`、单条语句超时 `60s`、锁等待超时 `5s`。可通过 `EDUGRADE_POSTGRES_MAX_*`、`EDUGRADE_POSTGRES_CONN_MAX_*`、`EDUGRADE_POSTGRES_STATEMENT_TIMEOUT` 和 `EDUGRADE_POSTGRES_LOCK_TIMEOUT` 调整；非法范围会使 API 启动失败。调整连接数前必须结合 PostgreSQL `max_connections`、API 副本数和后台工具连接预算，默认值不是容量验收结论。

模型运行参数：

```text
EDUGRADE_GRADING_MODEL_BASE_URL=http://host.docker.internal:8087/v1
EDUGRADE_AI_MODEL_VERSION=Qwen/Qwen3-4B-GGUF:Q4_K_M
EDUGRADE_AI_PROMPT_VERSION=subjective-governed-cn-subject-routing-v6
```

## 校验与初始化

```powershell
.\scripts\preflight.ps1
.\scripts\init.ps1
```

按需启用可选服务（账号密码必须先写入 `.env`）：

```powershell
.\scripts\init.ps1 -SkipBuild -EnableOcr
.\scripts\init.ps1 -SkipBuild -EnableQuality
.\scripts\init.ps1 -SkipBuild -EnableProcessing
.\scripts\init.ps1 -SkipBuild -EnableObservability
```

### 公式运行时标定（不同电脑分别执行）

公式模型生命周期和 batch 不再根据固定 RAM 阈值猜测。OCR 镜像首次部署或 CPU/GPU、容器资源限制、PaddleOCR/模型版本变化后，执行：

```powershell
.\scripts\calibrate-formula-runtime.ps1
```

脚本会在当前部署机上实测 batch `1/2/4/8`，逐轮输出当前 batch、重复轮次和已完成轮次，校验各 batch 输出与 batch 1 一致，把带硬件/软件指纹的画像写入持久化 `ocr_model_cache`，然后重启公式 Worker。默认 `compatibility` 模式会在连续任务队列排空后释放模型；明确要求降低冷启动延迟且已为常驻模型预留资源时，才使用：

```powershell
.\scripts\calibrate-formula-runtime.ps1 -LifecycleGoal latency
```

可用匿名化的真实公式 ROI 替换内置性能样本：

```powershell
.\scripts\calibrate-formula-runtime.ps1 -SampleDirectory D:\formula-calibration-rois
```

标定只验证部署性能和不同 batch 的输出一致性，不冒充公式绝对准确率评测。公式/ROI 准确率仍须使用人工标注的项目验证集。设计依据和证据边界见 [`docs/ocr-formula-ai-evidence-review-2026-09-12.md`](../../docs/ocr-formula-ai-evidence-review-2026-09-12.md)。模型权重和画像都在持久缓存中；重启 Worker 不会重新下载模型。

本地 Docker Desktop 环境默认把 PaddleOCR/公式模型放在仓库根目录的
`.cache/ocr-models`（约需数 GB，已加入 `.gitignore`），而不是 Docker 的内部卷。
因此执行 `down`、重建容器或更新业务代码都不会重新下载模型。可在 `.env` 中通过
`EDUGRADE_OCR_MODEL_CACHE_DIR` 改到其他磁盘；相对路径从
`infra/docker-compose` 解析。

日常从仓库根目录启动时使用：

```powershell
powershell -ExecutionPolicy Bypass -File scripts\start-local.ps1
```

该命令复用已有镜像，不会无条件构建。依赖或源码确实发生变化时才执行：

```powershell
powershell -ExecutionPolicy Bypass -File scripts\start-local.ps1 -Build
```

`-Build` 只重建日常修改的 `api-gateway`、`grading-agent` 和
`web-admin`，不会重建或重新下载体积较大的 PaddleOCR 镜像。只有修改
Worker 时才显式执行：

```powershell
# 重建全部 Worker（包括 OCR）
powershell -ExecutionPolicy Bypass -File scripts\start-local.ps1 -Build -BuildWorkers

# 只重建一个指定服务
powershell -ExecutionPolicy Bypass -File scripts\start-local.ps1 -Build -BuildService ocr-worker
```

等价手工命令：

```powershell
docker compose --env-file .env -f docker-compose.yml up -d postgres redis minio qdrant
docker compose --env-file .env -f docker-compose.yml --profile tools run --rm db-migrate
docker compose --env-file .env -f docker-compose.yml --profile tools run --rm minio-init
docker compose --env-file .env -f docker-compose.yml up -d --build grading-agent api-gateway web-admin nginx
```

## 访问地址

- Web：`http://127.0.0.1:8088`
- API：`http://127.0.0.1:8080`
- MinIO Console：`http://127.0.0.1:9001`
- Qdrant：`http://127.0.0.1:6333`

没有公开的 grading-agent 地址。需要诊断时使用：

```powershell
docker compose --env-file .env -f docker-compose.yml exec grading-agent wget -q -O - http://127.0.0.1:8100/health
docker compose --env-file .env -f docker-compose.yml exec grading-agent wget -q -O - http://127.0.0.1:8100/ready
```

## 常用运维

```powershell
docker compose --env-file .env -f docker-compose.yml ps
docker compose --env-file .env -f docker-compose.yml logs -f api-gateway grading-agent
.\scripts\smoke-test.ps1
.\scripts\backup.ps1
```

停止服务不会删除数据：

```powershell
docker compose --env-file .env -f docker-compose.yml down
```

## 本地 Lab 模型集成

`grading-agent` 容器通过 `host.docker.internal:8087` 调用 Lab 固定的 llama.cpp 运行时。Lab 启动脚本会生成运行时 API key，因此在 preflight 前必须把它同步到私有 Compose `.env`：

```powershell
powershell -ExecutionPolicy Bypass -File ..\..\lab\scripts\prepare-local-runtime.ps1
powershell -ExecutionPolicy Bypass -File ..\..\lab\scripts\start-local-server.ps1
.\scripts\sync-local-grading-model-key.ps1
.\scripts\preflight.ps1
.\scripts\init.ps1
```

密钥只写入已忽略的 `.env`，不得提交或放进前端配置。`grading-agent` 保持内网服务，只返回待教师复核的建议，不能发布最终成绩。

`down -v` 会永久删除当前 Compose 项目的数据库和对象存储卷，只能在确认目标项目后用于一次性环境。日常升级不得执行。

恢复命令（默认恢复到隔离的验证库；覆盖主库需显式加 `-AllowPrimaryDatabase`）：

```powershell
.\scripts\restore.ps1 -PostgresDump .\backups\postgres-YYYYMMDD-HHMMSS.dump -TargetDatabase edugrade_restore_check -CreateTargetDatabase -ConfirmRestore
```

## 模型启动

仓库不提交 GGUF 和 llama.cpp 二进制。实验机已按 `lab/config/local-runtime.json` 准备时，可在仓库根目录运行：

```powershell
powershell -ExecutionPolicy Bypass -File lab\scripts\start-local-server.ps1 -Candidate qwen3_4b
```

日志写入忽略文件 `lab/.runtime/llama-server.stdout.log` 和
`lab/.runtime/llama-server.stderr.log`。停止模型：

```powershell
powershell -ExecutionPolicy Bypass -File lab\scripts\stop-local-server.ps1
```
