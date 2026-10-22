# AI 评分输出截断保护与异常诊断方案

日期：2026-10-18。

这是一份设计方案，不是实施记录。目标是让 AI 评分响应在“输出到达上限、被过滤、拒答、结束原因缺失或供应商返回未知结束原因”时安全转人工，并为后续一次受限恢复保留可核对事实。方案结合当前 `ai-services/grading_agent`、Go `subjective` 评分链、worker runtime 和现有评分工作台；不引入附件项目的源码、提示词或 UI。

P0/P1 的实现和本地验收进度见[实施记录](../../reports/ai-output-truncation-implementation-2026-10-18/implementation.md)。P2 尚未实现，自动扩额保持关闭；设计中的全生命周期预算不能当作当前实现保证。

## 1. 结论与分阶段范围

建议先做两个阶段，第一阶段默认关闭自动扩额，先交付可证明的安全边界；第二阶段在有持久化预算账本后再开启一次恢复。

| 阶段 | 交付 | 默认行为 |
| --- | --- | --- |
| P0：统一事实 | 所有评分 adapter 提取结束原因、请求 ID、用量状态和安全诊断；完整结果才可进入 schema/证据校验和成绩建议 | `length`、缺失/未知结束原因、拒答、工具调用、过滤结果直接转人工；不自动再调 |
| P1：失败可见 | v1/v2 错误契约、Go 错误映射、`GradingRun`/worker 失败详情和管理端显示统一 | 教师看到“AI 建议未完成，需人工评分”；不展示半截模型正文，不改变任务权限 |
| P2：一次受限恢复 | 新增持久化逻辑工作单元预算、调用尝试和 token 预留；由 Go 编排最多一次第二调用，Python 单次请求不再自带隐式恢复 | 只有明确 `output_limit` 且能力、上下文、剩余时间和预算都满足时才恢复；否则转人工 |

P0/P1 可以完全使用本地 fake adapter、HTTP fixture、内存 Store 和 PostgreSQL 隔离库验证。P2 可以验证调用次数、预算、租约和迟到结果规则；不能仅靠本地测试证明真实供应商恢复率、费用或评分准确率。

## 2. 为什么当前实现需要补这一层

当前评分入口和重试层分散在多个边界：

- Python `/grading/grade` 在 `ai-services/grading_agent/app.py` 内按 `model_max_retries` 重试；`app_v2.py` 有独立的重试判断。失败 inflight 删除后，相同幂等键可以再次开启一轮 Python 重试。
- `ai-services/grading_agent/model.py` 的兼容 adapter 主要解析 `content`；v1/v2 的 `request_structured` 也不能统一保证结束原因和角色事实。DashScope v1 的部分路径在解析成功后才保存 `last_usage`，截断或坏 JSON 可能丢失已知用量。
- Go `subjective/adapter_http.go` 和 `adapter_http_v2.go` 各有 HTTP 重试；它们复用 request ID 和 `Idempotency-Key`，但与 worker runtime 的 `MaxAttempts` 是另一层。
- `workerruntime` 会按租约和 `MaxAttempts` 安排失败重试，人工重新入队还可能增加一次机会。不能把 Python 的两次、Go 的两次和 worker 的次数写成一个总预算。
- `model_usage_event` 是插入式事件账本，现有成绩成功事务才完整写入 `model_usage_event`/`model_call_fact`；失败、截断和失租路径可能只更新 run 状态。缺失用量必须表示为未知，不能填 0。
- v2 错误路径需要保持 v2 错误 schema；当前通用 `AgentError.payload()` 固定 `grading-agent-v1`，实现时必须同步修正契约版本，不能只增加一个字段。

## 3. 统一内部事实模型

在 adapter 解析模型正文前，先从 envelope 提取有界事实。内部名称可按代码现有风格调整，但语义必须固定：

```text
FinishOutcome:
  completed              # 明确 stop/正常结束，角色和内容形态符合本 adapter
  output_limit           # length / max output / token limit
  filtered_or_refused    # content_filter 或供应商拒答
  unsupported_generation # tool/function call 等本项目未启用的生成方式
  finish_missing         # 完成响应没有结束原因
  finish_unknown         # 有结束原因但不在白名单
  envelope_invalid       # 响应 envelope、角色或字段形态不合法
  unavailable            # 没有收到 envelope，不能推测结束原因
```

