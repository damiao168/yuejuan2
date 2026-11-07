# 生产安全与可靠性控制

本文描述当前主分支已经实现的生产控制及其边界。实现入口以 API Gateway、数据库 migration、Docker Compose 和 CI 为准；本文不把未实现能力声明为可用。

## 1. 认证与设备会话

- Web 登录使用 HttpOnly Cookie。`POST /api/v1/auth/login` 的 Web 响应不向 JavaScript 返回访问令牌。
- “保持登录”创建服务端 `remembered_device` 会话；浏览器不保存密码，也不在 LocalStorage、SessionStorage 或 IndexedDB 保存长期令牌。
- 桌面客户端使用独立的 `desktop_device` 会话类型，不与 Web Cookie 机制混用。
- `GET /api/v1/auth/sessions` 列出当前用户设备，`DELETE /api/v1/auth/sessions/{id}` 撤销指定设备。登出、修改密码、用户停用和租户停用都会使相关会话失效。
- 共享设备上退出登录会清理浏览器阅卷草稿；本地草稿按用户和任务隔离，并设置过期时间。
- Web 的 POST、PUT、PATCH、DELETE 请求必须携带 `X-EduGrade-CSRF: 1`。该非简单请求头结合 CORS 预检阻止第三方页面利用 Cookie 发起写操作；Bearer Worker/桌面会话不经过此浏览器门禁。

## 2. 数据范围权限模型

RBAC 决定“能否执行某类操作”，`AccessScope` 决定“能够访问哪些数据”。后端从数据库中的角色范围、学校/班级关系、考试任务、学生身份和任务分配推导范围，不采信客户端提交的 tenant、school 或 exam 范围。

支持的平台、租户、学校、年级、班级、考试、已分配任务和学生本人范围会继续下推到考试、答卷、文件、阅卷、仲裁、成绩和报表查询。范围缺失或关系不匹配时默认拒绝；前端隐藏入口只用于体验，不构成安全边界。

`student`、`submission`、`submission_page`、`answer_segment`、`ocr_result`、`human_grade`、`final_grade`、`appeal` 和 `file_asset` 同时启用并强制 PostgreSQL RLS。API 使用独立的非 superuser、无 `BYPASSRLS` 登录账号，连接包装器按请求上下文设置 tenant；缺少 tenant 的普通上下文默认看不到受保护行。只有显式标记的进程级对账、投影、Outbox 和解析任务可进入跨租户 maintenance scope，迁移账号不进入 API 运行时。

迁移 `000221` 将同一强制策略扩展到 `question_grade`、`submission_grade`、`score_release`、`score_release_item`、`question_appeal`、`exam_candidate_snapshot`、`review_task`、`arbitration_task`、`answer_candidate`、`ai_grade` 和 `subjective_grading_run`。此清单不表示所有租户表都已覆盖；新增表的检查要求及校准分池升级边界见 [成绩数据维护说明](../engineering/grade-data-protection.md)。

## 3. Worker 身份、Lease 与 Fencing

- Worker 使用专用服务账号和最小权限，不复用平台管理员身份。
- 领取任务后必须携带任务 ID、Lease Token、Worker 服务名和实例 ID。
- 网关从持久化任务推导 tenant、考试、答卷和文件能力；Worker 不能用请求头自行选择租户。
- 心跳、完成、失败和文件下载均校验当前 Lease。过期 Attempt 或已被新 Worker 领取的 Attempt 无法覆盖新结果。
- 结果接口保留幂等和版本检查；死信、重试与过期回收由持久化状态驱动。

## 4. 文件生命周期

`file_asset.lifecycle_status` 使用 `pending_upload`、`active`、`quarantined`、`upload_failed`、`pending_delete`、`delete_failed`、`deleted`、`missing_object` 和 `orphan_recovered` 表达 PostgreSQL 与 MinIO 之间的 Saga 状态。

