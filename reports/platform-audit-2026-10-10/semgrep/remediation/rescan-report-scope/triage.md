# 报告范围定向复扫核验

2026-10-11，Semgrep 1.179.0 Windows native beta / OSS；735条配置，421条实际运行，21.875秒。
46个显式固定副本文件全部列入 paths.scanned，包括原105条命中的全部37个文件；没有按目录默认忽略tests来代替报告文件验收。这是报告范围定向复扫，不是全仓复扫。

真实结果：65条命中、3条PartialParsing。65条均结合命中原字节offset、相邻代码及调用来源重新核验，未抑制规则，未为消除误报修改业务代码；27个命中文件不因此被声称已完成整文件审读。

按规范化rule/path计数，原105条中63条类计数保留、42条减少、2条新增。42条减少 = 35个本次处理位置不再命中 + SG-090此前已修复1条 + Nginx既有安全头上下文匹配差异6条。本次36个处置位置 = 35个不再命中 + SG-034解析边界修复后仍残留的标准库XML生成import误报1条。不能把42的扫描减幅写成42处漏洞修复。

SG-034残余import只使用标准库ET生成、排版和序列化；解析使用defusedxml safe_xml.parse并禁止DTD。新增2条dangerous-subprocess命中分别是TLS测试固定OpenSSL证书生成和Nginx测试固定-v查询，可执行文件只由本地测试操作者选择、参数用数组且不开shell，未见生产API用户可控命令。

三条PartialParsing涉及两个E2E Dockerfile和OCR Dockerfile。SG-091的失败解析区域包含最终USER，因此原USER类告警消失不能作为运行身份保证；静态代码明确USER 10001:10001，但Docker引擎无响应，未声称容器运行身份通过。OCR仍命中安装阶段USER root；完整文件末尾为USER nobody。

Windows结果部分Go命中的reported line与原字节offset推算行号不同；下表同时保留二者，原始JSON没有改写。46个扫描副本SHA-256均与比对时工作区相同，见comparison.json。

