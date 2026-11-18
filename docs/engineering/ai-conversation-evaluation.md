# AI 对话与公开缓存评测

本文件对应 [完整优化方案](../design-reviews/2026-11-13-ai-conversation-optimization-plan.md) 的缓存和评测交付。确定性测试、供应商提示词对照和生产端到端指标分别记录；一种结果不能代替另一种结果。

## 离线门禁

在仓库根目录运行：

```powershell
node --test scripts/ai-conversation-eval.test.mjs
node scripts/ai-conversation-eval.mjs --offline
```

默认模式仅校验 12 个合成样例的结构与规则，不发生网络访问。样例覆盖中文约束、长消息中部条件、明确更正、撤销、主题切换、无业务证据、工具定义、JSON、引用诱导、范围澄清和缓存原理。样例没有真实学校、学生或成绩。`fixtures_validated` 仅表示样例可执行，不能描述为模型回答质量通过。

实际上下文选择、来源记忆、权限、工具调用和取消行为由对应 Go 与前端生产代码的测试验证。公开网页的定向测试和基准在 `services/api-gateway` 目录执行：

```powershell
go test -p 1 ./internal/schoolassistant -run '^TestPublicPage' -count=1
go test -p 1 ./internal/schoolassistant -run '^$' -bench '^BenchmarkPublicPageCacheLoad$' -benchtime=100x -count=1
```

基准使用进程内模拟读取，不访问真实外网；输出 `ns/op`、`fetch/op` 和 `shared/op`。cold 使用不同键；hot 预先填充；burst 在初次冷请求上施加并发，完成后进入热命中。它只说明本机合并和缓存机制的成本，不能用于承诺公网延迟改善。

## 真实供应商提示词对照

真实调用只在显式传入 `--live` 且以下三个专用环境变量齐备时运行。不读取 `.env`、产品数据库、用户模型密钥或其他服务的默认凭据。

| 环境变量 | 含义 | 限制 |
| --- | --- | --- |
| `YUEJUAN_EVAL_ENDPOINT` | 完整兼容聊天完成接口地址 | HTTPS，不接受用户信息、查询串、片段，不跟随重定向 |
| `YUEJUAN_EVAL_API_KEY` | 本次评测专用凭据 | 仅请求头使用，不写报告 |
| `YUEJUAN_EVAL_MODEL` | 明确的模型版本标识 | 最多 200 字符，禁止换行 |
| `YUEJUAN_EVAL_MAX_REQUESTS` | 全场请求数上限 | 默认 9，范围 3–60，失败也占次数，不重试 |
| `YUEJUAN_EVAL_TIMEOUT_MS` | 每请求完整读取超时 | 默认 30000，范围 100–120000 |
| `YUEJUAN_EVAL_MAX_OUTPUT_TOKENS` | 每请求输出上限 | 默认 512，范围 128–1024 |

配置后运行 `node scripts/ai-conversation-eval.mjs --live`。每个样例对比一次直接生成与两次“草稿后生成”；下一完整样例可能超过请求上限时停止，默认只评测前 3 项，12 项全量对照至少需要 36 次请求。轮换策略执行顺序以减小固定先后的缓存偏差；严格性能比较还应交错多次重复并另设热/冷缓存条件。

报告仅包含合成案例 ID、请求的模型版本标识、配置/数据集指纹、固定错误码、每策略请求数、总输入/输出/缓存命中 tokens、成功样例的 P50/P95 总延迟和词面规则通过情况。不会输出端点、凭据、提示词、生成正文或供应商错误正文。供应商已经返回 usage 的截断/不完整回答仍记入观测用量；网络失败未返回用量时报告不能推断其实际费用。缓存命中 tokens 是输入子集，多个别名取最大值，不相加。

这是供应商兼容接口上的受控提示词对照，未经过产品的身份、工具执行和 SSE 链路。词面规则不能代替人工审查，也不能证明语义质量；非流式测试不测首字延迟；脚本不猜测价格，token 数不能直接冒充人民币费用。应把结果与产品真实链路的脱敏观测结合后决定实验路径是否启用。

