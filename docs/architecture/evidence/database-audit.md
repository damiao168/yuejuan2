# 数据库事实、关系和审计边界

本报告基于 2026-10-10 的当前工作区，Git HEAD 为 `d002c7b1f928e938b841e1203976c763b03e8485`。工作区存在未提交修改；迁移及应用源码以本次保存的文件哈希为依据，不能把 HEAD 本身视为当前全部源码。这里只读取业务实现并生成文档，没有修改业务代码、历史迁移或持久数据库。

## 1. 证据与覆盖范围

- [database-catalog.json](database-catalog.json)：按迁移顺序重建的字段、类型、PK、FK、唯一约束、显式索引、RLS和来源历史。
- [database-reviewed-files.json](database-reviewed-files.json)：逐文件审读状态及 `source_sha256`。未完整审读的文件仍是 `pending`；文件枚举和 SQL 抽取不算人工审读。
- [database-validation.json](database-validation.json)：源哈希、全部FK字段/父表、117条动态租户FK、ER覆盖和SQLite内存执行的交叉验证。
- [图04](../diagrams/04-database-er.drawio)：1张全局页、1张关系读法页和27张业务域页。全部206张表都能在业务域页找到；主干和详页只绘精选外键，完整关系保留在目录中。

| 项目 | PostgreSQL | 桌面 SQLite |
| --- | ---: | ---: |
| 目录中的表 | 198 | 8 |
| 字段 | 3083 | 64 |
| 外键定义 | 883 | 5 |
| 显式 `CREATE INDEX` 索引 | 305 | 1 |
| DDL启用RLS的表 | 42 | 不适用 |

183个SQL迁移编号覆盖 `000001` 至 `000184`，编号 `000134` 在当前仓库不存在。197张PG表来自迁移，另1张 `schema_migration` 来自 Compose 的迁移初始化（`infra/docker-compose/docker-compose.yml:166`）。迁移登记表不是题目或考试业务实体。显式索引数不包含PK/UQ隐式索引，也不代表数据库中的物理索引总数；`LIKE INCLUDING ALL` 复制的结构保留来源说明。外键定义数包含同一业务关系同时保留的单列FK和后补租户复合FK，不能当成883种独立业务关系。

目前数据库相关逐文件清单包含336个文件，60个记录完整审读、7个部分审读，其余269个未完成人工审读；总 `pending` 是276个。该统计只说明数据库分工范围，不是全仓审计完成声明。已完整审读迁移的受保护状态表示未改历史SQL，不表示数据库已执行这些迁移。

PG结构采用源码静态重建，未连接已部署数据库，也未执行PG迁移。SQLite校验只在 `:memory:` 中执行现有 `schema.rs` 的SQL并读取 `PRAGMA`，没有创建或修改用户的 `offline.sqlite3`。SQLite实查结果与清单一致：8表、64字段、5外键、1显式索引。

## 2. 数据访问与关系读法

Go API主要通过 `database/sql`、pgx和各模块 `store_postgres.go` 的SQL访问PG。字段与约束的权威来源是迁移，不以API响应结构或Go类型代替DDL。桌面Rust使用rusqlite维护独立离线库。相关实现：`services/api-gateway/internal/db/postgres.go:26`、`apps/desktop-client/src-tauri/src/durable_store/schema.rs:4`。

图中父表→子表的实线表示真实FK。`1:N`不要求每个父行至少有一个子行；`1:0..1`表示子表的FK字段受到PK或完整唯一约束限制。条件唯一索引只约束满足 `WHERE` 的当前记录，不得将全历史画成一对一。可空FK允许不存在父引用；同租户FK也不自动证明同学校、同考试或同答卷。

真实多对多关系通过桥表表达：`app_user → user_role ← role`、`role → role_permission ← permission`、`app_user → teacher_class ← school_class`、`exam → exam_class ← school_class`。两边均为实际1:N FK；没有在两个父实体之间虚构直接FK。依据：迁移 `000001:60/72`、`000002:58`、`000003:22` 和后补复合关系 `000020`。

