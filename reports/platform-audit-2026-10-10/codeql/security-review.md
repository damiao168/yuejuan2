# CodeQL 安全候选核验

核验对象是 `2026-10-11 10:55:42` 已验证的固定源码快照，路径 `D:/tools/audit-codeql-20261010/source-20261011`。本文件属于人工核验附件；原始平台等级仍保留，不把自动候选直接等同于完整生产攻击证明。

## Python：页脚正则存在 CPU 放大风险

| 项目 | 核验结果 |
| --- | --- |
| 原始规则 | `py/polynomial-redos` |
| 官方规则安全分值 | `7.5`，High |
| 官方精度 | 见 `alerts.csv` 的 `precision` 字段 |
| 报告位置 | `ai-services/grading_agent/paper_compact/anchors.py:619` |
| 正则定义 | `ai-services/grading_agent/paper_compact/schema.py:30` |
| 人工状态 | 已验证原始正则可产生超线性 CPU 开销，且 OCR block 路径缺少该调用前的单块文本上限 |
| 验证边界 | 使用合成数据的隔离函数实验；未调用运行中的阅卷接口、未进行完整部署压力测试 |

页脚正则定义为：

```python
r"(?:试卷|答题卡)?\s*第\s*\d+\s*页\s*[,，、]?\s*(?:共\s*\d+\s*页)?"
```

`页` 后的两个 `\s*` 被可选标点分隔。标点缺省、末尾有不匹配字符时，Python 回溯引擎会重新分配同一长段空白的匹配位置。`text.strip()` 只去掉字符串两端的空白，无法去掉这种位于内部的长空白，因此不会消除该输入形态。

触发条件可以是具有有效 `bbox`、被认定为页面底部、未命中题目/答案/解析/选项保护锚点的 OCR block。`anchors.py:618–620` 在检查另一个分支的 `len(text) <= 80` 之前，先执行 `_PAGE_FOOTER.fullmatch(text.strip())`；该 `80` 字符条件不保护当前正则调用。

输入边界的实际限制：

- `paper_input.py:61` 将 `document.content` 截断为 `700000` 字符，但通过 `{**document, ...}` 原样保留 `blocks`；`blocks[*].text` 没有在该正规化阶段截断。
- `chunks.py:42` 在把文本分成 `MAX_TEXT_CHARS_PER_CHUNK = 1100` 的块之前调用 `_without_page_furniture(blocks)`。分块预算属于后续步骤，不是该正则的前置上限。
- `server.py:88` 在读取业务请求前验证 Bearer service token，因此不能把本结果描述成“已证明匿名公网拒绝服务”。HTTP 纸张解析接口允许最高 `48 MiB` 的请求体（`server.py:243–246`），该总量预算也不能保证单个 block 的正则耗时可接受。
- CodeQL 的一条数据流穿过直接文本路径，但直接文本生成的 block 的 `bbox=None`，该路径会在 `anchors.py:608` 提前返回。人工核验因此没有把这条单独的直接文本路径认定为已证明可利用；具有有效坐标的 OCR block 路径仍能满足调用条件。

## 合成实验结果

实验使用固定快照原始 `_PAGE_FOOTER` 定义，通过 AST 读取正则，而不是手抄另一个实现。输入由页脚前缀、重复空白和一个失败后缀组成，仅用于本地隔离进程；没有学生数据，没有业务服务调用。每个进程设置严格超时。

| 内部空白数量 | 原始正则实测耗时 |
| --- | --- |
| 512 | 约 `0.0097 s` |
| 1024 | 约 `0.0414 s` |
| 2048 | 约 `0.1349 s` |
| 4096 | 约 `0.6755 s` |
| 8192 | 隔离进程达到 `2 s` 超时后终止 |

见 `footer-regex-benchmark.json`。这些单次耗时受本机并行任务影响，不是稳定性能基准；但相同原始正则的放大趋势与自动规则相符。

另一个实验直接执行从固定快照提取的 `normalize_paper_input`、`_box`、`_block_text`、`_is_page_furniture` 等原始纯函数 AST。带有效底部坐标的合成 block 在正规化后保持 `1028` / `2052` 字符，调用分别约 `0.049 s` / `0.215 s`；更大的子进程达到严格超时。见 `footer-furniture-benchmark.json`。该实验核对了“正规化未限长”和“调用条件可满足”，仍不等于完整接口端到端压测。

建议在页脚识别调用前限制页脚候选文本长度，并把正则改为有界或不回溯的解析；同步验证所有合法中文页码/空白/标点形式，避免静默误删真实正文。修复应另开代码变更，本次扫描未修改业务逻辑。

## Python 质量候选分布

54 条非安全质量候选包括：无效果语句 22、未使用全局变量 15、空 except 5、隐式字符串拼接 4、属性遮蔽方法 3、不必要 lambda 2，以及过程返回值被使用、混合 import、未使用 import 各 1。并行核验智能体已逐条检查这 54 条候选及其必要上下文，结论为 39 条上下文误报、7 条有意实现／兼容行为、8 条低影响维护项，0 条待核验，未确认导致运行错误的质量缺陷。详细依据、逐条状态及隔离回调验证见 [Python 质量核验附录](python-quality-review.md) 和 `python-quality-review.json`。这个结论只覆盖列出的质量告警上下文，不代表全仓逐文件人工审读完成。

## Actions YAML

官方 `codeql/actions-queries:actions-security-and-quality.qls` 的 28 条查询实际执行成功，退出码 `0`，生成 `actions.sarif`，本次固定快照没有返回候选。这里是 CodeQL 对本地 GitHub Actions 配置的静态安全分析，不是 GitHub Actions 平台作业已经运行，也不表示 CI 中所有测试、覆盖率和构建都通过。
