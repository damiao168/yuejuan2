# AI 建议失败诊断

普通评分建议仍使用 `/api/v1/answer-segments/{id}/subjective-ai-grade`。输出没有正常完成或未通过校验时返回 `502 ai_output_incomplete`，没有成功建议可供采纳。模型不可用保持 `503`；没有诊断的旧协议不能据此认定输出完整。

`execution_diagnostics` 是可选白名单对象。`attempts` 与 `provider_request_count` 记录实际派发次数，不等于 worker 的租约尝试次数。`usage_status` 为 `reported`、`partial`、`unavailable` 或 `invalid`，token 字段缺省表示未知。`unavailable` 不带 usage；完整用量至少报告 input/output/total。诊断不含供应商正文、任意错误、URL 或供应商关联号。供应商关联号仅经过有界格式检查后用于服务端日志。

`recovery_attempted` 当前固定 false，`recovery_blocked_reason` 可为 `not_enabled`。已知实际派发过供应商请求后，Go 和 worker 不自动重开外层推理；进程内幂等缓存不等于持久化调用预算。P2 的跨层预留和一次扩额尚未启用。

内部 worker execute 的失败响应可包含 `task_failure_recorded` 和 `retryable`。只有来源 run 与当前租约失败在同一事务提交后，网关才返回 `task_failure_recorded=true`；Python worker 据此跳过重复 failure 回调。缺失或非布尔标记、未知错误码、超限或损坏响应不能证明失败已经提交。租约过期或提交失败仍遵守既有存储错误流程。

批次详情与评分轮次批次列表增加可选 `failure_summary`，只包含固定失败类别、数量、尝试/调用计数和用量状态。包含旧记录导致总次数不可知时省略次数，不能把已知部分当作完整总量。

教师任务上下文增加可选 `ai_failure.failure_kind`。它只关联当前租户和任务、答题片段/题目、最新答案、运行冻结的精确题目快照、冻结量规和所属评分轮次；数学还校验当前证据、修订和裁剪绑定。最新相关运行成功或处理状态会替代旧失败。迁移前没有快照身份的运行不猜测绑定。

`ai_second_opinion` 的当前建议与最多 20 条可采纳历史也只包含同一答题片段、题目、最新答案、精确快照及冻结量规的记录；旧版本仍保存在成绩管理历史中。来源运行缺少快照或没有来源运行时保持未知，不把当前快照补造为历史依据。

AI 建议不可用时，工作台隐藏旧建议与第二意见，并禁止该任务快捷确认；教师仍可保存和提交人工评分。诊断读取暂时失败时返回通用人工提示，不向教师暴露内部错误。提示不是创建了新人工任务的承诺，也不增加教师触发模型调用的权限。暗样本仍使用现有合成任务协议，不从普通来源诊断反查 Gold。

权威公开结构见 OpenAPI 的 `ExecutionDiagnostics`、`SubjectiveAIExecutionFailure`、`BatchFailureSummary`、`ReviewAIFailure`；Python v1/v2 JSON schema 对应的诊断约束与 Go 验证一致。数据库使用 `000195` 保存诊断，`000197` 为新 run 保存快照身份；旧数据不回填猜测值。
