# EduGrade Enterprise

> [!IMPORTANT]
> ## Source Code Notice / 源代码使用声明
>
> 本项目为个人开发的**专有软件（Proprietary Software）**，不是开放源代码软件（Open Source Software）。
>
> 本仓库公开源代码仅用于**项目展示、个人学习、教育研究和技术参考**。源代码公开不代表授予任何开源许可、部署许可、商业许可或其他软件使用许可。
>
> 未经版权所有者事先明确的书面授权，不得复制或将本项目代码用于其他项目，不得修改后发布，不得编译、运行、部署、托管、商用、机构使用、提供 SaaS / 私有化服务、再分发、转授权或制作并发布衍生软件。
>
> 你可以阅读代码并学习其中的通用技术思想、工程方法和架构设计，但“学习、研究或教育用途”不意味着获得复制、复用、部署或发布代码的权利。
>
> Copyright © 2026 damiao168. All Rights Reserved. 详细条款见 [`LICENSE`](./LICENSE)。

EduGrade Enterprise 是面向学校和教育机构的智能阅卷与学情分析平台。系统覆盖考试创建、试卷与评分标准配置、答题卡生成、答卷采集、OCR 与切题、客观题评分、主观题辅助阅卷、人工复核、仲裁、成绩发布、申诉及学习分析。

项目采用 monorepo 组织，支持 Web 管理后台、扫描工作站、Go 业务服务、Python Worker、内部阅卷 Agent 以及 Docker Compose 私有化部署。

> 当前 AI 能力遵循“模型提供建议，教师作出决定”的原则。模型结果不能绕过复核、质量门禁和发布流程直接成为最终成绩。

## 适用场景

- 学校统一考试、阶段测评、模拟考试和日常作业阅卷。
- 纸质答卷批量采集、页面质量检查、OCR、切题和客观题自动评分。
- 主观题按 Rubric 辅助评分，并支持单评、双评、复核和仲裁。
- 年级、班级和学生名单管理，以及考试冻结名册管理。
- 成绩确认、版本化发布、申诉、重评、审计和学情分析。
- 校内部署、数据不出校及本地模型接入。

## 核心能力

| 业务阶段 | 主要能力 |
| --- | --- |
| 考试准备 | 学校、学年、年级、班级和学生管理；分步骤创建考试；冻结考试名单 |
| 试卷配置 | 题目蓝图、试卷与答案 AI 解析、Question 对账、AnswerKey、Rubric、答题卡模板 |
| 答卷采集 | 受控答题卡、二维码身份、批量上传、页面质量检测、页面配准和异常处理 |
| 智能处理 | PaddleOCR、公式识别、答案区域切分、证据提取和处理状态追踪 |
| 阅卷管理 | 客观题规则评分、主观题建议、人工复核、双评、仲裁、回评和漂移监控 |
| 成绩发布 | 完整性检查、发布 Gate、不可变成绩快照、版本对比、申诉与重评 |
| 运营治理 | 权限控制、租户隔离、审计日志、模型治理、质量看板和系统状态 |

## 业务流程

```mermaid
flowchart LR
  A[组织与学生数据] --> B[创建考试并冻结名单]
  B --> C[配置试卷、答案与 Rubric]
  C --> D[制作并锁定答题卡模板]
  D --> E[采集答卷]
  E --> F[质量检查、OCR、配准与切题]
  F --> G[客观题评分 / 主观题辅助阅卷]
  G --> H[教师复核、双评与仲裁]
  H --> I[发布完整性检查]
  I --> J[成绩发布、申诉与分析]
```

系统中的考试名单、试题、评分标准、答题卡模板和成绩发布记录均具有明确的状态边界。考试进入 `ready` 后，正式试卷事实和答题卡布局将被冻结；成绩发布前必须通过名单、答卷、题目覆盖、复核和总分一致性检查。

## 开发与授权部署：Docker Compose

> [!WARNING]
> 以下部署步骤仅用于**项目维护者以及已获得版权所有者书面授权的开发者/部署方**。技术文档的公开不构成部署授权。
>
> 未经书面授权，请勿运行、部署或托管本系统，也不要将本系统用于个人实际业务、学校、教育机构、企业、生产环境、商业项目、SaaS 或其他服务。
>
> 如果你只是浏览本仓库进行学习和技术研究，无需执行以下部署步骤。所有行为均受 [`LICENSE`](./LICENSE) 约束。

### 推荐：一键安装与启动

电脑只需预先安装 Git 和 Docker Desktop。下载仓库后，在仓库根目录执行同一条命令：

```powershell
.\start-edugrade.cmd
```

首次运行时，脚本会自动完成以下工作：

1. 启动并等待 Docker Desktop，确认使用 Linux containers。
2. 从 Compose 样例创建 `infra/docker-compose/.env`，自动生成数据库、Redis、MinIO、Qdrant、内部服务和 Worker 随机密钥。
3. 下载并校验固定版本的 llama.cpp 与 Qwen3-4B 模型，启动本地模型并同步 API Key。
4. 执行部署预检、数据库迁移、MinIO 初始化、核心镜像构建和健康检查。
5. 只提示一次 `platform_admin` 管理员密码，创建管理员并完成带登录的 smoke test。
6. 自动打开 `http://127.0.0.1:8088`。

以后每天启动仍然执行同一条 `.\start-edugrade.cmd`；脚本会识别已经安装的环境，复用配置、数据、模型和镜像，不会重新生成密钥。首次安装需要下载约 2.4 GiB 的本地模型以及 Docker 镜像，实际耗时取决于网络。

