# Go 候选修复与核验

原23条候选逐条处理：8条修复、14条上下文误报、1条有意TLS负例。必要说明补齐高熵令牌摘要、低版本TLS拒绝测试和二维码末列哨兵的边界；这些注释没有改变原行为。

数学验证新增4096条独立预算，在容量相加前核验保留语法记录与本轮输入。旧符号结果被整批替换，重复修订不会按全部历史条目累计容量。

模型探测按静态策略阻断特殊用途/共享/转换地址，保留DNS整体校验和数字IP拨号。网段依据核对了 [IANA IPv4注册表](https://www.iana.org/assignments/iana-ipv4-special-registry/) 与 [IANA IPv6注册表](https://www.iana.org/assignments/iana-ipv6-special-registry/)。该策略主动拒绝特殊服务地址，即便表中部分地址标记为全球可达；部署路由与防火墙仍需在实际环境核实。

| 原始索引 | 文件 | 状态 | 处理依据 |
| ---: | --- | --- | --- |
| 0 | `services/api-gateway/internal/modelgovernance/managed_config_probe_http_test.go` | `false_positive` | test.content 的 json.Marshal 返回完整且已转义的 JSON 字符串 token，模板在 content 冒号后直接拼入 token，没有额外包引号。finishReason 来自本例三个固定字段 stop/length，其他源码路径不是本条 source。此处有意测试非法/截断的内层模型回答；外层响应仍正确 JSON 编码。 |
| 1 | `services/api-gateway/internal/paper/handlers_test.go` | `false_positive` | 告警数据是 float64 score 经 json.Marshal 产生的数值 JSON token，直接拼在 score/max_score 键的冒号后，变量两侧没有再包字符串引号。数字不能包含可突破引号的双引号；不是将带用户字符的 JSON 字符串再嵌进引号。测试 helper 忽略非有限数的 Marshal 错误会使该特殊夹具 JSON 无效，但不会形成本条所报的引号逃逸。 |
| 2 | `services/api-gateway/internal/paper/handlers_test.go` | `false_positive` | 告警数据是 float64 score 经 json.Marshal 产生的数值 JSON token，直接拼在 score/max_score 键的冒号后，变量两侧没有再包字符串引号。数字不能包含可突破引号的双引号；不是将带用户字符的 JSON 字符串再嵌进引号。测试 helper 忽略非有限数的 Marshal 错误会使该特殊夹具 JSON 无效，但不会形成本条所报的引号逃逸。 |
| 3 | `services/api-gateway/internal/paper/handlers_test.go` | `false_positive` | 告警数据是 float64 score 经 json.Marshal 产生的数值 JSON token，直接拼在 score/max_score 键的冒号后，变量两侧没有再包字符串引号。数字不能包含可突破引号的双引号；不是将带用户字符的 JSON 字符串再嵌进引号。测试 helper 忽略非有限数的 Marshal 错误会使该特殊夹具 JSON 无效，但不会形成本条所报的引号逃逸。 |
| 4 | `services/api-gateway/internal/paper/handlers_test.go` | `false_positive` | 告警数据是 float64 score 经 json.Marshal 产生的数值 JSON token，直接拼在 score/max_score 键的冒号后，变量两侧没有再包字符串引号。数字不能包含可突破引号的双引号；不是将带用户字符的 JSON 字符串再嵌进引号。测试 helper 忽略非有限数的 Marshal 错误会使该特殊夹具 JSON 无效，但不会形成本条所报的引号逃逸。 |
| 5 | `services/api-gateway/internal/server/e2e_question_bank_import_test.go` | `false_positive` | qbJSON 检查 json.Marshal 错误后返回完整 JSON token。该告警参数 v.DefaultScore 在类型中为 float64，模板把结果直接放在 total_score 冒号后，不是放进双引号字符串。因此不是引号逃逸；同一模板其他 fixture ID 拼接未包含在本条数据流判断内，不能以本条误报推断整个模板适用于任意外部输入。 |
| 6 | `services/api-gateway/internal/mathunderstanding/handlers.go` | `fixed` | 已有验证记录与合并后验证记录均限制为4096条，新增符号验证仍限制为1..2048条。容量相加前先按保留的syntax数量用减法检查预算，旧符号验证在重复修订时被替换。超预算契约在深拷贝之前拒绝；验证覆盖数量上限、语法疑点保留、整批替换及输入不被改写。 |
| 7 | `services/api-gateway/internal/minioadapter/client_test.go` | `intentional` | TestNewClientRejectsTLSBeforeVersion12 有意构造最大 TLS1.1 的 httptest 服务，断言真实 NewClient 客户端在发送 HTTP 前拒绝该服务；生产 MinIO NewClient 的 cfg.UseSSL 分支设置 TLSClientConfig.MinVersion = TLS1.2。不是业务客户端允许 TLS1.1，也不是只按 _test.go 文件名排除。 |
| 8 | `services/api-gateway/internal/auth/login_limiter_redis.go` | `false_positive` | SHA256 输入是账号/来源/配对限流桶的键，而非密码。LoginAttempt 只有租户、标识、IP 和服务端解析的 AccountID；Redis 的哈希只是稳定索引。不能把这个用法视作低熵密码存储，也不能把无密钥 SHA256 当作不可猜测的匿名化。 |
| 9 | `services/api-gateway/internal/auth/token.go` | `false_positive` | HashToken 存储高熵随机令牌的摘要：NewToken 使用 crypto/rand 生成 32 字节，MFA 恢复码另为 16 字节随机数。激活/恢复流程明确将 input.Token 与 input.Password 分开。密码由 HashPassword 的带随机盐 Argon2id 处理，不能将 token 的快速摘要归为弱密码哈希；本条不评估全部认证流程。 |
| 10 | `services/api-gateway/internal/mathunderstanding/pilot_store.go` | `false_positive` | ListPilotGates 在 make 前把 limit<=0 或 limit>500 改为100；有效分配容量最多500。本告警忽略了同函数中的上限归一化。 |
| 11 | `services/api-gateway/internal/processing/store_postgres.go` | `false_positive` | ListExceptions 在 SQL 和 make 前把 Limit<=0 或 Limit>100 改为25，容量最多100，查询多取一行仍最多101。实际输入不是无界分配。 |
| 12 | `services/api-gateway/internal/workerruntime/store_memory.go` | `false_positive` | MemoryStore.claim 先调用 normalizeClaimInput，Limit<=0 或>100 被改为1；归一化返回值覆盖 input，之后才分配容量。因此这条告警中的 input.Limit 已有上限。 |
| 13 | `services/api-gateway/internal/workerruntime/store_postgres.go` | `false_positive` | PostgresStore.claim 同样先覆盖 input 为 normalizeClaimInput 的返回值，Limit 处于1..100；SQL LIMIT 与 make 的容量都使用该值。跨租户模式没有绕开这一步。本条只核验分配边界，不证明全部跨租户权限正确。 |
| 14 | `services/api-gateway/internal/modelgovernance/managed_config_probe.go` | `fixed` | 已将探测地址判断移至独立模块，拒绝IANA特殊用途网段、共享地址、文档/基准地址和IPv6转换前缀；IPv4映射地址先Unmap后核验，IPv6只允许2000::/3并排除其中特殊段。保留HTTPS、全DNS回答检查、数字IP拨号、禁重定向及默认TLS验证；混合特殊地址在任何拨号前拒绝。未连接部署网络，不将代码修复等同于验证实际路由或防火墙。 |
| 15 | `services/api-gateway/internal/schoolassistant/service.go` | `false_positive` | 传入日志的是独立 slog 属性 request_id；进入 preparedQuery 前 validateRequest 会 TrimSpace 并用 uuid.Parse 拒绝无效请求标识。攻击者的任意换行文字不能沿本告警路径进入此日志字段。不能从此推断所有其他日志字段也已审查。 |
| 16 | `services/api-gateway/internal/capture/print_package_pdf.go` | `false_positive` | drawQRCode 故意让 columnIndex 到 len(row) 的哨兵位，以冲刷最后一段黑色模块。唯一 row[columnIndex] 读取位于 `columnIndex < len(row) && row[columnIndex]` 右侧，Go && 短路保证哨兵位不会执行读取；range 提供实际行，列索引从0递增。后续只用列差计算绘图矩形，无第二次 bitmap 索引；外层空 bitmap 也已提前返回。不需依赖 qrcode.Bitmap 保证方阵才能排除这条越界，空行同样不读取。 |
| 17 | `services/api-gateway/internal/modelgovernance/store_memory.go` | `fixed` | 移除首次UpdatePolicy中未被读取的current.TenantID赋值；最终policy.TenantID和租户映射键保持不变。 |
| 18 | `services/api-gateway/internal/modelgovernance/store_memory.go` | `fixed` | 移除首次UpdatePolicy中未被读取的current.PolicyKey赋值；最终policy.PolicyKey仍设为default。 |
| 19 | `services/api-gateway/internal/modelgovernance/store_memory.go` | `fixed` | 移除首次UpdatePolicy中未被读取的current.Status赋值；最终policy.Status、版本检查与保存流程保持不变。 |
| 20 | `services/api-gateway/internal/questionbank/metadata_acl_search_test.go` | `fixed` | 撤权UpdateACL的未使用对象返回值改接到_，保留旧doc.Revision实参、错误检查和撤权后查询断言。 |
| 21 | `services/api-gateway/internal/server/e2e_question_bank_acl_ux_test.go` | `fixed` | 末次UpdateACL的未使用对象返回值改接到_，保留旧updated.Revision实参及直接授权/组权限数据库断言。 |
| 22 | `services/api-gateway/internal/server/e2e_question_bank_metadata_acl_search_test.go` | `fixed` | 再授权UpdateACL的未使用对象返回值改接到_，保留旧acl.Revision实参、再授权动作与后续权限/审计检查。 |


`mathunderstanding`、`modelgovernance`、`questionbank`包测试已通过，日志见 `go-tests.log`。两个PostgreSQL ACL集成场景的本轮实际执行/跳过状态另列于 `go-server-tests-attempt2.log`。

单独发现并处理的验证阻塞：`e2e_scoring_command_test.go`中的helper引用作用域外`db`，首次server测试编译失败。修复为显式传入同一数据库连接，保留PostgreSQL传输回执存储；该问题不计入原23条CodeQL候选。

本轮未访问线上接口或部署网络、未重扫CodeQL。原始报告、SARIF和固定快照保持不变。

两个 PostgreSQL ACL 集成场景的实际结果为 **2 skipped**：未配置专用隔离测试库。server 测试包已成功编译，但不能称这两条数据库行为验证已执行通过。
