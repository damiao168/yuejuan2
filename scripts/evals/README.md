# 答题卡区域离线评测

此脚本读取已经完成的脱敏标注和预测，不调用模型、不修改模板。模型调用状态、区域类别、正式题号和完整作答范围是不同指标，不能以请求成功或单一 IoU 代替最终正确性。

```powershell
python scripts/evals/answer_sheet_regions.py scripts/evals/fixtures/answer-sheet-regions.synthetic.json --output output/region-evaluation.json
python -m pytest scripts/evals/test_answer_sheet_regions.py -q
```

输入 `documents` 包含 `template_family`、`split` 和 `pages`；每页有 `ground_truth`、`predictions` 数组。区域至少包含 `role`、`bbox`（来源页 0～1 的 x/y/width/height），可带 `question_id` 或 `printed_question_label`、`semantic_check`。身份区域应提供静态字段名 `identity_label`，`per_category` 和 `category_confusion` 才能区分姓名、准考证号及条码；`per_role` 只统计粗类别。同模板及其扫描/裁剪变体必须归入同一分组；脚本拒绝模板家族跨集合混用。

附带夹具是没有图像和学生资料的合成故障，不是历史模型原始响应，也不是模型准确率证明。它应报告多个分类错误，即使所有边框完全重合。

匹配使用固定 IoU 阈值的一对一最大重合贪心法，然后统计分类 precision/recall/F1、混淆、题号关联召回、错误 supported 数，以及匹配框的漏裁/外溢比例。报告会明确匹配方法和阈值。几何均值只覆盖匹配成功的区域，必须同时看漏检计数；这组基础指标不代替复杂多区域映射或端到端学生归属评测。

真实模型评测应在固定的、学校授权且脱敏的独立模板集上运行，再将输出交给脚本。不要把原始学生图像、填写姓名/考号或未经处理的供应商响应加入仓库。CI 固定响应回归与真实供应商质量评估应分别报告，不能把前者称为真实模型验收。

v6/v7 提示词版本登记与固定模型响应重放见[提示词契约回放](README.template-analysis-v7.md)。这条工具链验证清理前语义矛盾、隐私及候选降级，不计算上述真实区域质量指标。
