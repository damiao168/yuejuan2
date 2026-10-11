# checkpoint02 新增 Python 问题

单独跟踪 19 个新 key：5 Critical、8 Major、6 Minor。17 个由本批修改，2 个 verify_typography 字典构造问题在接手时已由外部修改解消，保留当前安全 XML 解析。九个归属文件均全文审读。

5 个工具在相同本地输入下输出等价，生成写入全部截获在内存；另覆盖抽读版本匹配、旧 SHA 拒绝、范围越界拒绝和新 partial 来源补记，不继承旧完成声明。真实 TLS/配置/传输、数学 TLS 和证书测试均 exit 0，共 143 个测试 + 154 个 subtests，0 skip。每个 key 与当前 SHA 及验证报告绑定，等待 root 实际平台复扫。

| key | 级别 | 文件 | 状态 |
| --- | --- | --- | --- |
| `52b383e7-afce-4aab-99d4-18fba060d8a4` | CRITICAL | `docs/architecture/tools/finalize_delivery_records.py` | validated_fix_pending_platform_rescan |
| `1c1527ae-c2f0-4edf-a04f-b8a48bda3362` | CRITICAL | `docs/architecture/tools/fit_box_typography.py` | validated_fix_pending_platform_rescan |
| `a4a46a6b-9c41-4d91-891a-a08ad4fdbe46` | CRITICAL | `docs/architecture/tools/retire_stale_reviews.py` | validated_fix_pending_platform_rescan |
| `0f3d55da-1188-4581-8dac-2499c0709621` | CRITICAL | `docs/architecture/tools/write_frontend_specs.py` | validated_fix_pending_platform_rescan |
| `5b6b5d0e-fb6e-4719-ae68-d3cc47b6818b` | CRITICAL | `docs/architecture/tools/write_frontend_specs.py` | validated_fix_pending_platform_rescan |
| `f2fcdb03-5d4f-454e-a909-0d702893428b` | MAJOR | `ai-services/tests/test_model_transport_security.py` | validated_fix_pending_platform_rescan |
| `9174a6ed-e229-42cf-bb75-bc5232b4eec4` | MAJOR | `ai-services/tests/test_server_tls_handshake.py` | validated_fix_pending_platform_rescan |
| `821e82a0-2c20-4239-8efa-32982d052bbe` | MAJOR | `ai-services/tests/test_server_tls_handshake.py` | validated_fix_pending_platform_rescan |
| `ead07595-6c96-47f2-a1e8-b480c4a1731d` | MAJOR | `ai-services/tests/test_server_tls_handshake.py` | validated_fix_pending_platform_rescan |
| `0dbe8090-525c-4ef2-ac35-9793f8eca03c` | MAJOR | `ai-services/tests/test_server_tls_handshake.py` | validated_fix_pending_platform_rescan |
| `05751b2e-4f9d-439b-8076-c16b596cffc4` | MAJOR | `services/math-verification-worker/tests/test_server_tls_batch54.py` | validated_fix_pending_platform_rescan |
| `d594d48e-5fba-4e0f-9d58-06617ab93fc8` | MAJOR | `services/math-verification-worker/tests/test_server_tls_batch54.py` | validated_fix_pending_platform_rescan |
| `91c32d4c-9dc7-4158-b8be-ac18462db392` | MAJOR | `tests/infra/test_internal_test_certificates.py` | validated_fix_pending_platform_rescan |
| `3e6e4231-098d-492a-8d3e-fc9a26f6589a` | MINOR | `docs/architecture/tools/finalize_delivery_records.py` | validated_fix_pending_platform_rescan |
| `f1912c02-01b9-4658-b729-66c418f8cf62` | MINOR | `docs/architecture/tools/finalize_delivery_records.py` | validated_fix_pending_platform_rescan |
| `62ff6fb6-69a9-4e74-966a-5fabc9c97966` | MINOR | `docs/architecture/tools/finalize_delivery_records.py` | validated_fix_pending_platform_rescan |
| `1f77c921-711f-487d-8848-2f09bb3003f4` | MINOR | `docs/architecture/tools/finalize_delivery_records.py` | validated_fix_pending_platform_rescan |
| `9c64a937-6ff7-4c05-ae68-f279043238a6` | MINOR | `docs/architecture/tools/verify_typography.py` | validated_existing_resolution_pending_platform_rescan |
| `20be2f43-bae8-4be8-aab2-510e954d2e85` | MINOR | `docs/architecture/tools/verify_typography.py` | validated_existing_resolution_pending_platform_rescan |