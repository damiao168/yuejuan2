# 修复验收证据汇总（2026-09-27）

本文件只读核对修复报告、保存的测试日志与当前测试名称清单，不重新执行测试、不改写原始审查结论。验收范围为原 [README](README.md) 的 F01–F10 与 T01；原始缺陷复现日志继续保留，不能将其中“成功复现缺陷”算作修复通过。

## 修复专项证据

| 范围 | 核对结果 | 证据及边界 |
| --- | --- | --- |
| F01 默认部署权限 | 真实 PostgreSQL 的新低权限应用登录完成首次管理员创建和登录；false/缺失 RLS 在 Docker 操作前被预检拒绝，launcher 生成配置自检通过。 | [部署报告](fix-deployment.md)、[真实数据库日志](fix-deployment-postgres.log)、[预检日志](fix-deployment-preflight.log)。未操作现有业务部署或用户 `.env`。 |
| F02、F03、F08、F09 数学 Worker | 最终 **52 项通过**，包括精确小数、原始 AST 实数定义域、整数/正实数全集、大响应及真实 spawn 进程清理；交叉复查新增 16 项。 | [数学报告](fix-math.md)、[交叉复查](fix-cross-review.md)。最终 `52 passed in 27.14s` 来自执行代理保存的报告，此目录没有对应独立原始 pytest 日志。中间一次超时异常用例未在预算内返回，最终使用相同预算重跑通过；未运行 Linux 部署镜像测试。 |
| F06 阅卷提交竞态 | workbench **71 项通过**，包括实际 React 生命周期的 5 项新增回归。 | [客户端报告](fix-clients.md)、[workbench 日志](fix-web-workbench-tests.log)。使用模拟 API；未冒称本轮真实浏览器后端 E2E。 |
| F07 离线草稿同步 | desktop **68 项通过**，包括 OfflineWorkbench 的 17 项新增回归；日志没有未处理拒绝报告。 | [desktop 日志](fix-desktop-tests.log)。模拟存储遵守原生 update-only 行为，并区分密文与 envelope 状态；本轮未运行 Tauri GUI、原生 SQLite/Windows 凭据库联调。 |
| F04、F05、F10 备份恢复 | 最终 **22 项模拟控制流 + 7 项真实 PostgreSQL + 4 项真实 Docker 回归通过**，执行者确认退出码 0；Docker 日志最后明确 `4 Docker recovery tests passed.`，并保留隔离容器、网络和卷删除完成记录。 | [最终恢复测试日志](fix-recovery-tests-final.log)、[最终 Docker 日志](fix-recovery-docker-final.log)。覆盖冻结/恢复原运行容器集合、主库/主桶恢复成功及失败路径、manifest 输入绑定、桶引用重映射、实际目标对象字节和 hash，以及不可变资产触发器的原始模式和事务回滚。 |
| T01 多凭据数据库断言与门禁 | `TestPostgresSubjectivePanelAndEvaluationRoundTrip` 真实 PostgreSQL 通过；CI 已改为完整 server 包。 | [部署报告](fix-deployment.md)、[专项日志](fix-deployment-postgres.log)。遵守 000176 多凭据约定，没有恢复旧唯一索引。 |

## server 测试清单核对

解析 Go JSONL 的顶层 `Test`（排除名称带 `/` 的子测试），仅统计 `pass`、`skip`、`fail` 终态，并按测试名去重。对照 [fix-server-tests.txt](fix-server-tests.txt) 的 **123 项**，当前 `internal/server/*_test.go` 中的顶层测试名称同样为 123 项，名称集合一致。

| 批次 | 顶层结果 | 整包状态 |
| --- | --- | --- |
| [首批](fix-postgres-server.jsonl) | 35 pass、1 skip | 进程中断，下一测试仅有 run；没有整包 PASS。 |
| [补批](fix-postgres-server-remainder.jsonl) | 11 pass | 进程中断，下一测试仅有 run；没有整包 PASS。 |
| [最后续跑](fix-postgres-server-resumed.jsonl) | 77 pass | 日志明确以 `PASS` / 包级 `pass` 结束，270.978 秒。 |
| 去重并集 | **122 pass、1 skip、0 fail、0 缺失** | 分批覆盖清单，不表示一次连续全量运行。 |