只允许 `completed` 进入 JSON 提取、schema 校验、证据映射和 `AdapterOutput`。即使 `content` 恰好是完整 JSON，`output_limit` 也不能采纳。

每次实际发出上游请求都形成一次有界 attempt fact：

```text
attempt_no, logical_work_id, provider_request_id,
finish_outcome, normalized_failure_kind,
usage_status, input_tokens?, cached_input_tokens?, output_tokens?, total_tokens?,
output_token_limit, dispatched_at, elapsed_ms
```

`usage_status` 至少有 `reported`、`partial`、`unavailable`、`invalid`。字段缺失时保持未知；不得用 0 伪造“没有消耗”。供应商原始正文、拒答文本、任意错误字符串和内部 URL 不进入教师 DTO。

建议对外沿用 `model_output_invalid` 等兼容错误码，增加白名单 `execution_diagnostics.failure_kind`，例如：

```text
output_truncated, finish_reason_missing, finish_reason_unknown,
provider_refused, content_filtered, unsupported_generation,
response_envelope_invalid, schema_invalid, evidence_invalid, capability_unavailable
```

诊断最多带 `attempts`、`provider_request_count`、`usage_status`、`recovery_attempted`、`recovery_blocked_reason` 和 `output_token_limit`。供应商 request ID 只供日志/管理权限使用；教师只看到可行动的失败类别。

## 4. Python 评分服务设计

### 4.1 adapter 解析顺序

在 `model.py` 的 Local/兼容和 DashScope 路径、结构化请求路径分别执行同一顺序：

1. transport 返回 envelope；先读取并校验响应大小。
2. 独立提取 usage、provider request ID 和结束原因，先写入本次 attempt 的内存事实。
3. 检查角色、拒答/工具调用和 finish outcome。
4. 非 `completed` 立即抛出带固定 `failure_kind` 的 `AgentError`。
5. 仅完整输出进入 JSON、schema、证据和分值校验。
6. 在成功或失败的 `finally` 中累计本次 attempt；失败也保留已知 usage。

`DashScopeNativeAdapter` 的 text-generation 与视觉包装必须分开验证。当前生产 math v2 发送 OpenAI `image_url` 消息，而 native structured 路径直接走 text-generation 输入，并没有自动使用 `build_dashscope_multimodal_*`；P0 不能宣称 native math v2 视觉链已经可用。若继续支持，另列原生多模态协议适配任务；否则返回 `capability_unavailable` 并转人工。

### 4.2 Python 重试边界

P0/P1 保留现有重试配置，但 `length` 不得被普通 JSON 修复重试掩盖。P2 改为由一个 execution options 传入单次 token 上限、剩余 deadline、attempt identity；adapter 自身只发一次，恢复循环由上层持有。

一次逻辑工作单元的候选顺序只有：

```text
首次 output_limit -> 第二次受限扩额
首次 schema/evidence invalid -> 第二次修复或扩额（两者共用同一恢复额度）
首次恢复仍失败 -> 结束，转人工
```

以下不自动第二次：`finish_missing`、`finish_unknown`、拒答/过滤、鉴权、上下文超限、参数不支持、能力不匹配和无法证明剩余预算的 timeout。v1 与 v2 必须同时检查 `retryable`，不能让 v2 因错误码集合而重试明确 `retryable=false` 的鉴权失败。

不能把 `model_context_tokens` 当作已经可用的剩余上下文。当前实际上限不同：local v1 默认 384，structured 路径至少 4096，文档/视觉链可能至少 8192；P2 必须由 adapter capability profile 给出合法参数名、输出上限和上下文余量，不能统一乘二或在参数报错后换字段重发。

### 4.3 错误契约

`errors.py`、`server.py`、`contracts/grading-agent/v1/error.schema.json` 和 `v2/error.schema.json` 一起修改：