`score_release_current` 的PK为 `(tenant_id, exam_id)`，因此每个考试至多一个当前发布指针；它通过 `(tenant_id, release_id, exam_id)` 引用同考试的发布。SQLite `upload_session.local_asset_id` 同时为PK和FK，因此每个本地资产至多一个上传会话。`question_answer_key`没有单题完整唯一约束，不能画成每题只有一条答案历史。

## 3. 核心实体、隔离和版本链

### 学校与考试

`tenant` 是隔离单位，`school` 是实际学校实体。`app_user.school_id`、年级、班级、学生、考试等保留学校归属。不能把所有 `tenant_id` 自动改称 `school_id`，也不能凭平台学校投影把租户与学校画成DDL保证的一对一。

迁移 `000020_story047_tenant_constraints.sql` 使用 `pg_temp.ensure_tenant_identity` 和 `ensure_tenant_fk`，给早期业务表补 `(tenant_id,id)` 唯一键及复合FK；本次目录和独立验证展开了117条动态FK。迁移 `000024_completed_scope_integrity_hardening.sql` 进一步把页面质量记录和文件引用绑定到相同租户、答卷与页面。这里的复合字段比仅检查 `tenant_id` 更强，但仍只对具体声明的关系生效。

`exam_session` 表示整场考试，`exam` 表示学科考试；`exam.exam_session_id`引用整场。`exam_class`绑定班级，`exam_candidate_snapshot` 保存考生姓名、学号、年级和班级快照，后续学籍变化不应直接覆盖考试名册事实。`academic_year`、`grade_cohort`、`student_enrollment`分别表达学年、届别和学籍过程。快照里的 `grade_id_snapshot` / `class_id_snapshot` 没有声明FK，保留快照ID不等同于实时组织关系。依据：`000104`、`000107`、`000108`。

### 试卷、模板、答案和评分

`exam_paper` 有 `(tenant_id,exam_id,version_no)` 唯一约束；`question`关联考试及试卷。标准答案 `question_answer_key.answer_version` 和 `rubric_version.version/content_hash` 分别表达答案及评分细则版本；`question_rubric`关联具体评分细则。实际字段类型和完整约束以catalog为准，字符串版本号不能画成不存在的独立版本表FK。依据：`000004_paper_question.sql:23/37/58/72/89`。

`answer_sheet_template`、`exam_answer_sheet_template_binding`表达模板与考试绑定；`page_template_match_run`、`page_registration_run`、`submission_page_quality_run`保留处理运行记录。模板关联、ROI和处理代次不是同一个实体，图04分为采集、质量/配准、打印域。依据：`000024`、`000130`、`000131` 及各表catalog历史。

`submission → submission_page → answer_segment`保存答卷、页面和题目答案区域。`answer_segment_answer`保存答案文本/载荷事实，`question_grade`保留客观评分版本；`uq_question_grade_current` 是 `WHERE is_current AND deleted_at IS NULL` 的部分唯一索引，保证每个答案段至多一个当前有效客观评分，并允许历史评分记录。依据：`000006`、`000008`、`000010`、`000036`、`000050`。

`subjective_grading_run`冻结 `answer_version/rubric_version/model_version/prompt_version`。`subjective_grading_panel`关联答案段与冻结答案/细则版本，分别绑定 `primary_a/primary_b/arbiter` 的运行。`uq_subjective_grading_panel_answer_version`约束未软删面板的答案和细则版本组合；`uq_subjective_grading_run_panel_role`约束同面板每个角色。`subjective_panel_policy.model_set_reference`绑定评估使用的模型组合，面板策略及运行回链触发器提供三智能体可追溯性的部分数据库保证。实际决策算法仍需结合业务服务审读，不能只凭表名证明仲裁已正确运行。依据：`000063`、`000155`、`000160`、`000162`。