常用参数：

```powershell
# 源码或依赖更新后重建核心镜像
.\start-edugrade.cmd -Build

# 启动完成后不自动打开浏览器
.\start-edugrade.cmd -NoBrowser

# 只显示本次会执行的模式，不改文件、不启动服务
.\start-edugrade.cmd -DryRun
```

默认一键流程先启动可登录、可管理的核心系统。OCR、图像质量、页面处理等 Worker 需要与平台服务账号匹配，不能用随机占位账号冒充；完成账号配置后按第 4.2 节启用。脚本参数和实现见 [`scripts/launch-local.ps1`](scripts/launch-local.ps1)。

下面保留完整手工流程，供排错、定制配置和运维审计使用。它面向一台新的 Windows 电脑，覆盖从下载代码到首次登录、健康检查和日常启停。命令均在 PowerShell 中执行；除非特别说明，路径都以仓库根目录为起点。生产或跨机器部署还必须继续阅读[私有化部署说明](infra/docker-compose/README.md)和[预生产 Runbook](docs/deployment/preproduction-runbook.md)。

### 1. 部署前准备

#### 1.1 电脑配置

| 项目 | 最低要求 | 建议 |
| --- | --- | --- |
| 操作系统 | 64 位 Windows 10/11 | 安装最新系统更新并启用 CPU 虚拟化；Windows Server 部署见预生产 Runbook |
| CPU | x64 | 8 核以上；仓库提供的本地 llama.cpp 运行时是 Windows x64 CPU 版 |
| 内存 | 16 GB | 同时启用 OCR、公式识别、页面处理和本地模型时使用 32 GB 以上 |
| 可用磁盘 | 能容纳镜像、模型和业务数据 | 建议至少预留 100 GB；扫描件和 OCR 模型会持续占用空间 |
| 网络 | 能访问 GitHub、Docker Hub、PyPI 和模型下载地址 | 首次构建和下载模型时保持稳定网络 |

本地 Qwen3-4B 模型约 2.33 GiB，llama.cpp 压缩包约 17.4 MiB；PaddleOCR/公式模型和 Docker 镜像还需要数 GB。生产数据量不应按上述开发机容量估算。

#### 1.2 必装软件

