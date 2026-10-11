# CodeQL 本地扫描报告

本目录记录 GitHub 官方 CodeQL CLI 的真实本地执行。用户已确认具有适用的 GitHub Code Security / GHAS 授权；未访问项目远端仓库，未上传源码、CodeQL 数据库或 SARIF。报告目录沿用任务启动日期 `2026-10-10`，恢复扫描实际发生于北京时间 `2026-10-11`。

## 当前执行状态

以 `summary.json` 的每语言 `status` 为准。`pending_analysis` 表示只完成建库，不能解释成“零漏洞”；`database_failed` / `analysis_failed` 表示未得到该语言的完整分析结论。SARIF 只会在官方分析命令成功后列为有效结果。

| 语言 | 状态 | 实际查询数 | 原始候选 | 安全候选 | 查询耗时 |
| --- | --- | ---: | ---: | --- | ---: |
| Python | 已完成，退出码 0 | 174 | 55 | High 1；其余质量 54 | 102.06 秒 |
| Actions YAML | 已完成，退出码 0 | 28 | 0 | 本次未返回候选 | 35.69 秒 |
| JavaScript / TypeScript | 已完成，退出码 0 | 203 | 14 | High 4；其余质量 10 | 960.00 秒 |
| Go | 已完成，退出码 0；23 条候选均已核验 | 59 | 23 | Critical 7 / High 8 / Medium 1；其余质量 7 | 437.77 秒 |
| Rust | 真实资产上下文建库和完整查询退出码均为 0；14 条均已核验 | 41 | 14 | 自动 Critical 14；人工 3 条上下文误报、11 条有意测试行为 | 2780.03 秒 |

Python 诊断记录为 `182/182` Python 文件已扫描；Actions 为 `3/3` 工作流文件已扫描。Python 的 54 条质量候选已人工核验为 39 条上下文误报、7 条有意实现／兼容行为、8 条低影响维护项，详情见 [质量核验附录](python-quality-review.md)。安全候选已验证有条件的正则 CPU 放大风险，见 [安全核验附件](security-review.md)。自动等级、人工结论与当前工作区后续改动分别保留，不将候选数量称为已确认漏洞数量。

JavaScript / TypeScript 的 `960/960` 文件扫描诊断、退出码 `0` 与 SARIF 成功状态已核验，首次内存不足失败不计入有效结果。14 条候选均已逐条人工核验：4 条规则 High 7.7 为开发／CI 生成路径的文件检查与访问间竞争，利用需额外的本地共享写权限等条件；未确认线上漏洞。其余 10 条质量候选为 5 条低影响维护项、3 条有意行为、2 条上下文误报，详情见 [JavaScript 核验附录](javascript-finding-review.md)。

Go 官方数据库归档与扫描诊断均覆盖原清单 `1026/1026` 个 Go 文件，缺失 `0`，没有外部依赖 `.go` 源码计入本仓清单。建库于 `13:21:12` 成功，完整查询于 `13:28:30` 成功。`23` 条候选均已核验为 14 条上下文误报、1 条有意 TLS 负例、6 条低影响维护项、1 条资源预算加固、1 条条件性部署检查；没有确认可利用的 Go 生产漏洞。数学验证容量告警没有已证明的整数溢出路径，但已有记录缺独立数量上限；模型探测已有 HTTPS、DNS 地址检查、数字 IP 拨号与禁重定向保护，特殊用途地址和部署路由仍需核对。详情见 [Go 核验附录](go-finding-review.md)。提取过程包含测试文件类型分析，没有执行测试或服务；覆盖证据见 `extraction-coverage.json`。Go 后逐项复验 `2502` 维护源码与 `12` 构建上下文文件的 SHA256 均未变，见 `source-after-scan-verification.json`。

Rust 原固定快照第三轮数据库于 `14:21:53` 创建成功，完整 `41` 条查询于 `15:03:24` 退出 `0`；`16/16` 个维护 Rust 文件已扫描。原始 `14` 条候选均为 `rust/hard-coded-cryptographic-value`，平台自动等级为 Critical 9.8；逐条核验为 `3` 条上下文误报和 `11` 条有意测试行为，未确认生产环境硬编码密码学密钥漏洞。该轮 SARIF、执行日志与绑定原始 SHA 的核验完整保留为 `rust-attempt3.sarif`、`rust-*-attempt3*`、`rust-attempt3-finding-review.*`。当时本轮五语言合计 `505` 条查询全部真实完成、`106` 条原始候选全部核验。

