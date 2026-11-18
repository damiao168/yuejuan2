# AI 对话底部用量指标

AI 对话输入框下方显示本次模型输入量、应用上限与平均输出速率。指标按当前账号、对话和本次使用的模型隔离；新请求清空旧速率，切换模型不沿用另一模型的用量。刷新只恢复历史内容，不恢复用量快照。

## 数据口径

- 输入量优先采用 SSE `context.context_budget.actual_input_tokens`，它来自最近一次模型调用返回的 `response.Usage.InputTokens`，只有正数上游用量才返回。显示标注“实报”；上游未返回用量时采用 `estimated_input_tokens` 并明确标注“估算”。只采用 `accepted=true` 的非探测请求。
- 每次调用先发估算，调用返回后再发同类型 `context` 修正事件，沿用同一次调用的窗口、阶段和裁剪状态。新阶段的估算帧会替换上一阶段的实报。最终回答输入量不能用 `done.completion.usage.input_tokens` 替代，后者汇总了整轮探针、规划和回答，存在重复前缀。
- 分母来自同一事件的 `context_window_tokens`；尚未收到事件时可显示模型列表返回的应用生效窗口。该值来自平台配置和已有保守默认策略，并非自动探测出的供应商最大支持容量。输入预算还会扣除预留输出和安全余量，可能小于窗口。
- “本次输入”描述最近单次模型调用，包含上游计入输入的缓存部分，不是下一轮的预计占用，也不包括尚未发送的草稿及随后生成的输出。“实报”表示上游接口返回的数据；使用兼容网关时，应用无法验证其是否改变供应商用量，不能将其承诺为独立验证过的官方计费值。
- “约”和“估算”表示服务端预算估计，包括系统提示、已选择的历史、附件及工具结果。`utf8_bytes` 用字节数作保守预算，可能明显高于上游 Token 数；该预算继续用于防超限，不因一次观测偏差改变全局系数。容量不足时的历史裁剪和记忆省略提示继续沿用现有反馈。
- “平均速率（含等待）”来自 `done.generation_metrics`：最终回答单次调用的上游 `output_tokens` 除以应用侧这次模型调用的总耗时，单位 Token/s。包含本地请求准备、首 Token 等待、网络和同步 SSE 发送，以及上游计入输出量的推理 Token；不包含之前的搜索、业务工具或规划调用。它不是纯模型解码速率。
- 流式返回期间显示“统计中”，完成且计量有效后显示速率。供应商未返回用量、请求中断，或复用规划结果而没有最终回答调用时，保持未知；不以字符数、网络块数或整轮累计输出量伪造速率。

这些指标不改变模型预算、计费或缓存策略，也不会新增模型请求。

## 源码复用与许可

参考项目为官方 [deepseek-ai/deepseek-harness](https://github.com/deepseek-ai/deepseek-harness)，固定审读版本 `39163dd30f703a79079e2d2446545cff3c453738`。源码下载保存在本地 `output/reference-sources/deepseek-harness-20261114`，不作为本项目依赖整体引入。

直接适配复用 [token-format.ts 的 formatTokens](https://github.com/deepseek-ai/deepseek-harness/blob/39163dd30f703a79079e2d2446545cff3c453738/packages/client/ui-chat/src/client/chat/token-format.ts)，将项目专用翻译回调改为 K/M 单位。实现位于 `apps/web-admin/src/features/school-assistant/tokenFormat.ts`，无需新运行依赖。原始版权为 Copyright (c) 2026 DeepSeek，采用 MIT 许可；[完整许可](../../apps/web-admin/public/licenses/deepseek-harness-LICENSE.txt) 随管理端静态文件发布，运行中可通过 `/licenses/deepseek-harness-LICENSE.txt` 查看。

上下文占用与累计用量分开、容量未知时不显示百分比的原则参考其 Token meter。测速使用本项目服务端调用耗时，没有照搬浏览器首末块间隔：代理缓冲后集中送达可能夸大这类指标，官方亦有[相关问题讨论](https://github.com/deepseek-ai/deepseek-harness/discussions/7960)。

## 验收

- 相关前端测试共 116 项通过，包括视图、4750 估算切换为 989 实报、模型阶段切换、整轮累计量隔离、并行线程、中断、流传输和刷新恢复。无效或缺失实报回退估算，实际计量超过应用上限时保留原比例并提示校验配置。
- `schoolassistant`、`modelgovernance` 两个完整 Go 包测试通过；生成 SDK 一致性、OpenAPI 兼容检查和 10 项契约门禁通过。管理端生产构建及改动文件的 Biome 检查通过。
- Playwright 使用既有 API 夹具验证更新后的管理端，1366×768 和 390×844 下均位于输入框下方、处于可视范围且没有横向溢出。截图与布局脚本位于本地 `output/playwright/chat-metrics-accurate`。夹具先返回 4750 估算，再返回 989 单次输入与 2367 整轮累计输入，检查界面最终采用 989、3.0%。截图中的输入量与 9.9 Token/s 为模拟值，不是本轮供应商实测性能；检查没有触发真实模型请求。
- 2026-11-14 已更新本地 API/Web，数据库维持 `000232`，18 个项目容器保持运行。健康检查、管理端、学生端、AI 对话、会员页和 MIT 许可入口均返回 200。部署摘要位于本地 `output/chat-metrics-accurate-20261114/deployment-summary.json`。旧标签页需刷新并进行新的模型调用才能收到校正事件，不会从历史正文猜测或补造旧调用用量。
