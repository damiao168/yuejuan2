# 当前实现架构审查

源码审计始于2026-10-10，交付复核于2026-10-11；依据为`main`、HEAD `d002c7b1f928e938b841e1203976c763b03e8485` 及现有未提交工作区。任务只读业务代码，新增架构文档；没有修复下面的问题。优先级描述建议处理顺序，静态风险不等于线上事故或已完成利用复现。

## 1. 当前系统实际架构

系统是 **多应用前端 + Go 模块化单体 + 独立 Python 计算进程**。Web 管理端内含教师体验，独立学生门户处理公开成绩与申诉，Tauri/Rust 桌面维护采集、离线 SQLite/加密资产与同步队列。共享 SDK、类型、UI 和设计令牌由 npm workspaces 管理。独立教师目录只有预留 README。见 [根工程](../../package.json)、[前端/API审计](evidence/frontend-api-audit.md)。

Go `server` 是组合根：Identity、ExamPreparation、CaptureProcessing、GradingQuality、Release、AIGovernance、AIFoundation、SchoolAssistant 等逻辑域在同一进程装配。路由经过全局 HTTP 中间件、认证、业务 guard 和幂等层；并非每个 Handler 都有独立 Service，部分直接调用 Store/协调器。生产使用手写 SQL 的 PostgreSQL Store，MemoryStore 支持测试。见 [application_modules.go](../../services/api-gateway/internal/server/application_modules.go)、[模块索引](MODULE_CATALOG.md)、图02/03。

PG 保存业务事实、运行状态、版本、审计和收据；MinIO 保存二进制对象，文件资产元数据与对象写入依赖状态与对账恢复，二者不构成单一数据库事务。Redis 用于认证限流，不能称为所有任务的消息队列。Worker 通过 HTTP claim/heartbeat/complete，PG 租约、generation、attempt 与来源事实决定可执行范围。outbox 当前组装日志 Publisher，没有确认外部消息总线接通；Qdrant 仅确认健康检查，不能据部署存在声称已有生产向量检索。见 [infrastructure.go](../../services/api-gateway/internal/server/infrastructure.go)、[root-audit.md](evidence/root-audit.md)。

实际主链为考试/名册/题目与评分资料准备，答卷上传，图像解码/质量，已知模板匹配和绑定，OpenCV 配准与 ROI，OCR/客观题规则或 AI 建议，人工终評、汇总确认，发布与学生读取。处理顺序、失败返回和不同评分模式见图05–09及 [pipeline-audit.md](evidence/pipeline-audit.md)。不把所有步骤画成一个无条件自动执行函数。

AI 治理配置、评估资格、评分规则与运行记录各自独立。普通生产入口使用单适配器，Go冻结答案/细则/证据及版本后请求 Python grading-agent，服务端结构化校验与结算，再进入相应人工流程。A/B 并行、C 盲仲裁内核及影子评估已有实现，但普通生产评分入口未自动组装该 Panel；因此三智能体全生产闭环属于接入缺口。六种 scoring_mode 必须使用真实大写枚举：`RULE_AUTO / AI_ASSIST / AI_FAST_CONFIRM / HUMAN_PRIMARY / DUAL_HUMAN / MANUAL_ONLY`。见 [assessment/policy.go](../../services/api-gateway/internal/assessment/policy.go)、[module_grading.go](../../services/api-gateway/internal/server/module_grading.go)、图07。

## 2. 盘点、关系与版本

| 范围 | 事实及限制 |
| --- | --- |
| 工程 | 原始3396文件；921个按文件名/目录识别的测试项，数量不代表断言覆盖率或执行通过 |
| Go依赖 | 68内部包、351唯一import方向，未发现静态import环；接口注入和动态调用不在此检测范围 |
| 前端入口 | 24生产静态入口、4 mock入口，另有动态考试工作区/总览；兼容入口不计为独立重复实现 |
| API契约 | 536字面量Handle注册；288精确OpenAPI、248例外；OpenAPI298操作规范化10参数别名后均能匹配注册 |
| 前端调用 | 405静态调用点、376唯一匹配路由；139个生成SDK operationId；唯一未匹配 `/health` 由HandleFunc真实注册 |
| PG结构 | 183迁移；198表、3083字段、883外键定义、305显式CREATE INDEX、42表DDL启用RLS |
| 桌面结构 | 8表、64字段、5FK、1显式索引；现有schema在SQLite内存库实际执行验证 |

