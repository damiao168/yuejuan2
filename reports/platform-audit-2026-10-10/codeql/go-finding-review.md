# CodeQL Go 23 条候选核验

核验时间：2026-10-11T13:36:49.821316+08:00。实际扫描执行和原始等级见 [CodeQL 主报告](README.md)。

核验固定扫描快照，列明源码证据均与逐文件 SHA256 清单一致；没有修改业务源码或运行生产接口。自动候选保留官方安全分数，人工结论单列。

全部23条候选已分配并检查告警上下文：14条上下文误报、1条有意测试行为、6条低影响维护项、1条资源预算加固、1条条件性部署检查。没有确认可利用的 Go 生产漏洞；部署前提待确认不等于风险已经排除。这也不代表逐文件人工审读或跨学校数据隔离已经验证完成。

数学验证容量告警不具备已证明的整数溢出路径，但已有记录缺独立数量上限；模型探测具备 HTTPS、DNS 地址检查及禁重定向保护，特殊用途地址和部署路由仍应核对。

原始 SARIF SHA256：`8146976c6190873c2e5ca65046b469853d99bab5a489b0e0d6c6259a637b72de`。结构化结论和证据见 [go-finding-review.json](go-finding-review.json)。

| 编号 | 规则 | 固定快照位置 | 官方安全分数 | 人工结论 |
| --- | --- | --- | --- | --- |
| CQ-GO-001 | `go/unsafe-quoting` | `services/api-gateway/internal/modelgovernance/managed_config_probe_http_test.go:395` | 9.3 | false_positive_context / none |
| CQ-GO-002 | `go/unsafe-quoting` | `services/api-gateway/internal/paper/handlers_test.go:519` | 9.3 | false_positive_context / none |
| CQ-GO-003 | `go/unsafe-quoting` | `services/api-gateway/internal/paper/handlers_test.go:523` | 9.3 | false_positive_context / none |
| CQ-GO-004 | `go/unsafe-quoting` | `services/api-gateway/internal/paper/handlers_test.go:527` | 9.3 | false_positive_context / none |
| CQ-GO-005 | `go/unsafe-quoting` | `services/api-gateway/internal/paper/handlers_test.go:527` | 9.3 | false_positive_context / none |
| CQ-GO-006 | `go/unsafe-quoting` | `services/api-gateway/internal/server/e2e_question_bank_import_test.go:214` | 9.3 | false_positive_context / none |
| CQ-GO-007 | `go/allocation-size-overflow` | `services/api-gateway/internal/mathunderstanding/handlers.go:356` | 8.1 | low_hardening / low |
| CQ-GO-008 | `go/insecure-tls` | `services/api-gateway/internal/minioadapter/client_test.go:72` | 7.5 | intentional_test_behavior / none |
| CQ-GO-009 | `go/weak-sensitive-data-hashing` | `services/api-gateway/internal/auth/login_limiter_redis.go:160` | 7.5 | false_positive_context / none |
| CQ-GO-010 | `go/weak-sensitive-data-hashing` | `services/api-gateway/internal/auth/token.go:21` | 7.5 | false_positive_context / none |
| CQ-GO-011 | `go/uncontrolled-allocation-size` | `services/api-gateway/internal/mathunderstanding/pilot_store.go:53` | 7.5 | false_positive_context / none |
| CQ-GO-012 | `go/uncontrolled-allocation-size` | `services/api-gateway/internal/processing/store_postgres.go:215` | 7.5 | false_positive_context / none |
| CQ-GO-013 | `go/uncontrolled-allocation-size` | `services/api-gateway/internal/workerruntime/store_memory.go:113` | 7.5 | false_positive_context / none |
| CQ-GO-014 | `go/uncontrolled-allocation-size` | `services/api-gateway/internal/workerruntime/store_postgres.go:237` | 7.5 | false_positive_context / none |
| CQ-GO-015 | `go/request-forgery` | `services/api-gateway/internal/modelgovernance/managed_config_probe.go:404` | 9.1 | conditional_deployment_review / medium |
| CQ-GO-016 | `go/log-injection` | `services/api-gateway/internal/schoolassistant/service.go:160` | 6.1 | false_positive_context / none |
| CQ-GO-017 | `go/index-out-of-bounds` | `services/api-gateway/internal/capture/print_package_pdf.go:141` | 非安全候选 | false_positive_context / none |
| CQ-GO-018 | `go/useless-assignment-to-field` | `services/api-gateway/internal/modelgovernance/store_memory.go:303` | 非安全候选 | low_maintenance / low |
| CQ-GO-019 | `go/useless-assignment-to-field` | `services/api-gateway/internal/modelgovernance/store_memory.go:304` | 非安全候选 | low_maintenance / low |
| CQ-GO-020 | `go/useless-assignment-to-field` | `services/api-gateway/internal/modelgovernance/store_memory.go:305` | 非安全候选 | low_maintenance / low |
| CQ-GO-021 | `go/useless-assignment-to-local` | `services/api-gateway/internal/questionbank/metadata_acl_search_test.go:117` | 非安全候选 | low_maintenance / low |
| CQ-GO-022 | `go/useless-assignment-to-local` | `services/api-gateway/internal/server/e2e_question_bank_acl_ux_test.go:190` | 非安全候选 | low_maintenance / low |
| CQ-GO-023 | `go/useless-assignment-to-local` | `services/api-gateway/internal/server/e2e_question_bank_metadata_acl_search_test.go:154` | 非安全候选 | low_maintenance / low |

