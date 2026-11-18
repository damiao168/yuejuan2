# AI 对话上下文与缓存维护说明

本说明对应校级 AI 助手的上下文预算、历史摘录和公开网页缓存。它们分别解决输入过长、早期用户要求丢失、重复读取公开资料的问题。供应商的前缀缓存属于模型侧机制，不会扩大模型上下文窗口，也不能代替历史管理。

当前实现按 [完整优化方案](../design-reviews/2026-11-13-ai-conversation-optimization-plan.md) 演进。实际复用的成熟源码包括 `tiktoken-go/tokenizer`、其 `regexp2` 依赖，以及 Go 官方 `x/sync/semaphore`。源码版本、下载审读和随镜像交付的许可原文分别见 [计数器来源](ai-context-tokenizer-sources.md) 与 [并发组件来源](ai-concurrency-sources.md)。下方五个聊天/推理项目的表格仍区分设计参考与实际纳入的依赖。

## 上下文预算

模型配置通过 `context_policy` 暴露应用预算，存入既有 `data_policy.context_budget`，无需修改历史迁移。管理员应按实际模型、版本和部署端点核对供应商容量；系统不按模型名称猜测真实窗口。

| 字段 | 缺省值 | 单位与边界 |
| --- | --- | --- |
| `context_window_tokens` | 32768 | 应用使用的上下文上限，2048–2097152 |
| `max_input_tokens` | 27648 | 应用输入上限，256–2097152；仍受上下文减去输出与余量限制 |
| `max_output_tokens` | 4096 | 单次输出预留，128–32768 |
| `tokenizer` | `utf8_bytes` | `utf8_bytes`、`cl100k_base`、`o200k_base`；管理员须显式核对实际模型编码 |

表中缺省值对应 32768 的窗口。只填写较小窗口时，输出缺省值为 `min(4096, context_window_tokens / 4)`，输入缺省值再扣除该输出和安全余量。编辑原模型时省略整个 `context_policy` 会保留原设置；显式提交部分字段时，未填字段按上述规则计算。模型身份改变且没有显式提交容量时恢复产品默认值；仅轮换密钥保留原容量。

发送前同时检查系统提示词、工具定义、消息、工具调用参数和图片的成本，为输出与安全余量预留空间。安全余量为 `min(1024, max(128, context_window_tokens / 16))`。默认以序列化 UTF-8 字节作保守估算；显式编码使用成熟离线 tokenizer 计数，再加协议与图片余量，因此总输入仍标记估算。按阶段、模型配置与版本记录估算和真实 usage 的偏差，不把一次响应自动学习成跨用户全局系数。缺省窗口属于产品保护值，不是接口自动发现的模型容量。

超限时只移除最早的完整用户轮次，保留系统约束、当前问题和本轮工具调用/结果批次。当前问题与必要资料仍无法容纳时，在向供应商发送前拒绝，而不是截断工具参数或悄悄删掉当前问题。每次规划和最终回答都要经过预算检查，不能只检查聊天入口。

单轮累计 60000 usage tokens 仍是另一层保护。探针最多预留 256 输出 tokens，规划最多 1024，均受模型策略的更小上限约束；继续规划前按当前材料保留一次最终回答的输入和短输出，不能预测后续工具结果。最终回答可在剩余额度内缩短输出，最低 128；已有工具结果且不能继续规划时尝试回答已核实部分。模型窗口不足与单轮剩余额度不足分别返回 `ai_chat_context_exceeded`、`ai_chat_budget_exceeded`。由于实际 usage 与估算可能不同，60000 不是精确计费上限；缓存输入仍计入容量。

## 历史与可信边界

界面展示历史与发送给模型的消息视图分开。显示历史最多 200 条，正文与推理累计最多 200000 个 UTF-16 单元，附件内容另受 16 MiB 字符预算约束；当前问题及其回答仍保留，旧轮次整体淘汰。普通请求最多 49 条，旧完整轮次按 180000 个 Unicode 字符和 9 MiB 图片预算进一步裁剪。

记忆源池使用版本 2，最多 64 个原文片段、16000 个 Unicode 字符；来源消息有稳定标识、顺序和分片编号。按句/行分片、当前问题的词面相关性、更正候选和显式保留选取出站材料，最多 16 条、每条 1000、合计 4000 个 Unicode 字符。用户可以保留或移除要求；容量已满时拒绝新保留，不静默驱逐已有保留项。取消后标记来源不再自动进入源池。自动策略是启发式选择，不承诺理解撤销对象或完整概括语义。

普通请求通过独立 `memory` 字段发送，近期消息仍最多 49 条；来源标识不作鉴权依据。服务端分条构造普通用户材料，优先裁旧轮，再省略低优先级记忆，显式保留项优先于普通项。当前问题和系统规则优先级最高。旧 v1 摘录可迁移，但已经被旧版本截掉的原文不能恢复。助手回答、附件正文、推理与业务工具结果不自动进入记忆。