- v1 错误保持 `grading-agent-v1`，v2 错误返回 v2 schema 版本。
- `execution_diagnostics` 为可选、封闭字段；不允许任意供应商错误扩展。
- HTTP 建议：真正 rate limit/服务不可用 503，超时 504，截断/结束原因/JSON/schema/evidence 失败 502，能力或请求不支持沿用 400/422。
- `retryable` 只描述调用方是否可以按统一预算继续；不能把“模型输出可修复”与“Go/worker 可无限重试”混在一起。
- inflight 错误复制必须完整保留诊断和 usage status；同幂等键的后到请求不能丢失失败事实。

## 5. Go subjective 与 worker 设计

### 5.1 P0/P1 最小接线

修改范围应集中在：

- `services/api-gateway/internal/subjective/adapter_http.go`
- `services/api-gateway/internal/subjective/adapter_http_v2.go`
- `services/api-gateway/internal/subjective/types.go`
- `services/api-gateway/internal/subjective/worker_failure_atomic.go`
- `services/api-gateway/internal/subjective/worker_completion_service.go`
- `services/api-gateway/internal/subjective/handlers_grade.go`、`handlers_worker.go`
- 生产错误 DTO、OpenAPI/契约生成源及对应 tests

Go adapter 解析 Python error 的白名单诊断，填入 `GradingAgentError`；不得把 Python 半截正文当作 `AdapterOutput`。`WorkerFailureInput.ErrorDetail` 保存固定诊断和 usage status；`FailWorkerAtomic` 与 run 状态、worker lease 结果保持同一事务边界。旧租约迟到时，诊断可以记为 attempt fact，但不得污染新 lease 的 run/grade。

`GradingRun.ErrorCode` 保持可筛选的主类别，`ErrorDetail` 或新的失败事实表保存细节。现有 `Grade.FailureReason` 只在失败 grade 需要占位时使用，不把失败响应写成成功 grade。

### 5.2 P2 持久化预算

不建议直接扩展 `model_usage_event` 充当预留表，因为它是 INSERT-only 事件且现有唯一键为租户、request、feature，无法可靠表达释放、结算和逻辑工作单元。

新增一个与 `GradingRun`/worker task 绑定的持久化工作单元和 attempt 表，最小字段：

```text
logical_work_id, tenant_id, run_id, task_id, lease_generation,
state, max_provider_calls, calls_dispatched,
reserved_input_tokens?, reserved_output_tokens?, consumed_*?,
usage_status, recovery_used, current_attempt_id,
created_at, updated_at
```

每次 dispatch 在同一事务中锁定工作单元、确认 run 未取消且 lease generation 仍有效、占用一次调用额度，再生成 attempt。恢复只允许 `recovery_used=false` 且 `max_provider_calls=2`；Python/Go/worker 重试都只能消耗这个同一额度。重复 HTTP 请求使用相同 attempt identity，不能再次占用额度。

真实 usage 回来后结算；未知 usage 记录未知并按保守预留释放策略处理，不能把未知当 0。取消、超时和失租只关闭当前 attempt；迟到响应必须通过 lease generation 和 attempt state 检查，不能写 grade 或新任务。

P2 首版只允许同一 provider、同一冻结模型/提示词/题目版本的受限恢复；不在恢复时切换供应商、改题目、改 rubric 或复用半截正文。panel 每个成员可以各自有一次恢复，但 panel 总工作单元同时受总调用数、总 token 预留和 deadline 限制。

## 6. 教师和管理端体验

教师当前主工作台继续人工评分，不增加模型收费按钮。AI 建议失败时：

- 显示“AI 建议未完成，请人工评分”；提示不代表已经新建人工任务；
- 若为截断，显示“模型输出达到上限，未采用不完整建议”；
- 保留学生答题图、评分细则和人工评分输入；不显示半截 JSON、内部 request ID 或供应商错误原文；
- 不自动覆盖已有人工草稿，不关闭任务，不改变提交/发布流程。

模型重试只出现在已有评分管理/批次异常入口，并要求现有模型管理权限；普通 `review:work` 教师不能因看到失败提示而触发无限重试或修改预算。重试失败批次生成新的 run/attempt 事实，保留原失败事实和模型版本，不覆盖已完成建议。

管理/运维视图可显示：失败类别、尝试数/上限、实际 provider request count、usage status、是否使用恢复、阻断原因、模型/提示词版本、run/batch/task 关联和时间。未授权角色只能看到数量与人工处理提示。

