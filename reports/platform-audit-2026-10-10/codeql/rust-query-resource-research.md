# Rust 查询耗时与缓存的只读核验

记录时间：2026-10-11 14:52:57（北京时间）。本附件只读官方本地 QL、CLI help、已保存的执行日志及 JVM 计数；没有运行查询、编译、压力程序，没有改变当前进程、数据库、源码或系统资源。查询仍由原扫描智能体继续执行，原 16 个维护 Rust 文件和 41 条查询保持不变。

## 当前耗时不等于挂死

保存当时 `rust-analyze.log` 的只读副本为 `rust-query-resource-observation.log`，SHA-256 及每条完成记录见 `rust-query-resource-research.json`。其中 AST 一致性查询完成于 16m42s、CFG 完成于 2m36s，曾运行约 7 分钟的数据流一致性查询随后正常完成于 **9m30s**。截至上述记录时间已有 7/41 查询完成；随后的 ExtractedFiles 65ms、ExtractionErrors 4.2s、ExtractionWarnings 4ms、SSA 3.4s。完成记录证明这次数据流查询确实在推进，不能因为几分钟没有新日志判为挂死；这些已完成时长也不能作为其余查询的固定预期或保证。

这是官方库的模型一致性诊断，不是阅卷业务测试，也不是简单对 16 个文件逐行计数。查询 [DataFlowConsistencyCounts.ql](/D:/tools/audit-codeql-20261010/codeql/qlpacks/codeql/rust-queries/0.1.44/queries/diagnostics/DataFlowConsistencyCounts.ql:8) 调用 Rust 数据流一致性库；后者 [DataFlowConsistency.qll](/D:/tools/audit-codeql-20261010/codeql/qlpacks/codeql/rust-all/0.2.23/codeql/rust/dataflow/internal/DataFlowConsistency.qll:1) 引入控制流、数据流实现、流摘要、节点、污点模型及通用一致性检查。

通用库 [DataFlowImplConsistency.qll](/D:/tools/audit-codeql-20261010/codeql/qlpacks/codeql/dataflow/2.1.15/codeql/dataflow/internal/DataFlowImplConsistency.qll:364) 对模型中的节点、类型、位置、局部读写边、调用上下文、参数及更新关系作多组计数。Rust [DataFlowImpl.qll](/D:/tools/audit-codeql-20261010/codeql/qlpacks/codeql/rust-all/0.2.23/codeql/rust/dataflow/internal/DataFlowImpl.qll:11) 还依赖 SSA、路径解析、控制流、类型推断和调用摘要。这解释了求值工作量为什么不能只按维护源码文件数估算；归档包含依赖/标准库输入也不表示每个归档项都产生相同数量或相同成本的模型关系。

## 已发现的缓存边界

原始运行日志第 6 行提示：`Not caching stages during query-loading, since max heap size is only 1876 MB.` 这句话明确限定 **query-loading 阶段的 stage 缓存**；不能据此写成所有运行时缓存或磁盘缓存均已关闭。本研究没有发现官方文档公开对应的精确堆阈值，不能猜一个数值或承诺提高内存后一定启用该优化。

官方库 [CachedStages.qll](/D:/tools/audit-codeql-20261010/codeql/qlpacks/codeql/rust-all/0.2.23/codeql/rust/internal/CachedStages.qll:1) 把 AST、CFG、路径解析、类型推断与数据流各组谓词组织为缓存阶段。其注释解释：组合阶段能增加共享计算复用，但具体查询只需要部分谓词时，也可能计算整个组而做更多工作。缓存并不意味着首次计算免费。

[DataFlowImpl.qll](/D:/tools/audit-codeql-20261010/codeql/qlpacks/codeql/rust-all/0.2.23/codeql/rust/dataflow/internal/DataFlowImpl.qll:1176) 的 `cached` 模块包含数据流调用/类型、局部流步骤、源、汇、屏障等关系；[SsaImpl.qll](/D:/tools/audit-codeql-20261010/codeql/qlpacks/codeql/rust-all/0.2.23/codeql/rust/dataflow/internal/SsaImpl.qll:208) 也缓存 SSA 与数据流集成的公共关系。安全查询如 [SqlInjection.ql](/D:/tools/audit-codeql-20261010/codeql/qlpacks/codeql/rust-queries/0.1.44/queries/security/CWE-089/SqlInjection.ql:14) 使用同一数据流/污点公共库，因此具备共享计算复用的机会；各安全配置仍有自己的源汇和全局路径计算，不能承诺后面全部只花毫秒。

