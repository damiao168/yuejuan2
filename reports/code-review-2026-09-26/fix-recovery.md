# 备份与恢复修复验收

日期：2026-09-27。审查项 F04、F05、F10 已修复并通过回归验证。

| 审查项 | 实现与验证 |
| --- | --- |
| F04：数据库快照与对象复制缺少一致性边界 | 备份先获取当前 Compose 项目实际运行的容器，停止除 PostgreSQL、Redis、MinIO、Qdrant 外的服务；未知/新增服务默认纳入停写。完成后再次检查维护边界，成功或失败都只恢复原运行集合。清单升级为 v3，绑定数据库 dump、对象目录和有效文件引用；逐条验证对象大小与 SHA-256。 |
| F05：恢复输入未绑定清单，破坏性操作前未完整校验 | 在任何 Docker 操作前校验清单与所有文件、路径和哈希，传入 dump/对象目录必须是清单指定的路径。缺失、替换、损坏、未列入清单的文件会直接失败。 |
| F10：隔离 bucket 恢复未重映射文件引用 | 恢复后事务性重映射数据库所有源 bucket 引用，保留其他 bucket；针对生产题库不可变文件触发器，事务内临时停用并恢复原 O/A/R/D 模式，其他触发器和约束继续生效。读取恢复库实际引用，重新下载目标对象验证大小与 SHA-256，通过后才报告成功。 |

恢复使用与备份相同的停写边界。目标包含主数据库或主 bucket 时，若已开始数据变更却未完成验证，保持应用容器停止，并输出原运行容器 ID 的启动命令；变更前失败、完全隔离目标失败、成功恢复会还原原运行集合。演练子命令的普通输出不再混入最终 JSON。

测试结果：

- PowerShell mock 回归：22/22 通过（Windows）。覆盖输入绑定、对象损坏、未来服务停写、同时间戳备份隔离、备份各阶段失败后恢复服务、恢复引用与实际对象检查，以及主数据库/主 bucket 的成功、变更前失败、变更后失败矩阵。Linux 另有大小写不同 dump 的条件用例，本次未在 Linux 执行。
- 原生 PostgreSQL 17：7/7 通过。使用隔离临时库，加载生产迁移中的真实题库文件保护触发器；覆盖 O/A/R/D 模式、其他触发器继续生效、约束失败时数据和触发器原子回滚、无旧触发器的备份兼容。
- 真实 Docker（PostgreSQL 16 + MinIO）：4/4 通过。完整执行备份、恢复、恢复演练及缺失源对象导致备份失败；验证原运行服务恢复、原先停止服务保持停止、题库资产 remap、对象字节/哈希校验、演练库清理。独立项目 `edugrade-recovery-test-9a9623e930d64590a35a859112fc57a4` 的容器、卷、网络已清理，随后查询确认无残留。
- `git diff --check` 通过；仅提示工作区 CRLF 转换，不存在空白错误。

运行命令与日志：

```powershell
./infra/docker-compose/scripts/tests/recovery.tests.ps1 -PostgresTestURL 'postgres://fixreview@127.0.0.1:55441/postgres?sslmode=disable'
./infra/docker-compose/scripts/tests/recovery-docker.tests.ps1
```

- [mock 与 PostgreSQL 最终日志](fix-recovery-tests-final.log)
- [Docker 最终日志](fix-recovery-docker-final.log)

运维边界已写入 `docs/deployment/preproduction-runbook.md`：脚本不能冻结当前 Compose 项目外的直接数据库/S3 写入或对象生命周期删除规则，执行前必须在其所属平台停写；维护期间不得并行启动服务或另一轮备份/恢复。旧清单缺少停写和引用证据，需重新生成 v3 备份，不能手工提升旧清单版本。本次 Docker 验证使用合成数据和独立卷，未操作实际部署。

中断测试清理记录：上次独立项目 `edugrade-recovery-test-27b560d5dd3e404c94f07f2f4d750991` 的容器、卷、网络已使用精确项目名清理。合并 Docker 清理与临时目录递归删除的命令曾被自动审批拒绝（仅返回 `blocked by policy`）；随后采用获准的单独精确 Docker 清理，保留其临时文件和 `.recovery-test-27b560d5dd3e404c94f07f2f4d750991.yml`，未重试递归删除。
