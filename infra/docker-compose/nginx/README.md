# Nginx 上游 Host 边界

三份配置的 `proxy_pass` 都使用固定 upstream。API 通过认证身份和业务作用域区分租户，静态 Web 服务不依赖公网域名选择内容，因此代理发送 `Host $proxy_host`，不把客户端的 Host 交给上游。`$proxy_host` 来自 `proxy_pass`，实际分别是 `edugrade_api`/`edugrade_web`、`story056_api`/`story056_web`、`story060_api`/`story060_web`。

每个代理 location 同时清空 `X-Forwarded-Host` 和 `Forwarded`，防止通过另一种头把外部 authority 送到上游。`proxy_set_header` 在当前层有任何声明时就停止继承，不能只在 server 层清空后让 location 另加其他头。现有 `Origin`、`X-Forwarded-Proto`、路径、查询参数、超时和响应安全头保留原语义。前端同源 `/api/` 请求不受上游 Host 改动影响；Nginx 默认 `proxy_redirect` 仍处理 upstream authority 的绝对重定向。

`server_name _` 仍接收未知域名，测试网关也继续支持随机宿主端口。这项约束保护上游收到的 authority，不代表公网入口已经实施域名 allowlist 或 TLS；真实公网域名应由部署入口配置决定。

回环合成验收使用已安装的 Nginx（Windows 可使用官方便携版）：

```powershell
python .\infra\docker-compose\nginx\tests\check_proxy_authority.py --nginx C:\tools\nginx\nginx.exe
```

测试只把 listener 和 upstream 地址替换为回环端点，使用原代理指令验证 API、前端、健康路由、安全头、未知 Host、伪造转发头及绝对重定向。它不验证 Docker DNS、Linux 镜像或整套浏览器 E2E。当前 API 没有 WebSocket 服务入口，配置也没有 Upgrade 隧道；以后加入时需独立验证连接升级和 Origin 边界。