## CQ-GO-001：go/unsafe-quoting

固定快照位置：`services/api-gateway/internal/modelgovernance/managed_config_probe_http_test.go:395`。

test.content 的 json.Marshal 返回完整且已转义的 JSON 字符串 token，模板在 content 冒号后直接拼入 token，没有额外包引号。finishReason 来自本例三个固定字段 stop/length，其他源码路径不是本条 source。此处有意测试非法/截断的内层模型回答；外层响应仍正确 JSON 编码。

建议：保留模型能力失败闭合的测试场景；可改整体外层结构编码以减少扫描歧义，不删除 json.Marshal 转义。

已审读证据范围：
- `services/api-gateway/internal/modelgovernance/managed_config_probe_http_test.go:374–415`，SHA256 `9b70a95056ad98ba69e35f536fb2dd1c12231eb57844440410ee87932ce113c9`。

## CQ-GO-002：go/unsafe-quoting

固定快照位置：`services/api-gateway/internal/paper/handlers_test.go:519`。

告警数据是 float64 score 经 json.Marshal 产生的数值 JSON token，直接拼在 score/max_score 键的冒号后，变量两侧没有再包字符串引号。数字不能包含可突破引号的双引号；不是将带用户字符的 JSON 字符串再嵌进引号。测试 helper 忽略非有限数的 Marshal 错误会使该特殊夹具 JSON 无效，但不会形成本条所报的引号逃逸。

建议：本告警记录上下文误报；后续可用整体 struct/map 编码统一测试请求，并检查 Marshal 错误，避免手工模板维护。不得为此调整生产评分逻辑。

已审读证据范围：
- `services/api-gateway/internal/paper/handlers_test.go:518–533`，SHA256 `2f5c8775029058b7a6e86b1b6a219fa80a29d0ee8ec9f6d7b7a0873ff15aca92`。

## CQ-GO-003：go/unsafe-quoting

固定快照位置：`services/api-gateway/internal/paper/handlers_test.go:523`。

告警数据是 float64 score 经 json.Marshal 产生的数值 JSON token，直接拼在 score/max_score 键的冒号后，变量两侧没有再包字符串引号。数字不能包含可突破引号的双引号；不是将带用户字符的 JSON 字符串再嵌进引号。测试 helper 忽略非有限数的 Marshal 错误会使该特殊夹具 JSON 无效，但不会形成本条所报的引号逃逸。

建议：本告警记录上下文误报；后续可用整体 struct/map 编码统一测试请求，并检查 Marshal 错误，避免手工模板维护。不得为此调整生产评分逻辑。

已审读证据范围：
- `services/api-gateway/internal/paper/handlers_test.go:518–533`，SHA256 `2f5c8775029058b7a6e86b1b6a219fa80a29d0ee8ec9f6d7b7a0873ff15aca92`。

## CQ-GO-004：go/unsafe-quoting

固定快照位置：`services/api-gateway/internal/paper/handlers_test.go:527`。

