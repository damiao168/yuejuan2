# Rust 提取器资源控制研究

2026-10-11 只读检查本地 CodeQL 2.27.2 官方工具，并阅读公开官方文档；没有重建数据库、运行查询、改变系统设置或访问 yuejuan2 远端。此附件给出恢复方案，不是 Rust 扫描结果。

## 已核实的控制边界

本地 `codeql resolve extractor --language=rust --format=betterjson` 的实际输出见 `rust-extractor-options.json`，`extractor.exe --help` 见 `rust-extractor-help.log`，两次只读命令退出码均为 0。`codeql-extractor.yml` 和 help 都没有 Rust 的 `threads`、`ram`、`jobs` 选项。`index-files.cmd` 仅将参数文件传给原生 `extractor`。正式的选项清单应通过 `resolve extractor ... --format=betterjson` 查询；未声明选项不能当作受支持的 CodeQL extractor option。[GitHub 官方选项说明](https://docs.github.com/en/code-security/reference/code-scanning/codeql/codeql-cli/extractor-options)

CodeQL 的 `--threads` 与 `--ram` 在提取阶段是传给提取器的资源建议，不能据此承诺原生进程的硬限制；`CODEQL_THREADS`、`CODEQL_RAM` 环境变量可优先于对应参数。[GitHub 官方 trace-command 说明](https://docs.github.com/en/code-security/reference/code-scanning/codeql/codeql-cli-manual/database-trace-command)

本地 `extractor.exe` 含 Rayon 1.13.0 路径及 `RAYON_NUM_THREADS`、`RAYON_RS_NUM_CPUS` 字符串，没有找到 `CODEQL_THREADS`、`CODEQL_RAM` 字符串。计数、二进制及脚本 SHA-256 见 `rust-resource-control-research.json`。字串存在支持进行线程控制实验，但不能证明所有线程池都受其控制；字串缺失也不能证明完全没有间接控制。

Rayon 官方支持 `RAYON_NUM_THREADS`；未显式设置线程数的池会使用该环境值，否则按逻辑 CPU 数量选择。通过 `ThreadPoolBuilder::num_threads` 显式设置的池可覆盖环境变量；该变量不能限制其他线程或原生内存。[Rayon 官方 API](https://docs.rs/rayon-core/latest/rayon_core/struct.ThreadPoolBuilder.html#method.num_threads)

`CARGO_BUILD_JOBS=1` 是 Cargo 正式支持的并发配置，限制 Cargo 并行编译进程；不能把它等同于 Rust CodeQL TRAP 写出阶段的并发控制。[Cargo 官方环境变量](https://doc.rust-lang.org/cargo/reference/environment-variables.html)

## 保持原范围的恢复方案

1. 保留失败数据库与日志，在审计工具目录选择全新数据库路径。继续使用原冻结源码、原 16 个 Rust 文件、原构建上下文和原 41 条 security-and-quality 查询；不改 Cargo features、cfg、平台、源文件排除或查询集。
2. 仅在此次子进程环境增加 `RAYON_NUM_THREADS=1`、`CARGO_BUILD_JOBS=1`，继续保持 `--threads=1`、现有内存参数与 Cargo 离线设置。这是有依据的运行时恢复实验，尚没有找到 CodeQL 官方文档承诺 Rust 提取器所有内部并发都由 Rayon 环境变量控制。
3. 在提取过程中实际记录 extractor 的线程数、工作集/私有字节峰值和系统可用提交量。应判断是否确实降低并发与内存，而不是仅凭设置值宣称生效。若仍发生内存分配失败，需要更充足的本地资源；不通过删除标准库模型或降低扫描范围制造成功。
4. 建库 exit 0 后，核实源文件归档中原 16 个 Rust 文件全部存在且 SHA-256 匹配，再执行原 41 条查询。未成功创建/完成查询的中间数据库不得标为扫描完成或零漏洞。

本地 schema 正式支持 `cargo_target_dir`，可指向审计工具目录的专用持久缓存，以复用重复提取时的 Cargo 产物；它可能减少重复编译时间，并不提供内存硬上限。`extract_dependencies_as_source=false` 只提取依赖签名，是避免扩大到依赖完整源码的选项，不意味着标准库类型或依赖模型完全不加载。保持当前设置即可，不能把它解释为移除标准库扫描的资源开关。

`force-library-mode`、`cargo-extra-args`、`cargo-extra-env` 可在原生 help 中看到，但当前 CodeQL Rust options schema 未声明它们，因此不作为本方案的正式数据库参数。缩减 `cargo_features` 或 `cargo_cfg_overrides` 虽是正式选项，也会改变被分析的语义范围，本方案不用它们。

## compile-time-deps 与本次故障的区别

Rust CodeQL 正式支持 build mode `none`；它仍通过 rust-analyzer 处理构建脚本与过程宏，而不是完全不触发编译器。[GitHub 官方 Rust 构建说明](https://docs.github.com/en/code-security/reference/code-scanning/codeql/build-options-for-compiled-languages#building-rust)

Cargo 的 `--compile-time-deps` 是给 rust-analyzer 等工具使用的永久不稳定功能，处理过程宏、构建脚本及其依赖。它不是控制 TRAP 写出并发或内存的参数；不要为了提取器在标准库 TRAP 写出阶段的内存分配失败而额外修改业务构建命令、全局 Cargo 配置或切换全局工具链。[Cargo 官方不稳定功能说明](https://doc.rust-lang.org/cargo/reference/unstable.html#compile-time-deps)

单独尝试官方 `autobuild.exe --help` 时，它没有提供 help 界面，而是在缺少 `CODEQL_EXTRACTOR_RUST_WIP_DATABASE` 的环境下立即返回失败。这是只读探索命令的行为，不能记为第三轮业务建库失败；没有为它设置工作库或让它写入任何扫描数据库。
