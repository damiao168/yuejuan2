# 对话计数器来源、许可与验证

核验日期：2026-11-13。此文件同时是新增计数器依赖的归属和许可通知，发行 API 二进制或镜像时应随产品保留本文件。

## 实际纳入的依赖

| 组件 | 固定版本 | 许可证与用途 |
| --- | --- | --- |
| [tiktoken-go/tokenizer](https://github.com/tiktoken-go/tokenizer/tree/v0.10.0) | v0.10.0 | [MIT](https://github.com/tiktoken-go/tokenizer/blob/v0.10.0/LICENSE)，提供显式选择的 `cl100k_base` 和 `o200k_base` 文本编码计数 |
| [dlclark/regexp2](https://github.com/dlclark/regexp2/tree/v2.5.1) | v2.5.1，Go 模块路径带 `/v2` | [MIT](https://github.com/dlclark/regexp2/blob/v2.5.1/LICENSE)，tokenizer 的运行时间接依赖 |

模块版本及完整性校验由 `services/api-gateway/go.mod`、`go.sum` 固定，使用 `go get` 与 `go mod tidy` 正常维护。没有向仓库复制 BPE 实现或生成词表。应用侧的预算、裁剪、错误分类和记忆保留逻辑独立编写。

### 实际源码复核（2026-11-14）

通过 Go 下载缓存中的固定版本源码审读了 `tokenizer.go`、`codec/codec.go`、两个编码构造器、已生成词表和 `internal/cmd/vocab.go`。应用的 `managed_context_counter.go` 实际调用 `tokenizer.Get(...).Count`，运行时进入依赖的 `Codec.tokenize` 和 BPE 合并实现；应用没有另写一份 BPE 算法。

词表由包内 `sync.Once` 初始化。应用只保存所选编码的计数函数，不调用可变的 `SetErrorMode`；依赖使用的 regexp2 `Regexp` 明确支持并发使用。特殊 token 字面量在这条 `Count` 路径按普通文本处理，十二项双编码参考样例包含该边界。私有请求计数缓存仍由应用维护，因为模型窗口、工具协议和会话记忆优先级属于本项目的约束。

本次审读的 Go 模块内容校验值为 `tokenizer v0.10.0: h1:xcMbdR1D/rTF2/yEj1GQg3/jl5D9n5aQ546mr32di/w=`、`regexp2/v2 v2.5.1: h1:E5Ug7Dh264W1ymdySmiHNcDG7fmsR307APCE5R07a20=`，与正式模块锁定记录一致。下载缓存属于开发环境，不纳入仓库或运行镜像；API 镜像通过 Dockerfile 将本文件复制到 `/usr/share/licenses/edugrade/`，使实际纳入依赖的版权与许可通知随交付保留。

所选模块把编码词表编译在 Go 包中，运行请求不会下载词表。其 [词表生成器](https://github.com/tiktoken-go/tokenizer/blob/v0.10.0/internal/cmd/vocab.go) 指向 OpenAI 公开编码资产；这些已生成文件随该模块发布，此处同时保留上游 [tiktoken 0.12.0 的 MIT 通知](https://github.com/openai/tiktoken/blob/0.12.0/LICENSE)。不在产品构建中运行第三方词表生成器，也不把构建时工具依赖当作产品运行依赖。

## 计数边界

`context_policy.tokenizer` 可选 `utf8_bytes`、`cl100k_base`、`o200k_base`；省略时采用 `utf8_bytes`。管理员必须根据实际部署模型选择编码，不根据 DeepSeek、Qwen 等模型名称套用其他模型的编码。

所选 tokenizer 精确计算对应编码的普通文本；服务端序列化后的消息、工具定义另有协议余量，图片采用保护性预留，因此最终输入总量仍是估算，不是供应商精确计费或多模态容量保证。文本内的特殊 token 拼写按用户普通文本计算，不让用户文字取得协议控制符含义。

每次预算处理预计算工具、各条消息和图片成本。裁完整旧轮时复用这些成本，兼容聊天直接从保留原附件的消息切片中选择，不把内部模型视图逆转换成客户端消息。辅助记忆仍是无附件的普通用户材料；它优先于旧轮保留，无法容纳当前问题时先省略低优先级记忆，同优先级先省略较早条目，并返回省略计数。当前问题和系统约束始终优先。

Agent 请求携带不导出的计数缓存，仅保存成本和 SHA-256 指纹。指纹绑定编码、适配器、工具、全部消息、图片元数据与内容、记忆标记和优先级；内容不变时，服务层和 HTTP 层可复用验证与计数，任何相关字段修改都会重新校验。窗口策略、输出预留和单轮余额每次仍检查，不能用已准备标记绕过预算。缓存仅随当前请求传递，不是全局用户正文缓存。

模型容量不足返回 `ErrAgentContextExceeded`，已能进入模型窗口但单轮剩余额度不足返回 `ErrAgentTurnBudgetExceeded`。先判断模型容量，再判断单轮额度，避免把费用预算不足错误地解释为模型上下文不足。

## 参考计数验证

使用独立临时目录安装 Python `tiktoken==0.12.0`，以 `encode(text, disallowed_special=())` 实际计算六组合成样本。Go 对两个编码共十二项固定计数回归测试均通过，覆盖英文、中文业务约束、代码、JSON、用户更正与特殊 token 字面量。参考 Python 环境只用于开发验证，没有加入产品依赖，也没有发送学生信息或调用生成模型。

`go test -p 1 ./internal/modelgovernance -count=1` 通过；测试覆盖容量/单轮错误区分、记忆保留与省略、无效记忆标记、兼容聊天显式编码、并发编码计数及配置生命周期。此处的本地测试不能代替供应商的实际 usage 校准或真实回答质量评测。

2026-11-14 再次执行本包回归通过。另建仅监听本机随机端口的 PostgreSQL 17 临时集群，应用全部仓库迁移后，`TestPostgresManagedContextPolicyLifecycle`、`TestPostgresChatAccessCASAndTenantRLS`、`TestPostgresAnswerSheetModelSelectionCASAndReferenceDeletion` 全部通过，测试集群已停止。生命周期测试使用显式 `o200k_base` 配置，覆盖 JSONB 保存、普通编辑保留、密钥轮换忽略夹带配置、模型身份变化后恢复默认和再次显式修改；没有访问用户现有数据库。

## 随交付保留的许可原文

以下版权与许可通知来自上述固定版本。后续升级依赖时须重新核对，不能只更新版本号。

### tiktoken-go/tokenizer v0.10.0

MIT License

Copyright (c) 2023 tiktoken-go

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
### dlclark/regexp2 v2.5.1

The MIT License (MIT)

Copyright (c) Doug Clark

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.


### OpenAI tiktoken 0.12.0

MIT License

Copyright (c) 2022 OpenAI, Shantanu Jain

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