历史摘录属于用户提供的非可信对话资料，不能作为系统规则或当前成绩证据。校内成绩、发布版本和权限仍由本轮工具读取和核实。公开联网分支不携带该摘录或校内业务历史。业务会话沿用原有保存边界，不因上下文优化新增服务端正文存储。

`context` SSE 事件反馈每次调用实际采用的估算方法、输入预算、裁剪与记忆省略情况。界面只根据真实反馈提示本次省略，不把本地候选记忆等同于模型已经收到。来源内容、网址、工具参数和凭据不进入这些事件或计数日志。

稳定系统规则与工具定义保持顺序，当前时间和当前考试等动态提示放在后部，以减少跨轮共同前缀变化。是否命中供应商缓存以响应中的缓存用量字段为准，不承诺必然命中。

OpenAI 兼容接口与 DeepSeek 的缓存输入用量继续由已有字段归一化；百炼原生接口同时解析 `prompt_tokens_details.cached_tokens`、旧版顶层 `cached_tokens`，并兼容部分上游的 `input_tokens_details.cached_tokens`。多个别名代表同一命中量，取最大值并限制在总输入量内；流式用量是累计快照，重复帧不能重复累加。兼容聊天仍保留原生响应的总量字段行为。

## 公开网页缓存

`internal/schoolassistant/web_fetch_cache.go` 只服务于匿名公开网页读取器：

- 每个 Reader 实例独立持有进程内缓存，最长 30 秒，最多 64 项、文本/标题/网址累计最多 2 MiB；到期或容量不足时清理，命中不续期。
- 先检查入口 URL，再查缓存。键使用 URL 的 SHA-256；仅去掉不会传给服务器的片段，查询参数保留。不同 URL 不作语义合并。
- 只写入成功提取的公开文本，保留原 `ReadAt` 和跳转后的最终 URL。读取时间不代表网页发布时间，缓存命中也不代表刚刚重新访问网页。
- `no-store`、`private`、`no-cache`、`Pragma: no-cache`、`Set-Cookie` 和 `Vary: *` 跳过存储；`max-age`、`s-maxage`、`Age`、`Date`、`Expires` 可进一步缩短寿命。此实现采用保守短期缓存，不实现完整 HTTP 条件请求或再验证协议。
- 未命中仍走原有逐跳 URL 检查、全部 DNS 地址检查和固定公网 IP 拨号。命中不会发生网络访问。缓存不携带浏览器 Cookie、模型密钥或校内会话。
- 同键冷请求合并，最多 8 个在途键、每键 64 个等待者，任务独立限时 10 秒。单个等待者取消只退出自己，全部退出才取消底层读取；失败不缓存。Go 官方 `semaphore.Weighted` 限制真正未返回的读取最多 8 个，关闭合并的回退模式也受同一限制。取消尚未清理完成的读取仍占槽位，不提前释放。

公开搜索候选继续使用原有 30 秒/128 项缓存。两者都不保存模型答案或校内工具结果，也不提供跨实例一致性。日志仅记录固定事件、耗时、状态与 `cache_hit`，不记录网址、搜索词或正文。

合并、驱逐、过期、实际读取、拒绝、字节和在途数值计数用于观察负载。低负载下继续使用本地缓存；HTTP 条件再验证和共享 Redis 须在重复率、资料更新频率和多实例观测支持后评审。具体运行方法及模拟基准见 [评测说明](../engineering/ai-conversation-evaluation.md)。

## 实验开关与回退

`EDUGRADE_ASSISTANT_DIRECT_ANSWER_ENABLED` 默认 false。启用后完整且有效的无工具调用响应可直接复用，整段下发一次，不重放规划思考；原二次生成路径继续保留。`EDUGRADE_ASSISTANT_MEMORY_DISABLED`、`EDUGRADE_ASSISTANT_CACHE_COALESCING_DISABLED` 默认 false，可分别关闭记忆输入和公开读取合并，不关闭原权限检查、缓存和 SSRF 校验。

直接回答目前只有确定性协议与调用次数测试，真实质量、P95 延迟与 token 收益尚未完成专用供应商评测，因此没有默认启用。配置说明与 Compose 示例均暴露以上开关；回退不需要修改数据库或历史迁移。

成绩、学生和班级答案不默认使用语义缓存：相近问题可能对应不同授权对象或数据版本。如以后增加校内查询缓存，必须在服务端完成授权，并将租户、权限范围和业务数据版本纳入键及失效规则。

## 参考来源与许可证

