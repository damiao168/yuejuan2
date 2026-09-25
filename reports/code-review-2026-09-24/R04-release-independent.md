# R04 — S06 发布后报告版本独立复核

## 结论

静态复核确认 S06-F01：生产学生学习报告会把 `submission_grade` 中 `status='published' AND locked=true` 的成绩和 `final_grade.score`，与查询时最新的 AI／人工反馈拼在一起。相关反馈行没有绑定发布版本或该 `final_grade` 的 `source_id`；发布后新增合格 AI grade 或 human grade 后，下一次报告请求会呈现新反馈／错因与原发布分数。结果是报告内部版本不一致，属于 P2 数据完整性问题。当前没有运行服务或连接数据库，因此没有运行时复现。

另核实 `answer_segment_answer`：管理员可以在发布后通过生产写入口追加答案行；学生报告查询会取最新 `answer_payload`，但 `StudentReport` 不返回该字段，也不从它生成学生反馈。最新 payload 确实会进入教师端题目分析的选项分布，所以发布后的答案修正可改变教师分析视图；现有证据不支持把它描述为学生发布成绩中的实际答案漂移。学生门户的正式结果／题目接口从发布快照读分数、反馈和 `actual_answer`，此路径有 release 版本绑定。

## 报告查询与触发链

`report/store_postgres.go:136-145` 以已发布且 locked 的 `submission_grade` 构造学生列表。`report/store_postgres.go:160-196` 用 `final_grade` 的 `score/max_score/source` 作为题目成绩，并分别在 `:179-185`、`:186-192` 对同一 `answer_segment_id` 取最新成功 `ai_grade`、最新未删除 `human_grade`；两个 lateral 都没有读取 `score_release_question.source_type/source_id`、最终成绩来源 ID、release ID 或发布时间。

`report/store_postgres.go:246-260` 将这些当前 AI `student_feedback/missing_points/risk_flags` 和 human `student_feedback/comments` 变为报告字段。`report/stats.go:16-61` 再把反馈与错因同时放进顶层汇总和每道题；题目分数仍来自查询中的 `final_grade.score`。所以同一份报告确实能出现旧发布分数和新反馈。

可写入入口也没有发布状态前置检查。主观 AI 评分路由 `server/routes_grading.go:31` 只要求 `grading:manage`；`subjective/handlers_grade.go:32-64,168-178` 读取题段并创建评分；`subjective/store_postgres.go:415-460` 将其插入 `ai_grade`，不检查考试 release 状态。人工评分由 `server/routes_review.go:34,49` 的 `review:manage` 建任务、`review:work` 或 `review:manage` 提交；`review/store_postgres_tasks.go:25-48,257-305` 创建并提交任务时未检查 release 状态，提交会插入 `human_grade`。因此触发需要具备相应租户权限并指向本租户题段／已分配 review task；它不是学生能直接写入，也不是跨租户读取。`grading:manage` 的路由 guard 在 `server/router_guards.go:175-176`；学生报告 handler 在 `report/handlers.go:22-42` 要求 `report:read`，或要求 `student:report:read` 且 student scope 必须等于路径 student ID。查询始终以用户 `TenantID` 传给 store。

## 学生可见与下载边界

`server/routes_release.go:63` 将 `/api/v1/students/{studentId}/reports/{examId}` 暴露给已认证用户；handler 的自助权限分支允许学生读取本人学习报告。该 API 可返回上述漂移字段。静态前端搜索未发现 `apps/student-portal` 调用这个 `/reports/` 路径；学生门户当前使用 `/api/v1/student/exams/{examId}/result` 和 `/questions/{questionId}`（`studentportal`/`scorerelease` 注册于 `server/routes_release.go:38`、`scorerelease/handlers.go:81-84`，前端请求在 `apps/student-portal/src/api.ts:194-202`）。`scorerelease/student_detail_postgres.go:11-33` 先解析当前发布 release，再按该 release ID 读 items；`scorerelease/store_postgres.go:341-420` 以该版本构造结果和问题；`questions` 在 `:912-938` 只读该 release 的 `score_release_question`；`student_analytics.go:67-87` 从快照投影分数、反馈和（可见时）`actual_answer`。快照字段在发布时写入（`scorerelease/store_postgres.go:716-735,827-845`），迁移触发器拒绝修改已发布 release 及其 item/question 行（`migrations/000088_storyA18_score_releases.sql:115-155`）。因此发布后新建 AI/human grade 或 answer row 不会改变学生门户正式成绩页的快照内容；发布新 release 后显示新版本属于预期切换。

另有旧成绩接口 `/api/v1/students/{studentId}/exams/{examId}/grade`（`server/routes_release.go:40`）：`score/store_postgres.go:395-412` 读取已发布、locked 的 submission grade 和 final grade 列表，不返回上述 AI/human 反馈。题目回答图片接口根据发布 release 的学生／提交／题目关系授权后读取 answer-segment crop（`scorerelease/store_postgres.go:633-669`）；它没有读取 `answer_segment_answer` 文本或 payload。

报告导出是教师端下载：`server/routes_release.go:64` 要求 `report:export` 并限定 exam scope，`report/handlers.go:89-105` 返回带水印 CSV。`report/stats.go:398-428` 的 CSV 只写班级／考试统计和 grading-quality 指标，不导出学生反馈文本或答案 payload；其中 `AI adoption rate` 等质量指标仍基于当前报告 dataset，故可能随后续 AI/human rows 改变。未发现学生可下载该 CSV 的路由。

## `answer_segment_answer` 复核范围

`server/routes_grading.go:8` 把 `PUT /api/v1/answer-segments/{id}/answer` 接到 `RecordAnswer` 并要求 `grading:manage`。`grading/handlers.go:32-45` 调用 store；`grading/store_postgres.go:20-44` 先做 tenant + segment 存在校验，再插入一条新 `answer_segment_answer`。校验 SQL `:234-243` 只查 segment 存在，不检查考试是否已有 published release。

报告 SQL `report/store_postgres.go:172-178` 取最新 answer payload；`report/stats.go:119-160,296-305` 将其用于教师 `/reports/questions` 的选项分布。由此可静态确认，发布后答案更正可改变教师题目分析。学生报告构造器没有把 `gradeRecord.AnswerPayload` 暴露到响应（`:16-61`）；学生正式 release 结果则将实际答案固化到 `student_explanation`，之后按 release ID 读取。因此没有证据证明该表的最新答案写入会直接改变学生发布结果中的 `actual_answer`。

## 证据性质与限制

以上均为静态路由、handler、SQL、快照写入／读取和数据库触发器证据。没有启动 API、运行 Postgres、改写答案／评分记录或进行浏览器端到端复现；“下一次请求会变化”是由同一 SQL 的最新行选择和生产写入链推得的执行结果，不是本轮的运行时观察。工作树检查时已有多处用户改动；本复核只新增本报告，没有修改业务代码或重置文件。