随后串行离线、锁定依赖的 Cargo 检查取得明确失败诊断：缺少真实的 `icons/icon.ico`，Tauri Windows 资源生成因此失败。已建立独立 `source-rust-context-20261011` 副本，保持原 `2502 + 12` 个文件字节不变，仅补入当前本地真实图标，并保留正常构建实际生成的 `4` 个 schema JSON。相同 Cargo `--offline --locked --workspace --all-features --all-targets` 检查于 `15:12:45` 退出 `0`，没有修改配置或生成虚假资源。补充上下文的来源、SHA256、生成时点与前后校验见 `rust-context-source-verification.json`、`rust-context-assets-manifest.csv`、`rust-context-after-scan-verification.json`。

最终新上下文数据库于 `15:29:16` 建库退出 `0`，完整查询于 `16:20:55` 退出 `0`，官方日志实际记录 `41/41` 条 Evaluation done；SARIF 的 `executionSuccessful=true`。本轮仍为 `14` 条原始候选，最终 SHA256 为 `322d47e65455d5a6c04bc29428cfbb7d94409c0761a34cb1943162931d2c8423`；独立核验重新检查新源根和全部流路径并绑定该 SHA，结论为 `3` 条上下文误报、`11` 条有意测试行为、`pending=0`，见 [Rust 核验附录](rust-finding-review.md)。最终五语言共 `106` 条原始候选均有人工结论，不能称作 `106` 个已确认漏洞。原清单 `16/16` 个 Rust 文件已归档并返回扫描诊断，缺失 `0`；归档总数 `8536` 还包含标准库、缓存依赖和生成输入，不能算作本仓维护源码。

最终官方 BQRS 诊断为 `ExtractedFiles=16`、`ExtractionErrors=0`、`ExtractionWarnings=28`、`UnextractedElements=0`、`UnresolvedMacroCalls=0`，文件度量为 `2` 个 with-errors、`14` 个 without-error。官方前一个文件度量包含 error 或 warning；`ExtractionErrors` 仅枚举 error，不能把两者混作同一指标。原来的 `lib.rs:601` 两条宏／表达式警告未再出现，未解析宏诊断也由原库 `1` 降为新库 `0`，这次改善有平台与真实构建证据。剩余 `queue.rs` / `spool.rs` 的 `28` 条多行字符串 CRLF 提取警告完整保留。Rust 官方会在词法分析前归一化 CRLF，普通 Cargo 全配置检查也已成功；两者不能保证本次 CodeQL 提取器正确解码。文件归档完整与分析退出成功均不能证明所有局部语义完整，字符串解码和规则未建模的语义仍有漏报限制，见 `rust-extraction-diagnostics.json`、`rust-diagnostic-query-summary.json`、`rust-crlf-warning-review.json`。

最终于 `16:23:03` 复验原固定快照 `2502 + 12` 条目 SHA256 全部未变；于 `16:23:51` 复验新 Rust 副本原 `2514` 条目及补充的真实图标、`4` 个生成 schema 全部 SHA256 未变。业务源码、锁文件和配置没有为降低警告而修改。

## 扫描来源与范围

- 官方 CodeQL CLI：`2.27.2`，Git commit `d7b185e374bfe18c0484dac07a521a810eb9c4fb`。
- 官方 Windows bundle：`codeql-bundle-v2.27.2`；下载体积 `698921984` 字节。
- 官方 SHA256：`c4834b337e2db85bdd4e7e97455319ff70d28f06abb2987eff9ec0f62d51dcea`，本机校验一致，见 `bundle-provenance.json`。
- 本地维护源码清单：`../source-manifest-resume-2026-10-11.csv`，`2502` 文件；清单 SHA256 为 `f939ec3a24e837f45fba766fc1d614ad75d63707d8303e2f090029787506a56f`。
- 每个源码文件复制至本机 `D:/tools/audit-codeql-20261010/source-20261011` 后重新计算 SHA256，与清单逐项比对，见 `source-snapshot.json`。
- 维护源码快照于北京时间 `2026-10-11 10:55:42` 验证完成，构建上下文于约 `11:19` 补入。并行的其他开发任务仍会修改工作区，因此结果只针对该固定快照，不能声称覆盖交付时的每项最新改动。后续核验发现时读取固定快照；工作区漂移由上级目录 `source-drift.json` 单独记录。
- 另有 `12` 个本地构建上下文文件，见 `build-context-manifest.csv`，包括 TypeScript 配置、Go 依赖校验和、Rust 锁文件与 Tauri 配置；这些文件只复制到快照，不改工作区。
- 最终 Rust 独立使用 `D:/tools/audit-codeql-20261010/source-rust-context-20261011`，原 `2514` 个清单条目逐项 SHA 一致，另列 `1` 个当前本地真实 ICO 和 `4` 个实际构建生成 schema。补充输入未冒充原维护清单，来源和前后 SHA 单列于 `rust-context-assets-manifest.csv` 与上下文校验附件；其他语言仍使用原冻结副本。
- Git 忽略的第三方下载与运行缓存、`node_modules`、构建目录、报告输出和 `.git` 不进入本次维护源码快照。特别排除了 `lab/.downloads`、`lab/.runtime`、`lab/.models`。
- Go、JavaScript/TypeScript、Python、Rust 和 GitHub Actions YAML 为本次计划的独立语言任务；其他文件类型不是 CodeQL 支持的独立安全分析语言。

