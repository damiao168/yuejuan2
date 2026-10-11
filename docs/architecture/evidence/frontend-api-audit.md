# 前端、页面权限与 API 数据流审计依据

审计日期：2026-10-10。依据当前工作区（含既有未提交文件），仅生成架构文档。Git 基线见 [workspace-baseline.json](workspace-baseline.json)。功能图 [01](../diagrams/01-feature-tree.drawio) 按 F01–F10 分域；API 图 [08](../diagrams/08-api-data-flow.drawio) 分八页阅读。

## 1. 盘点口径与审读边界

静态清点覆盖 Web 管理后台、学生门户、桌面 React 前端以及 SDK/UI/shared-types/design-tokens。启动清点为 850 个 TS/TSX/CSS 文件、483 个非测试 TypeScript 源；末次重新清点为 852 / 484。末次目录见 [api-catalog-current-recheck.json](api-catalog-current-recheck.json)，变化源码复核见 [frontend-current-source-recheck.md](frontend-current-source-recheck.md)。教师独立目录只有 README；不能将预留目录记为独立应用已实现，也不能因此否认 Web 中实际存在的教师体验。

[api-catalog.json](api-catalog.json) 是 Go 字面量路由注册和 TypeScript AST 调用/导入清点，包含注册代码位置、调用位置、OpenAPI 对应关系及页面导入可达性。**AST 枚举不是完整审读，import 可达性不是函数执行证明**。目录中一个共享 API 模块可能被多页导入，模块所有操作不能都算成该页面的实际运行调用。图 08 只将已核对的核心调用链和按域关系画成主图；其余接口保留目录以便逐项追溯。

本分组审读记录共 944 项：完成全文及规则审读 56 项，pending 888 项；详见 [frontend-reviewed-files.json](frontend-reviewed-files.json)。本批未修改业务源和生成 SDK，未运行浏览器或生产 API，不宣称全部页面、表单和 536 个 handler 已完成动态验收。现有测试只核对相关断言，不把读取测试算作本次执行通过。

| 项目 | 当前证据 |
| --- | --- |
| 生产静态入口 | 24（productionRouteDefinitions） |
| mock 入口 | 4；生产 MODE 即使显式开 flag 也不注册 |
| 动态业务入口 | 考试工作区与管理考试总览另外统计，不在24内 |
| 后端 Handle 字面量注册 | 536；可能包含条件装配的模块 |
| 与 OpenAPI 精确匹配 | 288 |
| 注册例外清单 | 248；这是契约覆盖债务，不等于248个错误接口 |
| OpenAPI 操作 | 298；规范化参数名后全部存在对应注册 |
| 参数名等价 | 10；保留精确覆盖统计，不把参数别名当失踪路由 |
| 静态 SDK 使用 | 139 个唯一 operationId |
| 静态调用点 | 405；包含SDK、自定义传输与图片URL构造 |
| 有前端匹配的注册路由 | 376；其中217精确OpenAPI、159例外 |
| 另外的健康入口 | `/health`、`/health/live`、`/health/ready`：HandleFunc 不在536口径内 |

唯一未匹配的静态调用是桌面 `GET /health`。它在 [routes_system.go](../../../services/api-gateway/internal/server/routes_system.go) 实际注册，未匹配源于仓库路由统计脚本只收录 Handle。不能据此报告缺失接口，也不能把静态匹配扩张成全部536条已在生产运行。

## 2. 实际页面与身份结构

Web 使用 TanStack Hash Router；[appRouter.tsx](../../../apps/web-admin/src/router/appRouter.tsx) 匹配少量显式路径和通配兼容入口，实际内容由 [AppShell.tsx](../../../apps/web-admin/src/AppShell.tsx)、[routes.tsx](../../../apps/web-admin/src/router/routes.tsx)、[pageRendering.tsx](../../../apps/web-admin/src/router/pageRendering.tsx) 决定。生产入口的“productionReady”只是注册元数据，不能当作外部服务效果或硬件验收证明。

产品路径带 `/admin`、`/teacher`、`/auditor`、`/student` 前缀，表中给出移除体验前缀后的规范路径。角色决定允许的体验，业务权限本身不能把教师升级成管理端身份；[experience.test.ts](../../../apps/web-admin/src/router/experience.test.ts) 有对应断言。组织角色与工作区、路径授权和页面内操作权限分别存在，不能只用菜单隐藏证明授权。

