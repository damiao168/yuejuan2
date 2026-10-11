# Go 最终验证对账

生成时间：2026-10-11T19:41:18.097355+08:00

只读汇总既有日志与本地源码哈希，本轮没有修改产品源码、运行测试或访问远端 GitHub。

本代理负责原始 129 项及移交题库 5 项，共 134 原项；另处理 checkpoint02 新增 5 项。139 项均已源码处理并有关联包实际 pass 证据。其他代理的 75 原项不重复计入。

归属/维护源码 109 文件：当前工作区、记录的源码 SHA、full03 清单及实际冻结文件全部相同。保留90个 read_and_refactored、19个 annotated原标签；标准第6节将 annotated定义为已审读并记录维护点，汇总沿用已有人工记录，不依靠本轮哈希枚举升级状态。83 文件同时吻合最近成功关联包测试记录的 SHA；26 文件的早期成功日志未保存测试时 SHA，报告保留该证据缺口。

题库真实 PostgreSQL 场景：**0 通过、5 条件跳过**。server 最后一次共享包执行 exit 0，188 pass、0 fail、62 skip；其中 **61 因 PostgreSQL 环境、1 为 crash-helper 辅助入口**。包回归通过只证明已运行测试和编译，不能计作跳过场景的业务验证通过。

Sonar full03 官方分析 `a741715d-5427-49a6-ac74-7dd92ad5cfa1` / CE SUCCESS：134 原项 + 5 checkpoint02 新项逐 key 均为 **FIXED**。采用官方完整状态导出，不以辅助 AST 复杂度估值计关闭；全仓另2原项修复及full04最终扫描由root协调。

## 实际执行证据

同一包重复运行不相加作为独立测试数。早期 exit 1、修复后重跑、原测试 overlay 失败均保留。

|执行报告|实际 exit|Test pass / fail / skip|说明|
|---|---:|---|---|
|go-batch01-tests.json|0|未记录逐测试计数|实际通过；条件 skip 单列于 JSON|
|go-batch02-tests.json|0|827 / 0 / 1|实际通过；条件 skip 单列于 JSON|
|go-batch03-tests.json|0|444 / 0 / 0|实际通过；条件 skip 单列于 JSON|
|go-batch04-tests.json|0|48 / 0 / 0|实际通过；条件 skip 单列于 JSON|
|go-batch05-tests.json|0|951 / 0 / 0|实际通过；条件 skip 单列于 JSON|
|go-batch06-retry1-tests.json|0|348 / 0 / 2|实际通过；条件 skip 单列于 JSON|
|go-batch06-tests.json|1|0 / 0 / 0|原失败保留，关联包见后续通过证据|
|go-batch07-retry1-tests.json|0|232 / 0 / 1|实际通过；条件 skip 单列于 JSON|
|go-batch07-tests.json|1|13 / 0 / 0|原失败保留，关联包见后续通过证据|
|go-batch08-tests.json|0|42 / 0 / 0|实际通过；条件 skip 单列于 JSON|
|go-batch09-auth-retry1-tests.json|0|250 / 0 / 1|实际通过；条件 skip 单列于 JSON|
|go-batch09-tests.json|1|645 / 0 / 1|原失败保留，关联包见后续通过证据|
|go-batch10-11-tests.json|0|263 / 0 / 0|实际通过；条件 skip 单列于 JSON|
|go-batch12-tests.json|0|76 / 0 / 0|实际通过；条件 skip 单列于 JSON|
|go-batch13-21-retry-taskmaterials-tests.json|0|9 / 0 / 0|实际通过；条件 skip 单列于 JSON|
|go-batch13-21-tests.json|1|1857 / 2 / 4|原失败保留，关联包见后续通过证据|
|go-batch22-26-and-new-five-tests.json|0|1158 / 0 / 2|实际通过；条件 skip 单列于 JSON|
|go-taskmaterials-baseline-tests.json|1|2 / 2 / 0|原测试 overlay 同样失败，支持严格补齐已有生产 SQL 契约的测试夹具|
|go-server-batch02-tests.json|1|0 / 0 / 0|原失败保留，关联包见后续通过证据|
|go-server-batch03-tests.json|1|0 / 0 / 0|原失败保留，关联包见后续通过证据|
|go-server-batch04-tests.json|0|188 / 0 / 62|实际通过；条件 skip 单列于 JSON|

## 题库场景边界

|测试|结果|实际跳过原因|
|---|---|---|
|TestE2EPostgresUXHistoricalACLAndActualMemberResponsibilities|skip|e2e_question_bank_acl_ux_test.go:20: requires isolated PostgreSQL|
|TestE2EPostgresQuestionBankFrozenImport|skip|e2e_question_bank_import_test.go:23: EDUGRADE_E2E_DATABASE_URL is required for real PostgreSQL evidence|
|TestE2EPostgresQuestionBankMetadataACLSearch|skip|e2e_question_bank_metadata_acl_search_test.go:19: requires isolated PostgreSQL|
|TestE2EPostgresQuestionBankPublication|skip|e2e_question_bank_publication_test.go:22: requires isolated PostgreSQL|
|TestE2EPostgresQuestionBank|skip|e2e_question_bank_test.go:26: EDUGRADE_E2E_DATABASE_URL is required for real PostgreSQL evidence|

## 人工审读范围

归属源文件的逐文件审读状态、维护规则、issue key 与 SHA 详见 JSON `manual_review_scope.owned_file_review_records` / `issues`。哈希核对仅证明源码身份，不替代人工审读；19 个早期 annotated 标签沿用标准定义和实际维护记录。

另独立完整对照 reliability command 测试：基线 1213 行、当前 1633 行，检查多 CommandID、跨阶段状态、lease/Tx/defer、故障注入次序及编译修正；未发现当前语义回归。其两个 PostgreSQL 主测试实际 skip，不计真实数据库通过。

下列 5 文件仅检查具体受影响片段，整文件仍 pending：

- services/api-gateway/internal/server/e2e_subjective_command_test.go：当前行 19–72、104–137；完整审读 pending。
- services/api-gateway/internal/server/e2e_worker_runtime_reliability_test.go：当前行 189–278；完整审读 pending。
- services/api-gateway/internal/server/review_route_test.go：当前行 208–256；完整审读 pending。
- services/api-gateway/internal/server/e2e_review_recovery_test.go：当前行 246–327；完整审读 pending。
- services/api-gateway/internal/server/e2e_paper_import_reconciliation_test.go：当前行 480–552；完整审读 pending。

其余代理的 75 原项和未列出的全仓文件不由本报告确认完成。详细每包最新实际结果、逐执行跳过日志及原始证据 SHA 均在 JSON。
