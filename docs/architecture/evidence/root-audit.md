# 工程、组合根与权限审计依据

审计日期：2026-10-10（Asia/Shanghai）。对象是 `main` 的当前工作区；HEAD 为 `d002c7b1f928e938b841e1203976c763b03e8485`，包含原有未提交源码，不能只用该提交重现本次事实。

## 1. 工程事实

- 根 `package.json` 使用 npm workspaces；管理端、学生端、桌面 React 前端共享 SDK、类型、设计令牌及 UI 包。`apps/teacher-portal/README.md:3` 明确该独立目录预留，教师功能实际位于管理端教师体验和桌面工作站。
- `services/api-gateway/go.mod:1` 是一个 Go 模块。`internal/server/application_modules.go:49` 集中装配 Identity、ExamPreparation、CaptureProcessing、GradingQuality、Release、AIGovernance、SchoolAssistant 与平台学校入口。逻辑域不是独立微服务。
- Store 接口及 PostgreSQL 实现执行手写 SQL；MemoryStore 用于测试装配。实际生产入口 `server.New` 创建 PostgreSQL 图和后台试卷解析执行器。见 `internal/server/server.go:75`、`internal/server/application_modules.go:113`。
- `services/*-worker` 使用独立 Python 运行时；公共租约/HTTP 代码在 `packages/worker-runtime-python/edugrade_worker_runtime/`。业务数据库的任务事实与计算进程分离。
- 根静态盘点发现 68 个非测试 Go 内部包、351 个唯一 import 方向，未发现静态 import 环。提取器按源码 import 字符串统计，不能证明运行时注入、回调或业务流程没有循环。全边有文件/行号，见 `module-imports.json`。
- `.github/workflows/ci.yml` 声明 Web 静态、单测、构建、mock E2E，Go 静态/单测/race/覆盖/安全/PG，Python、Rust、Compose、SBOM、真实隔离栈 E2E 等门禁。这里是配置审计，不表示这些 CI 本次已执行或生产模型精度已验证。

## 2. 已运行的只读架构门禁

命令：`node scripts/check-architecture-boundaries.mjs`，退出码 **1**。末次工作区变更后重跑，原有代码触发三项页面体量门禁：

| 位置 | 实际行数 | 门禁上限 |
| --- | ---: | ---: |
| `apps/web-admin/src/features/question-bank/QuestionBankPage.tsx` | 827 | 700 |
| `apps/web-admin/src/pages/CaptureBatchPage.tsx` | 748 | 700 |
| `apps/web-admin/src/pages/PlatformModelConfigPage.tsx` | 773 | 700 |

同时报告 capture.Store 49 方法、questionbank.Store 26、review.Store 21、modelgovernance.Store 21，以及多个大 hook 的维护告警。门禁采用正则/物理行数；只是维护债务证据，不能替代业务漏洞判断。没有修改代码或放宽债务预算。

## 3. 认证、授权与数据边界

- `auth/middleware.go:36` 每次请求校验原始 token 的 hash、会话与当前 AccessScope；`auth/store_postgres.go:180` 查询角色、权限、撤销时间、过期时间与 security_epoch。账户恢复接口的空范围例外不授予普通业务访问权。
- `auth/role_policy.go:19` 定义实际人类角色 platform_admin、tenant_admin、school_admin、teacher、grader、arbitrator、auditor、student，以及服务角色。`exam_owner` 是前端工作区定义，并非此策略确认的独立角色。
- `auth/resource_scope.go:37` 将当前用户范围与实际资源的 tenant/school/class/exam/student/assigned task 关系比较；RLS 的 tenant 隔离不等价于 school/class/task 隔离。
- `db/tenant_context.go:55` 拒绝 superuser/BYPASSRLS 登录。每次数据库操作设置 runtime role 和 tenant_id，连接池归还清空范围；maintenance 是显式后台上下文。只有迁移实际启用 RLS 的表受这道数据库防线保护。
- `middleware/middleware.go:122` 的 BrowserCSRF 要求浏览器修改请求带指定非简单 header；有效 Bearer 请求和 token 登录有明确例外。CORS 约束浏览器读取响应，不能当业务权限。全局上传 body limit 与文件路由单独限制见 `server/server.go:191`。

## 4. P1：一般 guard 的收据重放先于当前授权

**代码事实**：`server/router_guards.go:98` 组合 `authenticate(idempotent(resourceBoundary(handler)))`；例如 `requireExamManage` 在 `:127` 将 permission 放在 handler 内。`idempotency/middleware.go:144` 缓存命中直接 `writeReplay` 返回，没有调用内层 handler。`idempotency/store_postgres.go:53` 的查询键是 tenant、actor、method、route、key；检查请求指纹，但没有会话/授权代次。

**触发与影响**：同租户、同 actor 持有旧成功命令的 key 和同一请求，在授权收窄后重新获得一个仍有合法较窄范围的会话，收据仍有效时，可能读到当前资源或权限已不允许读取的旧结果。业务写操作不会因此再次执行；也不能推导为任意用户跨租户读取。

**已核对的限制**：`auth/managed_access_store.go:154` 角色/班级授权变更会提升 security_epoch 并撤销旧会话，所以不能描述为已废止会话仍能认证。问题是重登后旧 actor 收据仍未复核当前资源权限。`requirePermission` 和 `requireRecentPermission` 在 `router_guards.go:112`、`:116` 将资源/权限检查放在幂等之前，其导出与关键命令测试见 `server/recent_auth_test.go:46`、`:76`，这些入口不适用本项。

**建议**：所有需要资源/业务权限的 guard 在返回缓存前重新执行当前授权，保持请求指纹和不可变业务收据；为授权收窄后重登、任务重新分派、跨学校范围变化补回放拒绝测试。本次没有运行利用复现，没有修改中间件。

## 5. 凭据与真实部署边界

- `auth/password.go:36` 新密码用 Argon2id，兼容 bcrypt 并在有效登录后升级。`auth/token.go:11` 生成随机 token，数据库保存 SHA-256 hash。
- `modelgovernance/managed_config.go:208` 从 masterKey 派生 256-bit 密钥，使用 AES-GCM、随机 nonce，AAD 绑定 tenantID/configID。`managed_config_store_postgres.go:44` 保存密文；`:211` 在受控运行配置读取时解密。图纸只展示字段与路径，不包含口令、主密钥或 API Key 值。
- `infra/docker-compose/docker-compose.yml` 定义 PostgreSQL、Redis、MinIO、Qdrant、Go API、web-admin、grading-agent、按 profile 启用的计算 Worker、Nginx 和可选 Prometheus/Grafana。Student 可以独立构建，当前主 Compose 没有独立 student 服务，桌面是客户端宿主进程。
- Nginx 定义当前 HTTP 与 `/api/`、静态页面代理；外部 TLS 终止、实际 profile、账号和已运行实例未检查。不能凭样例把 HTTPS、HA、Kubernetes、对象加密或生产可用性画成既有保证。
- Qdrant 的 Go 引用只确认 `deps/deps.go:182` 的 healthz 检查；没有确认生产向量业务查询。outbox 实际组装 `NewLogPublisher`（`server/infrastructure.go:157`、`outbox/publisher_log.go:14`），日志投递不能画成已经接通的外部消息总线。

## 6. 本次审读边界

已完成组合根、认证组合顺序、驱动租户上下文、密码/token、公共中间件与日志的重点核对。较大的 auth/store、config、CI、桌面 Rust、部署恢复脚本仅核对相关段落，其余审读任务保留 pending。全文件状态见合并后的 `file-review.tsv`；自动遍历/import 提取不会使 pending 变成 reviewed。