缺少任一专用配置时，`--live` 返回 `status: not_executed` 和固定原因，不能写作“真实模型评测通过”。CLI 配置错误也仅输出固定错误码。

## 公开网页冷请求合并

每个 Reader 实例最多 8 个不同 URL 在途键、每键最多 64 个等待者，任务有独立的 10 秒超时。相同 URL 的等待者共享一次匿名读取；一个等待者取消只退出自己，全部等待者退出才取消底层请求。超出上限返回固定 busy 阶段错误，编排层可保留搜索摘要并说明正文未读取，不能伪称成功。

任务结果完成后在锁内检查取消和剩余等待者，再按现有 HTTP 存储规则写缓存。失败、取消、超时不写入。URL、DNS 全地址、公网 IP 固定拨号、逐跳重定向和响应大小限制继续适用；命中仍保留原 `ReadAt`。实现没有接收租户或业务工具数据。

`NewPublicWebPageReaderWithOptions(PublicWebPageReaderOptions{DisableCoalescing: true})` 可关闭合并，保留普通缓存和访问校验。共享和独立读取都实际复用 Go 官方 `semaphore.Weighted`，最多 8 次真正尚未返回的读取；已取消但传输尚未清理完的读取继续占槽。读取器额外实现 `io.Closer`，显式关闭取消本实例共享和独立请求并拒绝新读取；没有显式关闭时也受全部等待者取消和固定超时约束。

日志含数值计数：hits、misses、coalesced、evictions、expirations、fetches、rejected、cache_items、cache_bytes、inflight、active_fetches 和 waiters。条数、字节和在途状态是快照，其余为本实例累计计数；实例重启会归零，平台聚合应采用 counter reset 规则。`inflight` 只表示共享任务，`active_fetches` 同时统计共享和独立读取。日志没有 URL、搜索词或正文。

缓存保持 30 秒、64 项、2 MiB 的保护性上限。本批不增加 Redis 或完整 HTTP 条件再验证。只有持续观测到跨实例重复读取占主要成本，才评估共享缓存；只有明确资源更新频率和再验证收益，才实现条件请求。不能以提高命中率为由延长已过期业务事实的可用期。

## 来源与许可

实际复用 [golang.org/x/sync v0.24.0 semaphore](https://github.com/golang/sync/blob/v0.24.0/semaphore/semaphore.go) 的加权信号量实现，沿用已有模块版本。源码归档已下载到系统临时目录，同时审读 [singleflight](https://github.com/golang/sync/blob/v0.24.0/singleflight/singleflight.go)；标准 `Group` 没有独立等待者取消、全部等待者退出和容量限制的完整接口，所以 URL 共享与取消作用域仍由窄适配层管理。BSD 三条款原文、校验值和实际复用位置见 [并发组件来源与许可](../architecture/ai-concurrency-sources.md)，通知随运行镜像交付。

评测脚本、规则和合成样例在本仓库独立编写。其他上下文与记忆参考来源沿用 [来源及许可记录](../architecture/ai-conversation-context-cache.md#参考来源与许可证)。

## 本批执行记录

- Node 评测脚本 9 项测试通过，包含请求数限制、超时、重定向策略、秘密不泄露、截断用量与响应大小。
- 12 个合成样例通过离线结构校验。
- 显式 `--live` 返回 `not_executed`：当前未提供专用供应商评测环境配置。本批没有产生真实模型调用或价格结论。
- 2026-11-14，实际复用信号量后的 Go 公开网页定向测试通过（8.078 秒）；新增取消后保持额度、回退并发上限/失败释放、独立读取关闭三项测试各重复 10 次通过（0.992 秒）。覆盖已有 URL/SSRF 与 HTTP 缓存规则，以及合并、全部取消、单等待者取消、关闭、容量和回退路径。
- 本机模拟基准（Windows/amd64，100 次，实际复用信号量后）：cold 19992 ns/op、1 fetch/op；hot 203 ns/op、0 fetch/op；初始 burst 8147 ns/op、0.01 fetch/op、0.11 shared/op。100 次初始突发/后续热请求合计只发起 1 次模拟读取。这些单次本机数值不证明与旧结果相比的稳定性能变化，也不代表真实外网或供应商性能。