告警数据是 float64 score 经 json.Marshal 产生的数值 JSON token，直接拼在 score/max_score 键的冒号后，变量两侧没有再包字符串引号。数字不能包含可突破引号的双引号；不是将带用户字符的 JSON 字符串再嵌进引号。测试 helper 忽略非有限数的 Marshal 错误会使该特殊夹具 JSON 无效，但不会形成本条所报的引号逃逸。

建议：本告警记录上下文误报；后续可用整体 struct/map 编码统一测试请求，并检查 Marshal 错误，避免手工模板维护。不得为此调整生产评分逻辑。

已审读证据范围：
- `services/api-gateway/internal/paper/handlers_test.go:526–533`，SHA256 `2f5c8775029058b7a6e86b1b6a219fa80a29d0ee8ec9f6d7b7a0873ff15aca92`。

## CQ-GO-005：go/unsafe-quoting

固定快照位置：`services/api-gateway/internal/paper/handlers_test.go:527`。

告警数据是 float64 score 经 json.Marshal 产生的数值 JSON token，直接拼在 score/max_score 键的冒号后，变量两侧没有再包字符串引号。数字不能包含可突破引号的双引号；不是将带用户字符的 JSON 字符串再嵌进引号。测试 helper 忽略非有限数的 Marshal 错误会使该特殊夹具 JSON 无效，但不会形成本条所报的引号逃逸。

建议：本告警记录上下文误报；后续可用整体 struct/map 编码统一测试请求，并检查 Marshal 错误，避免手工模板维护。不得为此调整生产评分逻辑。

已审读证据范围：
- `services/api-gateway/internal/paper/handlers_test.go:526–533`，SHA256 `2f5c8775029058b7a6e86b1b6a219fa80a29d0ee8ec9f6d7b7a0873ff15aca92`。

## CQ-GO-006：go/unsafe-quoting

固定快照位置：`services/api-gateway/internal/server/e2e_question_bank_import_test.go:214`。

qbJSON 检查 json.Marshal 错误后返回完整 JSON token。该告警参数 v.DefaultScore 在类型中为 float64，模板把结果直接放在 total_score 冒号后，不是放进双引号字符串。因此不是引号逃逸；同一模板其他 fixture ID 拼接未包含在本条数据流判断内，不能以本条误报推断整个模板适用于任意外部输入。

建议：该候选记录为上下文误报。测试模板可在后续统一为整体 JSON 编码以提高维护性。

已审读证据范围：
- `services/api-gateway/internal/server/e2e_question_bank_import_test.go:196–216`，SHA256 `d5f7756150d5784e28eba49db0e51b5ad59e17b9691cc5af3b4e2262cd7c689e`。
- `services/api-gateway/internal/server/e2e_question_bank_test.go:317–324`，SHA256 `a579dc5783afbf6de6f8b5394c829a73330db2969ba745bdb53c7f4c5c6ce56b`。
- `services/api-gateway/internal/questionbank/types.go:62–80`，SHA256 `b82c75c543cbe225687e7ad37857e9dc0ba5e53dbc751467335dfebf405e5b7a`。

## CQ-GO-007：go/allocation-size-overflow

固定快照位置：`services/api-gateway/internal/mathunderstanding/handlers.go:356`。

官方报告容量加法可能溢出。唯一调用处先把新增 checks 数量限制为 1..2048；已有 Verifications 是已经分配的非零大小结构切片，不能据此证明可让 len 接近 int 上限。HTTP JSON 入口另有 2 MiB 上限。未复现整数溢出或运行 panic；但已有验证列表的校验函数没有独立条目数上限，因此保留资源预算加固建议，不宣称所有输入规模已经有统一上限。

建议：为已有与合并后的验证记录设定明确数量预算，在容量相加前检查预算；覆盖重复修订、语法记录保留与合法符号验证场景。

已审读证据范围：
- `services/api-gateway/internal/mathunderstanding/handlers.go:320–374`，SHA256 `34b804d67eacd03f6f8f2a0c63250a62d6735a01521eff562b9a3f3c1a79d4ab`。
- `services/api-gateway/internal/mathunderstanding/handlers.go:258–278`，SHA256 `34b804d67eacd03f6f8f2a0c63250a62d6735a01521eff562b9a3f3c1a79d4ab`。
- `services/api-gateway/internal/mathunderstanding/handlers.go:674–680`，SHA256 `34b804d67eacd03f6f8f2a0c63250a62d6735a01521eff562b9a3f3c1a79d4ab`。
- `services/api-gateway/internal/mathunderstanding/types.go:315–324`，SHA256 `9bc0493063f755dfff84cf3b1a93077435fb4a6531243a195e6951f24ef901a6`。

