# STORY-080：学校工作台按待办与进度重排

状态：Implemented（待评审）。依赖：STORY-064 可信聚合、STORY-079 首次启用引导。部分实现 STORY-076 的单场考试进度，不含多科 session 聚合和精确 ETA。

## 问题

旧首页按菜单模块分区（成员管理、考试管理、阅卷与成绩），左上角常驻几乎不变的成员人数。待办分散在三处，后端返回的图像质量阻断项没有进入待办。“待复核”在指标条按答卷份数、在事项表按复核项数显示。草稿和配置中的考试计入“进行中”。阅卷进度只按教师统计。待阅统计没有排除已取消任务和已发布考试的任务。

## 首页结构

1. 状态结论：一句话说明有没有事会卡住出成绩。
2. 需要我处理：只列学校管理员要动手的事，按“阻断出成绩、学生在等、可以发布、基础数据缺口”排序。
3. 考试进度：只列采集中、阅卷中、复核中、待发布的考试；准备中的考试收成一行。
4. 阅卷进度：仍有未完成阅卷任务时才显示；先按题，再按老师（按老师仅学校管理员可见）。
5. 最近动态：最多 5 条。

成员与班级不再常驻。首次使用仍由 ReadinessBanner 引导；平时只有“班级没有学生”“教师未设置任教学科”进入“需要我处理”，且只对持有 `org:manage` 的用户显示。

## 接口增量（`GET /api/v1/dashboard/summary`，只增不删）

首页所有考试类计数只覆盖未发布、未归档的考试，与 `active_exams` 和教师阅卷进度的范围一致。

`statistics` 新增：

- `preparing_exam_count`：准备阶段（draft、configured、ready）的考试数。
- `grading_exam_count`：grading、reviewing 的考试数。

`statistics` 口径收紧：

- `pending_review_question_count`、`pending_review_submission_count`：只计 pending、assigned、in_progress、returned 状态，且考试在 `active_exams` 中。已取消的任务不再算待阅。
- `pending_arbitration_count`、`pending_arbitration_submission_count`：只计 pending、assigned 状态（仲裁任务没有 completed，submitted 即已裁决，cancelled 无需处理），且考试在 `active_exams` 中的仲裁任务。旧口径把已裁决的任务也算成待复核。

`active_exams[]` 新增：

- `pending_arbitration_count`：本场待人工复核项数。
- `review_task_count`、`review_done_count`：本场小题阅卷任务数与已交数。排除 cancelled；已交指 submitted、completed。
- `roster_count`、`roster_submitted_count`：冻结名册人数，以及其中已有未删除答卷的人数。名册来源不可用时两者为 null 并返回告警；本场尚无冻结名册时 `roster_count` 为 0。

新增 `grading_progress`（阅卷任务来源失败时为 null）：

- `window_seconds`：近期完成量的时窗，固定 3600。
- `questions[]`：`exam_id`、`question_id`、`question_no`、`task_count`、`done_count`、`remaining_count`、`recent_done_count`。口径与 `review_task_count` 相同。`recent_done_count` 按已交任务的最后更新时间落在时窗内近似计算；任务交卷后若再次变更，按最近一次更新时间计入。

新增 `appeal_statistics`（没有 `appeal:manage` 时省略；来源失败时省略并返回告警）：

- `pending_assignment_count`：status=submitted，等待分派复核教师。
- `pending_decision_count`：status=under_review 且复核教师已提交意见，等待管理员裁决。
- 范围与教职工申诉列表的授权范围一致。申诉发生在成绩发布之后，因此不受“未发布考试”口径限制。

新增告警文案：`考试名册人数暂时无法统计`、`成绩复核申请暂时无法统计`。

## 界面约定

### 需要我处理

列：事项、考试、数量、影响、操作。按下表顺序；数量为 0 的行不显示。“考试”列：只涉及一场显示考试名；多场显示“{数量最多的一场} 等 {n} 场”；与考试无关显示“—”。