- [Git for Windows](https://git-scm.com/download/win)。
- [Docker Desktop](https://docs.docker.com/desktop/setup/install/windows-install/)，切换到 Linux containers，并确保 WSL 2 或 Hyper-V 后端可以正常运行。
- PowerShell 7，或 Windows 自带的 Windows PowerShell 5.1。

只按 Docker Compose 部署时，宿主机**不需要**另行安装 Node.js、Go、Python、PostgreSQL、Redis、MinIO 或 Qdrant；这些组件由 Docker 镜像提供。只有进行源码开发时才需要 README 后面的开发工具链。

安装后打开 Docker Desktop，等待状态变为 Running，再在 PowerShell 中逐项检查：

```powershell
git --version
$PSVersionTable.PSVersion
docker version
docker compose version
docker info --format 'OSType={{.OSType}} Architecture={{.Architecture}}'
```

`docker version` 必须同时显示 Client 和 Server，最后一条应显示 `OSType=linux`。如果只有 Client、提示无法连接 daemon，或显示 Windows containers，请先修复 Docker Desktop，再继续。

#### 1.3 端口和安全边界

默认使用以下宿主机端口：

| 端口 | 服务 | 何时需要 |
| --- | --- | --- |
| 8088 | EduGrade Web / Nginx | 必需 |
| 8080 | API Gateway | 必需 |
| 5432、6379 | PostgreSQL、Redis | 必需，默认只绑定 `127.0.0.1` |
| 9000、9001 | MinIO API、Console | 必需，默认只绑定 `127.0.0.1` |
| 6333、6334 | Qdrant HTTP、gRPC | 必需，默认只绑定 `127.0.0.1` |
| 8087 | 宿主机 llama.cpp | 使用仓库内置本地模型时必需 |
| 9090、3000 | Prometheus、Grafana | 启用可观测性时需要 |

检查这些端口是否已被占用：

```powershell
$ports = 3000,5432,6333,6334,6379,8080,8087,8088,9000,9001,9090
Get-NetTCPConnection -State Listen -ErrorAction SilentlyContinue |
  Where-Object { $_.LocalPort -in $ports } |
  Sort-Object LocalPort |
  Format-Table LocalAddress,LocalPort,OwningProcess
```

如有冲突，先停止占用程序，或在后续 `infra/docker-compose/.env` 中修改对应的 `EDUGRADE_*_PORT`。本机部署不要把 `EDUGRADE_INTERNAL_BIND_HOST` 改成 `0.0.0.0`；否则数据库、对象存储等内部端口可能暴露到局域网。

### 2. 获取代码并创建部署配置

> 以下命令仅说明项目维护和经授权开发场景下的技术流程，不代表授予复制、运行、部署或其他使用权。

```powershell
Set-Location $HOME
git clone --branch main --single-branch https://github.com/damiao168/yuejuan2.git
Set-Location $HOME\yuejuan2
git status --short --branch
git remote get-url origin
git rev-parse HEAD
git log -1 --oneline
```

以上命令默认把仓库下载到当前用户目录下的 `yuejuan2`。如果改用其他目录，后文的 `Set-Location $HOME\yuejuan2` 也要替换成实际路径。已有仓库应使用 `git pull --ff-only origin main` 更新，不要再次 clone 到同名目录。

本安装说明对应 `damiao168/yuejuan2`；`damiao168/yuejuan` 是另一个仓库，不能用它替代本项目代码。部署时记录 `git rev-parse HEAD` 输出并与发布记录中的提交号核对；若使用指定版本，先切换到经验证的发布标签或提交再执行后续命令。

为部署创建独立配置：

```powershell
Set-Location $HOME\yuejuan2\infra\docker-compose
Copy-Item .env.example .env
notepad .env
```

这里必须使用 `infra/docker-compose/.env.example`。仓库根目录的 `.env.example` 是 API 源码直跑配置，不能拿来启动 Compose。`.env` 已被 Git 忽略，不得改名后提交。

#### 2.1 生成随机密钥

下面的函数兼容 PowerShell 5.1/7，生成只含十六进制字符的 64 位密钥，放入 PostgreSQL DSN 时不需要额外 URL 编码。命令会把密钥显示在当前终端，请只在受信任的本机执行：

```powershell
function New-EduGradeSecret {
  $bytes = New-Object byte[] 32
  $rng = [Security.Cryptography.RandomNumberGenerator]::Create()
  try { $rng.GetBytes($bytes) } finally { $rng.Dispose() }
  -join ($bytes | ForEach-Object { $_.ToString('x2') })
}

$postgresAdmin = New-EduGradeSecret
$postgresApp = New-EduGradeSecret

"EDUGRADE_POSTGRES_PASSWORD=$postgresAdmin"
"EDUGRADE_POSTGRES_APP_PASSWORD=$postgresApp"
"EDUGRADE_POSTGRES_ADMIN_DSN=postgres://edugrade:$postgresAdmin@postgres:5432/edugrade?sslmode=disable"
"EDUGRADE_POSTGRES_DSN=postgres://edugrade_app:$postgresApp@postgres:5432/edugrade?sslmode=disable"
"EDUGRADE_REDIS_PASSWORD=$(New-EduGradeSecret)"
"EDUGRADE_MINIO_ROOT_PASSWORD=$(New-EduGradeSecret)"
"EDUGRADE_MINIO_APP_SECRET_KEY=$(New-EduGradeSecret)"
"EDUGRADE_QDRANT_API_KEY=$(New-EduGradeSecret)"
"EDUGRADE_BARCODE_HMAC_KEYS=local-v1:$(New-EduGradeSecret)"
"EDUGRADE_GRADING_AGENT_TOKEN=$(New-EduGradeSecret)"
"EDUGRADE_MATH_VERIFY_TOKEN=$(New-EduGradeSecret)"
"EDUGRADE_GRAFANA_ADMIN_PASSWORD=$(New-EduGradeSecret)"
"EDUGRADE_OCR_WORKER_PASSWORD=$(New-EduGradeSecret)"
"EDUGRADE_IMAGE_QUALITY_PASSWORD=$(New-EduGradeSecret)"
"EDUGRADE_PAGE_PROCESSING_PASSWORD=$(New-EduGradeSecret)"
"EDUGRADE_SUBJECTIVE_WORKER_PASSWORD=$(New-EduGradeSecret)"
```

把输出逐项复制到 `.env` 的同名配置。PostgreSQL 的两个密码必须同时更新对应 DSN；`EDUGRADE_GRADING_AGENT_TOKEN` 与 `EDUGRADE_MATH_VERIFY_TOKEN` 必须不同。

#### 2.2 必须检查的配置

| 配置 | 本机部署要求 |
| --- | --- |
| `EDUGRADE_ENV` | 保持 `local` |
| `EDUGRADE_POSTGRES_*` | 替换管理密码、应用密码，并同步更新两个 DSN |
| `EDUGRADE_REDIS_PASSWORD` | 替换示例值；用户名保持 `edugrade-api` 即可 |
| `EDUGRADE_MINIO_ROOT_*` | Root 只用于初始化/运维，至少替换密码；生产环境同时替换示例用户名 |
| `EDUGRADE_MINIO_APP_*` | API 最小权限账号，Secret 必须替换并与 Root 凭据不同 |
| `EDUGRADE_QDRANT_API_KEY` | 至少 32 个字符 |
| `EDUGRADE_BARCODE_HMAC_KEYS` | 保留 `local-v1:` 前缀，替换冒号后的密钥 |
| `EDUGRADE_GRADING_AGENT_TOKEN`、`EDUGRADE_MATH_VERIFY_TOKEN` | 各至少 32 个字符，且不能相同 |
| `EDUGRADE_GRAFANA_ADMIN_PASSWORD` | 即使暂不启用 Grafana，也建议先替换 |
| `EDUGRADE_*_WORKER_PASSWORD` | 启用对应 Worker 前填入非空强密码，并确保用户名/密码与平台内的服务账号一致 |
| `EDUGRADE_GRADING_MODEL_API_KEY` | 使用仓库本地模型时先留空，下一步由同步脚本写入 |

Worker 密码包括 `EDUGRADE_OCR_WORKER_PASSWORD`、`EDUGRADE_IMAGE_QUALITY_PASSWORD`、`EDUGRADE_PAGE_PROCESSING_PASSWORD` 和 `EDUGRADE_SUBJECTIVE_WORKER_PASSWORD`。核心系统可先不启动这些可选 profile；首次登录后再按组织与用户管理流程创建相应服务账号并启用 Worker。

保存后可确认 Git 不会跟踪密钥文件：

```powershell
Set-Location $HOME\yuejuan2
git check-ignore -v infra/docker-compose/.env
git status --short
```

输出应表明 `.env` 被 `.gitignore` 忽略。任何密码、API Key 或真实学生数据都不得提交到仓库。

### 3. 准备并启动本地阅卷模型

仓库不提交模型权重或 llama.cpp 二进制。首次部署在仓库根目录执行准备脚本；它会下载固定版本、核对文件大小和 SHA-256，然后解压到已忽略的 `lab/.runtime` 和 `lab/.models`：

```powershell
Set-Location $HOME\yuejuan2
powershell -ExecutionPolicy Bypass -File lab\scripts\prepare-local-runtime.ps1
```

下载完成后启动 `qwen3_4b`。脚本会在后台启动 llama.cpp、生成随机 API Key，并等待健康检查通过：

```powershell
powershell -ExecutionPolicy Bypass -File lab\scripts\start-local-server.ps1 -Candidate qwen3_4b
Invoke-RestMethod http://127.0.0.1:8087/health
```

健康响应的 `status` 应为 `ok`。模型服务必须保持运行；Docker 内的 `grading-agent` 通过 `host.docker.internal:8087` 访问它。

把脚本生成的 API Key 同步到 Compose 配置：

```powershell
Set-Location $HOME\yuejuan2\infra\docker-compose
.\scripts\sync-local-grading-model-key.ps1
$modelKeySetting = Get-Content .env | Where-Object { $_ -match '^EDUGRADE_GRADING_MODEL_API_KEY=' } | Select-Object -First 1
if ($modelKeySetting -notmatch '^EDUGRADE_GRADING_MODEL_API_KEY=.+$') { throw 'Model API key was not synchronized.' }
Write-Host 'EDUGRADE_GRADING_MODEL_API_KEY is configured.'
```

最后三行只确认该项非空，不会打印实际密钥。不要把 `.env` 截图、粘贴到 Issue 或提交到 Git。模型日志位于 `lab/.runtime/llama-server.stdout.log` 和 `lab/.runtime/llama-server.stderr.log`。需要停止模型时执行：

```powershell
Set-Location $HOME\yuejuan2
powershell -ExecutionPolicy Bypass -File lab\scripts\stop-local-server.ps1
```

如果使用自己的 OpenAI-compatible 服务，可以跳过本节的下载和启动命令，直接在 `.env` 中设置 `EDUGRADE_GRADING_MODEL_BASE_URL`、`EDUGRADE_GRADING_MODEL_API_KEY` 和 `EDUGRADE_GRADING_MODEL_NAME`。远程/生产服务必须使用 HTTPS，并在使用前完成质量评测。

### 4. 预检并初始化系统

先运行只读预检：

```powershell
Set-Location $HOME\yuejuan2\infra\docker-compose
.\scripts\preflight.ps1
```

预检会验证 Docker daemon、Compose 配置、必填变量、密钥长度和模型 API Key。看到 `EduGrade deployment preflight passed for environment 'local'.` 才继续。关于示例凭据的 warning 必须按第 2 节处理；缺少必填项或 Compose 配置无效则不能继续。

#### 4.1 首次初始化核心系统

平台管理员密码至少 15 个字符，且不能使用常见弱密码。以下写法不会把明文保存到 README、脚本或 `.env`：

```powershell
$secure = Read-Host 'Bootstrap password (at least 15 characters)' -AsSecureString
$ptr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secure)
try {
  $env:EDUGRADE_BOOTSTRAP_PASSWORD = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($ptr)
  .\scripts\init.ps1 -BootstrapAdmin
} finally {
  [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($ptr)
  Remove-Item Env:EDUGRADE_BOOTSTRAP_PASSWORD -ErrorAction SilentlyContinue
}
```

`init.ps1` 会依次执行预检、启动 PostgreSQL/Redis/MinIO/Qdrant、运行数据库迁移、初始化 MinIO、构建并启动 `grading-agent`、API、Web、Nginx、创建首个平台管理员并执行 smoke test。首次构建需要下载基础镜像，耗时取决于网络和电脑性能；迁移运行时不要关闭终端或 Docker Desktop。

如果命令因网络或构建失败中断，修复原因后可重新运行；迁移和 MinIO 初始化被设计为可重复执行。已经成功创建平台管理员后，不要再次添加 `-BootstrapAdmin`。

#### 4.2 启用 OCR、图像质量和页面处理

先确保 `.env` 中对应 Worker 的 tenant、username、password 与平台里的服务账号一致，再运行：

```powershell
Set-Location $HOME\yuejuan2\infra\docker-compose
.\scripts\init.ps1 -EnableOcr -EnableQuality -EnableProcessing
```

第一次执行不要加 `-SkipBuild`，因为 Worker 镜像可能尚不存在。以后只需复用已有镜像时可以执行：

```powershell
.\scripts\init.ps1 -SkipBuild -EnableOcr -EnableQuality -EnableProcessing
```

`-EnableOcr` 只启动主 `ocr-worker`。如需同时启动同一 `ocr` profile 下的数学识别、公式识别和数学验证服务，在完成对应配置后执行：

```powershell
docker compose --env-file .env -f docker-compose.yml --profile ocr up -d --build
```

OCR/公式模型会缓存在仓库根目录的 `.cache/ocr-models`，重启或重建容器不会重复下载；首次就绪可能明显慢于后续启动。

主观题异步 Worker 是独立 profile。在已经配置 `EDUGRADE_SUBJECTIVE_WORKER_*` 服务账号后启动：

```powershell
docker compose --env-file .env -f docker-compose.yml --profile subjective-grading up -d --build subjective-grading-worker
```

需要 Prometheus 和 Grafana 时执行：

```powershell
.\scripts\init.ps1 -SkipBuild -EnableObservability
```

不要为了省事在未配置服务账号时启用所有 profile；容器可能在运行，但 Worker 会因登录失败而无法消费任务。

### 5. 检查服务状态

```powershell
Set-Location $HOME\yuejuan2\infra\docker-compose
docker compose --env-file .env -f docker-compose.yml ps
.\scripts\smoke-test.ps1
```

核心服务应显示为 `running` 或 `healthy`，smoke test 最后应显示 `EduGrade smoke test passed.`。也可以直接检查三个公开端点：

```powershell
Invoke-WebRequest -UseBasicParsing http://127.0.0.1:8080/health/live
Invoke-WebRequest -UseBasicParsing http://127.0.0.1:8080/health/ready
Invoke-WebRequest -UseBasicParsing http://127.0.0.1:8088/health
```

如启用了本地阅卷模型，再检查容器内的模型就绪状态：

```powershell
docker compose --env-file .env -f docker-compose.yml exec grading-agent wget -q -O - http://127.0.0.1:8100/ready
```

默认访问地址如下：

| 服务 | 地址 | 用途 |
| --- | --- | --- |
| EduGrade Web | `http://127.0.0.1:8088` | 用户操作入口 |
| API Gateway | `http://127.0.0.1:8080` | API 与健康检查 |
| MinIO Console | `http://127.0.0.1:9001` | 文件存储管理，仅限运维人员 |
| Qdrant | `http://127.0.0.1:6333` | 向量服务，已启用 API Key，仅限运维诊断 |
| Grafana | `http://127.0.0.1:3000` | 可观测性，启用 profile 后可用 |

### 6. 首次登录

打开 `http://127.0.0.1:8088`，使用初始化时创建的平台管理员登录：

- 机构代码：`platform`
- 用户名：`platform_admin`
- 密码：初始化时通过 `EDUGRADE_BOOTSTRAP_PASSWORD` 设置的密码

平台管理员用于完成租户和初始管理员配置。学校日常业务应使用学校管理员或教师账号，不建议长期使用平台管理员处理考试。

登录后建议再做一次带身份的 smoke test：

```powershell
$env:EDUGRADE_SMOKE_TENANT_CODE = 'platform'
$env:EDUGRADE_SMOKE_USERNAME = 'platform_admin'
$secure = Read-Host 'Platform admin password' -AsSecureString
$ptr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secure)
try {
  $env:EDUGRADE_SMOKE_PASSWORD = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($ptr)
  .\scripts\smoke-test.ps1
} finally {
  [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($ptr)
  Remove-Item Env:EDUGRADE_SMOKE_TENANT_CODE,Env:EDUGRADE_SMOKE_USERNAME,Env:EDUGRADE_SMOKE_PASSWORD -ErrorAction SilentlyContinue
}
```

### 7. 日常启动、停止和查看日志

推荐直接使用与首次安装相同的一键命令；它会启动模型、应用和健康检查：

```powershell
Set-Location $HOME\yuejuan2
.\start-edugrade.cmd
```

以下命令保留给需要绕过一键入口的手工运维。先启动宿主机模型：

```powershell
powershell -ExecutionPolicy Bypass -File lab\scripts\start-local-server.ps1 -Candidate qwen3_4b
```

若只完成了核心配置，使用下面的核心启动命令：

```powershell
Set-Location $HOME\yuejuan2\infra\docker-compose
docker compose --env-file .env -f docker-compose.yml up -d postgres redis minio qdrant grading-agent api-gateway web-admin nginx
```

只有源码、依赖或 Dockerfile 发生变化时才重建：

```powershell
Set-Location $HOME\yuejuan2
# 以下 start-local 命令要求所有 profile 已完成配置
powershell -ExecutionPolicy Bypass -File scripts\start-local.ps1 -Build

# 修改过 Worker 时重建全部 Worker
powershell -ExecutionPolicy Bypass -File scripts\start-local.ps1 -Build -BuildWorkers

# 只重建一个 Worker
powershell -ExecutionPolicy Bypass -File scripts\start-local.ps1 -Build -BuildService ocr-worker
```

`-Build` 默认只重建 API、Grading Agent 和 Web，不会重新构建体积较大的 OCR/Paddle 镜像。仅配置核心服务的电脑，应重新运行第 4.1 节的 `init.ps1`（不要带 `-BootstrapAdmin`）来迁移、构建和启动核心服务。不要把 `docker compose up -d --build` 当作每天的启动命令。

查看状态和实时日志：

```powershell
Set-Location $HOME\yuejuan2\infra\docker-compose
docker compose --env-file .env -f docker-compose.yml ps
docker compose --env-file .env -f docker-compose.yml logs --tail 200 api-gateway nginx grading-agent
docker compose --env-file .env -f docker-compose.yml logs -f ocr-worker image-quality-worker page-processing-worker
```

停止 Docker 服务但保留数据库、对象和缓存：

```powershell
docker compose --env-file .env -f docker-compose.yml --profile '*' down
Set-Location $HOME\yuejuan2
powershell -ExecutionPolicy Bypass -File lab\scripts\stop-local-server.ps1
```

> [!CAUTION]
> 不要在日常停止、重启或升级时执行 `docker compose down -v`。其中的 `-v` 会永久删除当前 Compose 项目的数据库和对象存储卷。

### 8. 更新到 GitHub 最新 main

更新前先备份并确认没有未提交的本地代码改动：

```powershell
Set-Location $HOME\yuejuan2\infra\docker-compose
.\scripts\backup.ps1
Set-Location $HOME\yuejuan2
git status --short
git fetch origin main
git pull --ff-only origin main
```

`.env` 不会随 Git 更新。拉取后应查看 `infra/docker-compose/.env.example` 是否增加了配置，把新增项手工补入自己的 `.env`，然后重新预检、迁移和构建：

```powershell
Set-Location $HOME\yuejuan2\infra\docker-compose
.\scripts\preflight.ps1
.\scripts\init.ps1
```

如本次更新修改了 Worker，再按第 7 节重建相应 Worker。初始化和升级过程中不要手工修改数据库 schema，也不要跳过迁移。

### 9. 常见部署故障

| 现象 | 排查与处理 |
| --- | --- |
| `Docker daemon is not available` | 启动 Docker Desktop，确认已切到 Linux containers，再运行 `docker version` |
| `Required deployment setting is missing` | 打开 `infra/docker-compose/.env` 补齐报错中的变量；本地模型 Key 应运行同步脚本生成 |
| PostgreSQL DSN 认证失败 | `EDUGRADE_POSTGRES_PASSWORD` 与 admin DSN、`EDUGRADE_POSTGRES_APP_PASSWORD` 与 app DSN 必须分别一致 |
| 端口已被占用 | 用第 1.3 节命令查出进程，停止冲突程序或修改 `.env` 中对应端口 |
| 首次构建下载超时 | 确认代理同时对 Docker Desktop 生效；修复网络后重新运行同一 `init.ps1` 命令 |
| 模型未就绪 | 运行 `Invoke-RestMethod http://127.0.0.1:8087/health`，再查看 `lab/.runtime/llama-server.stderr.log` 并重新同步 API Key |
| Web 打不开但容器在运行 | 运行 smoke test，再查看 `nginx`、`web-admin` 和 `api-gateway` 日志 |
| Worker 一直无任务或反复登录 | 检查对应 `EDUGRADE_*_WORKER_TENANT_CODE/USERNAME/PASSWORD` 是否与平台服务账号一致 |
| 管理员登录失败 | 核对机构代码 `platform`、用户名 `platform_admin`；连续输错会触发临时登录限制 |
| 磁盘快速增长 | 用 `docker system df` 查看镜像/构建缓存；先做备份，不要删除正在使用的数据卷 |

若仍无法定位，收集以下**不含 `.env` 和密钥**的诊断信息：

```powershell
docker version
docker compose version
docker compose --env-file .env -f docker-compose.yml ps
docker compose --env-file .env -f docker-compose.yml logs --tail 200 api-gateway nginx grading-agent
```

### 10. 本机部署与生产部署的区别

本节使用 `EDUGRADE_ENV=local`、HTTP、loopback 绑定和可变镜像 tag，只适合单机开发、演示和授权验收。学校或生产环境不能直接照搬：必须配置 HTTPS、内部 TLS、Redis/MinIO TLS、`Secure` Cookie、租户 RLS、不可变镜像 digest、备份恢复、监控告警和容量验证。完整要求见[私有化部署说明](infra/docker-compose/README.md)与[预生产部署 Runbook](docs/deployment/preproduction-runbook.md)。

## 学校管理员操作指南

> 本节描述系统自身的产品流程，供项目维护、评审、演示和经授权使用场景参考；不构成对本系统的部署或机构使用授权。

### 一、完成机构启用

1. 登录后进入“组织与用户”或首次启用向导。
2. 创建学校，填写稳定且唯一的学校代码。
3. 创建学年、届别/年级和班级，确认班级所属学年与年级正确。
4. 下载学生导入模板，填写学号、姓名和班级代码后上传 CSV。
5. 在导入预览中处理重复学号、未知班级和必填字段错误，再确认导入。
6. 创建学校管理员、年级管理员和阅卷教师账号，并按岗位分配角色。
7. 使用业务账号重新登录，确认学校、班级和学生范围符合权限预期。

更完整的首次启用说明见[机构启用与考试工作区操作说明](docs/user-guides/organization-onboarding-and-exam-workspace.md)。

### 二、创建考试

1. 打开“考试”，点击“新建考试”。
2. 填写考试名称、科目、考试类型和考试时间。
3. 选择学校、学年、年级和参考班级。
4. 设置总分、题型、题量及各题分值。系统会按蓝图预生成正式 Question。
5. 选择阅卷方式、成绩发布策略和申诉策略。
6. 在确认页检查总分、题目数量和参考范围，然后创建考试。
7. 创建后进入考试工作区，不要重复创建同一场考试。

### 三、配置试卷、答案和评分标准

1. 在考试工作区打开“试卷与评分标准”。
2. 上传正式试卷 PDF。扫描版 PDF 会进入现有页面处理与 OCR 流程。
3. 上传答案或包含答案的文件，等待 AI 解析完成。
4. 在解析预览中逐题核对题号、题型、分值、题干、标准答案和 Rubric。
5. 如页面显示分值、题型、漏题、多题或总分不一致，先人工修正；系统不会静默覆盖考试蓝图。
6. 确认无误后点击应用，将内容补充到预生成 Question。
7. 对主观题进一步完善 Rubric、采分点和允许的等价答案。

### 四、制作答题卡并确认开考准备

1. 上传或创建答题卡模板。
2. 点击“自动识别区域”生成候选答题区域。
3. 逐页核对题号关联和识别置信度，必要时移动、缩放、删除或重新框选区域。
4. 保存草稿并预览答题卡，确认二维码、页码和所有题目区域完整。
5. 锁定模板，并按冻结名单生成受控打印答题卡。
6. 打开“开考准备”，依次处理未通过项目。
7. 确认 readiness 后，系统冻结考生名单、试题、答案、Rubric 和正式答题卡布局。

> 进入 `ready` 后不能继续修改正式试题或锁定新模板。如确需调整，应按学校流程取消当前考试或新建考试版本，不要直接修改数据库。

### 五、采集与处理答卷

1. 打开考试工作区的“答卷采集”，创建采集批次。
2. 上传扫描 PDF 或图片；建议同一批次使用一致的扫描参数和页面方向。
3. 启动处理后，关注页面质量、身份识别、页码、配准、OCR 和切题状态。
4. 对模糊、缺页、重复页、未知考生、二维码冲突或配准失败页面进入异常处理。
5. 核对受控答题卡身份。系统会按打印时绑定的模板 ID 和内容 hash 配准，不会自动改用后来创建的模板版本。
6. 所有有效答卷完成切题后，再进入评分阶段。

### 六、阅卷、复核和仲裁

1. 客观题先运行规则评分，检查未识别选项和低置信度结果。
2. 主观题进入阅卷工作台，教师根据原始答题证据、Rubric 和 AI 建议评分。
3. AI 建议仅用于辅助判断；教师应核对采分点、证据位置和分值上限。
4. 对达到双评条件的题目完成第二次独立评分。
5. 对分差超阈值、证据冲突或质量异常的答案进入仲裁。
6. 完成所有复核任务后，确认每位考生每道题均存在最终成绩。

### 七、发布成绩

1. 打开“成绩管理”，先执行发布前检查。
2. 处理所有阻断项，包括缺少答卷、身份未确认、缺页、OCR 失败、未完成复核、题目覆盖不完整和总分不一致。
3. 确认考试 Question 集合、每份答卷的 AnswerSegment 集合和 FinalGrade 集合一致。
4. 生成成绩发布草稿并复核人数、总分、异常数和发布范围。
5. 由具备权限的管理员显式发布。发布后系统保存不可变成绩快照和审计记录。
6. 后续更正通过申诉、重评或新的发布版本完成，不直接覆盖历史发布记录。

## 常见问题

### 页面无法打开或登录失败

先确认容器状态：

```powershell
Set-Location infra\docker-compose
docker compose --env-file .env -f docker-compose.yml ps
docker compose --env-file .env -f docker-compose.yml logs --tail 200 api-gateway nginx
```

检查访问地址是否为 `http://127.0.0.1:8088`，并确认机构代码、用户名和密码对应同一租户。连续输错密码可能触发临时登录限制。

### 看不到某个菜单

菜单按角色和数据范围显示。请联系管理员核对用户角色、学校范围及年级范围。不要通过共享管理员账号绕过权限检查。

### 试卷 AI 解析没有自动应用

这是预期行为。解析结果必须先进入预览；题型、分值、总分、漏题或多题不一致时必须人工确认。低置信度 OCR 结果不会自动写入正式试卷。

### 答卷一直停留在处理中

检查 OCR、图像质量和页面处理 Worker 是否已启动：

```powershell
docker compose --env-file .env -f docker-compose.yml --profile ocr --profile quality --profile processing ps
docker compose --env-file .env -f docker-compose.yml logs --tail 200 ocr-worker image-quality-worker page-processing-worker
```

不要重复上传同一文件。先根据页面错误码处理 Worker 凭据、模型连接、文件格式或页面质量问题，再使用系统提供的重试操作。

### 发布按钮不可用

发布 Gate 采用失败关闭策略。打开成绩页面查看阻断项，优先处理名单、未知答卷、缺页、题目覆盖、未完成评分和总分一致性问题。不能通过直接修改数据库绕过发布检查。

## 本地开发（维护者 / 已授权开发者）

开发与评审请遵循[代码注释与维护说明标准](docs/engineering/commenting-standard.md)，同步维护业务规则、状态边界及失败恢复说明。

> [!WARNING]
> 本节仅用于项目维护和经授权开发。公开开发命令不授予复制、修改、运行、部署或发布本项目的许可。

### 开发环境

- Node.js 20+ 与 npm。
- Go 1.26。
- Python 3.11+。
- Docker Desktop，用于依赖服务和容器验收。
- 构建桌面端时还需要 Rust、Cargo 和 Tauri Windows 依赖。

### Web 管理后台

```powershell
npm.cmd install
npm.cmd run typecheck
npm.cmd run dev
```

默认开发地址为 `http://127.0.0.1:5173`，以 Vite 实际输出为准。

生产构建与单元测试：

```powershell
npm.cmd run build
npm.cmd --workspace apps/web-admin test
```

### Go API

```powershell
Set-Location services\api-gateway
go test ./... -count=1
go run ./cmd/api-gateway
```

本地运行 API 前，应从根目录 `.env.example` 创建仅供本机使用的配置，并确保 PostgreSQL、Redis、MinIO 等依赖可用。

### Python 服务与 Worker

```powershell
python -m pytest services\ocr-worker\tests -q
python -m pytest services\page-processing-worker\tests -q

$env:PYTHONPATH = "ai-services"
python -m unittest discover -s ai-services\tests -p "test_*.py"
```

### 桌面端

```powershell
npm.cmd --workspace apps/desktop-client run dev
npm.cmd --workspace apps/desktop-client run build
npm.cmd --workspace apps/desktop-client run tauri:dev
```

## 系统架构与边界

- Web Admin 负责考试、试卷、采集、阅卷、成绩和治理操作。
- Go API Gateway 是生产业务请求、权限校验和结果落库的唯一入口。
- OCR、图像质量、页面处理和主观题 Worker 通过现有 Worker Runtime 执行异步任务。
- `grading-agent` 只处理去身份化评分请求，返回带证据的教师建议。
- Lab 负责模型、提示词、评测、校准和发布门禁，不直接访问生产数据库。
- PostgreSQL 保存业务事实，MinIO 保存受控文件，Redis 用于运行时协调，Qdrant 用于受治理的检索能力。
- 最终成绩必须由平台业务流程确认和发布，AI、Lab 与 Worker 均不具备成绩发布权限。

主观题 AI 边界：

| 能力 | 当前行为 |
| --- | --- |
| 短答题、计算题 | 可生成 `teacher_suggestion`，必须人工复核 |
| 作文、论述题 | 仅保存 `shadow_only` 影子结果 |
| 证据 | 必须来自学生答案，服务端重新校验 |
| 分数 | 服务端按已匹配采分点重新计算，不信任模型总分 |
| 置信度 | 未完成生产校准时使用保守治理值 |
| 最终成绩 | 只能由正式业务流程确认、发布和锁定 |

## 仓库结构

```text
apps/
  web-admin/                 Web 管理后台
  desktop-client/            Windows/Tauri 扫描工作站
  teacher-portal/            教师端边界
  student-portal/            学生端
services/
  api-gateway/               Go API、业务流程、迁移和 Worker Runtime
  ocr-worker/                OCR 与版面文本识别
  page-processing-worker/    PDF 解码、页面配准和切题处理
  image-quality-worker/      页面质量检测与规范化
  subjective-grading-worker/ 主观题异步阅卷任务
ai-services/
  grading_agent/             内部智能阅卷 Agent
  prompts/                   版本化提示词及 manifest
contracts/                   跨服务契约
lab/                         模型适配、评测、校准与发布门禁
infra/docker-compose/        私有化部署和运维脚本
docs/                        产品、架构、API、安全、部署和用户文档
tests/                       E2E、负载、安全和容器验收测试
```

## 质量验证

提交前建议执行：

```powershell
npm.cmd run typecheck
npm.cmd run build
npm.cmd run check:openapi-breaking
npm.cmd run check:lab-integration

Set-Location services\api-gateway
go test ./... -count=1
```

Compose 配置检查：

```powershell
docker compose --env-file infra\docker-compose\.env.example `
  -f infra\docker-compose\docker-compose.yml config --quiet
```

## 当前状态

- 当前能力和验证证据以[验证状态文档](docs/verification-status.md)为准。
- AI 阅卷仍是教师建议，不具备自动发布最终成绩的权限。
- 作文和论述题目前只允许 `shadow_only`。
- Mock、协议模拟器和合成数据只能证明技术链路，不代表真实学校数据上的模型效果。
- 生产 Pilot 前仍需完成真实受治理数据评测、教师一致性、置信度校准、公平性检查、容量测试和恢复演练。
- 模型文件、运行时密钥、数据库密码和部署 `.env` 不应提交到 Git。

## 文档索引

- [机构启用与考试工作区操作说明](docs/user-guides/organization-onboarding-and-exam-workspace.md)
- [当前能力与验证状态](docs/verification-status.md)
- [私有化部署说明](infra/docker-compose/README.md)
- [预生产部署 Runbook](docs/deployment/preproduction-runbook.md)
- [企业验收清单](docs/deployment/enterprise-acceptance-checklist.md)
- [系统架构](docs/architecture/overview.md)
- [生产安全与可靠性](docs/architecture/production-security-and-reliability.md)
- [API 错误模型](docs/api/errors.md)
- [主观题阅卷 API](docs/api/subjective-grading.md)
- [Lab 使用说明](lab/README.md)

## 安全与责任说明

EduGrade 处理学生身份、答卷和成绩等敏感教育数据。经授权的部署方应根据所在地法律法规和学校制度配置访问控制、数据保留、备份、审计、网络隔离和模型使用政策。任何 AI 输出都应由具备资质的教育工作者审核，不应作为影响学生权益的唯一依据。

## License & Copyright / 授权与版权

Copyright © 2026 damiao168. All Rights Reserved.

本项目是**专有的源代码可查看项目**，不是开放源代码软件。

在遵守 [`LICENSE`](./LICENSE) 的前提下，你可以阅读代码，并将其用于个人学习、教育研究和非商业技术参考；但未经版权所有者事先明确书面授权，不得：

- 复制、摘取或将本项目代码用于其他项目；
- 修改、改编后作为自己的项目或衍生项目发布；
- 编译、运行、部署或托管本系统；
- 用于学校、教育机构、企业或其他组织的实际业务；
- 用于商业、收费、营利或生产环境；
- 基于本项目提供 SaaS、托管、私有化部署、咨询或集成服务；
- 出售、出租、转授权、再发布或以其他方式向第三方分发；
- 删除或修改项目中的版权、许可和权利声明。

仓库在技术上可以被 GitHub 用户访问、下载或 Fork，并不意味着获得上述权利。第三方依赖、模型、库、字体、数据和其他材料继续适用其各自许可证。

如需部署、二次开发、机构使用、研究合作或商业授权，请事先取得版权所有者的书面许可。完整中英双语条款见 [`LICENSE`](./LICENSE)。