## CQ-GO-008：go/insecure-tls

固定快照位置：`services/api-gateway/internal/minioadapter/client_test.go:72`。

TestNewClientRejectsTLSBeforeVersion12 有意构造最大 TLS1.1 的 httptest 服务，断言真实 NewClient 客户端在发送 HTTP 前拒绝该服务；生产 MinIO NewClient 的 cfg.UseSSL 分支设置 TLSClientConfig.MinVersion = TLS1.2。不是业务客户端允许 TLS1.1，也不是只按 _test.go 文件名排除。

建议：保留低协议版本拒绝测试，针对本条记录负例用途。实际运行部署和用例执行结果未在此次人工核验中验证。

已审读证据范围：
- `services/api-gateway/internal/minioadapter/client_test.go:68–98`，SHA256 `a94ea4453cbee7d344976a6a4927ab14e6eb9a038c9a41f268073203b715aa2f`。
- `services/api-gateway/internal/minioadapter/client.go:15–47`，SHA256 `c72be7ea24666abf4654b906438dbbfdf5cf1ed1e053be57477290fa24fd7032`。

## CQ-GO-009：go/weak-sensitive-data-hashing

固定快照位置：`services/api-gateway/internal/auth/login_limiter_redis.go:160`。

SHA256 输入是账号/来源/配对限流桶的键，而非密码。LoginAttempt 只有租户、标识、IP 和服务端解析的 AccountID；Redis 的哈希只是稳定索引。不能把这个用法视作低熵密码存储，也不能把无密钥 SHA256 当作不可猜测的匿名化。

建议：保持限流桶和密码存储职责分开；若未来需要对低熵标识作保密匿名化，再使用独立有密钥方案。

已审读证据范围：
- `services/api-gateway/internal/auth/login_limiter_redis.go:159–162`，SHA256 `dedb2b542e57db315a36b0d4705ba31d40e55d28b77bb0cf937265ae161a88ae`。
- `services/api-gateway/internal/auth/login_limiter.go:17–23`，SHA256 `687f88fd391a92e19b90f27d31663831aebd585c7924deca94f65f643f8b5250`。
- `services/api-gateway/internal/auth/login_limiter.go:130–142`，SHA256 `687f88fd391a92e19b90f27d31663831aebd585c7924deca94f65f643f8b5250`。
- `services/api-gateway/internal/auth/login_limiter.go:168–185`，SHA256 `687f88fd391a92e19b90f27d31663831aebd585c7924deca94f65f643f8b5250`。

## CQ-GO-010：go/weak-sensitive-data-hashing

固定快照位置：`services/api-gateway/internal/auth/token.go:21`。

HashToken 存储高熵随机令牌的摘要：NewToken 使用 crypto/rand 生成 32 字节，MFA 恢复码另为 16 字节随机数。激活/恢复流程明确将 input.Token 与 input.Password 分开。密码由 HashPassword 的带随机盐 Argon2id 处理，不能将 token 的快速摘要归为弱密码哈希；本条不评估全部认证流程。

建议：继续要求令牌具有充足随机熵，不让 HashToken 处理用户密码或未来的短码。

已审读证据范围：
- `services/api-gateway/internal/auth/token.go:1–23`，SHA256 `de0be9f08eebb837f2543b675a2327b310d146405cfbcea4c68be30725ec7988`。
- `services/api-gateway/internal/auth/password.go:31–46`，SHA256 `38bb4612c0847e31cb6330a84926974f7cafbfd751ae37d385663c3cf48c1167`。
- `services/api-gateway/internal/auth/activation.go:101–118`，SHA256 `f92d71b244ea2b70f8d6596a62fa0ccae44a95963a385136d728588048760a24`。
- `services/api-gateway/internal/auth/recovery.go:101–119`，SHA256 `31025e44c552d0b117987cb67fd2db716616402434b28282f4c9a555c01bbbc2`。
- `services/api-gateway/internal/auth/mfa.go:145–167`，SHA256 `4a0f43d3ff82b5ee661f2023fc6c5fc542978684fe3993d7c8e42ddb999454b8`。

