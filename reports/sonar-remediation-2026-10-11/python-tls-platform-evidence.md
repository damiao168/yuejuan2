# 原两条生产监听 TLS 告警的平台评审证据

两服务生产和未知环境都先检查证书策略，随后包装 TLS socket，再进入服务循环。ThreadingHTTPServer 构造告警未反映后续 socket 的真实 TLS 行为。未改生产 TLS 源码、未加入抑制或排除。

当前 SHA 的真实 TLS/配置测试全部通过：grading 42 个测试 + 154 个 subtests，数学 96 个测试，均实际 exit 0 / 0 skip。可信 CA / SAN 的 HTTPS、错误 CA / SAN 拒绝、明文拒绝、资源回收均有既有测试证据；数学另验证 TLS1.1 拒绝和 TLS1.2 / 1.3 可用。

- `7f939914-9bb6-4005-80a0-4c2efe12eedb`：`ai-services/grading_agent/server.py`，SHA `62c2b234657f1f29d444968d8ce815647f820b1b8c7b3eb7d6a6db6d617c637e`。建议 root 按详细 JSON 证据评审平台误报处置，当前处置仍为 pending_root。
- `0cb24e71-bce5-4ed4-b2c5-ca9aac275754`：`services/math-verification-worker/math_verification_worker/server.py`，SHA `d9d6a02c89cea615738f507e3462e185e4c9992537acdb6b93ce28427f9a4f1f`。建议 root 按详细 JSON 证据评审平台误报处置，当前处置仍为 pending_root。