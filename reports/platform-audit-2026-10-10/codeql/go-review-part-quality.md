# Go CodeQL 指定 14 条候选核验

核验时间：2026-10-11T13:31:58.652130+08:00。原始 [go.sarif](go.sarif) SHA-256：`8146976c6190873c2e5ca65046b469853d99bab5a489b0e0d6c6259a637b72de`。

本附录覆盖零基索引 0..5、7、16、17..22，共 14 条：7 条上下文误报、1 条有意的 TLS 低版本拒绝负例、6 条低优先级冗余赋值维护。其他 9 条由主智能体核验，须合并后才能称该 Go SARIF 的全部 23 条人工结论完整。

只依据 2026-10-11 固定本地副本；每个告警和支持调用链源哈希与扫描清单一致。读告警所在测试不是排除标准：仍实际检查 JSON 编码/嵌入位置、TLS 客户端限制、变更副作用、版本约束与错误断言。此次未运行这些测试或生产服务；对源码边界的解释不等于部署验证。

原始 result.level 缺省为 null 时保留 null，并按 SARIF rule 默认等级及 problem.severity 派生展示等级；不会把 `error` 自动当作安全漏洞。7 条安全标签保留原平台分数（6 个 unsafe-quoting 9.3、1 个 insecure-tls 7.5），人工解释另列。

drawQRCode 的越界候选有明确 `columnIndex < len(row) && row[columnIndex]` 短路保护，尾哨兵用于画出结束于黑色的最后一段。修改循环边界而删除尾哨兵会破坏绘图，不能照着告警直接修。

## CQ-GO-001 · go/unsafe-quoting

[services/api-gateway/internal/modelgovernance/managed_config_probe_http_test.go:395](<D:/tools/audit-codeql-20261010/source-20261011/services/api-gateway/internal/modelgovernance/managed_config_probe_http_test.go:395>)

原始 problem level `warning`，security severity `9.3`；人工 `false_positive_context` / `none`。

test.content 的 json.Marshal 返回完整且已转义的 JSON 字符串 token，模板在 content 冒号后直接拼入 token，没有额外包引号。finishReason 来自本例三个固定字段 stop/length，其他源码路径不是本条 source。此处有意测试非法/截断的内层模型回答；外层响应仍正确 JSON 编码。

建议：保留模型能力失败闭合的测试场景；可改整体外层结构编码以减少扫描歧义，不删除 json.Marshal 转义。

固定源 SHA-256：`9b70a95056ad98ba69e35f536fb2dd1c12231eb57844440410ee87932ce113c9`；所读支持范围与原值见 [go-review-part-quality.json](go-review-part-quality.json)。

## CQ-GO-002 · go/unsafe-quoting

[services/api-gateway/internal/paper/handlers_test.go:519](<D:/tools/audit-codeql-20261010/source-20261011/services/api-gateway/internal/paper/handlers_test.go:519>)

原始 problem level `warning`，security severity `9.3`；人工 `false_positive_context` / `none`。

告警数据是 float64 score 经 json.Marshal 产生的数值 JSON token，直接拼在 score/max_score 键的冒号后，变量两侧没有再包字符串引号。数字不能包含可突破引号的双引号；不是将带用户字符的 JSON 字符串再嵌进引号。测试 helper 忽略非有限数的 Marshal 错误会使该特殊夹具 JSON 无效，但不会形成本条所报的引号逃逸。

建议：本告警记录上下文误报；后续可用整体 struct/map 编码统一测试请求，并检查 Marshal 错误，避免手工模板维护。不得为此调整生产评分逻辑。

固定源 SHA-256：`2f5c8775029058b7a6e86b1b6a219fa80a29d0ee8ec9f6d7b7a0873ff15aca92`；所读支持范围与原值见 [go-review-part-quality.json](go-review-part-quality.json)。

## CQ-GO-003 · go/unsafe-quoting

[services/api-gateway/internal/paper/handlers_test.go:523](<D:/tools/audit-codeql-20261010/source-20261011/services/api-gateway/internal/paper/handlers_test.go:523>)

原始 problem level `warning`，security severity `9.3`；人工 `false_positive_context` / `none`。