## CQ-GO-011：go/uncontrolled-allocation-size

固定快照位置：`services/api-gateway/internal/mathunderstanding/pilot_store.go:53`。

ListPilotGates 在 make 前把 limit<=0 或 limit>500 改为100；有效分配容量最多500。本告警忽略了同函数中的上限归一化。

建议：保留归一化及租户过滤；本条不需要为扫描结果改动业务逻辑。

已审读证据范围：
- `services/api-gateway/internal/mathunderstanding/pilot_store.go:47–59`，SHA256 `5e8b9d7abbc76e201f5faf8791dc3d6755de888cfc5fa746f6c1d040b4e7d867`。

## CQ-GO-012：go/uncontrolled-allocation-size

固定快照位置：`services/api-gateway/internal/processing/store_postgres.go:215`。

ListExceptions 在 SQL 和 make 前把 Limit<=0 或 Limit>100 改为25，容量最多100，查询多取一行仍最多101。实际输入不是无界分配。

建议：保留查询与分配使用同一归一化值的顺序。

已审读证据范围：
- `services/api-gateway/internal/processing/store_postgres.go:178–215`，SHA256 `97ada29c843c7987a5c532e44689b1fcec1b4142056301618a9228c3d2fe9a6d`。

## CQ-GO-013：go/uncontrolled-allocation-size

固定快照位置：`services/api-gateway/internal/workerruntime/store_memory.go:113`。

MemoryStore.claim 先调用 normalizeClaimInput，Limit<=0 或>100 被改为1；归一化返回值覆盖 input，之后才分配容量。因此这条告警中的 input.Limit 已有上限。

建议：保留归一化在锁内任务选取和容量分配之前的边界。

已审读证据范围：
- `services/api-gateway/internal/workerruntime/store_memory.go:103–116`，SHA256 `b833cebeb06b700a2596fb778530cdc16de58ab372c1f0764f8ab7044543df91`。
- `services/api-gateway/internal/workerruntime/validation.go:47–64`，SHA256 `ed18938d37dc7ed8003cba6ea9562fd5103ef008224b94acd77754fafbaeb0a2`。

## CQ-GO-014：go/uncontrolled-allocation-size

固定快照位置：`services/api-gateway/internal/workerruntime/store_postgres.go:237`。

PostgresStore.claim 同样先覆盖 input 为 normalizeClaimInput 的返回值，Limit 处于1..100；SQL LIMIT 与 make 的容量都使用该值。跨租户模式没有绕开这一步。本条只核验分配边界，不证明全部跨租户权限正确。

建议：保留归一化在事务和查询之前的顺序。

已审读证据范围：
- `services/api-gateway/internal/workerruntime/store_postgres.go:112–118`，SHA256 `b260e8af94233c4dfdee875cfff15452cf85aa5fd0b0d9ed4f91a386ea85e5dc`。
- `services/api-gateway/internal/workerruntime/store_postgres.go:212–237`，SHA256 `b260e8af94233c4dfdee875cfff15452cf85aa5fd0b0d9ed4f91a386ea85e5dc`。
- `services/api-gateway/internal/workerruntime/validation.go:47–64`，SHA256 `ed18938d37dc7ed8003cba6ea9562fd5103ef008224b94acd77754fafbaeb0a2`。

## CQ-GO-015：go/request-forgery

固定快照位置：`services/api-gateway/internal/modelgovernance/managed_config_probe.go:404`。

官方 Critical 9.1 数据流在 client.Do 命中用户配置的模型地址，但生产构造器绑定自定义 Transport：只接受 HTTPS、禁重定向、无环境代理；一次解析后检查全部 DNS 地址，再以已验证数字 IP 拨号，拒绝回环、私网、未指定、链路本地和多播。没有证明这些常见内网 SSRF 路径可绕过。publicProviderIP 未明确拒绝全部特殊用途地址（例如共享地址段）；其部署路由及管理员可配置目标的实际权限仍需单独核对，所以保留条件性部署检查，不标为已确认 SSRF，也不把当前列表称为所有特殊地址都安全。

