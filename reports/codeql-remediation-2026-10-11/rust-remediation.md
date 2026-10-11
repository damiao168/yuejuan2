# Rust 候选处理

当前工作区14条候选已逐条复核：3条上下文误报、11条有意测试行为。生产路径在加密或保存密钥前调用系统随机源；固定测试密钥都处于实际 `#[cfg(test)]` 模块。保持已有密文而凭据丢失时拒绝生成新密钥的恢复边界。

| 原始索引 | 文件 | 处理状态 | 依据 |
| ---: | --- | --- | --- |
| 0 | `apps/desktop-client/src-tauri/src/durable_store/paths.rs` | `false_positive` | paths.rs:105 的 true 是 durable_data_exists 返回的布尔存在标志，crypto.rs:168 只把它用作已有密文时拒绝生成新密钥的分支条件；该分支 return Err。实际返回密钥来自凭据管理器已保存的32字节值，或 OsRng 填满的新32字节数组。原始 codeFlow 的 bool guard -> master_key 返回值关联是保守流摘要，不存在 bool 转为 AES key 的实现。 |
| 1 | `apps/desktop-client/src-tauri/src/durable_store/claim.rs` | `intentional` | 固定正确 key [7_u8;32] 用于新 UUID 临时 legacy SQLite 库里的合成 JSON 密文，整个测试模块被 cfg(test) 包围。 已读实际 #[cfg(test)] mod tests 的起止边界；正常非 test 构建排除整个模块。测试调用生产 crypto/storage helper 不会把测试常量变成生产入口。 |
| 2 | `apps/desktop-client/src-tauri/src/durable_store/claim.rs` | `intentional` | 同一测试用正确固定 key [7_u8;32] 验证前面生成的合成 legacy 密文成功，整个调用在 cfg(test) 模块。 已读实际 #[cfg(test)] mod tests 的起止边界；正常非 test 构建排除整个模块。测试调用生产 crypto/storage helper 不会把测试常量变成生产入口。 |
| 3 | `apps/desktop-client/src-tauri/src/durable_store/claim.rs` | `intentional` | 错误 key [8_u8;32] 是解密失败和文件保留断言的负例，不是生产加密密钥。 已读实际 #[cfg(test)] mod tests 的起止边界；正常非 test 构建排除整个模块。测试调用生产 crypto/storage helper 不会把测试常量变成生产入口。 |
| 4 | `apps/desktop-client/src-tauri/src/durable_store/crypto.rs` | `false_positive` | crypto.rs:127 的全零12字节数组只用于分配 nonce 缓冲区。第128行立即 OsRng.fill_bytes(&mut nonce)，第130行后才送入 AES-GCM；锁定 rand_core 0.6.4 的 OsRng.fill_bytes 在随机源失败时 panic，不存在继续使用全零缓冲区的回退路径。未执行系统随机源故障注入或完整Rust测试。 |
| 5 | `apps/desktop-client/src-tauri/src/durable_store/crypto.rs` | `false_positive` | crypto.rs:171 的全零32字节数组只用于分配新密钥缓冲区；第172行 OsRng.fill_bytes 后才写入 Windows Credential Manager 并返回。已有本地密文而密钥丢失时前一分支返回错误，不会新建替代密钥。凭据管理器读取成功的另一分支使用已保存值；随机源失败时 fill_bytes panic，不会返回初始化全零值。 |
| 6 | `apps/desktop-client/src-tauri/src/durable_store/drafts.rs` | `intentional` | KEY=[37;32] 是 cfg(test) 模块私有夹具常量，insert helper 只在本模块测试中加密内存 SQLite 的合成草稿。 已读实际 #[cfg(test)] mod tests 的起止边界；正常非 test 构建排除整个模块。测试调用生产 crypto/storage helper 不会把测试常量变成生产入口。 |
| 7 | `apps/desktop-client/src-tauri/src/durable_store/drafts.rs` | `intentional` | 错误 key [88;32] 有意触发清理失败，断言两条过期草稿都保留；调用只在 cfg(test) 测试中。 已读实际 #[cfg(test)] mod tests 的起止边界；正常非 test 构建排除整个模块。测试调用生产 crypto/storage helper 不会把测试常量变成生产入口。 |
| 8 | `apps/desktop-client/src-tauri/src/durable_store/mod.rs` | `intentional` | 固定 key [7_u8;32] 只为合成草稿的 AES-GCM 回环测试提供可重现密钥；caller 是 cfg(test) 模块中的 test。 已读实际 #[cfg(test)] mod tests 的起止边界；正常非 test 构建排除整个模块。测试调用生产 crypto/storage helper 不会把测试常量变成生产入口。 |
| 9 | `apps/desktop-client/src-tauri/src/durable_store/mod.rs` | `intentional` | 错误 key [8_u8;32] 有意验证同一合成密文用错误密钥解密失败；caller 是 cfg(test) 测试。 已读实际 #[cfg(test)] mod tests 的起止边界；正常非 test 构建排除整个模块。测试调用生产 crypto/storage helper 不会把测试常量变成生产入口。 |
| 10 | `apps/desktop-client/src-tauri/src/durable_store/mod.rs` | `intentional` | 固定 key [17_u8;32] 只用于 UUID 临时目录/内存 SQLite 的分块暂存、顺序与内容校验测试。 已读实际 #[cfg(test)] mod tests 的起止边界；正常非 test 构建排除整个模块。测试调用生产 crypto/storage helper 不会把测试常量变成生产入口。 |
| 11 | `apps/desktop-client/src-tauri/src/durable_store/mod.rs` | `intentional` | 固定 key [29_u8;32] 只用于内存 SQLite 中500页合成暂存与第173页中断恢复场景。 已读实际 #[cfg(test)] mod tests 的起止边界；正常非 test 构建排除整个模块。测试调用生产 crypto/storage helper 不会把测试常量变成生产入口。 |
| 12 | `apps/desktop-client/src-tauri/src/durable_store/mod.rs` | `intentional` | 固定 key [43_u8;32] 只用于 UUID 临时 A/B 账号与独立 SQLite 的隔离断言，不是生产账号密钥配置。 已读实际 #[cfg(test)] mod tests 的起止边界；正常非 test 构建排除整个模块。测试调用生产 crypto/storage helper 不会把测试常量变成生产入口。 |
| 13 | `apps/desktop-client/src-tauri/src/durable_store/storage.rs` | `intentional` | [61_u8;32] 是 cfg(test) 私有 fixture 返回的合成数据密钥，只由同测试模块调用。 已读实际 #[cfg(test)] mod tests 的起止边界；正常非 test 构建排除整个模块。测试调用生产 crypto/storage helper 不会把测试常量变成生产入口。 |

原报告提及的 `queue.rs` / `spool.rs` CRLF 已在本轮开始前处理：当前字节核验分别为0个CRLF，`.gitattributes`已有对应LF约束；真实ICO图标也存在。本轮保留这些已有改动。原提取器警告属于既有扫描证据，未重扫不能声称警告已清零。

测试结果记录见 `rust-test-result.json` 与 `rust-tests.log`。未执行随机源故障注入，不将候选结论扩展成全Rust安全保证。

本轮实际 Rust 测试：31 passed、0 failed、0 ignored；离线锁定依赖、单构建任务、单测试线程。首次因页面文件不足失败的日志保留为 `rust-tests-attempt1.log`，相同源码与配置重试成功。
