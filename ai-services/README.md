# 智能阅卷 Agent 服务

这是供平台内部调用的主观题辅助阅卷与试卷解析服务。服务调用需要认证；主观题结果仅作为教师建议，不负责确认或发布最终成绩。服务不接收租户或学生身份信息，会校验模型响应、核对答案中的证据、按 Rubric 采分点重新计算建议分数，并记录不含敏感答题内容的请求遥测。

主观题推理支持本地 llama.cpp 和阿里云百炼 DashScope 原生生成接口。试卷解析还支持学校在平台中配置的 OpenAI 兼容接口或 DashScope 原生接口；解析模型的选择不改变主观题辅助阅卷的治理规则。

## 本地测试

在仓库根目录运行：

```powershell
$env:PYTHONPATH = "ai-services"
python -m unittest discover -s ai-services/tests -p "test_*.py"
```

TLS 握手测试使用本机已安装的 OpenSSL 签发临时 CA 和带 `IP:127.0.0.1`、`DNS:grading-agent` SAN 的短期证书。可用 `EDUGRADE_TEST_OPENSSL` 指定 OpenSSL 的绝对路径，临时文件遵循 `TMPDIR`、`TEMP` 等系统临时目录配置。缺少 OpenSSL 时测试失败，不跳过。测试经过真实 `serve()`、TLS 上下文和网络请求，只用合成适配器替换外部模型就绪探测；通过不代表真实模型已连通。

## 内部接口

| 接口 | 用途 |
| --- | --- |
| `GET /health` | 检查服务是否存活。 |
| `GET /ready` | 检查当前配置的模型运行环境是否可用。 |
| `GET /grading/prompts/current` | 经认证后只读查看运行时实际加载的提示词文件快照。 |
| `POST /grading/grade` | 经认证后生成主观题阅卷建议。请求必须带 `Authorization: Bearer ...` 和 `Idempotency-Key`。 |
| `POST /grading/grade-v2` | 经认证后生成数学题采分点候选映射，不输出最终分数；同样需要上述两个请求头。 |
| `POST /paper/parse` | 经认证后解析试卷；设置 `Accept: application/x-ndjson` 可接收进度和结果流。 |

调用 `/paper/parse` 时，网关会解析学校的默认模型配置，通过内部连接传入仅对本次请求有效的 `managed_model`。构造文档提示词前会移除凭据，解析结果也不会返回凭据。学校未设置默认模型时，使用已配置的本地解析模型。外部模型连接仅接受公开可访问的 HTTPS 地址，校验 TLS 主机名且不跟随重定向。

## 配置与变更

运行参数见仓库根目录的 [`.env.example`](../.env.example)。`EDUGRADE_GRADING_AGENT_TOKEN` 少于 32 个非空白字符时，服务拒绝启动。

监听环境缺省为 `production`。配置 `EDUGRADE_GRADING_AGENT_TLS_CERT_FILE` 和 `EDUGRADE_GRADING_AGENT_TLS_KEY_FILE` 时，两者必须同时提供，服务以 TLS 1.2 或更新版本监听。证书加载或 socket 包装失败会中止启动，通知就绪探测线程退出，最多等待一秒后关闭监听；已经进行中的模型探测仍受其自身超时约束。直接调用 `serve(Settings(...))` 也会在加载应用和绑定端口之前校验 token 与监听配置。

无证书 HTTP 仅供显式本机开发：`EDUGRADE_ENV` 为 `local`、`development`、`dev` 或 `test`，`EDUGRADE_GRADING_AGENT_ALLOW_INSECURE_LOOPBACK_HTTP=true`，且 `EDUGRADE_GRADING_AGENT_HOST` 是 `127.0.0.1` 等 IPv4 字面回环地址。三项缺一即拒绝启动；未知或空环境、`localhost` 等 DNS 名称、wildcard、远端地址和 IPv6 都不开放此例外。opt-in 环境变量仅接受小写 `true` 或 `false`，缺省 `false`；直接构造 Settings 时仅接受实际布尔值。配置完整证书后，opt-in 不会使服务回落 HTTP。

跨容器调用仍需绑定容器可达地址并配置服务证书，证书 SAN 应包含调用 URL 使用的 DNS 名称，调用方须信任签发 CA 并验证主机名。主 Compose 的证书挂载、私钥读取权限和旧环境配置迁移见 [部署说明](../infra/docker-compose/README.md)。`/health` 检查进程，`/ready` 另外检查模型就绪；两者都应使用带 CA 验证的 HTTPS 请求诊断。

使用 DashScope 原生模式时，将 `EDUGRADE_GRADING_ADAPTER_TYPE` 设为 `dashscope_native`，`EDUGRADE_GRADING_MODEL_BASE_URL` 设为 `https://dashscope.aliyuncs.com/api/v1`，并通过现有密钥或环境变量注入 `EDUGRADE_GRADING_MODEL_API_KEY`。提示词查看接口是只读的；变更提示词须创建新的 manifest 版本，经过评测和审批后再部署。