建议：核对部署中的特殊地址段和提供商配置权限；按部署政策补充禁止网段或提供商允许列表，并保留 DNS 全量检查、数字 IP 拨号和禁止重定向。

已审读证据范围：
- `services/api-gateway/internal/modelgovernance/managed_config_probe.go:47–55`，SHA256 `0aa82bba41c2aef97d1dcb8950ad7392172e944b9b1b977a6285600e7b1c6550`。
- `services/api-gateway/internal/modelgovernance/managed_config_probe.go:305–317`，SHA256 `0aa82bba41c2aef97d1dcb8950ad7392172e944b9b1b977a6285600e7b1c6550`。
- `services/api-gateway/internal/modelgovernance/managed_config_probe.go:386–404`，SHA256 `0aa82bba41c2aef97d1dcb8950ad7392172e944b9b1b977a6285600e7b1c6550`。
- `services/api-gateway/internal/modelgovernance/managed_config_probe.go:638–644`，SHA256 `0aa82bba41c2aef97d1dcb8950ad7392172e944b9b1b977a6285600e7b1c6550`。
- `services/api-gateway/internal/modelgovernance/managed_config_probe.go:739–791`，SHA256 `0aa82bba41c2aef97d1dcb8950ad7392172e944b9b1b977a6285600e7b1c6550`。

## CQ-GO-016：go/log-injection

固定快照位置：`services/api-gateway/internal/schoolassistant/service.go:160`。

传入日志的是独立 slog 属性 request_id；进入 preparedQuery 前 validateRequest 会 TrimSpace 并用 uuid.Parse 拒绝无效请求标识。攻击者的任意换行文字不能沿本告警路径进入此日志字段。不能从此推断所有其他日志字段也已审查。

建议：继续在 prepare 前校验请求标识，保留结构化属性输出。

已审读证据范围：
- `services/api-gateway/internal/schoolassistant/service.go:44–52`，SHA256 `b418da1dc91932e30d6ab957f4d331cd1c61defe3868e6a154ea74e0359f6d5b`。
- `services/api-gateway/internal/schoolassistant/service.go:152–161`，SHA256 `b418da1dc91932e30d6ab957f4d331cd1c61defe3868e6a154ea74e0359f6d5b`。
- `services/api-gateway/internal/schoolassistant/auth.go:55–61`，SHA256 `583620cdc5560548d67da63f100b971d365c15c564a1ff4ec578dab4b2869485`。
- `services/api-gateway/internal/schoolassistant/auth.go:85–95`，SHA256 `583620cdc5560548d67da63f100b971d365c15c564a1ff4ec578dab4b2869485`。

## CQ-GO-017：go/index-out-of-bounds

固定快照位置：`services/api-gateway/internal/capture/print_package_pdf.go:141`。

drawQRCode 故意让 columnIndex 到 len(row) 的哨兵位，以冲刷最后一段黑色模块。唯一 row[columnIndex] 读取位于 `columnIndex < len(row) && row[columnIndex]` 右侧，Go && 短路保证哨兵位不会执行读取；range 提供实际行，列索引从0递增。后续只用列差计算绘图矩形，无第二次 bitmap 索引；外层空 bitmap 也已提前返回。不需依赖 qrcode.Bitmap 保证方阵才能排除这条越界，空行同样不读取。

建议：保留末尾哨兵及读取前的严格小于 guard；后续可补说明用于冲刷末尾黑段。不能仅把 <= 改成 <，否则会漏画以黑色结束的 run。

已审读证据范围：
- `services/api-gateway/internal/capture/print_package_pdf.go:69–98`，SHA256 `7ae67b0ab0415e34a35066c28dce40fe7583feac2025afec4c828641efd8f67b`。
- `services/api-gateway/internal/capture/print_package_pdf.go:133–158`，SHA256 `7ae67b0ab0415e34a35066c28dce40fe7583feac2025afec4c828641efd8f67b`。

## CQ-GO-018：go/useless-assignment-to-field

固定快照位置：`services/api-gateway/internal/modelgovernance/store_memory.go:303`。

首次 UpdatePolicy 创建的 current.TenantID 在此分支内赋值后不读取；最终新 policy 独立设置 TenantID=tenantID，并保存到 s.policies[tenantID]。这处确为不必要的初值写入，不是内存策略丢失租户隔离的证据。current.ID/Version/DisplayName 则有后续用途，不能整体删掉初始化分支。

