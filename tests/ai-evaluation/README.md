# AI 阅卷离线评测

本目录提供 EduGrade 主观题 AI 阅卷的离线评测脚本。评测器只比较已有样本和预测记录，不调用模型。仓库自带的样本和预测均为**合成或模拟数据**，不能当作真实模型效果；数据中也不得包含真实学生身份、姓名、学号、答卷或学校私有信息。

## 文件与数据格式

| 文件 | 作用 |
| --- | --- |
| [`samples/synthetic_subjective_v1.jsonl`](samples/synthetic_subjective_v1.jsonl) | 合成主观题样本，每行一个 JSON 对象。 |
| [`predictions/synthetic_mock_predictions.jsonl`](predictions/synthetic_mock_predictions.jsonl) | 与样本对应的模拟预测，可按模型和提示词版本比较。 |
| [`evaluate.py`](evaluate.py) | 不依赖第三方库的评测器，生成 JSON 和 Markdown 报告。 |
| [`test_evaluate.py`](test_evaluate.py) | 指标、分组、人工复核路由及报告生成测试。 |

每条样本必须含 `sample_id`、`synthetic: true`、`question`、`rubric`、`answer`、`human_score`、`human_rationale` 和 `expected_points`；嵌套的 `answer` 也必须含 `synthetic: true`。`question.max_score` 决定分数上限。

每条预测必须含 `sample_id`、`synthetic: true`、`model_version`、`prompt_version`、`suggested_score`、`confidence` 和 `needs_human_review`。样例还包含 `risk_flags` 与 `matched_points`；其中 `risk_flags` 会参与风险路由统计。预测通过 `sample_id` 与样本关联。

## 运行评测

在仓库根目录执行：

```powershell
python .\tests\ai-evaluation\evaluate.py `
  --samples .\tests\ai-evaluation\samples\synthetic_subjective_v1.jsonl `
  --predictions .\tests\ai-evaluation\predictions\synthetic_mock_predictions.jsonl `
  --out-dir .\tests\ai-evaluation\reports
```

结果写入 [`ai_evaluation_report.json`](reports/ai_evaluation_report.json) 和 [`ai_evaluation_report.md`](reports/ai_evaluation_report.md)。修改评测逻辑后，可运行：

```powershell
python -m unittest discover -s tests/ai-evaluation -p "test_evaluate.py"
```

## 指标怎么读

| 指标 | 含义 |
| --- | --- |
| `MAE` | 建议分与人工分之差的绝对值平均数。 |
| `RMSE` | 分数误差平方的平均值再开方，对较大误差更敏感。 |
| `exact_agreement` | 误差不超过精确匹配容差的比例，默认容差为 `1e-9`。 |
| `adjacent_agreement` | 误差不超过相邻分数容差的比例，默认容差为 `1.0`。 |
| `score_bias` | `suggested_score - human_score` 的平均值；正值表示整体偏高。 |
| 低置信度路由 | 检查置信度低于默认阈值 `0.8` 的预测是否送人工复核。 |

## 扩充评测集

1. 只加入合成样本，或经过数据治理流程批准的脱敏样本；每个 `sample_id` 对应一个明确场景。
2. 写全人工评分理由、预期采分点及最高分，并为要比较的每组模型和提示词版本提供预测记录。
3. 同时检查低置信度路由和风险标记。分数一致率高不能单独证明可用于生产。
4. 修改必填字段或指标定义时，同步更新测试。