这里的自动扫描不代替逐文件人工审读；原源码清单中的 `pending` 状态不因引擎扫描而改成“已审读”。

## 规则与运行方式

采用官方 `security-and-quality` 查询套件，并使用 bundle 内已安装查询包，运行时指定 `--no-download`。

| 语言 | 官方查询包 | 版本 | 已解析查询数 | 建库模式 |
| --- | --- | --- | --- | --- |
| JavaScript / TypeScript | `codeql/javascript-queries` | `2.4.7` | 203 | `none` |
| Python | `codeql/python-queries` | `1.8.12` | 174 | `none` |
| Go | `codeql/go-queries` | `1.6.12` | 59 | `autobuild`，包含测试文件 |
| Rust | `codeql/rust-queries` | `0.1.44` | 41 | `none` |
| GitHub Actions YAML | `codeql/actions-queries` | `0.6.37` | 28 | `none` |

套件包含安全查询及官方选定的质量查询；不能从查询套件名称推导出全面业务权限正确性、AI 评分准确性或所有架构边界都已验证。

共 `505` 条查询由官方 `resolve queries --format=json` 实际解析，最终五语言成功日志也逐条记录 `505` 次 Evaluation done；Rust 使用补充真实构建上下文后的最后完整 `41` 条结果。查询套件还包含官方诊断和度量查询，`505` 不能写成 `505` 条纯安全规则。最终选定数量、真实完成数量与五语言成功状态分别保留在 `summary.json`，不是将旧 Rust SARIF 借用新库建库状态。Rust 的 `cargo metadata --offline --locked` 预检已成功，见 `rust-cargo-preflight.json`；预检和普通 Cargo 检查均不能代替 CodeQL 查询结果。

默认引擎限制 `--threads=2`，有效最低内存预算 `2048 MiB`。首次 Python 建库传入 `1024 MiB`，引擎在日志中明确上调到最低值 `2048 MiB`，后续建库直接使用 `2048`。JavaScript 首次查询的 `2048 MiB` 总预算实际分配 Java 堆 `1032 MiB`，发生堆内存不足；重试改用 `3072 MiB` 总预算与单线程，实际 Java 堆 `1942 MiB`，保持全部 `203` 条查询。Go 和 Rust 后续任务也显式采用 `3072 MiB`、单线程。最终有效命令与状态以每语言执行记录为准。分析时另外限制磁盘缓存 `2048 MiB`，并设置最少剩余磁盘 `8192 MiB`。

运行时移除 GitHub token 环境变量，显式指定语言，避免远端语言自动探测；Go 和 Cargo 默认离线使用现有本地依赖缓存。原始 SARIF 保存在本地，不运行 `github upload-results`。

## 已处理的执行问题

