# Go 题库移交问题记录

由 root 正式移交的 5 条原始 `go:S3776` 问题均完成逐文件审读、按业务阶段提炼和 gofmt，并通过 server 包编译及回归，当前等待 SonarQube 正式复扫。5 个原 issue key、接手时源码 SHA、当前源码 SHA 和逐文件状态见同目录 `go-server-question-bank-progress.json`。此前 frontend 完成的 `qbRequest` 参数结构及 35 次请求调用已保留。

| 文件 | 提炼职责 | 状态 |
| --- | --- | --- |
| e2e_question_bank_acl_ux_test.go | 历史 ACL 保留、幂等更新、目录与 RBAC 交集、学校范围、失活成员和操作权限 | 编译及包回归通过，PG 条件跳过 |
| e2e_question_bank_import_test.go | 冻结来源、回执及追踪、去重、部分失败恢复、独立审批发布、跨考试复用、撤权重放 | 编译及包回归通过，PG 条件跳过 |
| e2e_question_bank_metadata_acl_search_test.go | 元数据定义、版本和 ACL、搜索素材、撤权、停用及历史保护 | 编译及包回归通过，PG 条件跳过 |
| e2e_question_bank_publication_test.go | 审批边界、并发审批及发布、已发布事实保护、复制恢复、冻结快照及后续来源变更 | 编译及包回归通过，PG 条件跳过 |
| e2e_question_bank_test.go | 并发版本、乐观更新、复合身份、归档、事务回滚、租户 RLS、运行权限、撤权 | 编译及包回归通过，PG 条件跳过 |

`go-server-question-bank-structure-execution.json` 记录 Go AST 辅助工具实际退出码 0，5 个文件所有函数参数不超过 7、辅助认知复杂度不超过 15。该估算用于发现职责仍过大的 helper，不能作为 SonarQube 已关闭告警的证据。

`go-server-question-bank-static-review.json` 记录接手时与当前版诊断文本及所有字符串字面量的多重集差集均为空，SQL、HTTP 路径、命令键、预期状态、失败消息保留；请求调用数量为 35，原 12 个子测试保留。静态核对不代替运行测试。审批及发布场景继续共享同一版本指针，并发场景均在 Wait 完成后关闭通道、收集结果及更新版本；冻结来源的维护事务仍先完成提交再进入导入检查，已提交事务在阶段返回时执行原 deferred Rollback，不影响后续数据库状态。

2026-10-11 19:13 的统一验证实际命令为 `go test -json -p 1 -count=1 -timeout=5m ./internal/server`，退出码 0、188 pass、62 条件 skip，测试前后源码 SHA 一致。执行证据为 `go-server-batch04-tests.json`、原始事件为 `go-server-batch04-events.jsonl`，冻结源码 SHA 为 `go-server-batch04-source-sha.json`。这 5 项均因缺少 `EDUGRADE_E2E_DATABASE_URL` 这一隔离 PostgreSQL 条件实际跳过，逐项原始原因保留在独立 JSON。包编译及其他单元测试通过不代表这 5 个数据库场景已通过，动态 PostgreSQL 通过数为 0。平台关闭数量目前为 0，待 root 复扫确认。