| 编号 | 文件 | Semgrep行 / 原字节行 | 核验结论 | 依据 |
| --- | --- | --- | --- | --- |
| RS-001 | `ai-services/grading_agent/managed_model.py` | 62 / 62 | 误报/已有控制 | 请求只接受 HTTPS，DNS 全部地址要求 global，socket 固定已验证地址但保留原主机 SNI。TLS client context 强制证书链和主机名校验；HTTP 状态非2xx报错，不自动跟随重定向。 |
| RS-002 | `ai-services/tests/test_http.py` | 29 / 29 | 误报/测试fixture | 测试自行启动127.0.0.1随机端口服务；URL来自该fixture并有timeout。服务令牌为合成测试值，没有生产用户可控目标或真实服务凭据输入。 |
| RS-003 | `ai-services/tests/test_http.py` | 31 / 31 | 误报/测试fixture | 测试自行启动127.0.0.1随机端口服务；URL来自该fixture并有timeout。服务令牌为合成测试值，没有生产用户可控目标或真实服务凭据输入。 |
| RS-004 | `ai-services/tests/test_http.py` | 36 / 36 | 误报/测试fixture | 测试自行启动127.0.0.1随机端口服务；URL来自该fixture并有timeout。服务令牌为合成测试值，没有生产用户可控目标或真实服务凭据输入。 |
| RS-005 | `ai-services/tests/test_http.py` | 50 / 50 | 误报/测试fixture | 测试自行启动127.0.0.1随机端口服务；URL来自该fixture并有timeout。服务令牌为合成测试值，没有生产用户可控目标或真实服务凭据输入。 |
| RS-006 | `ai-services/tests/test_http.py` | 64 / 64 | 误报/测试fixture | 测试自行启动127.0.0.1随机端口服务；URL来自该fixture并有timeout。服务令牌为合成测试值，没有生产用户可控目标或真实服务凭据输入。 |
| RS-007 | `ai-services/tests/test_http.py` | 75 / 75 | 误报/测试fixture | 测试自行启动127.0.0.1随机端口服务；URL来自该fixture并有timeout。服务令牌为合成测试值，没有生产用户可控目标或真实服务凭据输入。 |
| RS-008 | `ai-services/tests/test_http.py` | 117 / 117 | 误报/测试fixture | 测试自行启动127.0.0.1随机端口服务；URL来自该fixture并有timeout。服务令牌为合成测试值，没有生产用户可控目标或真实服务凭据输入。 |
| RS-009 | `ai-services/tests/test_http.py` | 151 / 151 | 误报/测试fixture | 测试自行启动127.0.0.1随机端口服务；URL来自该fixture并有timeout。服务令牌为合成测试值，没有生产用户可控目标或真实服务凭据输入。 |
| RS-010 | `ai-services/tests/test_math_v2_production.py` | 164 / 164 | 误报/测试fixture | 测试自行启动127.0.0.1随机端口服务；URL来自该fixture并有timeout。服务令牌为合成测试值，没有生产用户可控目标或真实服务凭据输入。 |
| RS-011 | `ai-services/tests/test_model_transport_security.py` | 100 / 100 | 新增/可信本地测试工具 | OpenSSL来自本地测试环境变量或shutil.which；要求绝对路径并strict resolve。参数为固定req选项与随机临时证书文件，shell未启用。测试操作者需要选择可信本地可执行文件，未见生产API用户输入。 |
| RS-012 | `apps/web-admin/nginx.conf` | 17 / 17 | 误报/已有控制 | health location声明add_header会停止继承；此处同时重复server层全部安全响应头和always，没有丢掉安全策略。 |
| RS-013 | `apps/web-admin/nginx.conf` | 19 / 19 | 误报/已有控制 | health location声明add_header会停止继承；此处同时重复server层全部安全响应头和always，没有丢掉安全策略。 |
| RS-014 | `apps/web-admin/nginx.conf` | 20 / 20 | 误报/已有控制 | health location声明add_header会停止继承；此处同时重复server层全部安全响应头和always，没有丢掉安全策略。 |
| RS-015 | `apps/web-admin/nginx.conf` | 21 / 21 | 误报/已有控制 | health location声明add_header会停止继承；此处同时重复server层全部安全响应头和always，没有丢掉安全策略。 |
| RS-016 | `apps/web-admin/nginx.conf` | 22 / 22 | 误报/已有控制 | health location声明add_header会停止继承；此处同时重复server层全部安全响应头和always，没有丢掉安全策略。 |
| RS-017 | `apps/web-admin/nginx.conf` | 23 / 23 | 误报/已有控制 | health location声明add_header会停止继承；此处同时重复server层全部安全响应头和always，没有丢掉安全策略。 |
| RS-018 | `apps/web-admin/src/auth/mfaFlow.test.ts` | 196 / 196 | 误报/测试fixture | MFA测试故意把script字样放入mock recovery_codes，断言confirm变uncertain且phase关闭；不是把不可信值写入DOM的生产代码。 |
| RS-019 | `apps/web-admin/src/auth/mfaFlow.test.ts` | 196 / 196 | 误报/测试fixture | MFA测试故意把script字样放入mock recovery_codes，断言confirm变uncertain且phase关闭；不是把不可信值写入DOM的生产代码。 |
| RS-020 | `apps/web-admin/src/auth/mfaFlow.test.ts` | 196 / 196 | 误报/测试fixture | MFA测试故意把script字样放入mock recovery_codes，断言confirm变uncertain且phase关闭；不是把不可信值写入DOM的生产代码。 |
| RS-021 | `docs/architecture/tools/build_diagrams.py` | 6 / 6 | 修复后XML生成import残余误报 | SG-034的标准库ET仅用于Element/SubElement/indent/write生成与序列化；生成文件再解析使用safe_xml.parse(forbid_dtd=True)。import命中没有区分生成与解析，解析边界已修复但保留标准库生成API。 |
| RS-022 | `infra/docker-compose/nginx/story056-e2e.conf` | 18 / 18 | 误报/测试fixture | 隔离E2E网关health仅设置Content-Type；server层没有需要继承的add_header。此测试配置不能当作生产安全响应头策略。 |
| RS-023 | `infra/docker-compose/nginx/story060-e2e.conf` | 18 / 18 | 误报/测试fixture | 隔离E2E网关health仅设置Content-Type；server层没有需要继承的add_header。此测试配置不能当作生产安全响应头策略。 |
| RS-024 | `infra/docker-compose/nginx/tests/check_proxy_authority.py` | 143 / 143 | 新增/可信本地测试工具 | 命中测试者显式--nginx本地路径的固定-v版本查询；路径resolve为绝对地址，subprocess参数为数组，shell未启用。测试操作者需要选择可信本地可执行文件，未见生产API用户输入。 |
| RS-025 | `scripts/test_fujian_math_batch29.py` | 25 / 25 | 误报/测试fixture | 测试从仓库固定脚本读取AST，只选择固定白名单的三个FunctionDef并compile/exec；不是生产接口接受的用户源码。仓库源文件本身需可信。 |
| RS-026 | `services/api-gateway/internal/auth/cookies.go` | 10 / 10 | 误报/已有控制 | 实际Cookie字面量包含HttpOnly=true和Secure=h.cookieSecure。构造器接收配置，生产类环境默认Secure=true并拒绝false启动；本地HTTP测试允许配置false。 |
| RS-027 | `services/api-gateway/internal/auth/cookies.go` | 27 / 27 | 误报/已有控制 | 实际Cookie字面量包含HttpOnly=true和Secure=h.cookieSecure。构造器接收配置，生产类环境默认Secure=true并拒绝false启动；本地HTTP测试允许配置false。 |
| RS-028 | `services/api-gateway/internal/auth/cookies.go` | 54 / 54 | 误报/已有控制 | 实际Cookie字面量包含HttpOnly=true和Secure=h.cookieSecure。构造器接收配置，生产类环境默认Secure=true并拒绝false启动；本地HTTP测试允许配置false。 |
| RS-029 | `services/api-gateway/internal/auth/cookies.go` | 62 / 62 | 误报/已有控制 | 实际Cookie字面量包含HttpOnly=true和Secure=h.cookieSecure。构造器接收配置，生产类环境默认Secure=true并拒绝false启动；本地HTTP测试允许配置false。 |
| RS-030 | `services/api-gateway/internal/auth/mfa_http_test.go` | 36 / 36 | 误报/测试fixture | httptest.AddCookie把合成Cookie作为请求头输入，用于验证CSRF/授权边界；这是请求fixture，不是浏览器Set-Cookie响应配置。 |
| RS-031 | `services/api-gateway/internal/auth/mfa_http_test.go` | 36 / 36 | 误报/测试fixture | httptest.AddCookie把合成Cookie作为请求头输入，用于验证CSRF/授权边界；这是请求fixture，不是浏览器Set-Cookie响应配置。 |
| RS-032 | `services/api-gateway/internal/files/store_postgres.go` | 221 / 221 | 误报/已有控制 | 拼接部分仅由内部状态列表长度生成$7以后数字占位符及固定assetColumns；租户、资产、revision、状态和detail均通过args绑定。 |
| RS-033 | `services/api-gateway/internal/middleware/middleware_test.go` | 64 / 64 | 误报/测试fixture | httptest.AddCookie把合成Cookie作为请求头输入，用于验证CSRF/授权边界；这是请求fixture，不是浏览器Set-Cookie响应配置。 |
| RS-034 | `services/api-gateway/internal/middleware/middleware_test.go` | 64 / 64 | 误报/测试fixture | httptest.AddCookie把合成Cookie作为请求头输入，用于验证CSRF/授权边界；这是请求fixture，不是浏览器Set-Cookie响应配置。 |
| RS-035 | `services/api-gateway/internal/middleware/middleware_test.go` | 78 / 78 | 误报/测试fixture | httptest.AddCookie把合成Cookie作为请求头输入，用于验证CSRF/授权边界；这是请求fixture，不是浏览器Set-Cookie响应配置。 |
| RS-036 | `services/api-gateway/internal/middleware/middleware_test.go` | 78 / 78 | 误报/测试fixture | httptest.AddCookie把合成Cookie作为请求头输入，用于验证CSRF/授权边界；这是请求fixture，不是浏览器Set-Cookie响应配置。 |
| RS-037 | `services/api-gateway/internal/middleware/middleware_test.go` | 93 / 93 | 误报/测试fixture | httptest.AddCookie把合成Cookie作为请求头输入，用于验证CSRF/授权边界；这是请求fixture，不是浏览器Set-Cookie响应配置。 |
| RS-038 | `services/api-gateway/internal/middleware/middleware_test.go` | 93 / 93 | 误报/测试fixture | httptest.AddCookie把合成Cookie作为请求头输入，用于验证CSRF/授权边界；这是请求fixture，不是浏览器Set-Cookie响应配置。 |
| RS-039 | `services/api-gateway/internal/middleware/middleware_test.go` | 132 / 132 | 误报/测试fixture | httptest.AddCookie把合成Cookie作为请求头输入，用于验证CSRF/授权边界；这是请求fixture，不是浏览器Set-Cookie响应配置。 |
| RS-040 | `services/api-gateway/internal/middleware/middleware_test.go` | 132 / 132 | 误报/测试fixture | httptest.AddCookie把合成Cookie作为请求头输入，用于验证CSRF/授权边界；这是请求fixture，不是浏览器Set-Cookie响应配置。 |
| RS-041 | `services/api-gateway/internal/minioadapter/client_test.go` | 73 / 73 | 误报/测试fixture | 这是故意设置MaxVersion=TLS1.1的httptest服务端，用来断言真实MinIO客户端在发送HTTP前拒绝旧协议；不是客户端生产TLS配置。 |
| RS-042 | `services/api-gateway/internal/modelgovernance/managed_config_probe.go` | 14 / 14 | 误报/已有控制 | math/rand只生成幂等模型列表探测重试的短暂停顿jitter；令牌、密钥与认证随机数没有使用此调用。 |
| RS-043 | `services/api-gateway/internal/score/store_postgres.go` | 100 / 100 | 误报/已有控制 | where由固定SQL片段与按args长度生成的数字占位符组成；tenant/exam/status/query均绑定参数，查询文本不会拼为SQL代码。 |
| RS-044 | `services/api-gateway/internal/server/e2e_business_command_test.go` | 354 / 354 | 误报/测试fixture | 函数/触发器名由固定前缀和去连字符UUID生成，commandID也是本地UUID；在临时E2E数据库中构造回滚故障，没有外部字符串输入。 |
| RS-045 | `services/api-gateway/internal/server/e2e_business_command_test.go` | 362 / 362 | 误报/测试fixture | 函数/触发器名由固定前缀和去连字符UUID生成，commandID也是本地UUID；在临时E2E数据库中构造回滚故障，没有外部字符串输入。 |
| RS-046 | `services/api-gateway/internal/server/e2e_business_command_test.go` | 363 / 363 | 误报/测试fixture | 函数/触发器名由固定前缀和去连字符UUID生成，commandID也是本地UUID；在临时E2E数据库中构造回滚故障，没有外部字符串输入。 |
| RS-047 | `services/api-gateway/internal/server/e2e_business_command_test.go` | 365 / 365 | 误报/测试fixture | 函数/触发器名由固定前缀和去连字符UUID生成，commandID也是本地UUID；在临时E2E数据库中构造回滚故障，没有外部字符串输入。 |
| RS-048 | `services/api-gateway/internal/server/e2e_compose_bootstrap_test.go` | 49 / 49 | 误报/测试fixture | 数据库/角色名由固定前缀和本地UUID生成并经pgx.Identifier.Sanitize引用；密码为测试现场生成的固定前缀和UUID。仅操作可丢弃E2E数据库。 |
| RS-049 | `services/api-gateway/internal/server/e2e_compose_bootstrap_test.go` | 53 / 53 | 误报/测试fixture | 数据库/角色名由固定前缀和本地UUID生成并经pgx.Identifier.Sanitize引用；密码为测试现场生成的固定前缀和UUID。仅操作可丢弃E2E数据库。 |
| RS-050 | `services/api-gateway/internal/server/e2e_compose_bootstrap_test.go` | 57 / 57 | 误报/测试fixture | 数据库/角色名由固定前缀和本地UUID生成并经pgx.Identifier.Sanitize引用；密码为测试现场生成的固定前缀和UUID。仅操作可丢弃E2E数据库。 |
| RS-051 | `services/api-gateway/internal/server/e2e_mfa_test.go` | 319 / 319 | 误报/测试fixture | 表名只由调用处固定四表白名单传入；隔离角色由本地UUID生成且pgx.Identifier.Sanitize引用，密码也由固定前缀和UUID生成。 |
| RS-052 | `services/api-gateway/internal/server/e2e_mfa_test.go` | 360 / 360 | 误报/测试fixture | 表名只由调用处固定四表白名单传入；隔离角色由本地UUID生成且pgx.Identifier.Sanitize引用，密码也由固定前缀和UUID生成。 |
| RS-053 | `services/api-gateway/internal/server/e2e_mfa_test.go` | 364 / 364 | 误报/测试fixture | 表名只由调用处固定四表白名单传入；隔离角色由本地UUID生成且pgx.Identifier.Sanitize引用，密码也由固定前缀和UUID生成。 |
| RS-054 | `services/api-gateway/internal/server/e2e_mfa_test.go` | 368 / 368 | 误报/测试fixture | 表名只由调用处固定四表白名单传入；隔离角色由本地UUID生成且pgx.Identifier.Sanitize引用，密码也由固定前缀和UUID生成。 |
| RS-055 | `services/api-gateway/internal/server/e2e_postgres_test.go` | 354 / 354 | 误报/测试fixture | 数据库/角色名由固定前缀和本地UUID生成并经pgx.Identifier.Sanitize引用；密码为测试现场生成的固定前缀和UUID。仅操作可丢弃E2E数据库。 |
| RS-056 | `services/api-gateway/internal/server/e2e_postgres_test.go` | 357 / 357 | 误报/测试fixture | 数据库/角色名由固定前缀和本地UUID生成并经pgx.Identifier.Sanitize引用；密码为测试现场生成的固定前缀和UUID。仅操作可丢弃E2E数据库。 |
| RS-057 | `services/api-gateway/internal/server/e2e_postgres_test.go` | 361 / 361 | 误报/测试fixture | 数据库/角色名由固定前缀和本地UUID生成并经pgx.Identifier.Sanitize引用；密码为测试现场生成的固定前缀和UUID。仅操作可丢弃E2E数据库。 |
| RS-058 | `services/api-gateway/internal/server/e2e_postgres_test.go` | 711 / 711 | 误报/测试fixture | 数据库/角色名由固定前缀和本地UUID生成并经pgx.Identifier.Sanitize引用；密码为测试现场生成的固定前缀和UUID。仅操作可丢弃E2E数据库。 |
| RS-059 | `services/api-gateway/internal/server/e2e_postgres_test.go` | 722 / 722 | 误报/测试fixture | 数据库/角色名由固定前缀和本地UUID生成并经pgx.Identifier.Sanitize引用；密码为测试现场生成的固定前缀和UUID。仅操作可丢弃E2E数据库。 |
| RS-060 | `services/api-gateway/internal/server/e2e_postgres_test.go` | 730 / 730 | 误报/测试fixture | 数据库/角色名由固定前缀和本地UUID生成并经pgx.Identifier.Sanitize引用；密码为测试现场生成的固定前缀和UUID。仅操作可丢弃E2E数据库。 |
| RS-061 | `services/api-gateway/internal/server/e2e_question_bank_import_test.go` | 166 / 166 | 误报/测试fixture | 故障触发器名称为固定字符串，拼入的secondID来自本地question fixture的UUID；在临时E2E数据库测试局部失败/重试，不接收用户SQL片段。 |
| RS-062 | `services/api-gateway/internal/server/e2e_question_bank_test.go` | 358 / 358 | 误报/测试fixture | qbCounts仅遍历函数内固定五表白名单，外部请求不能提供表名；仅统计隔离E2E数据库状态。 |
| RS-063 | `services/math-verification-worker/tests/test_timeout.py` | 34 / 34 | 误报/测试fixture | 仅pickle.dumps可信测试内存结果，断言大于管道缓冲区以覆盖spawn回归；没有pickle.loads/load读取外部数据。 |
| RS-064 | `services/ocr-worker/Dockerfile` | 6 / 6 | 误报/须保留解析限制 | OCR USER root用于依赖安装，真实文件末尾USER nobody后才运行CMD。此文件PartialParsing跨越最后USER，不能把扫描解析到的root解释为最终运行身份；Docker运行身份未验收。 |
| RS-065 | `services/page-processing-worker/tests/test_main.py` | 47 / 47 | 误报/测试fixture | 默认字符串仅在tests/test_main的_config返回给FakeClient/FakeRunner和monkeypatch；不是生产Worker密码配置。报告不复述测试值。 |