## 7. 验收矩阵

### Python/契约

- stop + assistant + 完整 envelope 接受；length 即使 JSON 合法也拒绝。
- content filter、拒答、tool/function、缺失/未知 finish 分别映射固定类别。
- local v1、local structured、native v1、native structured 均验证相同规则；文档模板已有的原生视觉/nullable 保护不回归。
- 截断、坏 JSON、坏 schema 都保留已知 usage；缺失 usage 为 unavailable/partial，不是 0。
- v1/v2 success/error schema 版本正确，新增字段通过契约检查，拒绝未知字段。
- v2 `retryable=false` 的鉴权/能力失败不被内部重试。

### Go/worker/持久化

- 同一 request ID 重复请求不会重复占用逻辑预算；失败重放不能重置 P2 预算。
- P0/P1 默认不扩额时，length 只产生一次 provider call 并转人工。
- P2 length→success 最多两次；length→invalid、invalid→length 不出现第三次。
- Python、Go HTTP、worker runtime 任意一层重试都计入同一 logical_work_id；取消、租约失效、run 版本变化后的迟到结果不写入 grade。
- 并发同 run/同 attempt 只有一个 owner；旧 lease 失败不能覆盖新 lease。
- PostgreSQL 事务失败时 attempt、usage 预留和 run/worker 状态一起回滚；成功建议仍沿用现有人工复核/发布链。

### UI/权限

- 教师看到人工可继续，草稿不被覆盖，半截内容不展示。
- 管理权限可看诊断；普通教师不看供应商细节、不触发收费恢复。
- 普通题与数学 v2、普通样本与暗样本统一错误白名单，不泄露来源类型。
- 刷新、换会话和取消后不会把旧诊断或旧建议写入新任务。

## 8. 实施顺序与文件审读记录

1. 先补 Python adapter finish/usage 提取和 v1/v2 error schema；不打开自动扩额。
2. 补 Go adapter 错误白名单、`ErrorDetail` 和 worker 原子失败事实；实现教师人工提示和管理端诊断。
3. 运行正常契约生成、Python 协议测试、Go adapter/worker/PG 测试，再做浏览器异常态验收。
4. 单独评审 P2 的工作单元/attempt/预算迁移；有真实成本上限和模型能力资料后才开放扩额。

本方案已读核心文件：`model.py`、`managed_model.py`、`errors.py`、`config.py`、`app.py`、`app_v2.py`、`server.py`、`provider_adapter.py`、`dashscope_native_contract.py`、`template_analysis_model.py`；Go `subjective` types、HTTP v1/v2 adapter、worker execution/completion/failure、workerruntime lease/failure 和当前评分流程文档。模板分析、paper visual、math rubric、contract 生成和全部 UI 文件尚未逐文件审读，实施前保持 `pending`，不宣称全仓完成。

## 9. 联网资料与采用边界

- OpenAI Chat Completions 官方文档说明 `finish_reason` 的 `stop`、`length`、`tool_calls`、`content_filter` 等语义，并说明 `max_completion_tokens` 是包含可见输出和 reasoning tokens 的上限：[Create chat completion](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create)。
- 阿里云百炼 OpenAI 兼容文档说明达到 `max_tokens` 时返回 `finish_reason=length`：[OpenAI 兼容 Chat](https://help.aliyun.com/zh/model-studio/qwen-api-via-openai-chat-completions/)。DashScope 文档也说明 `length` 表示达到输出上限：[DashScope API](https://help.aliyun.com/zh/model-studio/qwen-api-via-dashscope)。
- OpenAI 官方限流说明建议应用层控制重试并使用指数退避，避免 SDK/应用层嵌套重试放大调用：[Rate limits](https://developers.openai.com/api/docs/guides/rate-limits)。
- AWS Well-Architected 的重试建议强调最大次数、退避和幂等；这里只借鉴通用可靠性原则，不引入 AWS 代码或服务：[Control and limit retry calls](https://docs.aws.amazon.com/wellarchitected/2023-04-10/framework/rel_mitigate_interaction_failure_limit_retries.html)。

这些资料只支持协议语义与重试原则；不会据官方文档推断本项目真实模型的恢复率、费用或评分质量，也不把供应商行为当作本地授权事实。