核验日期：2026-11-13。以下链接固定到本次审读的 commit，避免后续主分支许可证或实现变更造成歧义。本批根据行为设计独立编写 Go/TypeScript 实现，未复制这些仓库的源码、提示词、测试或发行物，也未新增这些项目的运行依赖。下表是来源审读记录，不把“公开可读”当作可任意移植的许可。

| 项目与审读版本 | 借鉴点 | 已核验许可与处理 |
| --- | --- | --- |
| [LangChain `980e4a80`](https://github.com/langchain-ai/langchain/blob/980e4a80d01f699ba359db139cd7c8bfc348c110/libs/core/langchain_core/messages/utils.py#L1066) | 按预算保留最近完整消息，保持消息协议有效 | 包内 [MIT](https://github.com/langchain-ai/langchain/blob/980e4a80d01f699ba359db139cd7c8bfc348c110/libs/core/LICENSE)；若以后复制实质代码，需随复制部分保留版权与许可通知 |
| [LangMem `8620b986`](https://github.com/langchain-ai/langmem/blob/8620b986af5b0c7593e6b7fbece2998690d22d15/src/langmem/short_term/summarization.py#L48) | 历史与模型输入视图分开，增量处理旧消息 | [MIT](https://github.com/langchain-ai/langmem/blob/8620b986af5b0c7593e6b7fbece2998690d22d15/LICENSE)；本批采用原文摘录，没有移植其语义摘要节点 |
| [Open WebUI `b0e6098e`](https://github.com/open-webui/open-webui/blob/b0e6098e940942281d9c83f3529ea117287a3a22/backend/open_webui/utils/context_compaction.py#L41) | 展示历史、压缩检查点和模型输入分离 | 当前是带品牌保留条件的 [Open WebUI License](https://github.com/open-webui/open-webui/blob/b0e6098e940942281d9c83f3529ea117287a3a22/LICENSE)，另有 [LICENSE_HISTORY](https://github.com/open-webui/open-webui/blob/b0e6098e940942281d9c83f3529ea117287a3a22/LICENSE_HISTORY) 规定历史材料；本批未移植其实现或提示词，不按 MIT 代码处理 |
| [LiteLLM `27e9a04d`](https://github.com/BerriAI/litellm/blob/27e9a04d62f5d9b92a69704c9195754c30bf6ecf/litellm/caching/caching.py#L372) | 请求指纹、缓存范围和命名空间 | 审读的 `litellm/caching/` 路径适用 [MIT](https://github.com/BerriAI/litellm/blob/27e9a04d62f5d9b92a69704c9195754c30bf6ecf/LICENSE)；`litellm/enterprise/` 另有许可，不把该结论扩展到企业目录 |
| [vLLM `b3c08a25`](https://github.com/vllm-project/vllm/blob/b3c08a2555a0865edab3710136fe91c470002c45/vllm/v1/core/kv_cache_utils.py#L627) | 前缀变化影响复用，缓存隔离需包含作用域 | 文件声明及根 [LICENSE](https://github.com/vllm-project/vllm/blob/b3c08a2555a0865edab3710136fe91c470002c45/LICENSE) 为 Apache-2.0；未集成其 KV 缓存代码。若后续移植，需另核对版权、改动标记和适用 NOTICE |

供应商机制另见 [DeepSeek Context Caching](https://api-docs.deepseek.com/guides/kv_cache/) 与 [百炼 Context Cache](https://help.aliyun.com/en/model-studio/context-cache)。它们是接口行为资料，不在本仓库中实现供应商内部 KV 缓存。

后续若实际纳入第三方实现，应以具体文件、版本、版权头及许可历史重新核验，并将所需许可/归属通知随交付物保留；不能用本次“独立实现”的记录覆盖后来的复制行为。

## 验证与维护

网页缓存测试覆盖命中与原读取时间、精确到期、不续期、HTTP 禁止存储头、失败重试、取消优先、并发取消隔离、入口 SSRF 检查、跳转来源、项数及字节上限。测试通过自定义 HTTP transport，不声称验证了真实外网性能。

修改预算默认值、历史摘录、公开模式输入或缓存寿命时，应同时核对对应业务测试、OpenAPI 契约与本说明。估算值不能在界面或注释中表述成供应商精确 token 数。

第一阶段基线曾通过相关前端 314 项测试、后端四个包、隔离 PostgreSQL 与构建契约门禁。当前第二阶段的实现与验收结果以 [完整方案执行清单](../design-reviews/2026-11-13-ai-conversation-optimization-plan.md#7-实施清单与结果) 为准；不把前次结果当成本次验证。真实模型评测需要专用配置，缺失时明确为未执行。

扩大前端检查时另复现 `RouteContent.test.tsx` 的 6 项加载展示断言和 `InspectorFocus.test.tsx` 的 3 项交分提示断言失败；这两个模块未在本批修改，不能将本批定向通过描述为全仓测试通过。
