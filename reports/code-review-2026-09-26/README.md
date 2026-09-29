# 全仓并行审查（2026-09-26）

**结论：保留 10 项业务／部署／恢复问题（5 项 P1、5 项 P2），另列 1 项 P2 测试门禁问题。建议先处理默认部署无法访问数据库、数学错误认证和备份恢复一致性，再修复阅卷界面及桌面同步。** 本次是审查，未修改业务源码、未提交或发布代码。

基准 HEAD：`eecdcbe9a41c604e038a8aeff9d86a04b121b26c`。开始时仓库有 2,634 个 tracked 文件，唯一未跟踪目录为 `.tmp-pg-migration-verify/`。审查期间其他工作流持续增加试卷导入、评分参考等改动及 `000179` 迁移；本报告明确区分开始时的验证与后续工作区状态。并行分工覆盖 Go 业务、前端与桌面、AI/Worker/Lab、迁移及恢复；不宣称每个文件逐行穷尽或生产环境无其他问题。

P1 表示建议在下一次正式使用或恢复演练前修复；P2 为应安排修复的功能或可靠性问题。证据分为真实数据库、生产函数的合成复现、静态调用链，彼此不混同。

## 已确认问题

| ID | 优先级与问题 | 触发条件、影响与修复方向 | 定位及证据 |
| --- | --- | --- | --- |
| F01 | **P1 默认新部署的应用账号没有业务表权限** | 样例和一键脚本使用 app login，但关闭 RLS 连接包装；迁移创建 `NOINHERIT` login，只有 runtime 角色成员资格。直连不执行 `SET ROLE`，登录与首次管理员创建查询业务表会报权限错误。应统一默认部署与角色切换策略，并以新建 app login 验证 bootstrap/login；不要用超级用户测试替代。 | [样例配置](D:/project/yuejuan/infra/docker-compose/.env.example:109)、[角色创建](D:/project/yuejuan/infra/docker-compose/docker-compose.yml:221)、[直连分支](D:/project/yuejuan/services/api-gateway/internal/db/postgres.go:43)。**真实 PG16**：实际 Compose 迁移后查询 tenant 被拒，SET ROLE 后成功；[复现输出](deployment-role-repro.txt)。独立代理复核无其他自动角色切换。 |
| F02 | **P1 小数误差被当作确定的数学反例** | `0.1x+0.2x → 0.3x` 返回 contradicted / x=-3。有限小数转成 Float 后按精确零比较，正确变形被判错，并可进入数学评分建议的扣分证据。应按精确十进制转 Rational；近似数另设语义。 | [verifier.py:107](D:/project/yuejuan/services/math-verification-worker/math_verification_worker/verifier.py:107)。**生产函数复现**：[日志](math-worker-repro.log)、[调用链与人工复核边界](ai-findings.md)。未声称自动发布错误最终成绩。 |
| F03 | **P1 常量表达式跳过定义域检查，非法实数运算被认证** | 无变量时不检查原始 AST 定义域；`1/0 → 2/0` 被 verified，`sqrt(-1)*sqrt(-1) → -1` 也可被认证为实数等价。错误结果会以 confidence=1 进入数学证据链。需在任何化简相等快速路径之前校验原始每个子式的实数定义性。 | [verifier.py:40](D:/project/yuejuan/services/math-verification-worker/math_verification_worker/verifier.py:40)。**生产函数复现**：[日志](math-worker-repro.log)、[AI-2 详细说明](ai-findings.md)。评分仍受教师确认约束。 |
| F04 | **P1 在线数据库与对象备份可能恢复出缺文件的数据** | pg_dump 快照包含文件 A 后，正常删除将 A 从对象存储移除，再开始 mirror；dump 仍引用 A，备份对象没有 A。manifest 与恢复演练仅核对现存文件和数量，仍可能通过。应冻结相关写入／删除，或以对象版本和数据库快照清单备份，并逐项校验对象引用。 | [backup.ps1:62](D:/project/yuejuan/infra/docker-compose/scripts/backup.ps1:62)、[正常物理删除](D:/project/yuejuan/services/api-gateway/internal/files/handlers.go:340)。**静态时序与调用链**：[R1](recovery-findings.md)，未在真实部署制造数据丢失。 |
| F05 | **P1 恢复校验未绑定所选文件，错误对象路径在替换数据库之后才报错** | 验证的是 dump 父目录的 manifest，实际 dump 可以是同目录未列入的另一文件，MinIO 输入也可来自另一备份；对象目录直到 drop/create/restore 后才解析。运维选错日期或路径时可留下不一致恢复状态。所有路径、hash、备份 ID 对应检查应在替换前完成，以 manifest 派生实际输入。 | [restore.ps1:25](D:/project/yuejuan/infra/docker-compose/scripts/restore.ps1:25)、[迟到的路径检查](D:/project/yuejuan/infra/docker-compose/scripts/restore.ps1:86)。**真实 PowerShell 控制流、Docker 全 mock**：[复现脚本](recovery-file-validation.ps1)、[输出](recovery-file-validation-output.txt)、[R2](recovery-findings.md)。非真实 pg_restore。 |
| F06 | **P2 阅卷提交的迟到响应清除新任务界面** | 提交 A 期间切到 C，A 成功后无条件清上下文／草稿并跳到闭包中的 B。若提前切到 B，则 B→B 不触发加载 effect，界面停留空态。只让仍活动的任务／用户执行清理和跳转。 | [useGradingReviewActions.ts:199](D:/project/yuejuan/apps/web-admin/src/features/grading/workbench/hooks/useGradingReviewActions.ts:199)。**实际 action + React 状态 harness**：[2 个复现](grading-submit-race.test.ts)、[输出](grading-submit-race.log)、[细节](frontend-findings.md)。不等于永久草稿丢失或服务端错题提交。 |
| F07 | **P2 桌面直接同步未保存草稿，后台成功但界面报失败** | 下载后可直接同步；未先保存时 Native draft 行不存在。后台接收评分后本地 UPDATE 报错，界面 synced→failed，catch 再更新缺失行又抛异常。应先建立持久记录，并分开处理后台成功和本地状态写入失败。 | [OfflineWorkbench.tsx:243](D:/project/yuejuan/apps/desktop-client/src/components/OfflineWorkbench.tsx:243)、[Native 缺失行行为](D:/project/yuejuan/apps/desktop-client/src-tauri/src/durable_store/drafts.rs:90)。**实际 action + Native 契约 mock**：[脚本](desktop-unsaved-sync-repro.mjs)、[输出](desktop-unsaved-sync-repro.log)。未运行 Tauri/SQLite 实机链。 |
| F08 | **P2 大结果在 Queue 中等待消费，却被判计算超时** | 父进程先 join、后读 Queue；较大结果写满管道后子进程等待父进程消费，互相等待直至超时。合法 58,837 B AST 请求直接计算约 250 ms，bounded 约 20.7 s 返回 timeout；先取结果再 join 的对照成功。应在总截止时间内先接收结果再回收进程。 | [server.py:51](D:/project/yuejuan/services/math-verification-worker/math_verification_worker/server.py:51)。**实际多进程复现**：[脚本](math-worker-repro.py)、[日志](math-worker-repro.log)、[AI-4 范围](ai-findings.md)。已证实直接 AST normalize；未证明同一大样例可穿过 OCR LaTeX 长度限制。 |
| F09 | **P2 integer／positive_real 声明未限制实际求解全集** | Worker 接口接受变量声明，却仍以 Reals 求解：整数 `x²=2` 返回无理根，正实数 `x²=4 → x=2` 误报丢解。应把显式全集与原始定义域求交，或拒绝不支持的声明。 | [verifier.py:139](D:/project/yuejuan/services/math-verification-worker/math_verification_worker/verifier.py:139)。**生产函数复现**：[日志](math-worker-repro.log)。当前 OCR client 固定传 real，影响限定为内部 Worker 支持的参数分支，见 [AI-3](ai-findings.md)。 |
| F10 | **P2 恢复到新桶后，数据库仍引用旧桶** | `-TargetBucket` 将对象复制到新桶，但 file_asset.storage_bucket 保留源值；应用连接恢复库后仍读源桶。随机桶演练只核数量会漏检。应映射更新目标库的桶引用，或使用独立存储实例但保留桶名，并通过恢复库真实读取文件验证。 | [restore.ps1:90](D:/project/yuejuan/infra/docker-compose/scripts/restore.ps1:90)、[下载使用记录中的桶](D:/project/yuejuan/services/api-gateway/internal/files/downloads.go:54)。**静态完整调用链**：[R3](recovery-findings.md)。 |