| 规范路径 | 页面及主要 API 模块 | 注册状态 / 权限 |
| --- | --- | --- |
| `/question-bank` | QuestionBankPage → api/questionBank.ts | 生产静态入口；question_bank:read |
| `/dashboard` | DashboardPage / TeacherDashboardPage / IdentityLandingPage → api/dashboard.ts | 生产静态入口；会话/身份/工作区边界 |
| `/ai-chat` | SchoolAIChatPage → api/schoolAiChat.ts | 生产静态入口；角色：school_admin |
| `/account/sessions` | SessionManagementPage → api/auth.ts、api/mfa.ts | 生产静态入口；会话/身份/工作区边界 |
| `/platform/getting-started` | PlatformGettingStartedPage → api/onboarding.ts | 生产静态入口；tenant:manage；角色：platform_admin |
| `/platform/schools` | PlatformSchoolsPage → api/platformSchools.ts、api/org.ts | 生产静态入口；tenant:manage；角色：platform_admin |
| `/platform/model-config` | PlatformModelConfigPage → api/modelApiConfig.ts、api/modelGovernance.ts | 生产静态入口；任一：model:config:manage / model:provider:manage；角色：platform_admin |
| `/exams` | ExamManagementPage → api/exams.ts、api/workspace.ts | 生产静态入口；exam:manage |
| `/exams/new` | CreateExamPage → api/exams.ts、api/org.ts、api/examTemplates.ts | 生产静态入口；exam:manage |
| `/papers` | PaperRubricPage → api/papers.ts、api/questionBank.ts | 生产静态入口；exam:manage；file:manage |
| `/capture` | SubmissionCapturePage → api/submissions.ts、api/processing.ts | 生产静态入口；submission:manage；file:manage；ocr:manage；segment:manage |
| `/grading` | AdminGradingOperationsPage / GradingWorkbenchPage → api/review.ts、api/scores.ts | 生产静态入口；任一：review:manage / review:work |
| `/arbitration` | ArbitrationPage → api/review.ts | 生产静态入口；任一：arbitration:manage / arbitration:work |
| `/scores` | ScoreManagementPage → api/scores.ts、api/scoreReleases.ts | 生产静态入口；score:manage；exam:manage；submission:manage |
| `/reports` | LearningReportsPage → api/reports.ts | 生产静态入口；report:read |
| `/appeals` | QuestionAppealCenterPage → api/questionAppeals.ts、api/appeals.ts | 生产静态入口；appeal:read |
| `/members/students` | StudentManagementPage → api/org.ts | 生产静态入口；org:manage |
| `/members/classes` | ClassManagementPage → api/org.ts | 生产静态入口；org:manage |
| `/members/teachers` | TeacherManagementPage → api/users.ts、api/org.ts | 生产静态入口；org:manage |
| `/organization/setup` | OrganizationSetupPage → api/org.ts | 生产静态入口；org:manage |
| `/system/models` | AppShell 重定向 /platform/model-config?tab=governance；同页兼容入口 | 生产静态入口；model:read；角色：platform_admin |
| `/grading/subjective-batches` | SubjectiveGradingBatchPage → api/subjectiveGrading.ts | 生产静态入口；grading:manage |
| `/system/status` | SystemStatusPage → api/system.ts | 生产静态入口；system:read；角色：platform_admin |
| `/audit` | AuditLogPage → api/audit.ts | 生产静态入口；audit:read |
| `/review` | ModulePage；提供 /grading 替代入口按钮 | mock（生产不注册）；review:manage |
| `/quality` | ModulePage；模拟路由占位 | mock（生产不注册）；quality:read |
| `/permissions` | ModulePage；模拟路由占位 | mock（生产不注册）；org:manage |
| `/settings` | ModulePage；模拟路由占位 | mock（生产不注册）；system:manage |

动态管理总览为 `/exams/overview/(session|standalone)/:id`，返回整场考试管理投影。考试工作区为 `/exams/:examId/:section`，admin 分区有 `overview/students/paper/questions/template/capture/processing/grading/quality/scores/appeals/reports/settings`（13项）；teacher/auditor/student 分区数组为空。AppShell 将旧 `overview` 分区映射到 `students`。页面动作再按相应业务权限和归档只读事实控制，见 [pageRendering.test.tsx](../../../apps/web-admin/src/router/pageRendering.test.tsx)。因此“考试负责人”工作区定义存在不意味着所有持有 exam:manage 的用户都会得到管理体验。

`/system/models` 是兼容入口：AppShell 将其重定向到 `/admin/platform/model-config?tab=governance`，页面映射也复用 PlatformModelConfigPage；不是两套独立治理实现。`paper/questions` 同样复用 PaperRubricPage，以 `initialView` 区分资料与题目。`/review` mock 页只提供前往真实 `/grading` 的按钮。旧 AppealCenterPage 文件仍存在，但真实 `/appeals` lazy 导入的是 QuestionAppealCenterPage；仅文件存在不能计为第二条生产申诉路径。

