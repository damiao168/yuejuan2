# STORY-060 客观题自动化产能兑现与答卷完整性保障

## Status

Planned（2026-07-26 起草，待实现复审）

## First-principles decision

两条第一性判断决定本 Story 的取舍：

1. **该有分的人都有分，是教育产品唯一不可妥协的不变量。** 当前发布门禁的 `missing_final_grades` 只扫描已存在的 `answer_segment`——一个学生的答卷没有被扫描进系统，就没有 submission、没有 segment，门禁天然通过。漏扫的学生不是得 0 分，而是从成绩表中静默消失。全仓库没有花名册对账、没有缺考（absent）概念。分数缺失学生会来找老师，**跨学生的成绩错位则没有任何人能发现**。
2. **STORY-056 建成的客观题自动化机制没有兑现任何产能收益。** OMR 判分、审批闸门、不可变校准会话全部真实建成，但没有任何一个模板通过校准闸门。根因不是阈值太严，而是校准范式选错了作用域：绑定在 `(template_id, template_content_hash, question_id)` 三元组上，20 道选择题需要约 2000 次人工标注才能全部批准——没有任何教务会在首考前完成。同时样本池预筛 `decision='selected' AND confidence >= 0.98`，只在模型已经自信的子集上度量精度，系统性高估，存在"批准了校准、然后大规模自动确认错答案"的隐患。

因此本 Story 只做两件事：让客观题自动化真的省人力，并保证不丢学生、不错位学生。主观题 AI 效果、学生端、治理合规层全部明确推后。

## Goal

一场 500 名考生的考试，客观题人工确认率降到可承受区间（目标 ≤20%，但**偏置修正优先于确认率指标**——若分层抽样后实测精度不足以支撑自动确认，确认率不达标是可接受结果，不允许为达标回退抽样策略），且成绩发布前系统能够回答："应到 500 人，实收多少、缺考多少、身份未决多少"，任何一项未决即阻断发布。

## User outcomes

- 教务：开考前打印带学生身份条码的答题卡；扫描后系统按条码自动归属答卷，无条码或条码冲突的页面进入人工匹配队列，绝不按页数猜测归属。
- 教务：发布成绩前看到花名册对账表（已评分 / 已标记缺考 / 未匹配待处理 / 缺页待处理 四态），未决项阻断发布；缺考学生显式记录且不计入班级均分。
- 阅卷教师：选择题由通过校准的 OMR 自动确认，人工只处理模糊/空白/低置信度案例；不再面对每题上千条的重复确认任务。
- 教务：扫描失败的文件可以在批次内重跑、同一文件可以修复后重传、被图像质量误判的页面可以人工放行（quality override），不再出现"卡死且无路可走"的状态。
- 管理员：发布门禁面板上的每一项指标都有真实生产者，不再有恒为 0 的假指标。

## Existing capability and real gaps

### Reuse

- 条码签发与验证：`internal/capture/barcode_store.go` 已有页级 HMAC 条码（`EDUGRADE_BARCODE_HMAC_KEYS` 多版本密钥）。
- OMR 校准：`internal/grading/omr_calibration.go` + migration 000041 的审批闸门、双人控制、不可变会话语义全部保留，只改作用域与抽样。
- 人工匹配队列：STORY-055 的 candidate/page matching 与 registration review 流程。
- 采集批次生命周期：`internal/capture/` 的 batch/file/page 状态机。

### Gaps that block real use

1. `BarcodeClaims` 载荷不含 `student_id`/`sheet_serial`，条码只能证明"这页属于这场考试"，不能证明"这页属于哪个学生"。**该载荷进入 HMAC 签名，学校一旦开始印卡即不可回退，必须本期定稿。**
2. 无 roster/absent 概念（全仓 grep 零命中）；`score/store_postgres.go` 的成绩聚合以 `submission` 为基准而非应考名单。
3. OMR 校准按题目作用域 + 自信样本预筛（`populateOMRCalibrationCasesTx` 的 `WHERE decision='selected' AND confidence >= $9`）。
4. 采集不可恢复：`QueueBatch` 只捞 `status='uploaded'`（失败文件批内不可重跑）；同 sha256 直接判 duplicate（同文件不可重传）；`quality_override` 恒写 `'{}'`（误判页永久卡死）。
5. `score_anomaly_unconfirmed` 门禁无生产者，恒返回 0 却展示给管理员。
6. `human_grade` 不关联 `ai_grade`，未来所有人机对照数据只能按时间近似匹配。
7. `grading_mode` 为纯装饰字段（全仓零消费方），界面上选"双评+仲裁"不改变系统行为。
8. 公式题/编程题可配置但无评分路径（`grading/engine.go` 返回 unsupported），是开考当天才暴露的配置陷阱。（已在独立会话处理，本 Story 只验收不重复实现）

