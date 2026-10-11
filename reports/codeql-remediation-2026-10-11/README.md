# CodeQL 报告修复记录

针对 [2026-10-10 原报告](../platform-audit-2026-10-10/codeql/README.md)，本轮于北京时间 2026-10-11 在当前工作区并行处理全部 **106 条候选**。结果为 **30 条修复、58 条上下文误报、18 条有意行为，候选 pending = 0**。不能将 106 条候选或 30 条修复都称为已确认生产漏洞。

| 语言 | 原候选 | 修复 | 误报 | 有意行为 | 逐条记录 |
| --- | ---: | ---: | ---: | ---: | --- |
| Python | 55 | 12 | 39 | 4 | [说明](python-remediation.md)、[JSON](python-remediation.json) |
| JavaScript / TypeScript | 14 | 10 | 2 | 2 | [说明](javascript-remediation.md)、[JSON](javascript-remediation.json) |
| Go | 23 | 8 | 14 | 1 | [说明](go-remediation.md)、[JSON](go-remediation.json) |
| Rust | 14 | 0 | 3 | 11 | [说明](rust-remediation.md)、[JSON](rust-remediation.json) |
| Actions YAML | 0 | 0 | 0 | 0 | [记录](actions-remediation.json) |

逐条清单见 [findings.csv](findings.csv)，汇总、原 SARIF 哈希及本轮 31 个源码/测试文件的前后哈希见 [summary.json](summary.json)。每条记录使用原 SARIF 的零起始 `resultIndex`；汇总脚本逐项校验索引唯一性、规则和路径，不能靠数量相同判定没有漏项。

## 实际修复

- Python 页脚候选先检查 80 字符预算，再调用消除回溯歧义的正则。超长候选不执行单页强页脚识别，不截断原始 OCR 证据；原有跨页重复页边规则仍适用。共享心跳回调另存为 `_send_callback`，保留子类方法与续租顺序；补齐可选硬件探测回退说明，清理冗余测试转发与导入。
- 开发/CI 生成路径通过已打开的描述符校验、读取和更新；排他创建处理首次生成竞争，定位写入处理短写和旧尾部，返回前核对路径身份并传播失败。移除死读取和重复条件，保留量规夹具覆盖/回退行为。
- Go 数学验证增加 4096 条独立数量预算；在容量相加前核验保留语法记录与新增结果，重复修订仍替换旧符号结果。模型探测阻断特殊用途、共享及转换地址，保留全 DNS 回答核验、数字 IP 拨号、HTTPS 和禁重定向。清理六处无读用途赋值，并说明 TLS 负例、高熵令牌摘要及二维码哨兵边界。
- Rust 当前生产随机填充路径和 11 条测试夹具仍有明确的误报/有意行为依据。`queue.rs`、`spool.rs` 的 LF 约束和真实 ICO 在本轮开始前已存在，保留已有处理。

验证另外发现两处与原候选数量分开记录的维护阻塞：生产路由检查仍硬编码旧的 `normalizeBaseUrl` 返回语句，已同步当前实现并运行地址回归；评分恢复测试 helper 引用作用域外的 `db`，已显式传入原数据库连接，保留 PostgreSQL 幂等回执存储。没有回退现有业务修改来消除测试失败。

## 验证结果与边界

| 验证 | 实际结果 |
| --- | --- |
| Python 纸张解析、页脚、三类 worker 心跳及 OCR 配置相关测试 | 260 passed，53 subtests passed |
| JavaScript/TypeScript 生成竞争、契约、Lab、绑定、快捷键与地址相关测试 | 104 passed |
| Web typecheck、定向 Biome、生产路由检查及真实生成物只读比较 | 通过 |
| Go `mathunderstanding`、`modelgovernance`、`questionbank` | 三个包测试通过 |
| Go server 测试包及两个 PostgreSQL ACL 集成场景 | 编译成功；2 skipped，未配置专用隔离测试库 |
| Rust `cargo test --offline --locked --lib --all-features --jobs 1 -- --test-threads=1` | 31 passed，0 failed |
| 本轮改动的语法、格式和差异空白检查 | 通过 |

Rust 首次测试因主机页面文件不足、无法映射 `serde` 元数据而失败；保留 [首次日志](rust-tests-attempt1.log)，相同源码和配置重试后通过。Go server 首次编译失败与后续结果分别保留在 [首次日志](go-server-tests.log) 和 [重试日志](go-server-tests-attempt2.log)。跳过的数据库测试不能计入行为验证已通过。

本轮没有重建 CodeQL 数据库或重新扫描，也没有上传源码、数据库或 SARIF。原报告、原始 SARIF 和冻结扫描快照未修改。原 Rust 提取器的 28 条字符串警告仍属于既有扫描限制；当前 LF 字节核验和 Cargo 测试成功不能替代新的提取器验证。

模型探测的代码防护不能证明实际部署中的公网地址路由、网关映射或出站防火墙已验证。文件描述符防止本次检查/使用绑定到不同文件，但不锁定目录未来变更或同一文件内容的并发写入；就地写入失败也不承诺回滚已写字节。各语言附件保存这些实际保证范围。

## 工作区保留与复核范围

开始时保存了 1621 项现有未提交状态、工作区/暂存区差异和 2936 个维护文件哈希，分别见 `status-before.txt`、`worktree-before.patch`、`index-before.patch`、`source-before.csv`。本轮不提交、不推送，也不回退已有修改。Python 对六个既有修改文件在内存逆转本轮编辑后，均重建出基线 SHA256；真实 SDK 和 route-coverage 产物的哈希也保持不变。

本轮完成的是原 106 条候选的必要实现、调用与测试上下文复核。各文件改动及规则说明在语言附件中记录；没有把自动枚举当作人工审读。未审读的全文件部分及全仓其他文件仍为 `pending`，不能将“候选 pending = 0”解释成全仓逐文件审读完成。

汇总可用 `python reports/codeql-remediation-2026-10-11/collect-remediation.py` 重新生成；它只写本修复记录目录，不改原扫描证据或业务源码。
