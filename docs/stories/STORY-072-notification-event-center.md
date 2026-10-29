# STORY-072：通知与事件中心

状态：Planned。优先级：P0。依赖：既有 outbox、业务事务与 auth。

## 问题与范围

已有 event_outbox 和 dispatcher 能可靠记录和重试事务事件，但当前日志 Publisher 不能完成用户通知。扩展语义事件、收件人路由与站内通知，复用现有基础设施。

- 首切片仅做语义事件 envelope、站内持久化投递、未读/已读、业务下钻。envelope 包含 event_id、schema_version、tenant、aggregate、actor、occurred_at、correlation/command 与最小必要 payload。
- 事件包括 exam.ready_blocked、capture.issue_created、review.task_assigned、grader.drift_warning、arbitration.created、score.release_published、appeal.created、appeal.resolved；题库审核与质量信号按已实施能力追加。
- 既有泛化 mutation 事件需通过显式规则形成通知，不能根据任意 UPDATE 猜测“发布成功”。语义事件与业务变更同事务写入，失败事务不产生通知。
- 在现有 dispatcher 的 Publisher 边界完成持久化 fan-out，再标记 published；唯一键 `(tenant,event,recipient,channel)` 防重。未来多消费者需要独立 delivery 记录，不能多个 dispatcher 抢同一全局 published 标记。
- 收件人按任务分派和学校范围解析；payload 不携带学生答案或密钥，读取和下钻再次按当前权限检查。已读更新只允许本人操作。
- 提供积压、失败、重试与死信运营入口，复用现有租约和退避。

## 非范围

首版不接 Email、企业微信或任意外部消息，不复制 outbox/dispatcher，不以通知到达决定业务事务成功，不让日志包含敏感学生证据。

## 预计修改文件

修改现有 `internal/outbox/`、`internal/server/infrastructure.go` 和相关业务事务；拟新增 `internal/notification/`、通知/投递迁移、`apps/web-admin/src/features/notifications/`、学生端通知接口、OpenAPI 与生成 SDK。

## 测试方式与验收标准

Go 测试路由与 envelope；真实 PostgreSQL 验证回滚无事件、重复投递、投递后确认前崩溃恢复、租约失效与死信。Web 验证未读、本人已读和授权下钻。

验收要求重复事件不重复通知，业务事实与事件原子提交；不允许错误收件人读取；发布事件绑定实际 release_id；权限被撤销后通知不能泄露原内容；日志 Publisher 与通知 fan-out 接线明确。外部渠道留待独立切片。

## 规划审阅与实施记录

规划已根据[现有 outbox](../../services/api-gateway/internal/outbox/outbox.go)收敛为增量工作。原验证器试点的专用通知意图表随 `000220` 退役；既有 `audit_log` 和 `event_outbox` 历史事实保留，见[退役记录](../architecture/teacher-account-security-mfa-pilot.md)。后续通知中心应按本 Story 的业务事件范围另行建立收件人路由和投递状态，不继续依赖该专用表，也不自动把退役功能的旧事件发送给用户。fan-out 仍使用 `(event_id, recipient, channel)` 去重，不能把现有日志 Publisher 的 `published_at` 当作送达。