1. 官方 bundle 经 Windows `curl` 单连接下载仅约 `13–22 KB/s`。改用官方 URL 的 HTTP Range 分片并发下载，校验 Content-Range 后拼包，再校验官方整包 SHA256；恢复时复用了已完整下载的分片，没有使用来源不明的可执行文件。
2. Windows 默认 PowerShell 执行策略阻止官方 Actions 提取脚本，第一次 Actions 建库退出码 `2`。保留 `actions-create-attempt1.log` 及命令记录，只对本次 CodeQL 子进程设置 `PSExecutionPolicyPreference=Bypass` 后重试成功；未修改机器或用户级执行策略。
3. 首次 JavaScript 数据库未包含清单外的 TypeScript 配置。已补齐本地构建上下文，旧数据库与 `javascript-create-attempt1*` 记录保留，最终分析使用后续配置完整的数据库。
4. 本机有其他开发任务占用内存。与并行扫描智能体协调，数据库与查询阶段分批执行，报告中的时间戳是实际命令开始与结束时间。
5. 配置完整的 JavaScript 数据库于 `12:05:42` 创建成功；首次查询于 `12:07:36` 退出 `99`，详细官方日志明确为 `OutOfMemoryError: Java heap space`。失败输出及执行记录保存在 `javascript-analyze-attempt1*`，不将失败算成“零告警”。完整套件重试只调整内存及线程，不删减规则或源码范围；于 `12:26:15` 真正退出 `0`，生成有效 SARIF。
6. Rust 首次建库受官方工具链下载影响，退出 `1`；随后官方 `1.98.1` 工具链及 `rust-src` 经 SHA256 校验并安装完成。第二次建库退出 `2`，原生提取器内存分配失败，保留 `74` 个外部 TRAP 写入失败路径；同时进行的编译诊断也因分配内存失败退出 `101`，不能解释为应用缓冲区漏洞。两次失败数据库和 `rust-create-attempt1*` / `rust-create-attempt2*` / `rust-macro-probe-attempt1*` 均保留。
7. Rust 第三次使用独立数据库、已有编译缓存、官方 TRAP gzip 编码及仅对子进程生效的 `RAYON_NUM_THREADS=1` / `CARGO_BUILD_JOBS=1`，保持原源码、功能配置和完整查询范围。于 `14:21:53` 建库退出 `0`，未再出现 TRAP 写入失败。原生进程实际仍有 `4–7` 个线程，采样私有内存峰值约 `2.14 GiB`；这些环境变量不是内存硬上限。详见 `rust-native-resource-summary.json`、[资源控制依据](rust-resource-control-research.md)。查询阶段只读采集 JVM 性能计数，未修改 JVM、系统配置或其他应用。
8. Rust 第三轮完整查询于 `15:03:24` 真正退出 `0`，保留全部 `41` 条计算完成记录。官方度量是 `3` 个文件 with-errors、`13` 个 without-error，同时 `ExtractionErrors` 返回 `0`；官方前一个度量包含 error 或 warning，两者语义不同，并不矛盾。原库另有 `UnresolvedMacroCalls=1`，不能以文件扫描完整抹掉宏限制，见 `rust-file-metric-interpretation.json` 与 `rust-attempt3-*`。
9. 普通 Cargo 第二轮于 `15:07:43` 退出 `101`，已到应用构建并明确缺少图标，没有内存不足。补入真实本地图标后第三轮 Cargo 于 `15:12:45` 退出 `0`。原冻结源码副本保持不变；真实补充资源只进入新的独立 Rust 分析副本。
10. 新上下文首轮查询采用 `4096 MiB` 总预算，于 `15:29:23` 退出 `99`，尚未加载或执行查询。官方详细日志明确为约 `1.08 GiB` off-heap 大块分配失败，实际 Java 堆为 `1800 MiB`；总预算不能直接解释为 Java 堆大小。失败日志与执行记录保存在 `rust-analyze-context-attempt1*`。同一已成功数据库采用 `3072 MiB`、单线程和 `2048 MiB` 磁盘缓存恢复全部 `41` 条查询，实际 Java 堆上限约 `1876 MiB`，于 `16:20:55` 完整退出 `0`，未缩减源码或规则。详见 [查询资源解释](rust-query-resource-research.md)。

## 原始证据与复现

- `version.json`、`languages.json`：真实工具版本及支持的提取器。
- `*-create-execution.json`、`*-analyze-execution.json`：完整命令、开始结束时间、耗时与退出码。
- `*-create.log`、`*-analyze.log`：引擎输出及诊断。
- `*.sarif`：成功分析后生成的原始 SARIF。
- `alerts.csv`：逐条发现的规则、文件、行号与分类；不复制可能包含密钥的代码或消息片段。
- `summary.json`：机器可读的状态与统计，安全等级来自规则 `security-severity`，不能把普通 SARIF `warning` 直接当作安全漏洞等级。
- `*-archived-source-files.txt`：数据库实际归档的源码清单。
- `run_scan.py`、`collect_context.py`、`download_bundle.py`、`summarize.py`：本次隔离工具目录使用的辅助脚本副本。
- `rust-diagnostic-query-summary.json` 与 `rust-*-decoded.json`：当前成功分析数据库的官方诊断 BQRS 解码；须核对其数据库路径和 SARIF SHA，不能把旧库数量套到复扫结果。
- `rust-context-source-verification.json`、`rust-context-assets-manifest.csv`、`rust-context-after-scan-verification.json`：原冻结文本不变、补充真实构建输入及最终 SHA 校验。

脚本中的数据库和工具路径均属于本次本机隔离缓存。原始源码工作区保持只读；如需复跑，应新建数据库目录，避免覆盖既有证据。

## 官方依据

- [CodeQL CLI 能力与授权说明](https://docs.github.com/en/code-security/concepts/code-scanning/codeql/codeql-cli)
- [CodeQL 使用许可](https://github.com/github/codeql-cli-binaries/blob/main/LICENSE.md)
- [官方 bundle 下载](https://github.com/github/codeql-action/releases/tag/codeql-bundle-v2.27.2)
- [数据库创建命令](https://docs.github.com/en/code-security/reference/code-scanning/codeql/codeql-cli-manual/database-create)
- [数据库分析及 SARIF 命令](https://docs.github.com/en/code-security/reference/code-scanning/codeql/codeql-cli-manual/database-analyze)
- [CodeQL 平台与软件要求](https://codeql.github.com/docs/codeql-overview/system-requirements/)
