# 独立复核：AI 主观题覆盖与阅卷主流程

复核范围：当前工作树中 `paper/scoring_coverage.go`、paper readiness advisory、评分运行路由与 `StartScoringRun`、以及主观题批次入口和 worker。只读检查；未改业务源码，也未运行测试。

## 结论

确认存在主流程覆盖落差。`short_answer`、`calculation`、`essay`、`discussion` 被全局覆盖表归为 AI-assisted，且因此不出现在“全人工评阅”准备提醒中；但考试工作台的正常“开始评分”流程不会创建主观题批次或 `ai_grade` worker task。它只为这些题块创建普通人工 review task。也就是说，独立主观批次确实能产出 AI 建议，但其存在不等于标准评分 run 会自动触发它。

因此，若产品语义是“开始评分时主观题也自动获得 AI 建议”，当前实现不满足该语义；用户通过标准阅卷入口处理这四类题时会落到纯人工评分。若 AI 批次本来就要求独立、显式启动，则不是 worker 丢任务，而是覆盖分类和考前提醒没有表达“需要另行选段并启动 AI 批次”。

## 发现

### [P2] AI-assisted 覆盖分类没有接入标准评分 run

触发条件：试卷含 `short_answer`、`calculation`、`essay` 或 `discussion`，管理员只从阅卷工作台点“开始评分”。

`paper/scoring_coverage.go:21,24,33-34` 将这四种题型与主观适配器支持类型镜像，并让 `HasAutomatedScoringPath` 对它们返回 true。`paper/template_validation.go:216,223` 因而跳过这些题型；考前“全人工评阅题目”advisory 只针对完全没有覆盖路径的题型，消息在 `paper/template_validation.go:230`。但评分 run 的路由在 `grading/scoring_run_routing.go:40,49,52,59` 只把规则题和 OMR 题送入自动路径；对上述四种题型会落入默认 `rule_review_required` 人工分支。`grading/scoring_run_start.go:216,317` 对路由结果只创建 review task，没有创建主观 run 或 `ai_grade` task；HTTP handler 在 `grading/handlers.go:275,280` 只调用 `StartScoringRun` 和 `ProcessRuleCandidates`。

标准评分 readiness 会把未自动处理的可评分题块计入人工候选，工作台显示“预计人工”计数（`grading/scoring_readiness.go:255`、`ExamScoringPanel.tsx:68,83`），但不提示 AI 建议需要另行启动。考前 advisory 对上述题型则完全不提醒。最终这些题会有人工任务，却没有由该评分 run 生成的 AI 建议。该任务还会被标成 `rule_review_required`，工作台显示“规则评分需要确认”及“规则未能自动确认结果”（`grading/scoring_run_routing.go:49,52,59`；`gradingWorkbench.model.ts:32,45`）；而 essay 等题型在该 flow 中并未先执行规则判分。

主观批次是独立入口：路由分别暴露 scoring-runs 与 `/subjective-grading-batches`（`server/routes_grading.go:23,32,36`）；批次 UI 要管理员输入答题片段 ID，再分别创建批次与点击“入队评分”（`SubjectiveGradingBatchPage.tsx:105,126,181,185`）。后端创建批次时 Store 保存 `planned` 状态（`subjective/handlers_batch.go:51`、`subjective/store_postgres.go:133`）；只有显式 `EnqueueBatch` 才由 Store 建 run 并创建 `ai_grade` 队列任务（`subjective/handlers_batch.go:87,155`）。worker 随后 claim `subjective-grading` 队列并调用 `/execute`、`/result`（`subjective-grading-worker/api.py:40,70,76`；服务端 worker 路由在 `server/routes_worker.go:34-36`）。在评分 run 及其处理调用链中没有到这些批次操作的调用。


## 边界核实

独立 AI 能力并非空壳：`subjective/validation.go:13-17` 支持这四类题，批次 handler 会验证题型并在显式入队时创建 worker task；worker 的 execute/result 流也已连通。所以准确结论是“有可单独启动的 AI 评分路径，但标准 scoring run 没有自动触发它”，不是“这四类题型完全没有 AI 能力”。此外，单题 `/answer-segments/{id}/subjective-ai-grade` 路由也单独连接到 `subjective.Handler.Grade`（`server/routes_grading.go:31`），同样没有从 `StartScoringRun` 调用。

当前树中的 `grading/scoring_runs_manual_only_test.go` 还把 `essay` 预期为 `rule_review_required`（`TestRouteScoringSegmentKeepsAutomatedRoutes` 中的 `ai assisted subjective` case），这验证了当前落点，却没有验证两条工作流之间的连接。
