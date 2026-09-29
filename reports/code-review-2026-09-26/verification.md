# 验证记录（2026-09-26）

本轮审查基准为 `eecdcbe9a41c604e038a8aeff9d86a04b121b26c`。开始时唯一未跟踪目录为 `.tmp-pg-migration-verify/`，本轮未修改它。审查期间其他工作流持续修改试卷导入、评分参考和 AI 代码，新增 `000179` 迁移；各次测试仅证明执行时对应代码状态，不代表所有并行修改都已验证。

## 已执行检查

| 检查 | 结果与边界 | 证据 |
| --- | --- | --- |
| `npm run ci:web:fast` | lint、全部 workspace typecheck、build 完成；lint 有 62 个警告，构建提示大 chunk。执行早于并行业务修改。 | [web-fast.log](web-fast.log) |
| `npm run ci:contracts` | 通过；508 条注册路由中 256 条已有 OpenAPI，252 条处于已登记豁免；当时 schema 为 `000178`。 | [contracts.log](contracts.log) |
| `go test ./... -count=1` | 其余包通过；platformschools/releasegate/report 三包因 Windows 分页内存不足编译失败，随后 `GOMAXPROCS=2 go test -p 1` 重跑三包通过。没有配置数据库的初始运行会跳过 PG 测试。 | [go-test.log](go-test.log)、[go-retry.log](go-retry.log) |
| `go vet -p 1 ./...` | 完成，未输出诊断。 | [go-vet.log](go-vet.log) |
| PostgreSQL 16 实际部署迁移 | 从 Compose 提取 db-migrate shell（展开 Compose 的 `$$` 转义）在独立容器全量执行至 `000178`；再次执行全部 skip，幂等检查通过。 | [deployment-migrate.sh](deployment-migrate.sh)、[首次执行](deployment-migrate.log)、[第二次执行](deployment-migrate-repeat.log) |
| 默认应用数据库账号 | 实际迁移创建的 `NOINHERIT` 账号直连查询 tenant 被拒；显式 SET ROLE 后成功。 | [deployment-role-repro.txt](deployment-role-repro.txt) |
| PG server/auth/db 验证 | PG16 第一批 server 整包累计触及 10 分钟默认超时；auth/db 通过。Docker 退出导致一次续跑连接失败。之后分批在 PG17 补齐，server 合并去重 122 个顶层测试：120 通过、1 crash helper 跳过、1 旧唯一性断言失败。不是单一快照的一次全绿运行。 | [postgres-tests.log](postgres-tests.log)、[连接失败记录](postgres-remaining.log)、[PG17 第一批](postgres-native-remaining.log)、[PG17 最后一批](postgres-final-remainder.log) |
| 历史有数据升级 | `TestPostgresMigrationUpgradeFrom000154`、`TestPostgresManagedConfigProbeTimestamps` 通过；此时目录含新增 `000179`。 | [postgres-upgrade-final.log](postgres-upgrade-final.log) |
| 前端单元测试 | `--maxWorkers=1`：desktop 16 文件/51 测试，student 1/13，web-admin 64/302，总计 81 文件/366 测试通过。 | [frontend-tests.log](frontend-tests.log) |
| mock 浏览器流程 | 分批覆盖 71 项：首批中断前有 27 项通过记录，后续选集 44 项完整通过（6.7 分钟）。日志存在未拦截辅助请求的 8080 连接拒绝，但相应断言通过；不能替代真实服务验收。 | [第一批](browser-e2e.log)、[剩余选集](browser-e2e-remaining.log) |
| 工作流、架构、产品静态门禁 | workflow lint、architecture boundaries、onboarding、production routes、desktop security config 通过。架构门禁有已有体积警告。 | [workflow-lint.log](workflow-lint.log)；其余见本轮工具输出 |
| npm 依赖审计 | 无 high/critical；2 条 moderate 条目来自同一个 Vitest 公告，涉及开发测试依赖，未证明生产可利用。 | [npm-audit.json](npm-audit.json) |
| Go 漏洞扫描 | 未完成：`proxy.golang.org` 连接超时，无法下载 govulncheck，不作为扫描通过。 | [govulncheck.log](govulncheck.log) |

## 环境和范围

- Docker Desktop 在过程中两次退出；本轮恢复过一次，第二次转用独立原生 PostgreSQL 17。应用实际数据库未用于写入测试。
- 原生测试集群位于 `output/review-pg-20260926`，测试时仅监听本机 `55439`，结束时已核实停止。Docker 测试容器 `edugrade-review-20260926-pg` 已按本轮专属 label 验证身份后移除，见 [清理记录](cleanup.txt)。
- UI 浏览器测试使用仓库已有 Playwright mock API 套件；不等于真实浏览器到数据库、模型和扫描硬件的端到端验证。
- 未验证实际外部付费模型、OCR 模型下载和真实扫描仪；没有执行生产恢复或发布操作。
- 前轮智能体报告 Python 六组测试及 Lab 通过：AI 113，OCR 96（另有 5 个 subtests），页面处理 63，图像质量 32，主观题 Worker 7，数学验证 12，Lab 132。运行早于后续并行 AI 修改；本轮后续微型复现单独留证。

## 并行修改后的门禁观察

过程中只读复查曾发现：`check-schema-version` 报新增迁移为 `000179`，但 `.env.example` 和 Compose 仍声明 `000178`；`check-openapi-route-debt` 报新增 `POST /api/v1/paper-imports/{id}/rubric-draft` 尚无 OpenAPI/已批准路由豁免。随后并行开发完成同步，最终两项均通过，见 [schema](final-schema-check.txt) 和 [路由](final-route-check.txt)。不计为遗留问题，也不能将这两项通过外推为后续所有源码的完整构建／测试通过。

PG 唯一失败：`TestPostgresSubjectivePanelAndEvaluationRoundTrip` 在 `e2e_subjective_panel_test.go:332` 要求同校/供应商/模型重复配置被拒，和 `000176_managed_model_api_multiple_credentials.sql` 的显式新规则相反。CI 的 PostgreSQL `-run` 模式未包含该测试，默认无 DSN 的全包单测会跳过它。应更新断言并扩充实际数据库测试选集；不应恢复已被产品需求移除的唯一约束。

Vitest 依赖告警的适用条件与修复版本以 [维护者公告 GHSA-82fw-gwwq-j7x9](https://github.com/vitest-dev/vitest/security/advisories/GHSA-82fw-gwwq-j7x9) 为准；该公告明确区分公开 mocker 插件的开发服务器与带 token 的 Vitest browser RPC，不能仅凭依赖存在就认定生产任意文件读取。
