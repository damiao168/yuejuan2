# 后端注释审读发现

本文件只登记现有实现的疑点，注释整理未修改业务逻辑。以下结论来自静态调用链审读，尚未补充数据库集成复现。

## 考试基本信息更新可绕过考生名册冻结

- 条件：有考试编辑范围的调用者，向 `UpdateExam` 提供当前 `ExpectedRevision` 和非空指针 `ClassIDs`，考试已处于 `ready`、`collecting`、`grading`、`reviewing` 或 `finalized` 等准备完成但尚未发布的阶段。班级列表即便未变化，也会触发名册重建。
- 入口：`services/api-gateway/internal/exam/store_postgres.go:160` 的 `UpdateExam` 在第 177 行仅检查 `IsCoreLocked`。`services/api-gateway/internal/exam/status.go:75` 的该检查只锁定 `published` 和 `archived`。
- 后果：`services/api-gateway/internal/exam/store_postgres.go:232` 更新班级后无条件调用名册刷新；`services/api-gateway/internal/exam/candidate_snapshot_postgres.go:19` 的 `RebuildCandidateSnapshot` 先删除旧快照，再根据当前有效学籍重新写入，已冻结名册可能随班级或学籍变化。
- 不一致：显式刷新入口 `services/api-gateway/internal/exam/candidate_snapshot_postgres.go:48` 只允许 `draft`、`configured`，其他阶段返回 `ErrCandidatesFrozen`；基本信息更新路径未执行这一边界。
- 后续修复建议：在所有可能重建名册的业务入口统一执行冻结约束，并覆盖准备完成后携带原班级列表或新班级列表更新的回归场景。此次仅修正了帮助函数中原先声称调用方已保证冻结的注释。

## 学生本人整页图片的题目范围校验弱于切题图片

- 位置：`services/api-gateway/internal/scorerelease/store_postgres.go` 的 `StudentPaperPageImage`，`highScore=false` 分支。
- 静态审读结果：该查询校验当前发布版本、本人试卷和 `show_question_scores`，但题目只与当前 `answer_segment` 匹配，没有要求它存在于 `score_release_question`。同文件的 `StudentQuestionImage` 则有这一发布题目关联。
- 可能影响：若发布后新增了该试卷的切题记录，本人整页接口可能接受未包含在发布快照中的题目 ID。是否可由现有业务流程触发，仍需数据库集成复现；现有内存测试走另一实现，不能证明 PostgreSQL 分支满足同样约束。
- 后续建议：确认整页图片应采用的题目范围，并覆盖发布后新增切题记录的场景。本次仅将类型注释收窄到实现已有的保证，未修改查询。
