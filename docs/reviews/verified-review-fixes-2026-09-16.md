# 已复核问题修复记录

本次处理独立复核后确认的 9 项问题，顺序为 R03、R01、R05、R04、R06/R07、R08、R09、R10。R02 仍属于 Needs Verification，没有据此改动旧发布业务规则。本次没有提交或部署。

## 修复与验证

| ID | 修复 | 回归验证 |
| --- | --- | --- |
| R03 | 在考试锁和当前发布记录锁内核对复评来源版本；过期草稿返回 `score_release_stale_source`，保持草稿与当前发布指针不变。 | 内存测试及 PostgreSQL 测试：同一基线生成不同题目的两个修正，发布 A 后拒绝 B；重试发布 A 成功。 |
| R01 | 终评分汇总读取当前已确认的 `question_grade` 自动评分事实；仅在没有新版事实时兼容旧 `ai_grade`。 | STORY-056 完整 worker 流程：67 份自动确认、33 份人工确认全部生成最终成绩；重复汇总不新增成绩。 |
| R05 | 桌面提交携带任务包的 `expected_revision`；提交前检查任务包版本，旧格式或过期版本要求重新下载核对。 | 桌面请求序列化测试、全量桌面测试和类型检查。 |
| R04 | 普通任务资源解析失败时解析隐藏质检任务，并严格校验领取人和租户。 | PostgreSQL + 生产路由：创建标准卷、完成校准后领取隐藏任务，领取人可读取上下文并提交，其他用户被拒绝。 |
| R06 | 允许复用 `pending_upload` / `upload_failed` 文件记录，重写相同哈希的对象后激活。 | 对象存储首次写入失败，再次完成上传成功；保留相同资产 ID，不重复注册采集文件。 |
| R07 | 完成上传使用可过期租约和独立令牌；过期请求可以接管，旧请求不能完成或重置新租约；请求取消后用独立清理上下文恢复可重试状态。 | 内存和 PostgreSQL 租约接管、旧令牌拒绝、取消后重试、完成后唯一注册。 |
| R08 | 最后一次答案已保存但完成校准失败时，相同答案重试继续完成会话和资格生成；不同答案仍拒绝。 | 注入完成阶段失败；相同答案恢复成功，只保留一次作答，修改答案重试被拒绝。 |
| R09 | 图像质检源记录恢复为可重试状态与 worker 重新入队使用同一事务，按源记录 → worker 的顺序加锁。 | PostgreSQL：终止结果 → 异常投影 → 重试 → 再领取 → 成功结果；注入源记录更新失败，验证 worker 更新一并回滚。 |
| R10 | 学生查询仅读取本人逐题记录；总分模式不读取逐题；仅在展示策略需要时读取群体总分及高分卷。 | PostgreSQL 查询跟踪记录实际行数：普通查询 3 行、总分查询 0 行；附加验证高分卷和小样本统计保护。 |

## 数据库迁移与恢复

- 新增迁移：`services/api-gateway/migrations/000151_capture_upload_completion_lease.sql`。
- 部署配置要求的 schema 版本同步为 `000151`。应先按现有部署流程应用迁移，再运行新版 API。
- 迁移增加 `completion_token` 和 `completion_lease_until`；历史 `finalizing` 会话的空租约按已过期处理，可在重试时接管。
- 完成请求超时为 2 分钟，租约为 3 分钟。仍存活的租约拒绝并发接管，过期后下一次完成请求可恢复。数据库使用自身时间判断租约，避免应用主机时钟偏差。
- 不主动重发业务任务、不批量改写历史成绩；修复后的重试、汇总和发布入口执行上述保护。

## 测试入口

结果：后端全量单元测试、STORY-056 数据库回归、新增 PostgreSQL 回归、桌面端 21 项测试、桌面类型检查、schema 同步检查及 2 项 schema 脚本测试全部通过；`git diff --check` 通过。

后端工作目录：`services/api-gateway`。

```text
go test ./internal/...
go test ./internal/server -run '^TestReviewRepairs|^TestStory056ObjectiveScoringRecoveryE2EWithPostgresTestDatabase$' -count=1 -timeout=5m
```

数据库测试要求设置 `EDUGRADE_E2E_DATABASE_URL`，由既有测试工具创建和删除独立数据库。本次使用仅监听本机的 PostgreSQL 17 合成测试环境。新增数据库测试名称包含 `PostgresTestDatabase`，会被现有 CI PostgreSQL 测试选择器选中。

仓库根目录：

```text
npm --workspace apps/desktop-client test
npm --workspace apps/desktop-client run typecheck
node scripts/check-schema-version.mjs
node --test scripts/ci/schema-version.test.mjs
git diff --check
```

本次数据库测试使用进程内对象存储故障注入，不等同于真实对象存储集群故障演练；查询行数验证不等同于生产规模延迟压测。