告警数据是 float64 score 经 json.Marshal 产生的数值 JSON token，直接拼在 score/max_score 键的冒号后，变量两侧没有再包字符串引号。数字不能包含可突破引号的双引号；不是将带用户字符的 JSON 字符串再嵌进引号。测试 helper 忽略非有限数的 Marshal 错误会使该特殊夹具 JSON 无效，但不会形成本条所报的引号逃逸。

建议：本告警记录上下文误报；后续可用整体 struct/map 编码统一测试请求，并检查 Marshal 错误，避免手工模板维护。不得为此调整生产评分逻辑。

固定源 SHA-256：`2f5c8775029058b7a6e86b1b6a219fa80a29d0ee8ec9f6d7b7a0873ff15aca92`；所读支持范围与原值见 [go-review-part-quality.json](go-review-part-quality.json)。

## CQ-GO-004 · go/unsafe-quoting

[services/api-gateway/internal/paper/handlers_test.go:527](<D:/tools/audit-codeql-20261010/source-20261011/services/api-gateway/internal/paper/handlers_test.go:527>)

原始 problem level `warning`，security severity `9.3`；人工 `false_positive_context` / `none`。

告警数据是 float64 score 经 json.Marshal 产生的数值 JSON token，直接拼在 score/max_score 键的冒号后，变量两侧没有再包字符串引号。数字不能包含可突破引号的双引号；不是将带用户字符的 JSON 字符串再嵌进引号。测试 helper 忽略非有限数的 Marshal 错误会使该特殊夹具 JSON 无效，但不会形成本条所报的引号逃逸。

建议：本告警记录上下文误报；后续可用整体 struct/map 编码统一测试请求，并检查 Marshal 错误，避免手工模板维护。不得为此调整生产评分逻辑。

固定源 SHA-256：`2f5c8775029058b7a6e86b1b6a219fa80a29d0ee8ec9f6d7b7a0873ff15aca92`；所读支持范围与原值见 [go-review-part-quality.json](go-review-part-quality.json)。

## CQ-GO-005 · go/unsafe-quoting

[services/api-gateway/internal/paper/handlers_test.go:527](<D:/tools/audit-codeql-20261010/source-20261011/services/api-gateway/internal/paper/handlers_test.go:527>)

原始 problem level `warning`，security severity `9.3`；人工 `false_positive_context` / `none`。

告警数据是 float64 score 经 json.Marshal 产生的数值 JSON token，直接拼在 score/max_score 键的冒号后，变量两侧没有再包字符串引号。数字不能包含可突破引号的双引号；不是将带用户字符的 JSON 字符串再嵌进引号。测试 helper 忽略非有限数的 Marshal 错误会使该特殊夹具 JSON 无效，但不会形成本条所报的引号逃逸。

建议：本告警记录上下文误报；后续可用整体 struct/map 编码统一测试请求，并检查 Marshal 错误，避免手工模板维护。不得为此调整生产评分逻辑。

固定源 SHA-256：`2f5c8775029058b7a6e86b1b6a219fa80a29d0ee8ec9f6d7b7a0873ff15aca92`；所读支持范围与原值见 [go-review-part-quality.json](go-review-part-quality.json)。

## CQ-GO-006 · go/unsafe-quoting

[services/api-gateway/internal/server/e2e_question_bank_import_test.go:214](<D:/tools/audit-codeql-20261010/source-20261011/services/api-gateway/internal/server/e2e_question_bank_import_test.go:214>)

原始 problem level `warning`，security severity `9.3`；人工 `false_positive_context` / `none`。

qbJSON 检查 json.Marshal 错误后返回完整 JSON token。该告警参数 v.DefaultScore 在类型中为 float64，模板把结果直接放在 total_score 冒号后，不是放进双引号字符串。因此不是引号逃逸；同一模板其他 fixture ID 拼接未包含在本条数据流判断内，不能以本条误报推断整个模板适用于任意外部输入。

建议：该候选记录为上下文误报。测试模板可在后续统一为整体 JSON 编码以提高维护性。

固定源 SHA-256：`d5f7756150d5784e28eba49db0e51b5ad59e17b9691cc5af3b4e2262cd7c689e`；所读支持范围与原值见 [go-review-part-quality.json](go-review-part-quality.json)。

