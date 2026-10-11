# server 可靠性测试重构独立审查

审查结论：在明确列出的范围内，未发现当前重构版本仍存在的业务语义回归。只读审查未修改冻结源代码。主文件 `services/api-gateway/internal/server/e2e_reliability_command_test.go` 的接手基线 1213 行与当前 1633 行均已完整审读，并逐业务阶段比较；当前 SHA 与成功的 `go-server-batch04-source-sha.json` 一致。原 issue key、两版 SHA、核对行号和阶段结论见 `go-server-reliability-independent-review.json`。

| 核对重点 | 结论 |
| --- | --- |
| 多个真实客户端命令 | 恢复 `for i, commandID := range commandIDs`，每个命令仍与 `sessionIDs[i]` 配对计数。业务场景的 `scenario.CommandID` 不替代逐命令身份。 |
| 跨阶段状态 | 改变后 `SessionInput` 回写；导入 Job、Dispatch、OldClaim、Current、FailureTask/FailureClaim、FailedJob、Retried 及末次 lastClaim/lastOwner 均显式返回并传给后续阶段，未用零值替换原身份。 |
| 原代次与新代次 | 第 1 代取消后建立第 2 代；旧准备、旧解码和旧协议回调拒绝；第 2 代失败后同一重试命令恢复第 3 代。取消与完成仍共用开始屏障并收齐两结果。 |
| 租约 | 准备首轮延长同一数据库租约后继续使用原 claimed；投影 old/fresh 租约分离，同 owner 重领仍拒绝旧回调；投影只确认领取版本，后来请求继续待处理。 |
| 故障与恢复顺序 | 第三次准备耗尽和最终运行时耗尽均先安装故障触发器，运行失败并核对未部分提交，删除触发器后才新建 Store 完成恢复；重复恢复的零变更检查保留。 |
| 资源与 defer | 真实客户端的 server.Close 和超时 cancel 仍在原 CMD 子测试边界，cancel 先执行；TempDir 归属同一个 testing.T。该主文件未新增显式 BeginTx/Commit/Rollback；本审查不声称验证生产 Store 内部事务。 |
| 投影与采集 | 人工派发和确认历史保持；跨考试转移仍先目标后原考试刷新；采集命令嵌套子测试先完成，页删除/恢复使用原生命周期修订与有效页统计。 |
| 迁移修复 | 升级至 000122、种旧记录、全迁移、种新可恢复命令、验证未知历史的顺序保持。限定导入预演/应用/重复零变更后才全租户修复；原传输请求哈希、201 响应、Session.ID、needs_review 状态和第二次更新时间核对均保留。 |

`batch02`/`batch03` 的编译失败日志保留。修正时曾从真实客户端迭代中移除 range 的 commandID，独立审查指出这一问题；frontend 已恢复逐命令迭代并通过 `batch04`。当前版本诊断文本的多重集差集为空；所有字符串只增加用于新测试上下文类型声明的 `database/sql` 导入。除新增 46 个 `t.Helper` 外，选择器调用数量未变。这些机械核对只支持逐阶段审读，不单独证明参数和执行顺序等价。

额外只核对编译修正的明确影响范围：主观题命令 helper 的 identity/scopedIdentity 遮蔽与调用参数、worker 重领场景的 reclaimed/oldTask 租约比较、review 路由 helper 的第二 req/rec 赋值、复核恢复的局部 err 声明、导入并发 helper 的未使用 job 别名。范围与行号逐项记录在 JSON；这些额外文件未在本独立审查中全读，均明确保留全文件 pending，不作全文件结论。

实际运行证据 `go-server-batch04-tests.json`：退出码 0，188 pass、62 条件 skip，测试前后源码 SHA 一致。此次主审查的两个可靠性测试均因缺少 `EDUGRADE_E2E_DATABASE_URL` 实际跳过；原始原因在 `go-server-batch04-events.jsonl`。本结论为静态语义审查及包编译/回归证据，不计真实 PostgreSQL 工作流动态通过。SonarQube 告警关闭仍由 root 正式复扫确认。
