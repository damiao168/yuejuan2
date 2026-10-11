# CodeQL Rust 逐条候选核验

核验时间：2026-10-11T15:13:20.179243+08:00（Asia/Shanghai）。这份附录绑定 `rust.sarif` SHA-256 `69b53cea47bc6506c0a889325414b1f35a1ded9f492c61ba4e640a91a1766062`，来自首轮实际成功的41条官方查询；原始14条全部属于 `rust/hard-coded-cryptographic-value`，官方 security-severity 9.8 / Critical，problem level 为 warning，原字段保留。

**人工核验：3条上下文误报、11条有意测试行为，0条候选待分配核验；本次未确认这14条构成生产硬编码密钥/nonce漏洞。** 这不表示全Rust安全审查完成。14条告警的字面量真实存在；其中测试常量由实际 cfg(test) 边界确认，仅三条初始化/布尔流摘要列作上下文误报。

核验只读固定扫描副本，没有执行Rust测试、随机源故障注入或修改业务代码。全部主告警文件和项目支持源码SHA与维护清单匹配；锁定依赖和测试模块边界见 [根核验支持](rust-root-finding-review-part.json)、[11项测试边界支持](rust-review-test-scope.json)。

| 编号（原索引） | 位置 | 官方等级 | 人工结论 | 依据 |
| --- | --- | --- | --- | --- |
| CQ-RUST-001（0） | `apps/desktop-client/src-tauri/src/durable_store/paths.rs:105` | Critical 9.8 / warning | 上下文误报 | paths.rs:105 的 true 是 durable_data_exists 返回的布尔存在标志，crypto.rs:168 只把它用作已有密文时拒绝生成新密钥的分支条件；该分支 return Err。实际返回密钥来自凭据管理器已保存的32字节值，或 OsRng 填满的新32字节数组。原始 codeFlow 的 bool guard -> master_key 返回值关联是保守流摘要，不存在 bool 转为 AES key 的实现。 |
| CQ-RUST-002（1） | `apps/desktop-client/src-tauri/src/durable_store/claim.rs:443` | Critical 9.8 / warning | 有意测试行为 | 固定正确 key [7_u8;32] 用于新 UUID 临时 legacy SQLite 库里的合成 JSON 密文，整个测试模块被 cfg(test) 包围。 |
| CQ-RUST-003（2） | `apps/desktop-client/src-tauri/src/durable_store/claim.rs:450` | Critical 9.8 / warning | 有意测试行为 | 同一测试用正确固定 key [7_u8;32] 验证前面生成的合成 legacy 密文成功，整个调用在 cfg(test) 模块。 |
| CQ-RUST-004（3） | `apps/desktop-client/src-tauri/src/durable_store/claim.rs:451` | Critical 9.8 / warning | 有意测试行为 | 错误 key [8_u8;32] 是解密失败和文件保留断言的负例，不是生产加密密钥。 |
| CQ-RUST-005（4） | `apps/desktop-client/src-tauri/src/durable_store/crypto.rs:127` | Critical 9.8 / warning | 上下文误报 | crypto.rs:127 的全零12字节数组只用于分配 nonce 缓冲区。第128行立即 OsRng.fill_bytes(&mut nonce)，第130行后才送入 AES-GCM；锁定 rand_core 0.6.4 的 OsRng.fill_bytes 在随机源失败时 panic，不存在继续使用全零缓冲区的回退路径。未执行系统随机源故障注入或完整Rust测试。 |
| CQ-RUST-006（5） | `apps/desktop-client/src-tauri/src/durable_store/crypto.rs:171` | Critical 9.8 / warning | 上下文误报 | crypto.rs:171 的全零32字节数组只用于分配新密钥缓冲区；第172行 OsRng.fill_bytes 后才写入 Windows Credential Manager 并返回。已有本地密文而密钥丢失时前一分支返回错误，不会新建替代密钥。凭据管理器读取成功的另一分支使用已保存值；随机源失败时 fill_bytes panic，不会返回初始化全零值。 |
| CQ-RUST-007（6） | `apps/desktop-client/src-tauri/src/durable_store/drafts.rs:203` | Critical 9.8 / warning | 有意测试行为 | KEY=[37;32] 是 cfg(test) 模块私有夹具常量，insert helper 只在本模块测试中加密内存 SQLite 的合成草稿。 |
| CQ-RUST-008（7） | `apps/desktop-client/src-tauri/src/durable_store/drafts.rs:286` | Critical 9.8 / warning | 有意测试行为 | 错误 key [88;32] 有意触发清理失败，断言两条过期草稿都保留；调用只在 cfg(test) 测试中。 |
| CQ-RUST-009（8） | `apps/desktop-client/src-tauri/src/durable_store/mod.rs:111` | Critical 9.8 / warning | 有意测试行为 | 固定 key [7_u8;32] 只为合成草稿的 AES-GCM 回环测试提供可重现密钥；caller 是 cfg(test) 模块中的 test。 |
| CQ-RUST-010（9） | `apps/desktop-client/src-tauri/src/durable_store/mod.rs:117` | Critical 9.8 / warning | 有意测试行为 | 错误 key [8_u8;32] 有意验证同一合成密文用错误密钥解密失败；caller 是 cfg(test) 测试。 |
| CQ-RUST-011（10） | `apps/desktop-client/src-tauri/src/durable_store/mod.rs:135` | Critical 9.8 / warning | 有意测试行为 | 固定 key [17_u8;32] 只用于 UUID 临时目录/内存 SQLite 的分块暂存、顺序与内容校验测试。 |
| CQ-RUST-012（11） | `apps/desktop-client/src-tauri/src/durable_store/mod.rs:261` | Critical 9.8 / warning | 有意测试行为 | 固定 key [29_u8;32] 只用于内存 SQLite 中500页合成暂存与第173页中断恢复场景。 |
| CQ-RUST-013（12） | `apps/desktop-client/src-tauri/src/durable_store/mod.rs:466` | Critical 9.8 / warning | 有意测试行为 | 固定 key [43_u8;32] 只用于 UUID 临时 A/B 账号与独立 SQLite 的隔离断言，不是生产账号密钥配置。 |
| CQ-RUST-014（13） | `apps/desktop-client/src-tauri/src/durable_store/storage.rs:308` | Critical 9.8 / warning | 有意测试行为 | [61_u8;32] 是 cfg(test) 私有 fixture 返回的合成数据密钥，只由同测试模块调用。 |