## CQ-GO-008 · go/insecure-tls

[services/api-gateway/internal/minioadapter/client_test.go:72](<D:/tools/audit-codeql-20261010/source-20261011/services/api-gateway/internal/minioadapter/client_test.go:72>)

原始 problem level `warning`，security severity `7.5`；人工 `intentional_test_behavior` / `none`。

TestNewClientRejectsTLSBeforeVersion12 有意构造最大 TLS1.1 的 httptest 服务，断言真实 NewClient 客户端在发送 HTTP 前拒绝该服务；生产 MinIO NewClient 的 cfg.UseSSL 分支设置 TLSClientConfig.MinVersion = TLS1.2。不是业务客户端允许 TLS1.1，也不是只按 _test.go 文件名排除。

建议：保留低协议版本拒绝测试，针对本条记录负例用途。实际运行部署和用例执行结果未在此次人工核验中验证。

固定源 SHA-256：`a94ea4453cbee7d344976a6a4927ab14e6eb9a038c9a41f268073203b715aa2f`；所读支持范围与原值见 [go-review-part-quality.json](go-review-part-quality.json)。

## CQ-GO-017 · go/index-out-of-bounds

[services/api-gateway/internal/capture/print_package_pdf.go:141](<D:/tools/audit-codeql-20261010/source-20261011/services/api-gateway/internal/capture/print_package_pdf.go:141>)

原始 problem level `error`，security severity `无`；人工 `false_positive_context` / `none`。

drawQRCode 故意让 columnIndex 到 len(row) 的哨兵位，以冲刷最后一段黑色模块。唯一 row[columnIndex] 读取位于 `columnIndex < len(row) && row[columnIndex]` 右侧，Go && 短路保证哨兵位不会执行读取；range 提供实际行，列索引从0递增。后续只用列差计算绘图矩形，无第二次 bitmap 索引；外层空 bitmap 也已提前返回。不需依赖 qrcode.Bitmap 保证方阵才能排除这条越界，空行同样不读取。

建议：保留末尾哨兵及读取前的严格小于 guard；后续可补说明用于冲刷末尾黑段。不能仅把 <= 改成 <，否则会漏画以黑色结束的 run。

固定源 SHA-256：`7ae67b0ab0415e34a35066c28dce40fe7583feac2025afec4c828641efd8f67b`；所读支持范围与原值见 [go-review-part-quality.json](go-review-part-quality.json)。

## CQ-GO-018 · go/useless-assignment-to-field

[services/api-gateway/internal/modelgovernance/store_memory.go:303](<D:/tools/audit-codeql-20261010/source-20261011/services/api-gateway/internal/modelgovernance/store_memory.go:303>)

原始 problem level `warning`，security severity `无`；人工 `low_maintenance` / `low`。

首次 UpdatePolicy 创建的 current.TenantID 在此分支内赋值后不读取；最终新 policy 独立设置 TenantID=tenantID，并保存到 s.policies[tenantID]。这处确为不必要的初值写入，不是内存策略丢失租户隔离的证据。current.ID/Version/DisplayName 则有后续用途，不能整体删掉初始化分支。

建议：后续仅移除无读用途的 current.TenantID 初始化，保留 policy.TenantID、版本校验和映射键。

固定源 SHA-256：`564c76c3bbc38d95a7dfacf9201b9477b1e12e5be088a8575ce8f3a19a4d0771`；所读支持范围与原值见 [go-review-part-quality.json](go-review-part-quality.json)。

## CQ-GO-019 · go/useless-assignment-to-field

[services/api-gateway/internal/modelgovernance/store_memory.go:304](<D:/tools/audit-codeql-20261010/source-20261011/services/api-gateway/internal/modelgovernance/store_memory.go:304>)

原始 problem level `warning`，security severity `无`；人工 `low_maintenance` / `low`。

current.PolicyKey 的首次初始化未在后续被读取，新 policy.PolicyKey 直接设置 default；current 的整个结构没有在此分支写回 map。该告警对应冗余字段写入，不导致策略默认键缺失。