## Supported scope

按执行顺序，五个主项 + 随行小项：

### 1. 条码承载学生身份（不可回退项，最先定稿）

- `BarcodeClaims` 扩展 `student_id` 与 `sheet_serial` 字段，HMAC 载荷版本升级（新 key_id），**旧版本条码继续可验证**（多版本密钥机制已存在）。
- 解码成功才建立答卷归属；解码失败、无条码、同一 serial 重复出现的页面一律进入人工匹配队列，绝不按固定页数切分（固定页数切分在扫描仪卡纸时会导致其后所有学生成绩静默错位）。
- 打印包（PDF 底图叠加条码）本期只做**最小验证闭环**：30 份试印 + 真实扫描仪回扫 + 解码率/配准偏差报告，通过后才解锁批量打印能力的后续投入。打印保真度是第 2、3 项的物理前提。

### 2. 花名册对账与缺考语义

- 考试关联应考名单（复用 `class`/`student` 既有表，考试级 roster 视图）。
- 对账四态：已评分 / 已标记缺考 / 未匹配待处理 / 缺页待处理。
- `absent` 显式入 `final_grade`（不计入均分），新增发布门禁 `missing_submission_unresolved` 与 `unidentified_submission`，均为硬阻断。
- 成绩聚合改以 roster 为基准，而非以已存在的 submission 为基准。

### 3. OMR 校准范式修正

- 校准作用域从 `(template, content_hash, question_id)` 上移到模板 OMR profile 级（template + content_hash + profile_hash + reference）：同一 content_hash 已锁死印刷版式，OMR 读取是几何与成像属性而非题目语义属性。全模板合计 ≥100 样本、每个选项位 ≥10 样本，替代每题各 100。
- 样本池分层抽样：必须纳入 ambiguous / blank / 低置信度案例，废除 `decision='selected'` 高置信度预筛。0.98 置信度门槛与人工标注 `expected_options` 环节保持不变。
- 新增"校准场次"：开考前扫 N 份试填卡跑 OMR、人工标注后即可产出可批准的校准，解决首考冷启动。
- 校准结果随模板版本克隆；补齐校准会话的前端标注界面（当前只有 API）。
- 审批/撤销的双人控制与不可变会话语义不变。

### 4. 采集链路自救能力

- 失败的 `capture_file` 支持批次内重跑（放宽 `QueueBatch` 的状态筛选）。
- 同 sha256 文件在原文件失败/被废弃后允许重传（duplicate 判定加状态条件）。
- `quality_override` 真实生效：人工放行被质量检测误判的页面，带原因与审计。
- 旧的 submission 直传管线（产出无 crop 的 segment）下线或显式标记废弃，关闭静默废数据通道。

### 5. 门禁与数据诚实化（随行小项，可先行合入）

- 删除 `score_anomaly_unconfirmed` 假门禁及其 UI 展示。
- `human_grade` 新增 `ai_grade_id` 关联列，提交时服务端自取（不信任客户端）。
- 阅卷工作台"置信度 0%"改为"尚未完成生产校准"状态文案。
- `grading_mode` 在考试表单中诚实标注（字段仅作登记，真实双评按题目配置双评策略）。
- 申诉列表 fail-open 修复与公式/编程题开考禁用（独立会话已处理，本 Story 验收）。

## Explicitly deferred

