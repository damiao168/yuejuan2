# 智能阅卷 Agent 实验室

`lab/` 用于独立开发和验证阅卷 Agent 的规格、数据结构、提示词、合成数据评测、模型适配器、防护规则与发布门禁。这里的结果只用于研究和准入判断；主平台的数据库、界面、EXE 打包、权限、成绩发布和申诉流程不在本目录实现。具体边界见 [实验室规则](AGENTS.md)。

## 常用命令

先进入 `lab/`，再按需要运行：

```powershell
Set-Location lab
npm test                         # 单元测试
npm run eval                     # 合成数据与模拟适配器评测
npm run gate:dev                 # 检查开发阶段门禁
npm run annotations:workflow:demo # 标注流程演示
```

外部 JorGPT 数据流程需要依次准备、导入、治理数据，再运行规则基准：

```powershell
npm run dataset:jorgpt:prepare
npm run dataset:jorgpt:import
npm run dataset:jorgpt:govern
npm run benchmark:jorgpt:rules
```

命令定义见 [`package.json`](package.json)。这些流程生成的合成数据或外部基准结果都不能直接替代真实校内数据的 Pilot 验证。

## Story 工作流程

每个 Story 先编写、评审并修订规格，再仅在 `lab/` 内实现、评审和修订代码，完成后才进入下一个 Story。进度看板见 [stories/story-board.md](stories/story-board.md)。后续 Story 不会自动创建；接入主平台的影子流程需要单独授权和实施。

## 当前证据与适用范围

仓库中 [毕业审计报告](evals/reports/graduation-audit.json)记录 Story G–R 已完成，实验室状态为 `LAB_IMPLEMENTATION_COMPLETE`。但 [Pilot 就绪报告](evals/pilot/pilot-candidate-v1/readiness-report.json)仍为 `NOT_READY`：缺少本领域真实 Gold 数据、基于该数据的模型选择、教师一致性以及校准和公平性证据。确定性的对抗测试 Pilot 门禁已通过。这些状态对应报告生成时的证据，后续判断应以重新运行门禁和最新报告为准。

本地基线选择了通过便携版 llama.cpp 运行的 Qwen3 4B Q4_K_M。模型及运行时文件放在 `lab/` 下被 Git 忽略的目录中；当时的 14 GB 内存测试机上，Qwen3 8B 在固定测试集的 4 个样本中只完成 1 个，因此未被选用。这个硬件结论不应外推到其他机器。

Zenodo JorGPT 记录 `18981627` 可作为经过治理、仅用于评测的外部基准。其 3,041 份匿名真实答案来自目标场景之外，只有单位教师评分且 Rubric 由机器生成，不能满足 Pilot Gold 数据或训练门禁。

## 数据与输出约束

- 不使用私人或可直接识别身份的学生数据。公开真实答案来源须经过许可与用途审查、完整性锁定、字段最小化、隐私扫描，并明确可支持的证据范围。
- 每条合成数据必须设置 `synthetic=true`；模拟适配器输出必须设置 `mock=true`，并包含 `MOCK_OUTPUT`。
- AI 评分输出通过数据结构校验后才能作为建议；`suggested_score` 必须处于 `[0, max_score]`。
- 作文和论述题必须进入人工复核；OCR 置信度低于 `0.85` 时也必须进入人工复核。
- 命中的 Rubric 采分点必须有来自答案本身的证据；提示词、模型和 Rubric 输出必须带版本信息。