`review_task → human_grade`记录人工任务与评分事实。`final_grade`与`submission_grade`分别保存题目及整卷最终成绩；发布后展示数据来自 `score_release_item` / `score_release_question`，与可变工作成绩分开。`source_type/source_id`等多态来源字段只有实际声明的FK才在图中画成实线，其余属于应用层引用，不能按字段名补造关系。

`score_release` 用 `(tenant_id,exam_id,version)` 和幂等键约束版本与重放；`score_release_current`引用已发布版本。回滚创建新草稿并复制历史快照，不把旧版本改为草稿。普通发布服务先获取考试事务锁，创建快照、发布并移动当前指针。依据：`000088_storyA18_score_releases.sql:5/45/55/77`、`services/api-gateway/internal/scorerelease/store_postgres.go:31/77/259/908`。

### RLS、归档和审计

迁移 `000139_high_risk_tenant_rls.sql:54` 对学生、答卷、页面、答案段、OCR结果、人工评分、最终评分、申诉和文件资产启用并强制RLS；后续题库、MFA、面板与平台投影等迁移扩展覆盖，静态目录合计42张。其他表仍需依赖应用SQL作用域和具体FK；不能写成所有表均受到RLS保护。

启用租户连接器时，每次数据库操作覆盖 `edugrade.tenant_id`，连接重用时不沿用前一请求租户。缺少作用域写 `unscoped`，对受RLS保护的表拒绝放行；进程维护任务可显式使用 `maintenance`。连接器拒绝superuser/BYPASSRLS登录。是否实际启用仍由运行配置及部署身份决定，源码不能证明生产库全部具备该条件。依据：`services/api-gateway/internal/db/tenant_context.go:19/45/74/136`。

`deleted_at`表示软删除，不能用它推断数据库已物理删除关联事实。`revision`为部分聚合根及任务提供并发版本，不能泛化为所有写入均乐观锁。`audit_log`、`event_outbox`、幂等及业务命令收据是不同职责。迁移 `000183_exam_management_archive.sql` 对从考试可达的业务表建立归档写入守卫，并明确排除部分投影/来源凭据；归档守卫不替代成绩发布不可变守卫。

### 桌面离线库

本地8表：`local_asset`、`scan_batch_local`、`upload_session`、`upload_chunk`、`local_asset_ingest`、`local_asset_chunk`、`sync_event`、`draft`。初始化执行 `PRAGMA foreign_keys=ON` 和 WAL；5个本地FK均有 `ON DELETE RESTRICT`。SQLite内存验证确认SQL与目录匹配，但内存日志模式不是磁盘WAL运行验证。

账户目录由server、tenant和actor组合哈希隔离，载荷与资产使用AES-GCM，凭据主密钥按目录区分；这属于本地文件与身份边界，不是PG RLS。`exam_id`、`remote_id`、`task_id`等远端ID没有跨库FK。远端成功、本地未确认及中断恢复需结合队列、分块和上传流程，不凭FK存在声称网络操作原子性。依据：`apps/desktop-client/src-tauri/src/durable_store/schema.rs:4`、`paths.rs`、`crypto.rs`、`queue.rs`；部分文件状态仍为pending。

## 4. 已确认的问题与建议

### DB-01 / P1：已发布快照的数据库不可变规则不完整

证据：`000088_storyA18_score_releases.sql:133` 对明细UPDATE只查询 `NEW.release_id`；`:148/153` 的明细触发器仅覆盖 `UPDATE OR DELETE`，不覆盖INSERT。后续迁移没有重定义该函数或补齐这两个触发器。`000183`归档触发器只阻止已归档考试的业务写入，不会把所有已发布但未归档考试变为只读。

有数据库写权限的后续脚本或新调用方，可以向已发布release增加满足现有FK/UQ的明细；也可以把已发布明细的 `release_id` 改指向一个draft，旧release不会因 `OLD.release_id`被检查而拒绝。相关FK仍可成立，数据库不可变承诺因而有缺口。普通 `scorerelease.PostgresStore` 创建新draft后写快照，并未找到允许HTTP调用方任意改旧明细的路径，故该结论是数据库第二防线问题，不是已证实的普通API直接利用。

