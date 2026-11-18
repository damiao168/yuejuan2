# yuejuan 架构审查与整改

本报告对照 2026-10-10 架构审计的 P1/P2 项，保留 2026-11-13 工作区的审查记录，并与第一阶段集成后的迁移、路由清单和源码路径同步。原审计的逐项风险复核见 [历史交付复核](evidence/final-delivery-audit.md)。

## 系统架构

Web 管理端提供学校、考试、采集管理和教师阅卷。学生门户按已发布成绩版本提供结果与申诉。Tauri/Rust 桌面负责扫描、离线存储和上传队列。各端共享 OpenAPI 生成的 TypeScript SDK，流式、二进制和本地操作由相应客户端处理。[项目配置](../../package.json) · [共享 SDK](../../packages/sdk/src/index.ts)

Go API 是模块化单体，按身份、考试准备、采集、阅卷、发布、模型治理和学校助手装配。PostgreSQL 保存业务与任务状态，MinIO 保存二进制原件。Python Worker 通过 HTTP 领取、续租和提交任务。评分服务经受控配置接入推理模型。Redis 用于认证限流。各进程的部署关系见图 02、10。[模块装配](../../services/api-gateway/internal/server/application_modules.go) · [Compose](../../infra/docker-compose/docker-compose.yml)

阅卷主线为考试和冻结题集准备、答卷采集、图像质量与配准、题目裁片、客观题或 AI 评分、人工终评、确认、发布。常规文本、数学 v2、考试图像评分计划使用不同输入和适配器。图像计划可按配置执行单角色、A/B 主评或条件 C 仲裁。AI 建议经服务端校验，最终成绩与公开快照单独维护。[主观 Worker](../../services/api-gateway/internal/subjective/handlers_worker.go) · [图像评分计划](../../services/api-gateway/internal/subjective/image_panel_execution.go)

## 整改对照

| 项目 | 当前实现 |
| --- | --- |
| P1-01 兼容成绩发布 | 兼容 POST 保留原地址和响应，调用正式发布协调流程。当前快照、旧工作成绩发布状态和命令回执同事务提交。兼容学生 GET 从正式快照读取并应用可见性。迁移会冻结尚无当前快照的历史公开成绩，沿用历史可见题分口径。 [兼容适配](../../services/api-gateway/internal/score/publication.go) · [正式事务](../../services/api-gateway/internal/score/publication.go) · [迁移 000230](../../services/api-gateway/migrations/000230_score_release_snapshot_immutability.sql) |
| P1-02 幂等授权 | 当前身份、对象范围与业务权限在处理命令回执前校验。请求指纹绑定角色、权限和派生数据范围。普通读取按当前授权重新生成。 [路由授权](../../services/api-gateway/internal/server/router_guards.go) · [授权指纹](../../services/api-gateway/internal/idempotency/middleware.go) |
| P1-03 配准提交 | 配准运行、裁片、页状态和任务完成在同一事务写入，事务内锁定任务并以数据库时间复核租约。 [配准事务](../../services/api-gateway/internal/capture/registration_result_command.go) |
| P1-04 主观评分完成 | 成绩、用量、任务与运行状态同事务提交。相同完成请求在重新 Prepare 前验证回执。 [完成事务](../../services/api-gateway/internal/subjective/worker_completion_atomic.go) · [Worker 路由实现](../../services/api-gateway/internal/subjective/handlers_worker.go) |
| P1-05 发布快照保护 | 发布明细的插入、修改和删除均检查父发布状态，移动时校验旧、新父记录；与发布并发时按父记录锁排序。 [迁移 000230](../../services/api-gateway/migrations/000230_score_release_snapshot_immutability.sql) |
| P2-01 Panel 人工交接 | 原 Panel 内核在人工终态缺少任务时补齐交接，保留冻结证据。任务创建和关联写入失败均可恢复。 [恢复逻辑](../../services/api-gateway/internal/subjective/panel_orchestrator.go) · [故障回归](../../services/api-gateway/internal/subjective/worker_remediation_postgres_test.go) |
| P2-02 租户与学校外键 | 场次、考试、教学组织、学籍等父引用按业务范围补复合外键。考生快照的考试与学生身份校验同租户，冻结班级及年级继续保存历史。迁移检查存量约束，发现冲突时停止，由维护者核对后重试。 [迁移 000231](../../services/api-gateway/migrations/000231_exam_enrollment_scope_foreign_keys.sql) |
| P2-03 批量指标刷新 | 使用 PostgreSQL 语句触发器的变更表，按受影响租户去重刷新。跨租户更新兼顾旧、新租户；一条混合插入与更新的 UPSERT 可能分别产生两个非空事件。 [迁移 000232](../../services/api-gateway/migrations/000232_platform_school_metrics_statement_refresh.sql) · [数据库回归](../../services/api-gateway/internal/db/architecture_scope_postgres_test.go) |
| P2-04 API 与 SDK | 权威 OpenAPI、生成 SDK 和逐条路由覆盖共用 CI 契约门禁。第一阶段的 607 条注册路由中，385 条匹配 OpenAPI，另外 222 条由例外台账逐条登记。新打印操作有对应 OpenAPI 定义，二进制下载由生成 SDK 的 Blob 方法处理。 [路由覆盖](../../services/api-gateway/openapi/route-coverage.json) · [CI 配置](../../.github/workflows/ci.yml) |
| P2-05 模块与界面职责 | 采集、题库、复核、模型治理和组织按能力拆接口。组织 Handler、入门读取器、审核与采集审计依赖使用实际需要的能力；题库、学校助手、模板识别、复评和桌面导入按职责拆出处理逻辑。架构门禁保留测试文件长度提示。 [组织接口](../../services/api-gateway/internal/org/types.go) · [架构门禁](../../scripts/check-architecture-boundaries.mjs) |

## 数据库、权限与验证范围

当前 ER 覆盖迁移定义的 PostgreSQL 表和本地 SQLite 表。数据库目录记录完整字段、约束与出处，图纸选择核心关系。迁移 000233 在打印批次新增 `print_layout`，用于冻结人工确认的二维码布局，旧记录保留空值。RLS 提供租户边界，学校、班级、任务和个人范围继续由数据库关系及应用授权共同约束。模型密钥使用绑定租户与配置的 AES-GCM 密文，关键操作使用近期认证。[数据库目录](evidence/database-catalog.json) · [ER 校验](evidence/database-validation.json) · [权限与部署图](diagrams/10-security-deployment.drawio)

[交付校验](evidence/delivery-validation.json)记录原生图纸、当前出处和审读账本校验。原始文件清单按来源版本保留，已失效审读列回待复核，新版本只有实际审读后才记录完成。[逐文件账本](evidence/file-review.tsv) · [汇总](evidence/audit-coverage.json)

本轮的运行验收限于本地定向回归与隔离测试库。实际部署、供应商模型准确率、扫描仪现场和生产规模性能仍依赖各自环境与样本。