| 事项 | 单位 | 影响 | 操作 | 只涉及一场 | 涉及多场 | 级别 |
| --- | --- | --- | --- | --- | --- | --- |
| 答卷处理失败 | 份 | 会阻断后续阅卷 | 查看并重试 | `/exams/{id}/capture` | `/capture?issue=failed` | 阻断 |
| 答卷未匹配学生 | 份 | 无法计入学生成绩 | 确认学生身份 | `/exams/{id}/capture` | `/capture?issue=unmatched` | 阻断 |
| 图像质量需要处理 | 份 | 可能影响文字识别和切题 | 查看质量问题 | `/exams/{id}/capture` | `/capture?issue=quality` | 阻断 |
| 待人工复核 | 项 | 未确认最终得分 | 开始复核 | `/arbitration?status=pending` | 同左 | 阻断 |
| 成绩复核申请待分派 | 条 | 学生在等复核结果 | 去分派 | `/appeals` | 同左 | 等待 |
| 成绩复核待裁决 | 条 | 复核教师已提交意见 | 去裁决 | `/appeals` | 同左 | 等待 |
| 成绩待发布 | 场 | 阅卷已完成，检查后即可发布 | 去发布 | `/exams/{id}/scores` | `/scores?status=finalized` | 可发布 |
| 班级没有学生 | 个班 | 这些班级的答卷无法匹配学生 | 导入学生 | `/members/students` | 同左 | 基础数据 |
| 教师未设置任教学科 | 名 | 按学科查看阅卷进度时看不到这些教师 | 设置学科 | `/members/teachers` | 同左 | 基础数据 |

每场考试的数量来自 `active_exams[]` 的 `failed_count`、`unmatched_count`、`quality_issue_count`、`pending_arbitration_count` 和 `status=finalized`；总数来自 `statistics`。两行“成绩复核”只在返回 `appeal_statistics` 时出现；两行基础数据只对持有 `org:manage` 的用户出现。没有任何行时显示“当前没有需要处理的事”，副文案“新的答卷异常、复核申请和发布任务会显示在这里。”

### 状态结论

`<section aria-label="当前状态">` 内一句话。短语格式为“{数量} {单位}{事项}”，例如“1 份答卷处理失败”“3 项待人工复核”。基础数据行不计入结论。

- 有阻断行：`{阻断行数} 件事会卡住出成绩：{前两行短语，用“，”连接}`，超过两行时末尾加“等”。红色。
- 否则有等待或可发布行：`有 {行数} 件事等你处理：{前两行短语}`，规则同上。琥珀色。
- 否则：`目前没有卡住出成绩的事`；有阅卷中或复核中的考试时追加“，{k} 场考试正在阅卷”。绿色。
- `warnings` 非空时追加第二行“部分统计暂时不可用，结论可能不完整。”，且不使用绿色。

### 考试进度

- 准备中：draft、configured、ready、preparation_confirmed。其余未发布、未归档状态进表格；未知状态按“进行中”放进表格，不丢弃。
- 列：考试、阶段、当前进度、异常、下一步。“下一步”沿用现有 `examAction`。异常 = failed + quality_issue + unmatched。
- 阶段为五步：试卷 → 采集 → 阅卷 → 复核 → 发布。collecting、processing 当前步为采集；grading 为阅卷；reviewing 为复核；finalized 为发布。之前的步显示已完成。用有序列表，当前步加 `aria-current="step"`。
- 当前进度：采集中且 `roster_count>0` 显示“已收 {roster_submitted_count}/{roster_count} 人”，否则“已收 {submission_count} 份”。阅卷中、复核中显示“已阅 {review_done_count}/{review_task_count} 项”，有待人工复核时追加“，待复核 {n} 项”；`review_task_count` 为 0 或缺失时显示“暂无阅卷任务”。待发布显示“阅卷完成，待发布”。
- 排序：有异常或待人工复核的在前；再按离发布近的在前（待发布、复核、阅卷、采集）；再按创建时间倒序。最多 6 行，标题右侧“全部考试”。
- 表格下方：有准备中考试时只显示“另有 {n} 场考试在准备中”。考试列表不支持按状态筛选，入口只保留标题右侧“全部考试”，不再重复放按钮。
- 空状态：只有准备中考试时“暂无采集中或阅卷中的考试”；一场都没有时“暂无进行中的考试”，副文案“创建考试后，可从这里直接继续下一步。”

### 阅卷进度

只要某题或某位教师仍有剩余任务就显示。

