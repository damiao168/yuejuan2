# Go server 专项进度

62 项原告警：本智能体已修改 57 项；题库 5 项由 Go 智能体完成独立账本，源码总计 62 项。已实现不代表平台关闭或测试通过。

7 个长参数列表改为真实领域输入：仲裁夹具、文件资产、题库请求、题目配置、评分摘要、角色身份和校准证据。51 个调用同步改为具名字段，原值及角色独立性保留。

复杂度按实际业务阶段拆解：申诉派发与教师建议、仲裁回执与并发导出、命令占位接管与终态恢复、逐项人工评分、发布前确认门禁、学校与 OCR 权限场景、数学冻结量规与历史修订、MFA 通知回滚/并发禁用/审计、迁移升级的数据/密文/绑定审计、模型探测生命周期、用户开通幂等性。

所有新测试 helper 使用 t.Helper()；没有新增子测试改变失败传播，也没有通用 FatalIf(bool) 或删除 guard 来降低复杂度。断言数量辅助清单 go-server-assertion-inventory.json；每条业务条件仍按实际代码核对。

当前 gofmt 及 Go AST 解析通过。最新冻结批次 go-server-batch04 已通过 Go 编译和现有 server 测试：188 条 Test/subtest 通过、62 条条件跳过（61 条 PostgreSQL 环境条件，1 条 TestBusinessCommandReservationCrashHelper 辅助入口条件），源码测试前后 SHA 一致，所有改动与本批次 SHA 匹配。逐项原始跳过原因在 go-server-batch04-skip-classification.json，原测试日志未修改。环境本来没有数据库 DSN，没有清除数据库变量；条件跳过不计数据库业务验证通过。batch02/03 编译失败日志及两批修复记录全部保留，不以旧通过批次覆盖新改动。自制复杂度估算不能代替 SonarQube，本次不运行平台扫描。

逐原 issue key 状态见 go-server-progress.json。完整审读的 26 个文件记录为 annotated，其余文件仍 pending；参数调用更新不等同全文件完成审读。每份写入前源码备份在 server-extra-baseline，保留已有工作区修改。
