# 全仓代码审查范围规划与裁决准则（2026-09-24）

## 1. 基线、授权与证据边界

- 审查对象是 `D:\project\yuejuan` **当前工作区**，基线 HEAD 为 `84a33c572ab24f96f98d82426e101063cf4a1876`。`git status --short --branch` 显示管理端、Go 评分／试卷文件、E2E 与截图存在未提交修改，且有 `preparationSteps.ts`、`scoring_run_routing.go`、`scoring_coverage.go` 和对应测试等未跟踪源码。审查不得退化成仅检查 HEAD 或 `git diff --name-only`；未跟踪文件不在普通 `git diff` 中。
- 用户授权仅审查、不修复。此规划阶段未运行测试、未启动服务、未改业务代码；只写本文件。根目录、`D:\project` 与 `D:\` 未发现 `AGENTS.md`；仓库内仅发现 [`lab/AGENTS.md`](../../lab/AGENTS.md)，其规则只适用于 `lab/`，包括合成数据标记、模型输出校验、分数范围、人审与日志隐私。
- [`docs/code-review-context.md`](../../docs/code-review-context.md) 是 2026-09-15、另一 HEAD 的架构导航。其“61 个内部包、149 份顶层迁移”已不符合当前工作区：本轮只读盘点为 `services/api-gateway/internal` **66 个目录**、`services/api-gateway/migrations` **174 份顶层 SQL**。旧报告、Story、文档中的风险条目都不能直接升级为本轮发现；发现须重新沿当前入口、调用、SQL／契约与测试取证。
- 排除依赖与生成缓存、输出目录、运行时临时数据库目录 `.tmp-pg-migration-verify/` 的逐文件漏洞审查；它们不属于当前可交付业务源码。若其中生成物被正式构建或部署引用，应回溯源配置与生成流程。保留对工作区新增源码、迁移、测试和生产装配的覆盖。

## 2. 当前系统地图与审查深度

[`README.md`](../../README.md) 的业务流从考试与冻结名单，经试卷／模板、采集／OCR、评分／复核，直到发布与申诉。实际运行入口是 [`cmd/api-gateway/main.go`](../../services/api-gateway/cmd/api-gateway/main.go)、[`internal/server/server.go`](../../services/api-gateway/internal/server/server.go)、[`application_modules.go`](../../services/api-gateway/internal/server/application_modules.go) 与 [`application_stores.go`](../../services/api-gateway/internal/server/application_stores.go)：Go `api-gateway` 是业务模块化单体；生产存储图和测试 Memory 图须分开。服务拓扑另见 [`docs/architecture/overview.md`](../../docs/architecture/overview.md)，但功能闭环仍应以现行装配、路由、Store、SQL 和 Worker 调用为证。

| 层／目录 | 审查重点及证据入口 | 主责任务 |
| --- | --- | --- |
| `services/api-gateway/internal`、`cmd`、`migrations` | 66 个内部目录；路由包装、Store 事务、租户 SQL 上下文、状态机和 174 份顶层迁移。不能只读 Handler 或 Memory Store。 | S01–S06、S10–S12 |
| `apps/web-admin` | 管理／教师／阅卷 UI，57 个 API 文件、186 个 features 文件；准备页与工作区当前有改动。前端显隐和提示不构成服务端权限或发布门禁。 | S07，辅以 X02／X05 |
| `apps/student-portal`、`apps/desktop-client` | 公开成绩／本人证据，以及 Tauri 原生 IPC、SQLite／spool／扫描／离线恢复。须区分浏览器 fallback 与原生持久化。 | S08，辅以 X03／X05 |
| `packages/sdk`、`shared-types`、`ui`、`design-tokens`、`contracts` | OpenAPI 生成 client／types、手写传输、跨端契约及例外；共享 UI 组件重点查业务状态传播，不做风格审查。 | S09、S07／S08 |
| 五个 `services/*-worker`、`packages/worker-runtime-python`、`ai-services` | Python 认领／心跳／结果回传，OCR、页面处理、图像质量、主观评分、数学校验与 AI adapter。须验证真实 Go 结果处理端及取消／失租。 | S10／S11，辅以 X03／X04／X06 |
| `infra`、`scripts`、`.github`、`tests`、`lab` | Compose／迁移／备份恢复／契约生成／CI 门禁；E2E mock 与真实 Postgres、真实模型、硬件环境需明确区分。`lab` 遵循其局部 AGENTS。 | S11／S12，全部任务使用对应测试佐证 |

## 3. 核心不变量与最高风险

以下是**审查假设和应验证的业务约束，不是已确认缺陷**。每一项都应找出生产写入点、并发边界、数据库约束以及反例测试。

1. **身份与范围**：会话身份、动作权限、资源范围、存储租户隔离应同时成立。路径权限不能代替列表、body ID、SQL 查询和 Worker 任务范围检查。锚点：[`auth/access_scope.go:22`](../../services/api-gateway/internal/auth/access_scope.go)、[`auth/access_scope.go:332`](../../services/api-gateway/internal/auth/access_scope.go)、[`db/tenant_context.go:81`](../../services/api-gateway/internal/db/tenant_context.go)、[`workerruntime/scope_middleware.go:34`](../../services/api-gateway/internal/workerruntime/scope_middleware.go)。优先级：最高。
2. **考试事实冻结**：名单、题目、评分标准和模板在准备确认／进入 `ready` 后应有一致的冻结版本；评分消费同一考试题目快照。锚点：[`paper/template_validation.go:185`](../../services/api-gateway/internal/paper/template_validation.go)、[`migrations/000076_storyA01_assessment_domain.sql:615`](../../services/api-gateway/migrations/000076_storyA01_assessment_domain.sql)、[`docs/architecture/assessment-domain.md`](../../docs/architecture/assessment-domain.md)。优先级：最高。当前新增 readiness advisory 是非阻断信息，须特别核对与实际评分路由相符。
3. **当前有效来源**：文件、答卷页、归一化图、裁剪证据、OCR／数学修正及代次／revision 必须指向同一当前事实，旧异步结果不得覆盖新结果。锚点：[`files`](../../services/api-gateway/internal/files)、[`capture`](../../services/api-gateway/internal/capture)、[`segment`](../../services/api-gateway/internal/segment)、[`processing`](../../services/api-gateway/internal/processing)。优先级：最高。
4. **任务所有权与重试**：认领、租约、心跳、回传、取消、超时、重试／死信及域结果提交之间须阻断失租与重复副作用，并保持回执可恢复。锚点：[`workerruntime/store_postgres.go:115`](../../services/api-gateway/internal/workerruntime/store_postgres.go)、[`workerruntime/store_postgres.go:163`](../../services/api-gateway/internal/workerruntime/store_postgres.go)、[`server/worker_source_lease.go`](../../services/api-gateway/internal/server/worker_source_lease.go)。优先级：最高。
5. **评分与人工决定**：自动分数、AI 建议、人工分数、双评／仲裁、当前成绩必须保持来源、快照、总分及 revision 一致；模型不能直接形成公开最终成绩。锚点：[`grading/scoring_run_start.go:216`](../../services/api-gateway/internal/grading/scoring_run_start.go)、新增 [`grading/scoring_run_routing.go:40`](../../services/api-gateway/internal/grading/scoring_run_routing.go)、[`review`](../../services/api-gateway/internal/review)、[`subjective`](../../services/api-gateway/internal/subjective)。优先级：最高。
6. **发布不可变性与纠错**：发布前重算门禁，公开快照及学生可见证据不得随工作成绩变化而漂移；申诉与重评应通过新版本延续血缘。锚点：[`releasegate/coordinator.go:32`](../../services/api-gateway/internal/releasegate/coordinator.go)、[`scorerelease/store_postgres.go:251`](../../services/api-gateway/internal/scorerelease/store_postgres.go)、[`regraderelease`](../../services/api-gateway/internal/regraderelease)。优先级：最高。
7. **模型／隐私／部署边界**：受管模型配置、准入、调用输入与日志、服务凭据，以及迁移／部署环境必须与租户及业务状态一致。锚点：[`modelgovernance`](../../services/api-gateway/internal/modelgovernance)、[`ai-services/grading_agent`](../../ai-services/grading_agent)、[`infra/docker-compose`](../../infra/docker-compose)、[`lab/AGENTS.md`](../../lab/AGENTS.md)。优先级：高。

## 4. 十二项专项任务：沿主控既定 ID 分配

每项至少从 `server/routes_*.go`、对应 `module_*.go`／`application_stores.go` 追到生产 Store／SQL，再检查相关客户端、迁移与测试。任务边界用于责任划分，跨域调用不得因“另一项负责”而停止追踪。

| ID | 主责任域 | 必核查路径／问题 |
| --- | --- | --- |
| S01 | `auth`、`org`、`platformschools`、`onboarding` | 人／服务会话、MFA／恢复、角色和学校／班级范围、名单来源、平台操作、RLS／审计；联查 `server/routes_auth.go`、`routes_org.go` 与迁移。 |
| S02 | `exam`、`paper`、`assessment`、`questionbank` | 冻结名单、题目／Rubric／模板、文档导入代次、题库版本／ACL、准备确认；纳入当前新增 `paper/scoring_coverage.go`、`template_validation.go` 和对应测试。 |
| S03 | `files`、`capture`、`captureupload`、`submission`、`segment` | 分块摘要与完成、对象所有权、DB／MinIO 状态、二维码身份、页替换、来源 revision、有效裁剪及访问控制。 |
| S04 | `grading`、`subjective`、`evidence`、`mathunderstanding` | 客观／OMR 路由、AI 建议、人工任务落点、分数与证据；特别核对新增 `scoring_run_routing.go` 与变更的 start／process 生产路径。 |
| S05 | `review`、`reviewannotation` 与质控 | 任务 claim／资格／双评／仲裁、金标、校准、Seed、漂移、答案分组、回评；明确覆盖 `goldpaper`、`calibration`、`seedquality`、`graderdrift`、`answergroup`、`backmark`、`qualitydashboard`。 |
| S06 | `score`、`scorerelease`、`releasegate`、`regrade`、`regraderelease`、`appeal`、`report`、`studentportal` | 工作成绩与公开快照、发布门禁、当前指针、申诉重评与公开证据、报表／导出可见性及统计口径。 |
| S07 | `apps/web-admin` | 登录／权限显示、考试准备与试卷导入、评分工作台、命令恢复／轮询；所有当前 UI／样式／E2E 改动均纳入，重点比对 readiness advisory 与 API 语义。 |
| S08 | `apps/student-portal`、`apps/desktop-client` | 本人成绩与图片授权、Tauri Rust IPC、原生扫描／凭据／加密 spool／SQLite、离线上传与草稿冲突；`teacher-portal` 当前仅 README，核实是否有真实入口。 |
| S09 | `packages/sdk`、`shared-types`、`ui`、`design-tokens`、`contracts`、`apicontract` | OpenAPI／生成客户端／手写 API 请求与响应、route-coverage 例外、跨端枚举和错误语义；公共展示组件仅查会影响业务动作的状态。 |
| S10 | `workerruntime`、`processing`、`ocr`、`imagequality`、`outbox`、`idempotency`、五个 Python Worker 与 `packages/worker-runtime-python` | 租约、幂等回执、结果回传／投影、取消重试和跨进程竞争；不要用 Memory 测试替代 Postgres 结论。 |
| S11 | `modelgovernance`、`aieligibility`、`gradingevaluation`、`modelcalibration`、`aidisagreement`、`ai-services`、`lab` | 模型配置／密钥／准入／能力、provider adapter、建议证据与版本、合成评测及生产晋级边界；遵守 `lab/AGENTS.md`。 |
| S12 | `infra`、`scripts`、`.github`、`config`、`db`、`migrations`、server 装配公共层 | 迁移权限／checksum／RLS、Compose 入口、备份恢复、CI 契约和真实测试门禁、生产 Store 图、路由包装与配置降级。 |

主控的两名执行者可各承担原分配中的六项；对共用 `server`、迁移、SDK 文件采用“专项提出问题、跨模块复核端到端路径”的方式避免遗漏。当前工作区变更优先排在 S02／S04／S07，并联核对 S09 契约与 S10 异步结果；其他专项仍需审查全域当前源码。

## 5. 六项跨模块追踪

| ID | 从源到终点的追踪路线 | 交接专项 |
| --- | --- | --- |
| X01 | 浏览器／原生／Worker 身份 → `server` 路由包装 → AccessScope／TaskScope → SQL tenant context／RLS → 对象返回／审计。检查路径、body、列表、内部任务和平台维护例外。 | S01、S03、S06、S08、S10、S12 |
| X02 | 考试准备 UI 与 API → readiness／非阻断 advisory → 确认／冻结名单及题目快照 → 评分路由／人工任务 → 分数覆盖与发布门禁。当前未提交修改为首选样本。 | S02、S04、S06、S07、S09 |
| X03 | 桌面／Web 分块上传 → FileAsset／采集页 → 图像质量／OCR／切题／数学有效证据 → 阅卷显示及学生公开图片。按 source、hash、revision、tenant 校验。 | S03、S04、S06、S08、S10 |
| X04 | 用户命令或源任务创建 → business receipt／HTTP 幂等 → Worker claim／heartbeat／结果 → 域结果激活／投影 → 客户端重试与恢复。覆盖崩溃、失租、迟到、重复、取消。 | S03、S04、S07、S08、S10 |
| X05 | 规则／AI 候选 → 人工单评／双评／仲裁／质控 → 工作成绩 → gate／发布快照 → 学生读取 → 申诉／重评／新公开版本。验证每层当前指针与版本。 | S04、S05、S06、S08 |
| X06 | 受管模型配置／密钥 → 准入／能力 → AI adapter 输入与日志 → 建议证据／版本 → API／SDK 契约 → Compose／CI 生产接线。区分“可配置”与“已真实验证”。 | S04、S09、S11、S12 |

## 6. 六项复核与最终裁决规则

复核者与原发现者分开。每项复核对候选问题独立确认触发前提、生产路径、状态／权限条件、最小反例、影响范围及现有测试缺口；不能只复述初审意见。若某类无候选问题，改为抽查该类最危险的“未发现问题”路径。

| ID | 复核主题 |
| --- | --- |
| R01 | 身份、租户、学校／班级、学生本人及 Worker 越权候选；核对路由包装和实际 SQL/RLS。 |
| R02 | 当前工作区 readiness／scoring routing 候选；核对冻结快照、人工任务与所有题型，使用新增文件和未提交差异。 |
| R03 | 文件血缘、Worker 租约／回执、异步代次和并发候选；优先看 Postgres 事务，不以 Memory 语义代替。 |
| R04 | 分数、人工决议、发布快照、申诉／重评候选；核对旧／新公开版本和学生端可见性。 |
| R05 | Web／桌面／学生端与 OpenAPI／SDK 契约候选；核对实际 API 状态和恢复路径，不把 mock E2E 当生产集成。 |
| R06 | 模型治理、Python adapter、迁移／Compose／CI 候选；核对真实装配与环境要求，避免把文档声称当运行证据。 |

最终裁决只保留可由当前源码定位、解释具体触发条件与用户／数据影响的问题。每条发现需包含：严重度、文件与行号、调用／事务链、复现条件、预期与实际行为、受影响身份／状态、验证类型及局限；同一根因跨任务合并。优先顺序为：跨租户／公开成绩错误或不可逆数据损坏；身份与权限绕过；冻结事实／评分／发布状态错位；异步重复或丢失；契约与客户端恢复问题；低影响显示问题。没有生产路径或仅来自旧文档、README、测试名称、静态关键词的主张退回补证。测试通过只说明对应配置与样本通过，不能代替未覆盖场景的语义审查。

## 7. 已核对的验证入口与本阶段限制

- 根 [`package.json`](../../package.json) 提供 `ci:web:fast`、`ci:contracts`、`ci:web:full`、`test:e2e`、架构／schema／授权检查；各端有独立 build／Vitest。Go 声明见 [`go.mod`](../../services/api-gateway/go.mod)，CI 还有 Go、Postgres、Python、Rust、真实 Playwright 分层任务，见 [`.github/workflows/ci.yml`](../../.github/workflows/ci.yml)。这些只是可用门禁，不表示本规划者已执行或所有门禁已通过。
- 本阶段只做 `rg`／文件阅读／`git status`／`git diff`／数量盘点。未运行单元、集成、E2E、模型或硬件测试；未连接数据库；未验证运行期 RLS 与部署。后续审查如引用别的执行者测试结果，必须写清命令、环境、退出码与覆盖边界。