- 元数据先进入可恢复状态，对象操作成功后再推进状态。
- 上传或删除失败不会伪装成功，错误和重试次数持久化。
- 删除受 revision、保留期限和 legal hold 控制。
- 周期性对账默认只报告；显式 repair 才允许修复孤儿对象或缺失对象状态。
- 文件详情、下载和删除都执行 tenant、资源关系与任务级授权。

## 5. 强审计、普通审计与 Outbox

普通读取和低风险访问允许使用应用层审计，失败会产生结构化错误日志。关键业务表由数据库触发器在业务事务内同时写入：

1. 脱敏后的追加式 `audit_log`；
2. `event_outbox`。

受保护范围包括租户与用户启停、权限关系、考试、文件、答卷、人工评分、仲裁、成绩、申诉、报表导出记录和模型治理。任一写入失败都会回滚业务事务。审计表按 tenant 建立 SHA-256 Hash Chain，并由触发器禁止更新或删除；历史应用审计继续补充操作者、IP、User-Agent 与 request ID 等上下文。Outbox 使用 Lease、退避和死信状态进行至少一次投递。

## 6. 幂等协议

高风险 POST 使用 `Idempotency-Key`。Key 长度最多 128 个字符，并按 tenant、actor、路由和请求内容哈希隔离。

- 同一 Key、同一请求返回原状态码和响应，并设置 `Idempotency-Replayed: true`。
- 同一 Key、不同请求返回 `409 idempotency_key_reused_with_different_request`。
- 正在执行返回 `409 operation_in_progress` 和 `Retry-After`。
- 429、冲突和 5xx 等可重试失败不会缓存成成功结果。
- 生产环境对考试创建、采集、阅卷领取/提交、成绩确认/发布、申诉、导出和 Worker 完成等路由强制要求该 Header。

## 7. 健康检查

| 地址 | 鉴权 | 语义 | 响应内容 |
| --- | --- | --- | --- |
| `/health/live` | 否 | 进程能够处理 HTTP | 服务名与存活状态 |
| `/health/ready` | 否 | 必需依赖可用 | 仅依赖名、状态、是否必需，不返回地址和错误详情 |
| `/api/v1/system/status` | `system:read` | 管理员诊断 | 依赖详情、能力影响、Worker 状态、构建版本 |
| `/ready` | `system:read` | 兼容旧客户端 | 与 readiness 相同的脱敏结果；新探针不要使用 |

Compose 与 Nginx 探针使用 `/health/ready`；单纯存活检查使用 `/health/live`。

## 8. 能力降级矩阵

| 故障 | 保持可用 | 禁止或转人工 |
| --- | --- | --- |
| AI 评分服务不可用 | 登录、考试配置、答卷浏览、人工阅卷 | 自动主观题建议；任务进入失败/复核状态 |
| OCR Worker 不可用 | 已有识别结果、人工录入、考试与成绩管理 | 新 OCR 自动识别；页面显示影响和恢复动作 |
| 图像质量/页面处理 Worker 不可用 | 原文件保存、人工查看 | 自动归一化/切题；保留可重试任务 |
| Qdrant 未配置或异常 | 核心考试和人工阅卷 | 检索增强能力降级，不伪装为正常 |
| Redis 异常 | 已认证业务请求 | 登录限流与安全重试不可用时登录 fail closed |
| PostgreSQL 或 MinIO 不可用 | `/health/live` | `/health/ready` 返回 503，数据写入不继续 |

生产、预生产和演示环境不会自动回退到 Mock AI。模型未验收时不能进入自动评分路由。

## 9. 备份、恢复与 RPO/RTO

操作步骤见 [预生产部署 Runbook](../deployment/preproduction-runbook.md)。`backup.ps1` 生成 PostgreSQL custom dump、MinIO 镜像和包含 SHA-256/Schema/镜像信息的 manifest；`verify-backup.ps1` 离线校验；`restore-drill.ps1` 恢复到随机隔离数据库和 bucket，检查租户关系、migration 和对象数量后默认清理。