独立学生门户自己解析 `#/` 和 `#/exams/:examID`，同时校验 student 角色与 student:grade:read 后再进入成绩页面；Web 的 student 体验主要是 IdentityLandingPage 入口，不应与独立学生门户混淆。桌面客户端有登录、文件采集、匿名复核、离线草稿、同步队列、诊断和本地日志；独立 teacher-portal 的 [README](../../../apps/teacher-portal/README.md) 明确只是预留。

## 3. 组件、状态和表单边界

Web 采用 lazy 页面、features 业务模块、components 共享布局/工作台，以及 api 分域包装；不是每个页面都复制一份业务实现。AppShell 负责会话和产品身份，pageRendering 负责页面选择与权限属性，考试工作区投影负责归档只读；页面内部仍有业务状态与未保存输入。

[query/client.ts](../../../apps/web-admin/src/query/client.ts) 的查询默认30秒 staleTime、5分钟 gcTime，客户端4xx不重试，临时失败最多有限重试，写操作不自动重试。React state、ref、QueryClient、命令恢复记录职责不同：state负责渲染；ref/会话代次拒绝迟到结果；QueryClient保存可失效的服务端投影；[businessCommand.ts](../../../apps/web-admin/src/api/businessCommand.ts) 只持久化不透明命令ID，冻结输入由服务端恢复，不能用新编辑草稿覆盖结果未知的旧操作。共享Promise减少同会话重复续发，但不能据此保证跨进程所有命令只执行一次。

退出先使命令作用域失效，然后取消读请求与清Query缓存；已发出的远端写请求仍须通过原命令回执确认。认证写请求串行，防止迟到Cookie覆盖下一次登录；锁定保留身份显示但阻断命令scope。相关行为在 [sessionLifecycle.test.ts](../../../apps/web-admin/src/auth/sessionLifecycle.test.ts) 和路由渲染测试中有断言，本批未执行这些测试。

表单验证只能对已审读实例作结论。新建考试 [createExamValidation.ts](../../../apps/web-admin/src/features/exams/create/createExamValidation.ts) 检查名称、类型、学校、年级、参考班级、科目、满分、时间与阅卷方式；资料优先模式允许稍后补题目，模板模式才检查模板分区完整性与总分，容差为0.001。不能以此推断全部页面表单都使用统一 Schema 或所有输入都已充分验证。其余页面的详细校验、状态重复和异步边界仍按逐文件记录 pending。

Web、桌面和学生端各有传输包装，认证方式与本机能力不同。Web ApiClient 同源Cookie为默认，也可用Bearer；上传XHR报告请求体进度，100%仍须等待服务器回执；SSE按完整帧解析，网络chunk不是事件。默认写请求的随机Idempotency-Key只覆盖一次请求，业务重试必须显式复用原键。多份传输是维护边界，不能直接宣布是缺陷；改契约时应共同核对错误、CSRF、幂等与文件下载行为。

## 4. 核心 API 的真实数据路径

图08分域页对应路由和前端api包装，并保留代码出处。一般链为“页面 → 生成SDK或自定义包装 → Go ServeMux/guard → Handler/业务函数 → Store/协调器 → PostgreSQL或对象存储”，返回数据沿调用链反向回到页面。部分Handler直接调用Store，不绘制不存在的统一Service层。

- 组织、考试、试卷与题库：服务端从身份取得租户/资源scope，题库实例化同时要求考试管理和题库读取。资料导入解析与用户采纳分开，不能把解析产物自动当作最终题目或正式评分规则。
- 采集：上传原件后再登记领域文件并启动批次处理，解码、质量、模板匹配、配准与OCR是多个任务/命令。已有模板候选与人工绑定已实现；没有把未知版面自动建模板或YOLO备用识别连入实际主流程。
- 主观题：生产同步或worker入口调用一个 activeAdapter，Go传冻结证据给Python grading_agent，供应商响应经服务端结算，再进入人工确认。A/B/C Panel内核与影子评估存在，但普通路由没有自动调用它，详见图07与流水线审计。
- 人工复核：按分派、领取/续租、状态和修订限制处理；教师走匿名任务裁片，原件读取是独立管理权限接口。人工双评与AI Panel是不同机制。
- 成绩：最终汇总和公开发布分开。旧 /exams/{examId}/publish 更新grade状态并执行旧质量检查，不创建score_release快照/当前指针；新版 /score-releases/{id}/publish 才经相应发布协调器维护快照发布。学生端新版图像/批注/申诉用release锚定公开版本，旧学生grade接口仍注册；服务端读取原分，不信任浏览器传来的得分。
- 文件：PostgreSQL file_asset 保存归属、私有bucket/key、hash和生命周期，MinIO保存二进制。上传采用pending写入、对象Put/验证、激活和对账补偿；二者不是单一数据库事务。worker在文件授权前恢复持久任务scope并核对任务载荷文件。
- 桌面：原生SQLite/加密Spool是持久队列和草稿的权威，React队列是投影；browser-dev回退、WIA发现/预检、现场物理采集验收不能混为一类。设备登记/吊销与自动更新在能力接口中仍标为待接入。

