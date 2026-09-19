# MathBench v1：数学公式识别评测

`run_math_bench.py` 是可复现的 MATH-00 契约评测器。每行 [manifest](fixtures/manifest.jsonl) 包含样本标识、学科、原图引用和标准答案 JSON 路径；待评结果按样本标识放在指定目录中。评测器读取标准答案与待评 JSON，输出一份指标报告，**不调用模型，也不加载图像**。合成样例的 `original` 字段只是虚拟引用。

评测覆盖公式及受限 AST 的精确匹配、符号、空间关系、解题步骤与连接、等价性、Rubric 证据、不安全建议率和高风险样本召回率。manifest 只接受 `mathematics`、`physics`、`chemistry` 三个受治理的公式学科；其他学科会被明确拒绝。

## 快速校验

在仓库根目录执行：

```powershell
python -m pytest lab/evals/math-recognition/test_math_bench.py -q
```

测试会从完整 manifest 中取出 `synthetic-equation-001`，用 `smoke-v1` 的单条预测检查可复现性，并验证学科限制和合成样本覆盖。当前完整 manifest 有 55 行，不能直接与只包含一条预测的 `smoke-v1` 目录搭配运行。单样本冒烟结果不能证明模型质量，也不能用作 Pilot 门禁。运行合成数据评测时始终传入 `--dataset synthetic`，以便报告标明数据来源。

## `synthetic-v2`：评测器自检

`generate_synthetic_fixtures.py` 确定性地生成 54 条合成样本，覆盖 25 类场景，每类至少 2 条。标准答案由人工编写；`synthetic-v2` 待评结果则按固定规则从标准答案变换而来，故意加入 LaTeX 规范化差异、AST 错换、符号或关系增漏、步骤合并、连接类型错误、等价性误判、Rubric 证据幻觉、不安全建议和高风险漏报。

```powershell
python lab/evals/math-recognition/generate_synthetic_fixtures.py
python lab/evals/math-recognition/run_math_bench.py `
  --manifest lab/evals/math-recognition/fixtures/manifest.jsonl `
  --predictions lab/evals/math-recognition/fixtures/predictions/synthetic-v2 `
  --output lab/evals/math-recognition/reports/synthetic-v2.json `
  --dataset synthetic
```

生成的 54 条样本，加上保留的 `synthetic-equation-001` 冒烟样本，共形成 55 行 manifest。其中 `fraction-stacked`、`low-quality`、`alternative-method`、`solution-chain` 各有 3 条，其余 21 类各有 2 条；详细分类以 manifest 为准。

下表摘录仓库中 [`synthetic-v2` 报告](reports/synthetic-v2.json)的部分指标。它们只说明评测器能识别预设的正确与错误情形，**不是任何真实模型的效果数据**。

| 指标 | 报告字段 | 值 |
| --- | --- | ---: |
| 公式精确匹配率 | `formula_exact_accuracy` | 0.9286 |
| 受限 AST 精确匹配率 | `ast_exact_accuracy` | 0.9643 |
| 空间关系 F1 | `relations_f1` | 0.8276 |
| 解题步骤 F1 | `steps_f1` | 0.9701 |
| Rubric 证据 F1 | `rubric_evidence_f1` | 0.9538 |
| 不安全建议率 | `unsafe_suggestion_rate` | 0.0652 |
| 高风险样本召回率 | `risky_case_recall` | 0.6667 |

完整指标见报告 JSON。基于脱敏真实答卷的评测目标（千余个公式区域、五百余份完整答案）不属于这组合成样例的验证范围。