- OCR 结果自动写入 answer_segment_answer（吞吐问题非正确性问题，且需先有 CER/WER 基线反推放行阈值，规划为 STORY-061）。
- 统一分页与评分运行分批断点续跑（随 STORY-061 的规模验收一起做）。
- 主观题 AI 影子批量通道、置信度校准、作文解除 shadow_only（依赖 061 的答案文本输入，规划为 STORY-062）。
- 学生端 portal 与申诉闭环（规划为 STORY-063，fail-open 修复是其硬前置且已完成）。
- 多 Agent 编排、Qdrant 语义检索、答案分组（输入尚不存在；orchestrator 中九个无执行体的 agent_type 枚举建议随本期删除）。
- 治理合规层（字段脱敏、归档保留、许可证、导出审批、缺失角色）——横向加法，主链路跑通后一次性交付。

## Security, privacy and audit

- 条码载荷中的 student_id 为 UUID 而非学号明文；条码解码结果与人工匹配决定全部写审计。
- 缺考标记、quality override、对账未决项的人工处置均需 reason 且入审计。
- 校准审批/撤销保持双人控制；分层抽样不改变"人工标注才是 ground truth"的边界。
- 不改变 AI 硬边界：confidence 恒 0、shadow_only、AI 不写 final_grade。

## Acceptance criteria

1. 500 人模拟考试（可复用 fujian-2024 仿真数据），花名册对账表四态齐全，人为抽走 3 份答卷后发布被 `missing_submission_unresolved` 阻断，标记缺考后可发布，缺考不计入均分。
2. 扫描仪卡纸场景仿真（一份 PDF 中间多一页）：错位页进入人工匹配队列，其后学生归属不受影响。
3. 新条码经 30 份试印回扫，解码率与配准偏差达标（阈值以试印报告为准）；旧版条码仍可验证。
4. 一个 20 题模板经"校准场次"完成全模板校准（人工标注 ≤200 次），审批后自动确认率与人工抽查一致性达标；分层抽样样本池含 ambiguous/blank 案例的比例可查证。
5. 失败 capture_file 批内重跑成功；同文件重传成功；误判页 override 后进入配准流程，全程有审计。
6. 发布门禁面板无恒零指标；`human_grade.ai_grade_id` 在有 AI 建议的提交中正确落库。
7. 全量门禁绿：`go test ./... -count=1`、三个 worker pytest、`npm run typecheck`、compose config、`check:lab-integration`。

## Verification plan

- 单元/集成：roster 对账查询、条码新旧版本验证、分层抽样查询、override 状态机，全部 memory+postgres 双实现测试。
- e2e：在 `internal/server` 新增 story060 场景（复用 e2ePostgresRouter），覆盖验收标准 1/2/5。
- 规模验证：`tools/import-fujian-2024-math-simulation` 产出的数据上跑对账与发布门禁（该工具改造为走产品 API 属 STORY-061 范围，本期允许其现状）。

## Plan review

### Why this is the correct next Story

三份独立路线提案（价值优先/风险优先/地基优先）经三位角色评审（产品/架构/一线教师），对本 Story 的五个主项达成三方全票。核心理由：(a) 条码载荷是唯一有不可回退截止时间的决策；(b) 花名册对账是唯一不可妥协的完整性不变量；(c) OMR 校准范式修正是全项目单位投入产出最高的改动（2000 次标注 → ≤200 次），直接决定 STORY-056 的投资是真收益还是表演。

### Scope corrections from previous planning

- V1.0 总控计划中"STORY-057 主观题 AI"的编号已被 lab 接入线占用，本 Story 顺延为 060，后续能力重新编号（见 docs/stories/README.md 编号语义说明）。
- STORY-056 验收清单中"Playwright 真实流程通过"的勾选与仓库现状不符（无任何 Playwright 配置），应予修正；本 Story 不以该勾选为前提。

### Production risks to resolve during implementation review

- 打印保真度是物理前提：试印验证不通过则第 2、3 项的投入全部冻结，需重新评估条码方案（尺寸/纠错级别/位置）。
- 分层抽样修正偏置后，实测精度可能不足以支撑自动确认——这是可接受结果（宁可人工确认率 40%，不要混入系统性错判），验收时不允许为达标回退抽样策略。
- 校准标注的人力归属需在流程上写死（建议：教务负责校准场次组织，学科教师负责标注），避免隐藏成本在首次真实使用时变成扯皮。

## Decision

待评审。实现顺序建议：第 1 项（条码定稿）→ 第 5 项（随行小项，部分已并行开工）→ 第 3 项（校准范式）→ 第 2 项（对账）→ 第 4 项（自救）。