## 5. 已确认问题与建议

### P1：通用幂等收据可提前返回，内层当前资源/权限未重查

[router_guards.go:103](../../../services/api-gateway/internal/server/router_guards.go) 的通用 requireAuth 组合为 `authenticate(idempotent(resource(permission(handler))))`。认证仍执行；命中相同actor/tenant、请求路径/正文和键的已完成收据时，[idempotency/middleware.go](../../../services/api-gateway/internal/idempotency/middleware.go) 可直接返回旧结果，内层资源scope、权限及业务handler不会再次运行。旧会话会在访问/角色变更时被废止，因此不是未认证绕过；风险是同一actor的新合法会话仍可重放原键，在当前scope收窄后读取旧响应。根审计已独立核实此边界。

requirePermission、requireRecentPermission和平台模型专用guard已经把资源/权限（及近期认证）放在缓存读取前；题库CommandIdentity、MFA、学校助手还有专用路径。建议将所有允许重放的响应入口明确分型，并保证读取缓存前验证当前资源和业务授权，补充“旧命令成功→缩小scope→新会话原键重放”的定向回归。不能只重排当前图表来声称代码已修复，本次没有改动业务实现。

### P1：旧成绩发布与新版快照发布同时保留

[score/handlers.go:172](../../../services/api-gateway/internal/score/handlers.go) 调用旧 Store.PublishGrades；[score/store_postgres.go:333](../../../services/api-gateway/internal/score/store_postgres.go) 在自身事务里检查旧 qualityReportTx，更新 submission_grade/final_grade/exam 与命令回执，没有创建新版发布快照或当前指针。新版 [scorerelease/handlers.go:214](../../../services/api-gateway/internal/scorerelease/handlers.go) 经 PublicationPublisher 或新版 Service 发布快照。两条API在 routes_release.go 都有注册，旧学生单份grade读取也仍存在。旧路径有质量检查，不能写成完全无门禁；它没有保证新版策略门禁、快照和发布锚点。建议明确旧接口退役或统一发布协调入口，回归旧API仍可调用时的门禁、锁定和公开版本一致性。

### P2：接口契约覆盖债务与多传输维护成本

536个静态Handle有248个注册例外，其中159条有前端调用匹配；关键工作流的一部分只能由自定义api包装维护。OpenAPI298个操作经参数名规范化全部有对应注册，但精确字符串覆盖数只有288。10个参数名差异是别名，不是漏注册，覆盖统计与匹配统计应同时保留。建议按业务域推进权威OpenAPI、例外收敛和SDK正常生成，覆盖JSON/Blob/multipart/SSE/图片URL等契约，不手改generated目录。

### P2：既有设计/验收说明和当前代码存在时间差

enterprise-architecture 等早期文档保留独立教师目录、设备绑定、自动更新和原生采集规划；部分生产准备文档对学生端、AI和离线能力仍用较早阶段表述。当前代码已有独立学生端、真实worker、持久桌面存储和AI建议链，仍没有普通生产三智能体自动接入与未经现场验收的完整扫描仪承诺。建议以图纸/证据为当前实现入口，将规划和现场验收边界旁置；本次保留原文，未改设计或运行输入。

未确认全仓重复状态、全部无效页面或全部表单缺陷，不给出不存在的P0结论。缓存重放、流水线事务恢复和真实设备/供应商验收需分别处理，不以静态路由匹配证明运行可靠性。

## 6. 后续维护

改路由时同步核对production/mock/dynamic三个口径、体验前缀、hasRouteAccess和renderAppRouteContent；改API时从权威OpenAPI/真实注册更新证据，再检查领域调用与状态/权限后更新图08。`/system/models`等兼容入口应记录最终重定向目的，旧文件/旧路由不能自动记为新增生产功能。

静态目录可用 `node docs/architecture/evidence/extract-frontend-api.mjs` 重建；这会读取正常依赖与仓库路由覆盖工具，只写evidence目录。图规格用 `python docs/architecture/tools/write_frontend_specs.py` 生成，再由统一 `build_diagrams.py` 生成可编辑XML。生成器检查核心端点精确注册及证据路径；这种检查不代替handler审读、动态测试或视觉检查。逐文件状态更新只能在全文、调用与必要测试证据核对完成后进行，不因脚本枚举而清掉pending。