## 三条生产上下文的核验边界

paths.rs:105 返回的 true 是本地数据存在标志，crypto.rs:168 仅据此拒绝在已有密文时新建密钥并返回错误；它没有转为 AES key。原 SARIF codeFlow 将布尔 guard 摘要关联到 master_key 返回值，此处没有对应实现的数据字节流。

crypto.rs:127、171 的全零数组分别初始化12字节nonce、32字节新key缓冲区；OsRng.fill_bytes 位于加密/凭据写入/返回之前。已核锁定 rand_core 0.6.4 的实际实现：随机源报错时 panic，没有继续使用初始全零值的回退。它不能证明随机nonce永不碰撞，也不是随机源故障注入实验。已有密钥通过Windows Credential Manager读取，已有密文而密钥缺失时拒绝替换，保留恢复所需的失败边界。

## 测试上下文与抽取限制

其余11条的来源都处在实际 #[cfg(test)] mod tests 词法范围内：claim.rs:313–458、drafts.rs:198–289、mod.rs:84–541、storage.rs:297–438。正例固定key用于合成/临时/内存SQLite测试，错误key用于解密失败与数据保留断言；私有fixture/KEY也仅在相应测试模块内。正常非test构建排除整个模块，不能将这些字面量数量换算成11个生产密钥泄露。

本数据库官方度量为13个Rust文件without error、3个with errors；有30条warning，CRLF多行SQL字面量位置告警和generate_context宏未成功展开仍需在主执行报告保留。候选上下文结论不消除这些语义覆盖限制，不保证随机nonce永不碰撞。

全部14项的原始位置、关联位置、源SHA、逐条证据和建议保存在 [机器可读核验](rust-finding-review.json)。未审读的全文件部分仍保持pending。若后续改善构建上下文并复扫，先保存原SARIF和本核验，再对新结果逐项对齐；不得将本哈希绑定的结论冒充新的扫描证据。