QL 官方说明 `cached` 会完整计算实体并保存结果，后续当前查询和其他查询可复用；这也可能增加存储占用并减少按调用上下文优化的机会。[CodeQL 官方 cached 注解说明](https://codeql.github.com/docs/ql-language-reference/annotations/#cached)

## GC 采样说明

复用原扫描智能体已保存的 `rust-query-jvm-perf-samples.jsonl` 中 14:46:38 与 14:49:58 的同一 JVM 样本，没有追加或重新采样。200 秒窗口内，GC collector 时间增量分别为年轻代 7.428s、full 20.026s、并发周期暂停 0.351s，合计约 27.805s。它们是 JVM 累计计数差，不是某一 QL 谓词的耗时分解，也不能简单把剩余时间全部归为有效查询计算；但没有依据说整个窗口都被 GC 吞没。

本地 bundled Java `release` 为 25.0.4.1。OpenJDK 25 官方实现中，`sun.rt.safepointTime`、`applicationTime` 虽仍声明 `U_Ticks`，调用方传入的实际值是 `os::javaTimeNanos` 的差，服务直接累加。因此对应源码解释应按纳秒换算这些运行时计数，不能和 GC 字段一律除以 `sun.os.hrt.frequency`。两个样本的 safepoint 差约 27.840s、application 差约 172.053s，合计 199.893s，与 200 秒窗口近似一致。[OpenJDK 25 SafepointTracing](https://raw.githubusercontent.com/openjdk/jdk25u/master/src/hotspot/share/runtime/safepoint.cpp)，[RuntimeService 累加逻辑](https://raw.githubusercontent.com/openjdk/jdk25u/master/src/hotspot/share/services/runtimeService.cpp)

该单位说明来自对应上游 JDK 25 源码，没有逆向核验 bundled 厂商精确构建。共享计数是非同步读取，可能在采样过程中推进；计数不能证明查询语义覆盖完整。具体差值及读取时样本文件 SHA-256 已写入 JSON。

## 官方支持的后续资源选择

本地 CodeQL 2.27.2 `database run-queries --help -v` 的实际输出保存在 `rust-query-evaluator-help.log`，退出码 0。它明确支持 `--ram`、`--threads`、`--max-disk-cache`、`--keep-full-cache`、`--no-rerun` 和 `--evaluator-log`。这些是将来正常续跑或新运行时的选项，没有在当前运行中修改。[GitHub 官方 run-queries 参考](https://docs.github.com/en/code-security/reference/code-scanning/codeql/codeql-cli-manual/database-run-queries)

- 若当前正常结束或发生真实错误，再按机器可用内存考虑提高 `--ram`；它控制求值器总体预算，不能直接等同 JVM 堆大小，也不能承诺一定更快。不要直接用 JVM `-Xmx` 绕过求值器的预算划分。
- `--threads` 可增加求值线程。当前内存紧张且此前发生 OOM，增加线程可能增加内存竞争；没有测量依据就不提高当前并发。之前提取阶段的 `RAYON_NUM_THREADS` 也不能当作当前 Java QL 求值线程控制。
- 机器有足够磁盘余量时，后续提高 `--max-disk-cache` 可扩大中间关系缓存，帮助后面查询复用；不是本次已证实命中率或加速幅度。`--keep-full-cache` 只是保留完成后的缓存，不能热改当前查询。
- 真正需要恢复时，保留数据库、原 41 查询及已完成 BQRS，使用同一版本、模型和范围的 `--no-rerun`；它会跳过输出位置已有 BQRS 的查询。恢复前需核结果文件和执行记录，不能只凭文件存在便把旧版/不同范围结果混作本轮。不得减少诊断或安全规则。
- 后续 `--evaluator-log` 可以保存结构化性能日志，定位重计算和耗时；官方建议用 `generate log-summary` 生成稳定摘要。这里没有启用新诊断代理或运行总结程序。

不建议仅因这次数据流查询耗时几分钟就停止当前扫描；它已完成于 9m30s。所有源文件、pack 锁定版本、CLI help 和观察日志的哈希保存在 `rust-query-resource-research.json`，并与扫描结果文件分开，避免把研究附件当成扫描完成证据。
