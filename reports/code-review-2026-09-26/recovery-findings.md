# 备份与恢复可靠性独立审查

日期：2026-09-26。范围：`infra/docker-compose/scripts/backup.ps1`、`restore.ps1`、`restore-drill.ps1`、`verify-backup.ps1` 及对象访问/删除调用链。只读业务代码；未调用任何现有部署、Docker daemon、数据库或对象存储。排除权限、安全测试以及已由主审确认的默认数据库角色问题。

共确认 3 个代码层面的可靠性问题。R2 另有真实 PowerShell 控制流验证（所有 Docker 命令均被本地函数替换）；R1/R3 为完整调用链静态证明，未宣称做过真实环境恢复。

## R1 — P1：数据库与对象备份没有共同一致性边界，正常删除可使备份永久缺失文件

- **定位**：`infra/docker-compose/scripts/backup.ps1:47`、`:62-65`，适合行内评论位置 `:62-65`。关联：`services/api-gateway/internal/files/handlers.go:335-346`；`restore-drill.ps1:37-52`。
- **前提**：按手册直接运行在线备份，备份期间仍允许文件删除或后台对象清理。`docs/deployment/preproduction-runbook.md:197-214` 的备份流程没有要求停写；`:235` 的停写要求针对覆盖恢复。
- **触发顺序**：数据库 dump 的快照先包含 active 文件 A；随后业务 Delete 在数据库记录删除并实际调用 `objects.Remove` 删除 A；MinIO mirror 最后开始，故备份目录中已无 A。数据库备份仍保留 A 的存储引用。
- **影响**：脚本可成功生成 manifest，所有列入文件的 hash 都正确，但从该备份恢复后 A 无法下载。恢复演练只比对备份目录和目标桶的对象数量，双方都少 A，因此无法识别这一缺口。无对象版本留存时无法从这套备份补回 A。
- **修复建议**：建立跨数据库/对象存储的一致性策略。最直接的是在生成数据库快照和复制相关对象期间暂停文件写入、删除及清理；或者保留对象版本并按数据库快照中的 `(bucket,key,version/hash)` 清单提取。演练需逐项核对恢复库里可用文件的对象存在性、大小和 hash，不能只核对目录数量。
- **证据等级**：静态完整顺序与正常物理删除路径；没有在用户部署上制造删除或执行备份。

## R2 — P1：恢复校验没有绑定实际选入的备份文件，且对象路径检查晚于数据库替换

- **定位**：`infra/docker-compose/scripts/restore.ps1:25-27`（核心）、`:68-77`、`:86-91`。关联：`verify-backup.ps1:17-41`。
- **前提**：运维选错了 `-PostgresDump` / `-MinioBackupDirectory`，例如将其他日期的有效 dump 放进同一目录，或复制命令时保留了另一个日期的 MinIO 目录。
- **问题**：restore 仅验证 dump 所在目录中的 manifest，然后丢弃结果。它没有确认实际 `PostgresDump` 等于 `manifest.postgres_file`，也没有确认 `MinioBackupDirectory` 等于已经校验的 `manifest.minio_directory`。verify 只要求 manifest 中的 dump 被覆盖，且仅枚举 manifest 指定的 MinIO 目录，因而根目录的另一个 dump 不影响验证。额外的 `pg_restore --list` 只能检查 dump 格式，不能证明它属于这一份备份。对象目录直到 PostgreSQL 已 drop/create/restore 后才 Resolve-Path。
- **影响**：可以混合恢复不同时间点的数据库与对象并报告成功；若对象路径仅是拼写错误，也会先替换目标数据库再报错，留下数据库和对象存储处于不同恢复点。
- **修复建议**：以唯一 `BackupDirectory`/manifest 作为输入源，先解析并验证两份实际输入的规范路径，确认二者与 manifest 严格一致；所有本地完整性、路径和对应关系检查应在任何 drop/clean/restore 前完成。若支持外部独立输入，应独立校验该输入并明确要求同一备份 ID。
- **验证**：运行 `reports/code-review-2026-09-26/recovery-file-validation.ps1`。它只创建 `%TEMP%` 合成文本文件，并用 PowerShell 函数拦截全部 Docker 调用，没有启动任何外部 Docker 命令；因此证据限定于脚本的校验与执行顺序，合成文本并非真实 pg_dump。
- **实测结果**：`manifest_valid=true`；`unlisted_dump_selected=true`；`unrelated_minio_selected=true`；`target_replacement_invoked_before_missing_minio_error=true`。完整调用记录见同目录 `recovery-file-validation-output.txt`。这证明实际选入文件未受 manifest 约束，以及缺失对象路径在数据库替换命令之后才被发现。

## R3 — P2：恢复到新 bucket 后数据库仍指向旧 bucket，演练成功不代表文件可用

- **定位**：`infra/docker-compose/scripts/restore.ps1:86-94`，适合行内评论位置 `:90-94`。关联：`restore-drill.ps1:18-21`、`:35-52`；`services/api-gateway/internal/files/downloads.go:54`；`services/api-gateway/internal/files/storage_minio.go:38-43`。
- **前提**：使用公开支持的 `-TargetBucket` 恢复到不同于源 bucket 的名字；自动恢复演练始终使用随机新 bucket。
- **问题**：数据库按原样恢复，而对象被 mirror 到指定新 bucket。脚本没有更新 `file_asset.storage_bucket`，下载服务又明确以每条数据库记录中的 `asset.StorageBucket` 读取对象，而非用全局当前 bucket 覆盖它。
- **影响**：即使数据库、对象复制全部成功，把应用连接到恢复库后，历史文件依然读取源 bucket。源桶不可用时文件下载失败；源桶仍在时则读到了原部署内容，隔离恢复检查不能证明目标桶中的文件可用。演练只查租户关系和对象总数，因此仍会输出 `succeeded=true`。
- **修复建议**：恢复至新桶时显式记录源桶到目标桶的映射，在目标数据库中事务性更新相应存储引用，并以恢复库中的真实记录读取目标对象、校验 hash；或保持原 bucket 名但恢复到独立对象存储实例。若只支持数量级演练，应明确标为基础复制检查，不能替代文件可访问性验收。
- **证据等级**：静态恢复参数、数据库写入路径、实际下载路径交叉核对；没有连接源桶或执行真实下载。

## 验证边界

未修改业务代码或上述四个脚本。没有运行真实 pg_dump/pg_restore、生产恢复或权限测试。没有将未实际复现的 PostgreSQL/MinIO 端行为包装为实测结果。只新增本审查报告、合成验证脚本及其输出。