`TestPaperImportApplyGateE2EWithPostgresTestDatabase` 在首批和最后续跑各通过一次，只计一次。唯一 skip 为 `TestBusinessCommandReservationCrashHelper`：源码仅在 `EDUGRADE_E2E_CRASH_HELPER=1` 的子进程模式运行，普通顶层执行按设计跳过，不是缺少数据库 DSN 导致的业务测试跳过。

[fix-postgres-server-final.jsonl](fix-postgres-server-final.jsonl) 保留一次真实 **build/vet 失败**：其他并行开发新增测试的 `Fatalf` 使用 `%s` 格式化 `int64`。该格式错误随后修复，最后续跑重新编译并以包级 PASS 结束。此失败没有删除，也不计为当前未解决的失败。

源码时点边界：上述测试是不同编译时点的并集。首批编译后，其他流程修改了 `paper/objective_scoring_rule.go`、`paper/document_rubric_draft.go` 和 `paper/handlers.go`；最后续跑编译已包含这些改动，但没有重新运行首批的全部已通过测试。因此，本证据支持修复专项通过及测试清单分批覆盖，**不能宣称当前全部源码在同一个固定快照下完整全绿**。当前测试名称无新增漏项，也不能替代对所有并行开发内容的重新验收。

## 通用门禁

| 检查 | 可核对的结果 | 证据强度 |
| --- | --- | --- |
| 契约/SDK/路由/授权/schema | SDK 与源一致；509 条注册路由；契约 4 项、授权 2 项通过；最后明确 `Schema version 000179 is synchronized.` | [最终日志](fix-contracts-final.log) 有完整成功结尾。运行终端的退出码未保留，不额外声称可由此文件读取退出码。 |
| 全 workspace 构建 | desktop、student-portal、web-admin 均完成；最后明确 `built in 45.88s`。 | [最终日志](fix-build-final.log) 完整；终端退出码未保留。存在分包体积警告。 |
| desktop / web-admin TypeScript | 两个最终客户端 typecheck 通过。 | [desktop 日志](fix-desktop-typecheck.log)、[web 日志](fix-web-typecheck.log) 显示 `tsc --noEmit` 且无错误；退出码 0 由[执行代理报告](fix-clients.md)记录，静默日志自身不含 PASS 字样。 |
| 全 workspace TypeScript | 早一轮 typecheck 完成且无诊断。 | [日志](fix-typecheck.log) 早于最终客户端修改；最终客户端结果以上一行为准，不以旧日志替代新改动验收。 |
| config / db / auth | 三个 Go 包均明确 `ok`。 | [日志](fix-go-config-db-auth.log)，执行者记录退出码 0。 |
| STORY-052 | 明确 `STORY-052 production deployment check passed.` | [日志](fix-story052.log)。 |
| CI workflow lint | 执行者记录退出码 0。 | [最终日志](fix-workflow-lint-final.log) 仅有静默命令输出，不能单靠无诊断证明成功；与执行者的退出状态合用。 |
| 前端 lint | 检查 447 个文件、**67 个 warning**、执行者记录退出码 0。 | [最终日志](fix-lint-final.log)。不宣称零警告。 |
| diff 空白检查 | 执行者记录通过；日志只有行尾转换提示。 | [日志](fix-diff-check.log)。无格式错误诊断，行尾提示不等于测试失败。 |

尚未补做的验收包括：固定最终源码的一次完整 server 运行、真实 Tauri 原生集成、数学 Worker Linux 镜像验证。历史 README 的全仓测试属于审查阶段证据，不混算为本轮最终源码修复后的全仓测试。没有将后续未运行的检查写成已通过。