建议预生产基线：RPO 不超过 24 小时，RTO 不超过 4 小时；学校正式投产应按考试窗口缩短备份间隔并记录实际演练耗时。未经显式 `-AllowPrimaryDatabase` 不允许覆盖主库。

## 10. Migration Baseline

已有数据库只能使用带目标版本的显式 baseline。迁移器先比对表、字段、约束、索引与租户关系指纹，再登记历史版本；不匹配会停止。已执行 SQL 的 SHA-256 变化同样会停止部署。结构修复只能新增 migration，不得改写历史文件。

## 11. 指标与告警

`/metrics` 暴露按模板路由聚合的请求计数、并发请求、耗时直方图、PostgreSQL 连接池、SQL 操作耗时和慢查询计数，不记录 SQL 文本、参数、答卷或凭据。

建议初始告警阈值：

- `/health/ready` 连续 2 分钟返回 503：严重；
- 5xx 比例 5 分钟超过 2%：高；
- P95 API 延迟 10 分钟超过 2 秒：中；
- PostgreSQL 连接等待持续增长或 in-use 达上限 80%：高；
- Outbox 死信、文件对账 finding、Worker dead-letter 任一新增：高；
- 登录限流触发在 5 分钟内显著高于基线：中，并按 tenant/IP 调查。

指标不替代业务审计；日志通过 request ID 与 trace ID 关联。

## 12. 供应链安全

CI 中第三方 GitHub Actions 固定到提交 SHA；Go、Node、Python 和 Rust 分别执行锁文件/依赖校验与静态检查。普通 CI 为 API、Web、桌面端、Workers 和 AI 服务生成源码级 CycloneDX SBOM，并用 Trivy 阻断存在可修复 High/Critical 漏洞的提交。正式镜像工作流使用 digest 固定的基础镜像构建每个运行时镜像，在推送前执行 Trivy image 扫描并生成镜像级 SBOM，推送后以注册表返回的 digest 做 Sigstore keyless 签名和 SBOM attestation。生产镜像以非 root 用户运行，内部数据面默认只绑定回环或 Compose 网络。

Compose 的 production-like 预检强制所有运行时镜像和构建基础镜像使用 `@sha256:` 引用；Tag 只作为本地开发或人类可读别名。发布工作流为每个组件保留 SBOM 和 digest 证据，部署审批引用这些 digest。

## 13. 生产升级

升级顺序为预检、备份、隔离恢复验证、拉取按 digest 固定的镜像、执行只增 migration、滚动更新 API/Web/Worker、执行匿名与认证 smoke。状态接口返回 version、git SHA、build time、image digest、release ID 和 schema version，用于定位混合版本。

新增字段和 API 保持向后兼容；先部署兼容新旧 schema 的应用，再切换调用方。当前没有 down migration，结构不兼容时必须恢复升级前 PostgreSQL 与 MinIO 备份，不能只回退镜像。

## 14. API 错误模型

错误统一返回 `error.code`、安全的 `error.message`、`request_id` 和 `trace_id`。认证失败不区分租户、账号或密码；服务端日志可记录内部原因，但响应不得包含 SQL、堆栈、对象存储地址、密钥或模型供应商凭据。常用错误与客户端动作见 [API 错误码](../api/errors.md)。

## 15. 安全测试范围

CI 覆盖跨租户/跨考试越权、缺失数据范围、文件资源授权、浏览器 CSRF、Worker 过期 Lease 与任务资源不匹配、会话撤销、可信代理、登录限流、幂等冲突、并发 revision、CSV 公式注入、文件对账、恢复脚本语法和生产路由门禁；认证、幂等、Worker 与文件并发原语另执行 Go race detector。PostgreSQL 工作流在 CI 服务数据库中运行；本地没有 PostgreSQL/Docker 时不得用 Memory Store 结果冒充数据库集成验证。
