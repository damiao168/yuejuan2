# 评分模块复核记录

## ConfirmRuleGrade 完成计数疑点（未改逻辑）

`services/api-gateway/internal/grading/scoring_run_omr.go` 的 `ConfirmRuleGrade` 在写入 `question_grade` 后递增 `auto_confirmed_count`，并在同一条更新中依据 `queued_count=0`、`failed_count=0`、`review_count=0` 设置运行状态为 `completed`（约第 482–485 行）。这段条件没有在本处显式比较 `total_count` 与自动/人工确认总数，也没有再次确认所有题块都已有成绩。需要结合 `queued_count`、`review_count` 的维护来源及运行刷新流程确认是否可能提前完成；本轮仅记录疑点，未改变业务逻辑。