PG的197张表来自迁移，`schema_migration`来自Compose初始化。编号000134缺席是源码事实，未据此认定迁移损坏。883外键包含单列与后补复合FK并存；显式索引不含PK/UQ隐式索引。117条动态构造的tenant复合FK已静态展开与交叉核验。PG未实际执行，条件DDL与存量回填分支需部署数据库确认。详见 [database-validation.json](evidence/database-validation.json)。

`tenant`不等于`school`。复合FK保证其声明的tenant/考试/答卷范围，单列FK不能自动保证同学校。M:N以真实桥表表示；条件唯一索引只约束当前有效记录，不能把全部历史画成1:1。版本链包括exam_paper、question_answer_key、rubric_version、模板/处理代次、subjective run的冻结版本、final_grade以及score_release快照/current指针。多态source_type/source_id只画有DDL依据的FK；其余标为应用关系。见图04及 [database-audit.md](evidence/database-audit.md)。

## 3. 优先处理的问题

**P0：本次未确认。**这不表示系统不存在P0问题；没有充分可定位证据时不补造结论。P1建议优先处理可达的发布、授权与任务一致性；P2为有前提的内核恢复、数据库防线和维护/规模问题。

### P1-01：旧发布入口绕过新版完整性门禁与快照

旧 `POST /api/v1/exams/{examId}/publish` 和新版 score-release 发布同时注册。旧路径有自己的qualityReportTx、score:manage、近期认证及考试范围检查，并在事务内锁定final_grade、更新submission_grade/exam；但不创建release/current，也不调用新版完整性、质量证据及重评/策略门禁。见 [routes_release.go:18](../../services/api-gateway/internal/server/routes_release.go)、[score/store_postgres.go:333](../../services/api-gateway/internal/score/store_postgres.go)、[scorerelease/store_postgres.go:1126](../../services/api-gateway/internal/scorerelease/store_postgres.go)。

具体条件：考试官方有两题，答卷只生成一题片段，该片段已有终評和整卷确认，名单/页数合格且没有未完人工/OCR失败。旧缺终评分检查只枚举已有片段，可通过；新版`question_coverage_incomplete`会拒绝。旧学生grade接口可读取已发布旧成绩，新学生result却可能因无current快照返回not found。旧读取也未引用新版VisibilityPolicy。源码可达，未操作生产API、未确认实际调用或泄露。

建议统一发布到不可变release/coordinator，再保留旧响应适配；兼容学生接口读取同一当前快照与可见性策略。对旧API仍可调用时的完整性/重评/题分隐藏补回归。完整逐项比较见 [流水线审计第8节](evidence/pipeline-audit.md)。

### P1-02：通用幂等重放早于当前资源/业务授权

通用guard组合为`authenticate(idempotent(resourceBoundary(permission(handler))))`。相同tenant、actor、请求指纹与key命中成功收据时，中间件直接返回旧响应，内层当前资源/权限不会重查。见 [router_guards.go:98](../../services/api-gateway/internal/server/router_guards.go)、[idempotency/middleware.go:144](../../services/api-gateway/internal/idempotency/middleware.go)、[store_postgres.go:53](../../services/api-gateway/internal/idempotency/store_postgres.go)。

角色/班级授权变化会提升security_epoch并废止旧会话，不能说旧会话绕过认证。风险条件是同一actor在权限收窄后重新取得合法较窄会话，仍在有效期内持有旧key和同一请求，可读到当前不应可见的旧响应；不会重复执行写操作，也不是任意跨用户/跨租户读取。`requirePermission/requireRecentPermission`已把授权置于缓存前，不适用此项。未运行利用复现。

建议所有返回收据的入口先校验当前授权，保留不可变收据与请求指纹；补“旧命令→收窄权限→重登→原键回放”及重新分派任务测试。详见 [root-audit.md第4节](evidence/root-audit.md)。

### P1-03：配准领域提交与runtime完成分属事务

[capture/handlers.go:760](../../services/api-gateway/internal/capture/handlers.go)先ApplyRegistrationResult提交配准、裁片与页就绪，再runtime.Complete。[registration_store_postgres.go:410](../../services/api-gateway/internal/capture/registration_store_postgres.go)领域事务未共同校验当前runtime租约。入口[scope_middleware.go:143](../../services/api-gateway/internal/workerruntime/scope_middleware.go)已经AuthorizeLease；问题是入口之后处理期间租约到期/被接管，旧执行者可能先写领域结果，后续Complete失败不能撤销已提交裁片。