- 按题：列为考试、题号、进度（已阅/总数和进度条）、剩余、近 1 小时已阅、预计剩余时间。按剩余降序，默认 5 行，按钮“展开全部”“收起”。时窗标签由 `window_seconds` 生成。
- 预计剩余时间：剩余为 0 显示“已完成”；时窗内已阅少于 10 项显示“样本不足”；否则小时数 = 剩余 ÷ (近期已阅 × 3600 ÷ window_seconds)，小于 1 显示“不到 1 小时”，不超过 24 显示“约 {向上取整} 小时”，更大显示“超过 1 天”。
- 脚注：“按小题阅卷任务计数，双评时每位教师各计一项；预计时间按近 1 小时速度粗略估算，速度变化时会偏差。”
- 按老师：沿用 ReviewTeacherProgress。默认隐藏没有任务的教师，切换按钮“显示无任务教师（{n}）”/“隐藏无任务教师”；排序改为剩余降序、在线优先、用户名。

### 最近动态

沿用原“关键进展”的内容和下钻，最多 5 条，没有动态时不显示。

### 移除

成员管理卡片、01–04 编号、阅卷与成绩卡片的指标条和重复入口。

### 标题文案（e2e 依赖）

h2：“需要我处理”“考试进度”“阅卷进度”“最近动态”。按题、按老师用 h3：“按题”“按老师”。每个标题所在区块用 `<section aria-labelledby>` 指向该标题；按题、按老师嵌套在阅卷进度内。按钮文案“显示无任务教师（{n}）”使用全角括号。

## 权限与失败语义

- 后端作用域规则不变：平台管理员 403；聚合不可用返回 `dashboard_summary_unavailable`。
- 新字段遵循 STORY-064：未知不写成 0。名册不可用时为 null 加告警；阅卷任务来源失败时 `grading_progress` 为 null，沿用“阅卷待办暂时无法统计”。阅卷或仲裁任务来源失败时，每场的 `review_task_count`、`review_done_count`、`pending_arbitration_count` 省略。
- 动态来源失败时 `recent_activities` 仍为 `[]`，失败只由告警说明。
- 前端兼容旧后端：新字段缺失时隐藏对应行或区块，或显示不带分母的文字，不把缺失显示成 0。

## 非范围

不新增表或快照，不改评分、申诉和成绩事实，不做多科 session 聚合和精确 ETA（STORY-076），不把系统运维状态放到学校工作台，成绩分析图表留在“成绩分析”页。

## 与 STORY-064 的关系

聚合口径沿用 STORY-064。STORY-064 验收中“6 份答题卡包含 107 道待阅题目”的首页文案由本故事的阅卷进度区块替代。

## 验收证据

- Go：`go test ./internal/dashboard/` 通过。覆盖已取消任务和已发布考试的任务不计待阅、仲裁只计 pending/assigned、每题与近 1 小时已阅（固定时钟）、每场计数、阶段计数、名册不可用时为 null 加告警、申诉统计按 `appeal:manage` 返回、动态来源失败仍返回 `[]`。包内 4 个 PostgreSQL 用例（含新增的名册人数、申诉统计两项）在迁移到 000225 的临时库上通过。
- 前端：`src/features/dashboard` 与 `DashboardPage.reviewTeachers.test.tsx` 共 41 个用例通过，覆盖新字段缺失时的降级、只有图像质量问题的考试、准备中考试折叠、无剩余任务时隐藏阅卷进度、状态结论各档文案和预计时间阈值。
- e2e（模拟接口）：`dashboard-workbench.spec.ts`、`school-admin-golden-path.spec.ts` 全部通过。
- 浏览器：1440 宽默认数据、空状态和 390 宽窄屏截图无控制台错误；窄屏下表格横向滚动。
- 契约：OpenAPI 只新增 Dashboard 相关 schema，SDK 已重新生成，`check-openapi-breaking` 通过。

## 相关修复

`000225_question_appeal_opinion_event.sql`：000092 的申诉事件类型约束漏了 `review_opinion_submitted`，复核教师提交意见会被数据库拒绝，`pending_decision_count` 因而一直为 0。新增用例 `internal/appeal/published_question_event_type_postgres_test.go` 在修复前失败、修复后通过。
