# 基础设施问题修复记录

执行日期：2026-10-11（Asia/Shanghai）。原始 `REPORT.md`、原始扫描 JSON 和历史人工核验结论保持原样；本文件记录当前修复后的验证结果。

| 编号 | 处理 | 验证 |
| --- | --- | --- |
| SG-001～011 | `.github/dependabot.yml` 全部 11 项显式设置 `cooldown.default-days: 7`，保留每周调度、PR 上限和既有分组。 | PyYAML 真实解析通过，11 项均有 7 天 cooldown。GitHub 当前官方文档确认 cooldown 只适用于普通版本更新，不作用于安全更新。 |
| SG-044～045、047～048、050～051 | 三份 Nginx 代理配置统一发送固定 upstream 的 `Host $proxy_host`，每个代理 location 删除请求带入的 `X-Forwarded-Host` 和 `Forwarded`。健康代理路径也实施相同边界。 | 官方 Windows nginx/1.29.0 对测试配置执行 `-t`，三份均通过；48 个真实回环请求通过，详见 `nginx-authority-runtime.json`。 |
| SG-090 | 复核并保留此前多阶段镜像的 `USER 10001:10001` 和可写 HOME/cache/temp 目录。 | 当前文件与本次修复前保存的 baseline 字节完全一致，SHA-256 `20c9fbae828ab0c478929582e614253844e1f5ad5deb0a003af7962d99e8485e`。本分工没有再次改写该文件。 |
| SG-091 | 独立 E2E 客户端建立 UID/GID 10001，提供该用户持有的 `/home/e2e`，最终 `USER 10001:10001`。二进制仍由 root 持有。 | 最终阶段非 root USER 和 HOME 权限静态核对通过；`go test ./tools/story056-real-worker-e2e` 通过。真实 Docker build/runtime 验证受本机引擎无响应限制。 |

Nginx 合成测试保留原代理和响应头指令，只把 listener 和固定 upstream 地址换成回环测试端点。请求覆盖未知域名、随机宿主端口、伪造 `Forwarded`/`X-Forwarded-Host`，确认 API/Web 上游收到的 Host 固定；路径和 query 保持原值，`/health`、生产健康代理、安全响应头和前端绝对重定向重写可用。网关代码没有生产请求 Host 消费点或 WebSocket 服务入口；现有浏览器 CSRF/CORS 使用配置 Origin 和请求标记，前端 API 默认使用同源地址。测试范围不是完整浏览器 E2E。

客户端 Compose 服务没有输出目录挂载；Go 客户端的结果写 stdout，由宿主 runner 接收。非 root 镜像只提供 HOME 和已有 `/tmp` 的写入能力，没有给源码或二进制增加写权限。没有为不存在的挂载扩大权限。

Docker 当前 context 是 `desktop-linux`，`docker version --format '{{json .}}` 在 15 秒后超时；没有重启或停止 Docker，也没有触碰用户已有容器。此次不声称 Docker DNS、Linux nginx 镜像、E2E 镜像运行 UID 或实际宿主挂载已经通过运行验收。`server_name _` 仍接受未知域名；此次修复的是上游 authority 转发边界，公网域名与 TLS 的真实部署设置未验证。

新增维护文档与可复现测试分别是 `infra/docker-compose/nginx/README.md` 和 `infra/docker-compose/nginx/tests/check_proxy_authority.py`。官方便携测试运行时下载地址为 `https://nginx.org/download/nginx-1.29.0.zip`，ZIP SHA-256 为 `fed5551ac776a9001eb26d43134695403d6703e9c3c85759c592aea4d223ef09`；下载目录在 reports 下，仅用于本次回环验收。测试启动的 Nginx 进程已退出；自动审批策略拒绝了该下载目录的 `Remove-Item` 清理命令，便携文件留在 `remediation/test-runtime/nginx-1.29.0`，不影响源码修复。

官方依据：[Dependabot cooldown](https://docs.github.com/en/code-security/reference/supply-chain-security/dependabot-options-reference#cooldown) 说明普通更新的等待规则及安全更新豁免；[Nginx proxy_set_header 与变量](https://nginx.org/en/docs/http/ngx_http_proxy_module.html#proxy_set_header) 说明 `$proxy_host` 来自 `proxy_pass`、空值头不向上游发送和 location 的继承规则。