建议在新迁移中同时覆盖INSERT、UPDATE、DELETE，UPDATE检查新旧所属release；与发布事务共用明确的锁顺序，防止快照写入和发布并发绕过检查。增加针对已发布明细追加、搬移、并发发布的数据库集成验证。本次仅报告，不修改迁移。

### DB-02 / P2：后增考试/学籍关系没有同租户复合FK

证据：`000104_exam_sessions.sql:1/21/25`、`000107_exam_candidate_snapshot.sql:3`、`000108_school_enrollment_model.sql:3/26/69/70/85/86/96`。`exam_session.school_id/grade_id/created_by`、`exam.exam_session_id`、`exam_blueprint_section.exam_id`、快照 `exam_id/student_id`、学年/届别学校引用及学籍父实体引用主要采用单ID FK。`grade`、`school_class`新增学年/届别字段也没有对应 `(tenant_id,...)` FK。后续迁移至 `000184`未补齐这些关系；`000183`新增的 `archived_by`仍为单ID FK。

单ID FK证明父ID存在，不能保证子行tenant与父行tenant相同；RLS也不会自动把FK转成租户一致性约束。组织Store创建班级和学生时已经在SQL中校验tenant和school，见 `internal/org/store_postgres.go:310/360`，因此不能据此声称当前这些API已允许跨校写入。风险在于数据库第二防线不均匀，后续写入路径、维护导入或缺少scope的新SQL可能留下不一致引用。

建议先清点存量一致性，再通过正式新迁移补租户复合唯一键/FK；需要同学校、同考试的关系还应包含真实业务范围字段。将应用层校验保留，数据库约束作为独立防线。不要直接编辑受哈希保护的旧迁移。

### DB-03 / P2：学校运维投影的行级触发器可能放大批量导入成本

证据：`000165_platform_school_operations.sql:219/260/338/370/373/376`。`refresh_platform_school_metrics(p_tenant_id)`在一次刷新中聚合该tenant的账户、角色、学生、班级、考试、会话和活动等；学生/班级/考试等来源采用 `FOR EACH ROW` 触发刷新。这里是对受影响租户的全量聚合，不是扫描全部租户；名称“平台投影”不能推断成跨租户全库扫描。

批量插入N行会在事务内触发N次聚合和同一投影行的upsert，可能增加导入耗时和锁竞争。尚未做压测，不能给出耗时、吞吐下降比例或已发生线上故障的结论。后续迁移没有改成语句级或异步刷新。

建议用实际规模导入和多并发租户写入衡量聚合/锁成本，再评估语句级汇总、批次合并或异步增量投影；保留必要的低敏感字段边界。本次不改变投影实现。

## 5. 待确认事项与维护方法

未核验PG迁移在干净库或真实存量库的执行结果、生产角色/GRANT/RLS配置、条件DDL存量分支、触发器并发性能、执行计划和物理索引名称。自动推导的PG约束名标记为 `name_inferred`，不是数据库实际查询结果。未命名CHECK的历史命名差异及 `LIKE INCLUDING ALL` 的复制索引名称需结合目录 `history` 和实际数据库核验。不能把 `warnings=[]` 当成全部SQL语义已验证。

维护脚本位于 `../tools/`：依次执行 `extract_database_catalog.py`、`build_database_specs.py`、`verify_database_catalog.py`，然后交由统一 `build_diagrams.py` 重建图纸。这些脚本只生成文档，不连接持久业务数据库。源文件改变后，已有人工审读记录只有在哈希一致时可继续用于节点状态；新文件、变更源码和缺少哈希的记录不会自动认定审读完成。人工复核必须单独更新记录及本报告，自动枚举不能填充完成状态。

ER页只显示核心字段；完整字段类型、FK目标列、CHECK/UQ/索引定义和来源都在catalog。图纸没有画出某条FK，不表示数据库没有这条FK。PG迁移存在条件执行和数据回填，目录描述源码声明的目标结构，不能直接替代部署schema差异审计。