## 测试门禁问题

**T01 · P2：已失效的数据库断言未进入 CI 数据库选集。** [e2e_subjective_panel_test.go:332](D:/project/yuejuan/services/api-gateway/internal/server/e2e_subjective_panel_test.go:332) 仍要求同校、供应商、模型重复配置失败，但 [000176](D:/project/yuejuan/services/api-gateway/migrations/000176_managed_model_api_multiple_credentials.sql:3) 已按需求移除此唯一约束。真实 PG17 实测失败，且后面的 panel binding/evaluation 流程被提前截断。CI [选集](D:/project/yuejuan/.github/workflows/ci.yml:313) 不包含该测试名；普通无 DSN 的 `go test ./...` 会跳过。应更新业务断言并覆盖该数据库测试，不能为通过旧测试恢复已删除的唯一约束。[失败日志](postgres-final-remainder.log)。

## 验证结果与限制

- 前端单测 **366 通过**；浏览器 mock 流程分批 **71 通过**（首批中断前 27 条成功记录，后批 44 条完整成功）。包括 120 份跨页连续阅卷；不是一次连续全绿，也不是非 mock 全系统验收。
- Go 初始全包测试除 3 包本机内存导致编译失败外通过；这 3 包低并发重跑通过。`go vet` 通过。真实 PostgreSQL server 测试分批去重 **120 通过、1 跳过、1 失败**；auth/db 通过。实际部署迁移至 `000178` 与重复执行通过，有数据 `000154` 升级专项（执行时含 `000179`）通过。
- Python/Worker 六组及 Lab 通过记录来自前轮智能体，早于后续并行 AI 修改；细分计数与证据等级见 [验证记录](verification.md)。数学复现的 PASS 表示观察到缺陷，不表示功能正确。
- 初始 lint/typecheck/build/contracts 通过，lint 有 **62 警告**。过程中新增 `000179` 与评分草稿路由曾导致 schema／路由门禁失败；并行开发随后完成同步，最终只读复查两项均已通过，不计为遗留问题。未因此重复声称完整构建／测试覆盖了所有后续修改。[最终 schema 输出](final-schema-check.txt)、[最终路由输出](final-route-check.txt)。
- npm audit 无 high/critical；2 条 moderate 条目来自同一 Vitest 开发依赖公告，未证实生产利用路径。Go 漏洞扫描因下载超时未完成。未运行本轮 Rust 构建、真实外部模型、扫描硬件或完整非 mock 浏览器→数据库→模型链。
- 后端集合接口学校范围候选未完成验证：智能体执行及退回静态复核均被工具安全检查拦截。本报告不将其列为确认缺陷，也不声称访问控制审查已经完整通过。
- 本轮只新增报告和诊断工件；保留了所有外部并行修改。自建 Docker 测试容器已移除，独立原生 PostgreSQL 测试集群已停止；没有动已有 `.tmp-pg-migration-verify/`。

完整证据：[验证记录](verification.md)、[AI 分报告](ai-findings.md)、[前端分报告](frontend-findings.md)、[恢复分报告](recovery-findings.md)、[最终工作区状态](final-workspace-status.txt)。
