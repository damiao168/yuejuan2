# R03 — S04-F02 取消后规则候选仍可写入（独立静态裁决）

## 裁决

**竞态路径在代码上可达，且与 S05-F01 是不同入口／写入路径；建议保留为独立发现。** `ProcessRuleCandidates` 在开头读一次 run 状态，之后跨候选执行；若管理员此时取消并 finalize，后续规则确认不重新校验 run 状态，可以插入当前确认分并把 `cancelled` run 改成 `completed`。另一个非自动确认分支可在取消提交后插入新的 `pending` review task。本文只做源码与 PostgreSQL 锁序静态审查，未动态复现。

## 可达路径与证据

- 生产入口可并发使用：`routes_grading.go:23,25,28` 注册评分启动、评分摘要和取消端点。`handlers.go:269-288` 先提交 `StartScoringRun`，随后才同步调用 `ProcessRuleCandidates`；评分摘要查询 `scoring_run_query.go:32-39` 返回最新 run（不按状态过滤），可在启动请求仍处理候选时获知 run ID。取消处理器在 `handlers.go:353-378` 调用 begin、清理 worker 后 finalize。
- `scoring_run_process.go:7-16` 对 run 只做一次普通 `SELECT EXISTS`，没有 `FOR UPDATE`；随后先 materialize 候选列表。`scoring_run_process.go:36-54` 逐个加载上下文、运行规则引擎，自动确认时才调用 `ConfirmRuleGrade`。这段候选处理与取消事务没有共同的事务或覆盖全程的锁。
- 自动规则分支：`scoring_run_omr.go:411-419` 的确认事务只 `FOR UPDATE OF seg`，未锁或读取关联 run 状态；`:447-463` 插入 `is_current=true,status='confirmed'` 的 `rule_confirmed` grade，然后按 tenant/run ID 更新 run，`WHERE` 没有旧状态条件。该更新在 `queued_count=0,failed_count=0,review_count=0` 时直接设置 `status='completed'`。取消 finalize 会在 `scoring_run_lifecycle.go:97-101` 把 run 设为 `cancelled`，并把 queued/review 计数清零。
- 该计数条件有正常可达样例：启动规则输入会把已确认答案写成 `decision='confirmed'` 候选（`scoring_run_start.go:217-240`），只有未确认候选才在 `:241-245` 创建初始人工任务。纯规则输入 run 的初始状态由 `scoring_run_routing.go:65-72` 设为 `processing`；取消 finalize 可在没有活动 OMR worker 时完成。于是执行顺序 `ProcessRuleCandidates` 初查 active → 管理员取消并 finalize → 处理器恢复并调用 `ConfirmRuleGrade`，可在 cancelled run 上写当前分并将状态改为 completed。
- 非自动确认分支也不安全：`scoring_run_process.go:41-56` 对需要人工复核的候选直接执行 autocommit `INSERT review_task`。取消事务在 `scoring_run_lifecycle.go:94-101` 仅取消当时已存在的活动任务并提交；若候选插入发生在它之后，新 task 不在取消更新范围内。现有 partial unique index `migrations/000130_review_round_identity.sql:3-5` 只覆盖活动状态，`cancelled` 行不参与唯一约束，故不会阻止后续插入。之后计数更新 `scoring_run_process.go:60-64` 也没有状态条件；最终 `RefreshScoringRun` 会在 `scoring_run_lifecycle.go:192-199` 对 cancelled run 提前返回，因此该任务可以保持 pending 并挂在已取消 run 上。

## PostgreSQL 锁序与保护核对

取消 finalize 先在 `scoring_run_lifecycle.go:69` 对 run 行取得 `FOR UPDATE`，再更新现存 review task，最后写 cancelled 状态并提交（`:94-105`）。规则确认事务按相反对象顺序先锁 answer segment（`scoring_run_omr.go:411-419`），写 grade 后才更新 run（`:447-463`）。当取消先获得 run 锁并提交后，确认事务才开始／继续，segment 锁不与取消冲突；它之后可正常更新已取消的 run。若确认先拿到 run 的更新锁，取消会等待；这只决定哪笔事务先完成，不会为取消先提交的交错提供保护。

检查了相关迁移中的状态约束：`migrations/000038_story056_scoring_recovery.sql:4-13` 的 CHECK 只枚举合法状态，明确同时允许 `cancelled` 和 `completed`，不约束状态转移。未找到附着于 `scoring_run`、`question_grade` 或 `review_task`、用于禁止上述终态写入的业务触发器。没有发现会使上述“取消先提交、候选后恢复”交错不成立的行锁、部分索引或约束。

## 与 S05-F01 的关系、修复边界与限制

S05-F01（`S05.md:9-16`）是公开 review 管理／提交路径重新激活**已被取消的 task**后写入人工分；这里是启动请求中已通过 active 检查的 stale `ProcessRuleCandidates` 后续写入自动分，或创建新 task。即使修复 review assignment 对 cancelled task 的过滤，也不会覆盖 `ConfirmRuleGrade`；仅在人工提交时检查 run 状态也挡不住取消后新 task 的创建。因此应作为独立竞态发现记录，同时归入“取消后不得写入”的共同不变量。

修复至少需要让候选处理写操作与取消状态在数据库中原子协调：自动确认在提交 grade 前验证关联 run 仍可写，并使用能与取消事务互斥的 run 锁／条件更新；review task 的创建也应与 run 状态检查置于同一事务，且只在活动 run 时递增计数。单纯在入口保留一次 active 检查不够。

限制：没有构造隔离 PG 并发屏障，也没有运行应用测试；结论来自生产 handler、SQL、PostgreSQL 行锁次序及迁移约束的静态审查。可触发窗口要求 start handler 仍在处理候选时管理员并发取消；处理多个／较慢候选会延长窗口，但本文未测其出现概率。
