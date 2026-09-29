# F01 / T01 部署与测试门禁修复

验证日期：2026-09-26 至 2026-09-27。保留原始审查证据，本文件记录修复结果。

F01：Compose 示例配置、容器环境默认值和一键生成配置统一为 `EDUGRADE_POSTGRES_TENANT_RLS=true`。预检在任何 Docker 操作前拒绝关闭或缺少该配置的旧部署，并明确提示升级方法。保留低权限 `NOINHERIT` 应用登录账号，通过租户连接器切换到 `edugrade_tenant_runtime`。没有提升应用账号权限，也没有修改用户现有 `.env`。

正式 PostgreSQL 回归 `TestComposeApplicationBootstrapAndLoginWithPostgres` 从已发布的配置样例读取设置，在独立迁移库中创建全新 `NOINHERIT`、非超级用户、无 `BYPASSRLS` 登录，只授予 runtime 成员关系，然后使用该登录完成角色检查、首次管理员创建、重复创建拒绝、令牌登录、`auth/me` 以及浏览器会话和设备 cookie 验证。实际 PG17 通过，见 `fix-deployment-postgres.log`。

正式 PowerShell 回归 `scripts/test-compose-deployment.ps1` 对真实 preflight 调用进行 Docker 边界拦截，验证 false / 缺失配置在 Docker 前被拒，正确样例到达 Docker 边界；同时运行 launcher 的 `-DryRun`，验证新生成配置。通过日志见 `fix-deployment-preflight.log`。

T01：按迁移 000176 的多凭据业务约定修改 panel 数据库测试。相同学校、供应商和模型可创建两个独立配置；读回配置数量与不同密文数量，并继续执行后面的模型角色绑定流程。没有恢复已删除的唯一索引。`TestPostgresSubjectivePanelAndEvaluationRoundTrip` 在真实 PG17 通过，见 `fix-deployment-postgres.log`。

CI 的 PostgreSQL job 改为运行完整 server 包（20 分钟测试超时），不再依赖容易遗漏新测试的名称选集。部署配置与恢复模拟测试接入 STORY-052；桶重映射的真实 SQL 回归接入 PostgreSQL job。工作流 actionlint 与 STORY-052 静态门禁已通过。

辅助验证：`go test -p 1 ./internal/config ./internal/db ./internal/auth -count=1 -timeout 5m` 在测试 DSN 下通过，见 `fix-go-config-db-auth.log`。server 全包分批执行结果由 `fix-evidence-summary.md` 记录；用户多次继续导致进程中断，未宣称为一次连续全绿。

边界：实际部署 `.env` 仍需按 README 将旧 false 改成 true；本次没有启动、升级或变更现有业务部署。原生本地开发使用超级用户的默认配置路径保持原有语义。