建议：后续仅移除无读用途的 current.TenantID 初始化，保留 policy.TenantID、版本校验和映射键。

已审读证据范围：
- `services/api-gateway/internal/modelgovernance/store_memory.go:280–323`，SHA256 `564c76c3bbc38d95a7dfacf9201b9477b1e12e5be088a8575ce8f3a19a4d0771`。

## CQ-GO-019：go/useless-assignment-to-field

固定快照位置：`services/api-gateway/internal/modelgovernance/store_memory.go:304`。

current.PolicyKey 的首次初始化未在后续被读取，新 policy.PolicyKey 直接设置 default；current 的整个结构没有在此分支写回 map。该告警对应冗余字段写入，不导致策略默认键缺失。

建议：后续可移除 current.PolicyKey 的冗余初值，保留最终 policy.PolicyKey。

已审读证据范围：
- `services/api-gateway/internal/modelgovernance/store_memory.go:299–323`，SHA256 `564c76c3bbc38d95a7dfacf9201b9477b1e12e5be088a8575ce8f3a19a4d0771`。

## CQ-GO-020：go/useless-assignment-to-field

固定快照位置：`services/api-gateway/internal/modelgovernance/store_memory.go:305`。

current.Status 的首次初始化没有读取路径，新 policy.Status 随后直接设 active 并保存。赋值本身无副作用，属于维护冗余；不据此认定状态验证或乐观锁失效。

建议：后续可移除该 current.Status 冗余赋值，保留最终 policy.Status 及既有政策校验。

已审读证据范围：
- `services/api-gateway/internal/modelgovernance/store_memory.go:280–323`，SHA256 `564c76c3bbc38d95a7dfacf9201b9477b1e12e5be088a8575ce8f3a19a4d0771`。

## CQ-GO-021：go/useless-assignment-to-local

固定快照位置：`services/api-gateway/internal/questionbank/metadata_acl_search_test.go:117`。

撤权 UpdateACL 的返回 doc 被重新赋给局部变量后不再使用；旧 doc.Revision 在调用实参中仍被读取。调用执行撤权副作用，后续实际查询验证撤权，err 也有检查。仅返回新值无用途，不是撤权调用无效。

建议：后续可将该调用的新 doc 返回值赋给 _；必须保留变更调用、ExpectedRevision 和错误/撤权验证。

已审读证据范围：
- `services/api-gateway/internal/questionbank/metadata_acl_search_test.go:95–133`，SHA256 `1a2ab6c46cb094289b3d5a6c06e8fe5365e1cd14682dd1cca5a71c46c159cc97`。

## CQ-GO-022：go/useless-assignment-to-local

固定快照位置：`services/api-gateway/internal/server/e2e_question_bank_acl_ux_test.go:190`。

最后一次 UpdateACL 返回的 updated 不再读取；调用前的旧 updated.Revision 用于乐观版本检查。后续数据库查询检查管理者 direct action 和被移除组 edit 权限，调用副作用必须执行。

建议：后续可把这个返回对象接到 _，保留 err 和数据库断言；不能删除整个调用。

已审读证据范围：
- `services/api-gateway/internal/server/e2e_question_bank_acl_ux_test.go:172–204`，SHA256 `4c3bf3ae247c679875a481fa1da5dadd913b54ce68b5911aa5c709bc2d478b09`。

## CQ-GO-023：go/useless-assignment-to-local

固定快照位置：`services/api-gateway/internal/server/e2e_question_bank_metadata_acl_search_test.go:154`。

再授权 UpdateACL 的 acl 返回值不在后续使用；旧 acl.Revision 已在同一调用实参消费。授权后必须执行 RetireItem，后面的审计记录和历史 schema 保护也仍验证行为。

建议：后续可只丢弃返回对象；必须保留再授权动作、ExpectedRevision、err 和后续权限/审计断言。

已审读证据范围：
- `services/api-gateway/internal/server/e2e_question_bank_metadata_acl_search_test.go:140–169`，SHA256 `da3d785b18e17d6100323aadd3e414eae851dac5b4868418aaac9ce857d98a9c`。