建议在同一来源/task锁顺序与事务中验证lease、generation及冻结版本，一次提交结果和任务完成；参考模板匹配、质量结果已有的原子协调方式。未做竞争与故障注入，不能泛化成所有Worker结果都存在同样问题。

### P1-04：主观任务末步失败后正常完成重放受阻

[worker_completion_service.go:49](../../services/api-gateway/internal/subjective/worker_completion_service.go)按grade保存→runtime完成→UpdateRun成功三步执行。最后一步失败时grade已存、task已succeeded、run仍processing；再次CompleteWorker先Prepare，[worker_execution_service.go:91](../../services/api-gateway/internal/subjective/worker_execution_service.go)拒绝已成功task，当前正常端点不能补最后状态。它不表示grade丢失。

建议合并grade/task/run事务，或提供验证同requestID/结果hash的完成恢复路径，避免重复付费推理。静态异常链已确认，未做数据库失败注入。

### P1-05：发布快照数据库不可变守卫遗漏操作

迁移[000088:133](../../services/api-gateway/migrations/000088_storyA18_score_releases.sql)明细UPDATE仅查`NEW.release_id`；148/153的触发器只覆盖UPDATE/DELETE，没有INSERT。已发布release可被后续有写权限脚本追加明细，或把明细移到draft时绕过旧所属release检查。000183归档守卫只针对归档，不补所有发布保护。

前提是数据库写权限或未来新的错误调用方；当前正常scorerelease Store先建draft再写快照，没有确认普通HTTP任意编辑历史路径。建议正式新迁移覆盖INSERT/UPDATE/DELETE、UPDATE校验新旧release，明确与发布的锁顺序并测并发。详见 [DB-01](evidence/database-audit.md)。

### P2-01：Panel人工终态后任务/关联失败无修复

[panel_orchestrator.go:493](../../services/api-gateway/internal/subjective/panel_orchestrator.go)先写human_review，再创建人工任务，最后关联ID。后两步失败后重试在193的终态分支短路，不补任务或关联。只影响已实现Panel内核，不扩张为普通生产单适配器都受影响。建议同事务创建与关联，或终态加载时修复缺失handoff，见 [流水线审计第7节](evidence/pipeline-audit.md)。

### P2-02：后增组织/考试关系的租户数据库防线不均匀

迁移[000104](../../services/api-gateway/migrations/000104_exam_sessions.sql)、[000107](../../services/api-gateway/migrations/000107_exam_candidate_snapshot.sql)、[000108](../../services/api-gateway/migrations/000108_school_enrollment_model.sql)若干学校/学籍/考试父引用只用单ID FK，不能独立保证同tenant。org Store已有tenant/school查询校验，尚未证实正常API可直接跨校写入；这是后续导入/新SQL的数据防线风险。

建议先核查存量一致性，再以新迁移补符合业务范围的复合FK，保留应用校验。历史SQL和哈希不可直接改写，见 [DB-02](evidence/database-audit.md)。

### P2-03：行级运维投影刷新放大批量写入

[000165:219](../../services/api-gateway/migrations/000165_platform_school_operations.sql)refresh_platform_school_metrics聚合受影响tenant的全部相关来源；学生/班级/考试等行级触发器令N行批量操作触发N次聚合/upsert。不是扫描全部tenant。可能增加导入与锁成本，未压测、没有性能下降比例。建议真实规模测量后评估批次合并、语句级或异步增量刷新，见 [DB-03](evidence/database-audit.md)。

### P2-04：接口契约与实现维护边界

248条注册例外中159条有前端调用匹配，多种Cookie/Bearer传输、multipart、Blob、SSE和图片URL需要共同维护。10个参数名别名已有对应注册，不是漏接口。建议按域收敛权威OpenAPI与生成SDK，回归认证/CSRF/错误/幂等和二进制契约；不手改生成目录。见 [前端/API审计](evidence/frontend-api-audit.md)。

### P2-05：大页面、宽Store与跨域耦合

已运行`node scripts/check-architecture-boundaries.mjs`，退出码1，原有QuestionBankPage827行、CaptureBatchPage748行、PlatformModelConfigPage773行超过700门槛；capture.Store49方法、questionbank.Store26、review.Store21、modelgovernance.Store21及多个大hook有维护告警。未修改代码或债务预算。见 [门禁记录](evidence/architecture-gate.txt)。

