# Rust CodeQL 低 offheap 与分步查询后备研究

本附件只读取官方本地 CLI 帮助、原始失败记录和公开官方文档，并运行无数据库输入的 RAM 参数解析；没有启动查询、扫描、编译或测试，没有改当前数据库、源码、进程或系统资源。当前 3072 MiB／单线程复扫已有效推进，继续保持；下面只用于真实失败后的后备恢复。

## 官方参数的实际含义

本机 CodeQL 2.27.2 的 `database run-queries --help -vv` 实际退出 0，267–283 行明确支持 `--ram`、`--heap-ram`、`--off-heap-ram`。后两项属于隐藏的高级参数：heap-ram 指求值器缓存使用的 Java 堆，不能当作 JVM 的 `-Xmx`；off-heap-ram 从总 ram 预算扣除。总 ram 最低为 2048 MiB，较小值自动上调，也不是严格的操作系统内存上限。原文与 SHA 保存于 [JSON 证据](rust-low-offheap-fallback-research.json)。

`execute queries --help -vv` 218–221 行进一步说明：offheap 缺省或为 0 时，求值器只使用 Java 堆。官方 [resolve ram 文档](https://docs.github.com/en/code-security/reference/code-scanning/codeql/codeql-cli-manual/resolve-ram)说明，该命令根据总预算、数据库规模等生成 JVM 与 offheap 参数；实际预算必须以对应版本和数据集的解析结果为准。

保存的失败记录为：新构建上下文数据库 create 退出 0，随后 analyze 请求 4096 MiB，实际分成 JVM 最大堆 1800 MiB／offheap 1108 MiB；查询尚未加载便在 `Unsafe.allocateMemory` 分配 1,161,822,208 字节时退出 99。这是资源失败，不能计为代码漏洞，也不能推断提高总预算必然更稳定。

## 无数据库参数解析实证

本次只执行以下官方元数据计算，均无 `--dataset`，不会查询当前库。

| 命令 | 真实结果 |
| --- | --- |
| `resolve ram --ram=3072 --off-heap-ram=0 --format=json` | exit 0；只返回 `-J-Xmx1964M`，不含 offheap 参数 |
| `resolve ram --ram=3072 --off-heap-ram=256 --format=json` | exit 0，但警告 256 被忽略：否则留给 Java 堆的空间过小；仍只返回 1964M |

这证明不能只看退出 0 就宣称强制的 256 MiB 生效。1964M 是通用默认数据库规模下的结果，不能替代当前 Rust 数据集已经记录的 1876 MiB，也不证明这些参数可完成查询。原始 stdout、命令和哈希见 `rust-fallback-generic-ram-resolution.json`。

## 保持 16 维护源码与 41 查询的后备流程

若以后再次发生真实 offheap 分配失败，可优先保留高层 `database run-queries`，按机器可用资源选总预算，显式 `--off-heap-ram=0`，维持单线程与已有磁盘缓存。先保存针对真实数据集的 resolver 输出和警告，再判断实际参数。禁用大 offheap 块会把中间结果压力转回 Java 堆，仍可能增加 GC 或发生 heap OOM；没有测量依据就不承诺提速或成功。

如果整套运行确实失败，需要分步恢复，可按已有 `rust-query-manifest.json` 的完整 41 项分批或逐项执行 `database run-queries`，仍使用同一个新上下文数据库、同版官方包及模型，保留 `--no-rerun` 和 `--keep-full-cache`，逐次保存退出状态，并检查所有完成查询的去重并集恰好等于原清单。分步可能减少同时加载的查询，但尚未测量峰值改善，重新启动也可能增加重复计算；不得据此减少规则或忽略慢查询。

官方高层 run-queries 将结果放入数据库标准 results 子树。所有 41 项同库结果确实完成后，再用原完整查询选择执行 `database interpret-results` 输出本地 SARIF；该命令支持把查询运行与解释分为两步。[官方结果解释说明](https://docs.github.com/en/code-security/reference/code-scanning/codeql/codeql-cli-manual/database-interpret-results)。

`--no-rerun` 只根据输出位置已有 BQRS 决定跳过，不会替本任务证明来源一致。原来缺构建上下文的旧库结果不能混入新资产复扫。任意 `query run --output` 文件也不能直接当作全套 interpretation 已能找到的标准结果。[官方 query run 说明](https://docs.github.com/en/code-security/reference/code-scanning/codeql/codeql-cli-manual/query-run)。

官方 [execute queries 文档](https://docs.github.com/en/code-security/reference/code-scanning/codeql/codeql-cli-manual/execute-queries)把直接执行列为底层流程，通常建议由 run-queries 或 query-run 设置 JVM 参数；因此本后备方案保持高层接口，避免手动拼接 JVM 和 BQRS 布局。本次没有执行后备流程，也没有动正在运行的复扫。