建议：后续可移除 current.PolicyKey 的冗余初值，保留最终 policy.PolicyKey。

固定源 SHA-256：`564c76c3bbc38d95a7dfacf9201b9477b1e12e5be088a8575ce8f3a19a4d0771`；所读支持范围与原值见 [go-review-part-quality.json](go-review-part-quality.json)。

## CQ-GO-020 · go/useless-assignment-to-field

[services/api-gateway/internal/modelgovernance/store_memory.go:305](<D:/tools/audit-codeql-20261010/source-20261011/services/api-gateway/internal/modelgovernance/store_memory.go:305>)

原始 problem level `warning`，security severity `无`；人工 `low_maintenance` / `low`。

current.Status 的首次初始化没有读取路径，新 policy.Status 随后直接设 active 并保存。赋值本身无副作用，属于维护冗余；不据此认定状态验证或乐观锁失效。

建议：后续可移除该 current.Status 冗余赋值，保留最终 policy.Status 及既有政策校验。

固定源 SHA-256：`564c76c3bbc38d95a7dfacf9201b9477b1e12e5be088a8575ce8f3a19a4d0771`；所读支持范围与原值见 [go-review-part-quality.json](go-review-part-quality.json)。

## CQ-GO-021 · go/useless-assignment-to-local

[services/api-gateway/internal/questionbank/metadata_acl_search_test.go:117](<D:/tools/audit-codeql-20261010/source-20261011/services/api-gateway/internal/questionbank/metadata_acl_search_test.go:117>)

原始 problem level `warning`，security severity `无`；人工 `low_maintenance` / `low`。

撤权 UpdateACL 的返回 doc 被重新赋给局部变量后不再使用；旧 doc.Revision 在调用实参中仍被读取。调用执行撤权副作用，后续实际查询验证撤权，err 也有检查。仅返回新值无用途，不是撤权调用无效。

建议：后续可将该调用的新 doc 返回值赋给 _；必须保留变更调用、ExpectedRevision 和错误/撤权验证。

固定源 SHA-256：`1a2ab6c46cb094289b3d5a6c06e8fe5365e1cd14682dd1cca5a71c46c159cc97`；所读支持范围与原值见 [go-review-part-quality.json](go-review-part-quality.json)。

## CQ-GO-022 · go/useless-assignment-to-local

[services/api-gateway/internal/server/e2e_question_bank_acl_ux_test.go:190](<D:/tools/audit-codeql-20261010/source-20261011/services/api-gateway/internal/server/e2e_question_bank_acl_ux_test.go:190>)

原始 problem level `warning`，security severity `无`；人工 `low_maintenance` / `low`。

最后一次 UpdateACL 返回的 updated 不再读取；调用前的旧 updated.Revision 用于乐观版本检查。后续数据库查询检查管理者 direct action 和被移除组 edit 权限，调用副作用必须执行。

建议：后续可把这个返回对象接到 _，保留 err 和数据库断言；不能删除整个调用。

固定源 SHA-256：`4c3bf3ae247c679875a481fa1da5dadd913b54ce68b5911aa5c709bc2d478b09`；所读支持范围与原值见 [go-review-part-quality.json](go-review-part-quality.json)。

## CQ-GO-023 · go/useless-assignment-to-local

[services/api-gateway/internal/server/e2e_question_bank_metadata_acl_search_test.go:154](<D:/tools/audit-codeql-20261010/source-20261011/services/api-gateway/internal/server/e2e_question_bank_metadata_acl_search_test.go:154>)

原始 problem level `warning`，security severity `无`；人工 `low_maintenance` / `low`。

再授权 UpdateACL 的 acl 返回值不在后续使用；旧 acl.Revision 已在同一调用实参消费。授权后必须执行 RetireItem，后面的审计记录和历史 schema 保护也仍验证行为。

建议：后续可只丢弃返回对象；必须保留再授权动作、ExpectedRevision、err 和后续权限/审计断言。

固定源 SHA-256：`da3d785b18e17d6100323aadd3e414eae851dac5b4868418aaac9ce857d98a9c`；所读支持范围与原值见 [go-review-part-quality.json](go-review-part-quality.json)。