真实import显示subjective依赖grading/paper/segment/assessment/mathunderstanding/modelgovernance，schoolassistant依赖多个业务域；HTTP层之外不少领域文件直接采用auth/httpx和database/sql。它们增加变更扩散，但不凭import认定所有跨域调用不合理。建议把大型Store按业务能力和事务边界拆接口，公共授权scope保持一致，逐步分离HTTP呈现与领域协调。未发现Go静态import环，不据此声称无运行时耦合。

## 4. 权限、配置和真实部署限制

实际人类角色包括platform_admin、tenant_admin、school_admin、teacher、grader、arbitrator、auditor、student及服务角色；`exam_owner`是前端工作区标识，非独立后端角色。每次认证查询当前会话/权限/security_epoch，资源scope进一步限制school/class/exam/student/任务。42表DDL RLS只提供tenant防线，不自动等于全表、全校或任务隔离。租户连接器拒绝superuser/BYPASSRLS并显式维护上下文；生产启用情况仍需确认。

API Key使用AES-GCM随机nonce与tenant/config AAD，PG保存密文；运行解析受控解密给adapter。密码Argon2id，兼容bcrypt升级；token随机、数据库存hash。图纸没有密钥值和学生业务数据。CSRF/CORS、近期认证、服务worker capability是不同边界，不能替代资源授权。详见图10及 [root-audit.md](evidence/root-audit.md)。

Compose定义PG、Redis、MinIO、Qdrant、API、web-admin、grading-agent、profile Worker、Nginx和可选监控。学生端能独立构建但主Compose未定义其独立服务；桌面运行于客户端宿主。当前Nginx为HTTP代理，外层TLS、实际profile、HA、生产运行状态与真实硬件均未确认。lab是独立隔离评测/校准工具链，不画成生产用户界面或必然在线调用。CI配置有多语言、安全、PG和隔离栈门禁，本次未执行全套。

2026-10-11末次变化补核：Compose样例API→grading-agent与OCR→数学验证已使用内部HTTPS、只读CA/证书挂载与TLS健康探针，preflight要求这两条跨容器链路使用HTTPS；Agent与数学验证监听也校验证书和明确的本地回环例外。图10已同步。配置存在不等于证书可用或生产已运行；外Nginx HTTP与外层TLS待确认结论保持。发布门禁新增只读Inspect共用当前考试规则，正式发布仍重新求值并保存证据，未接管旧发布。见[末次变化复核](evidence/late-current-recheck.md)。

## 5. 缺失能力、历史文档与待确认

已有条码/模板候选、锁定考试模板、人工绑定、四点配准预览/应用/撤销及ROI。未找到YOLO备用或未知版面自动生成可用模板的可靠实现/规划依据；记待确认，不虚构规划。空白检测区分整页像素、OMR选项与OCR空结果，不能将空OCR等同零分。模型图片协议、视觉能力、识别阈值和实际评分准确率都需数据与环境验证。

普通三智能体接入、Panel人工恢复、旧新公开来源统一、设备登记/吊销和自动更新、扫描仪现场验收为后续重点。早期enterprise/production文档与当前实现存在时间差，本任务保留原文，使用本套图纸作为当前源码入口；不得把规划标题判定为上线功能。

## 6. 验证与审读边界

10份70页原生XML已实际由app.diagrams.net导入、切页、渲染并导出SVG；结构/出处/节点几何检查无错误，原生检查无悬空边、文本溢出或连线穿过无关节点；视觉复查覆盖70页。ER覆盖206表，SQLite内存验证通过，原始工作区内容保留核对见 [workspace-preservation.json](evidence/workspace-preservation.json)。详细命令与结果见 [acceptance-report.md](evidence/acceptance-report.md)。

截至2026-10-11 11:47（Asia/Shanghai），58个原始文件hash变化，HEAD不变；未确认变化来源，没有回滚，相关当前架构事实已复核，未重读文件保持pending，见[concurrent-workspace-review.md](evidence/concurrent-workspace-review.md)。基线检查退出1，不能报告全部原始文件hash未变。前端专项复核时为852文件/484非测试TS，路由/API数不变。

原始3396文件中181已完整人工审读、64部分审读、3151未完成人工审读；3215保持pending，参见[file-review.tsv](evidence/file-review.tsv)。PG迁移执行、完整CI、生产API、租约竞争/失败注入、真实模型与硬件验收未执行。这里只对有出处的架构事实和静态可达缺口作结论，不声明全文件安全审计或动态验收完成。
