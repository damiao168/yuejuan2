# Semgrep 报告修复与验证结果

日期：2026-10-11（Asia/Shanghai）。

已处置原报告全部 **105 条命中**：**36 条本次修复、1 条既有修复复核、68 条误报/已有控制保留核验说明**。原报告可处理项均已完成代码处置；运行验收限制另列。

## 修复内容

- SG-013～015、092～094、097～102、104：13 个凭据请求入口拒绝自动跳转；同源也拒绝，失败时关闭原响应。保留初始目标、TLS、超时、响应限额、错误映射及任务租约。
- SG-001～011：11 个 Dependabot 更新项设置普通版本 7 天 cooldown。安全更新不受该缓冲期影响，官方依据见 [GitHub 文档](https://docs.github.com/en/code-security/reference/supply-chain-security/dependabot-options-reference#cooldown)。
- SG-034～037：两处 XML 读取入口使用 defusedxml，拒绝 DTD、实体及外部引用；固定工具依赖与维护说明已同步。
- SG-044～045、047～048、050～051：三份 Nginx 使用固定上游 Host，并清除传入 Forwarded/X-Forwarded-Host。
- SG-091：E2E 客户端最终镜像使用 UID/GID 10001 和可写 HOME；SG-090 的既有非 root 修复保留。
- SG-105：E2E 客户端拒绝跳转，JSON 响应设 4 MiB 读取上限。

完整复扫额外发现 build_preview.py 和 verify_typography.py 的3条同类XML命中；两个解析入口同步迁移到 defusedxml 并拒绝DTD，补充测试和维护说明。这些新增处置单独记录，不混入原105条统计。

## 验证

模型服务 173 项测试通过；Worker 首轮 OCR 358 项和 68 subtests、主观题22项、图像质量84项通过；页面处理顺序复跑163项通过。响应关闭修正后，根代理的网络/TLS/租约/错误/XML回归237项通过，其中包括20个资源释放用例。XML单独12项、架构工具Node测试3项通过。相关Go 7个包及E2E客户端包通过，定向Ruff和差异空白检查通过。

三份配置用官方 Windows Nginx 1.29.0 真实运行，48 个回环请求通过；包括恶意 Host、伪造转发头、API/前端/健康、安全响应头及前端重定向兼容性。详见 [运行证据](nginx-authority-runtime.json)。

全仓复扫使用官方 Semgrep 1.179.0 的 Windows beta 原生版、同一份735条规则配置（实际运行434条）、OSS和单job。补充XML修复前快照包含 3236 个文本文件，引擎实际扫描 2605 个目标，CLI记录586个忽略路径。退出码 0，快照内 **27 条规则命中**、**18 条PartialParsing诊断**。与原Docker扫描覆盖不同，不能把105→27全算成修复，也不能用退出码或命中消失证明生产安全。

随后显式扫描报告范围及新增测试共46个文件（421条实际规则）：全部进入paths.scanned，原37个命中文件全覆盖，65条命中、3个PartialParsing。原rule/path减少42个，其中35个本次处置位置、1个SG-090既有修复、6个已有Nginx安全头匹配差异；另1个本次处置SG-034保留标准库生成import残余误报。新增两条subprocess命中是可信本地测试工具调用，已核对固定参数和输入边界。

补充XML修复后再显式扫描 5 个当前文件：0 条命中、0 条诊断，全部目标覆盖；当前字节与该次扫描记录一致。新增19项测试通过，合并原XML测试共31项；临时正常输出与补充基线一致。证据见 [补充XML结果](supplemental-xml-results.json)。

收尾期间两份原误报测试文件又发生外部修改，另存当前固定副本并增量审核、显式扫描：10条命中、0条诊断，目标全部覆盖，误报依据复核见 [最后增量核验](rescan-latest-external/review-results.json)。原105条全部当前文件字节与各自最后核验扫描一致；此前快照和原始扫描保持独立保留。

复扫详细判定见 [复扫核验](rescan-triage.md)；[原始结果](rescan/semgrep-raw.json)、[元数据](rescan/scan-metadata.json)、[范围清单](rescan/source-inventory.json)另存。

原Docker引擎与WSL调用超时，因此改用隔离虚拟环境安装的同版本官方Windows wheel。文件SHA-256已对照官方PyPI元数据，见 [工具来源](tools/tool-provenance.json)。配置本地规则、关闭metrics/version-check、空平台令牌，不调用ci/login/upload；Windows运行没有容器级禁网隔离。

## 保留与边界

首次 REPORT.md、原始扫描JSON、规则、历史迁移、生成SDK和带哈希提示词均保留；本次没有提交或推送。命中文件修复前原字节及既有测试备份在 baseline/files，原工作区状态在 baseline/workspace-status.txt；workspace-after-xml.patch 是较晚捕获的辅助差异，不能冒充开工前全仓补丁。

修复期间另有8个命中文件发生外部变动（测试注释/格式、测试函数拆分、只读模型工具辅助函数提取等），未回退或覆盖；相关误报依据在当前命中函数仍成立。字节变化逐文件记录，不能据此宣称全文审读完成。

本次未做Linux Docker镜像构建/最终UID运行验证、完整Compose/浏览器E2E、真实供应商或生产部署验证。两个E2E Dockerfile的部分解析涉及最终指令，告警消失仅作辅助记录；USER/目录权限以静态审核为依据。Nginx仍接收未知入口域名；上游authority已收敛，公网allowlist/TLS由实际部署核验。过期租约用例采用合成服务端403，不替代真实网关到期集成测试。依赖跳转的部署地址需改成最终授权地址。

自动审批策略拒绝测试运行时目录清理，便携文件保留在test-runtime下，测试Nginx进程已经退出。

## 逐项处置

位置沿用首次扫描行号；当前代码位置可能变化。误报依据来自原逐项核验并结合当前增量、字节和相关回归复核；不将负向TLS测试、参数化SQL、请求Cookie fixture或非密码学重试抖动改写成漏洞修复。

| ID | 原扫描位置 | 处置 | 实现/依据 |
| --- | --- | --- | --- |
| SG-001 | `.github/dependabot.yml:4` | 本次修复 | 显式普通版本 cooldown 7 天；GitHub 安全更新豁免 |
| SG-002 | `.github/dependabot.yml:13` | 本次修复 | 显式普通版本 cooldown 7 天；GitHub 安全更新豁免 |
| SG-003 | `.github/dependabot.yml:22` | 本次修复 | 显式普通版本 cooldown 7 天；GitHub 安全更新豁免 |
| SG-004 | `.github/dependabot.yml:31` | 本次修复 | 显式普通版本 cooldown 7 天；GitHub 安全更新豁免 |
| SG-005 | `.github/dependabot.yml:40` | 本次修复 | 显式普通版本 cooldown 7 天；GitHub 安全更新豁免 |
| SG-006 | `.github/dependabot.yml:49` | 本次修复 | 显式普通版本 cooldown 7 天；GitHub 安全更新豁免 |
| SG-007 | `.github/dependabot.yml:58` | 本次修复 | 显式普通版本 cooldown 7 天；GitHub 安全更新豁免 |
| SG-008 | `.github/dependabot.yml:67` | 本次修复 | 显式普通版本 cooldown 7 天；GitHub 安全更新豁免 |
| SG-009 | `.github/dependabot.yml:76` | 本次修复 | 显式普通版本 cooldown 7 天；GitHub 安全更新豁免 |
| SG-010 | `.github/dependabot.yml:85` | 本次修复 | 显式普通版本 cooldown 7 天；GitHub 安全更新豁免 |
| SG-011 | `.github/dependabot.yml:94` | 本次修复 | 显式普通版本 cooldown 7 天；GitHub 安全更新豁免 |
| SG-012 | `ai-services/grading_agent/managed_model.py:62` | 误报/已有控制 | managed_model.public_json_transport 先限制 HTTPS，拒绝任何非公网 DNS 结果，再固定已校验 IP；HTTPSConnection 保留原 hostname 进行 SNI、证书链和主机名验证，响应有 4MB 上限并关闭连接。未发现由此调用直接引入的 SSRF/TLS 绕过；未来 Python 升级需关注 _create_connection 私有接口兼容性。 |
| SG-013 | `ai-services/grading_agent/model.py:456` | 本次修复 | 局部 build_opener 对带模型凭据的 POST 以及 GET 就绪请求拒绝自动跳转，同源也拒绝；跳转响应关闭后按原 HTTPError 流程失败，未修改全局 opener 或默认 TLS 校验。 |
| SG-014 | `ai-services/grading_agent/model.py:604` | 本次修复 | 局部 build_opener 对带模型凭据的 POST 以及 GET 就绪请求拒绝自动跳转，同源也拒绝；跳转响应关闭后按原 HTTPError 流程失败，未修改全局 opener 或默认 TLS 校验。 |
| SG-015 | `ai-services/grading_agent/model.py:733` | 本次修复 | 局部 build_opener 对带模型凭据的 POST 以及 GET 就绪请求拒绝自动跳转，同源也拒绝；跳转响应关闭后按原 HTTPError 流程失败，未修改全局 opener 或默认 TLS 校验。 |
| SG-016 | `ai-services/tests/test_http.py:29` | 误报/已有控制 | 测试在 127.0.0.1 随机端口启动测试 HTTP 服务，URL 由 server_port 拼出，调用设置 timeout=2；测试覆盖认证、幂等和解析结果，未使用普通用户提供的目标 URL。此告警不代表生产 SSRF。 |
| SG-017 | `ai-services/tests/test_http.py:31` | 误报/已有控制 | 测试在 127.0.0.1 随机端口启动测试 HTTP 服务，URL 由 server_port 拼出，调用设置 timeout=2；测试覆盖认证、幂等和解析结果，未使用普通用户提供的目标 URL。此告警不代表生产 SSRF。 |
| SG-018 | `ai-services/tests/test_http.py:36` | 误报/已有控制 | 测试在 127.0.0.1 随机端口启动测试 HTTP 服务，URL 由 server_port 拼出，调用设置 timeout=2；测试覆盖认证、幂等和解析结果，未使用普通用户提供的目标 URL。此告警不代表生产 SSRF。 |
| SG-019 | `ai-services/tests/test_http.py:50` | 误报/已有控制 | 测试在 127.0.0.1 随机端口启动测试 HTTP 服务，URL 由 server_port 拼出，调用设置 timeout=2；测试覆盖认证、幂等和解析结果，未使用普通用户提供的目标 URL。此告警不代表生产 SSRF。 |
| SG-020 | `ai-services/tests/test_http.py:64` | 误报/已有控制 | 测试在 127.0.0.1 随机端口启动测试 HTTP 服务，URL 由 server_port 拼出，调用设置 timeout=2；测试覆盖认证、幂等和解析结果，未使用普通用户提供的目标 URL。此告警不代表生产 SSRF。 |
| SG-021 | `ai-services/tests/test_http.py:75` | 误报/已有控制 | 测试在 127.0.0.1 随机端口启动测试 HTTP 服务，URL 由 server_port 拼出，调用设置 timeout=2；测试覆盖认证、幂等和解析结果，未使用普通用户提供的目标 URL。此告警不代表生产 SSRF。 |
| SG-022 | `ai-services/tests/test_http.py:117` | 误报/已有控制 | 测试在 127.0.0.1 随机端口启动测试 HTTP 服务，URL 由 server_port 拼出，调用设置 timeout=2；测试覆盖认证、幂等和解析结果，未使用普通用户提供的目标 URL。此告警不代表生产 SSRF。 |
| SG-023 | `ai-services/tests/test_http.py:151` | 误报/已有控制 | 测试在 127.0.0.1 随机端口启动测试 HTTP 服务，URL 由 server_port 拼出，调用设置 timeout=2；测试覆盖认证、幂等和解析结果，未使用普通用户提供的目标 URL。此告警不代表生产 SSRF。 |
| SG-024 | `ai-services/tests/test_math_v2_production.py:164` | 误报/已有控制 | 测试在 127.0.0.1 随机端口启动测试 HTTP 服务，URL 由 server_port 拼出，调用设置 timeout=2；测试覆盖认证、幂等和解析结果，未使用普通用户提供的目标 URL。此告警不代表生产 SSRF。 |
| SG-025 | `apps/web-admin/nginx.conf:17` | 误报/已有控制 | apps/web-admin/nginx.conf:14-24 的 /health location 已显式重复所有 server 安全头；17 行 Content-Type 并未造成 CSP、X-Frame-Options 等丢失。19-23 行正是修复继承边界的防护代码。 |
| SG-026 | `apps/web-admin/nginx.conf:19` | 误报/已有控制 | apps/web-admin/nginx.conf:14-24 的 /health location 已显式重复所有 server 安全头；17 行 Content-Type 并未造成 CSP、X-Frame-Options 等丢失。19-23 行正是修复继承边界的防护代码。 |
| SG-027 | `apps/web-admin/nginx.conf:20` | 误报/已有控制 | apps/web-admin/nginx.conf:14-24 的 /health location 已显式重复所有 server 安全头；17 行 Content-Type 并未造成 CSP、X-Frame-Options 等丢失。19-23 行正是修复继承边界的防护代码。 |
| SG-028 | `apps/web-admin/nginx.conf:21` | 误报/已有控制 | apps/web-admin/nginx.conf:14-24 的 /health location 已显式重复所有 server 安全头；17 行 Content-Type 并未造成 CSP、X-Frame-Options 等丢失。19-23 行正是修复继承边界的防护代码。 |
| SG-029 | `apps/web-admin/nginx.conf:22` | 误报/已有控制 | apps/web-admin/nginx.conf:14-24 的 /health location 已显式重复所有 server 安全头；17 行 Content-Type 并未造成 CSP、X-Frame-Options 等丢失。19-23 行正是修复继承边界的防护代码。 |
| SG-030 | `apps/web-admin/nginx.conf:23` | 误报/已有控制 | apps/web-admin/nginx.conf:14-24 的 /health location 已显式重复所有 server 安全头；17 行 Content-Type 并未造成 CSP、X-Frame-Options 等丢失。19-23 行正是修复继承边界的防护代码。 |
| SG-031 | `apps/web-admin/src/auth/mfaFlow.test.ts:196` | 误报/已有控制 | mfaFlow.test.ts:194-199 向 mock response 注入固定 <script> 字符串，断言 confirm 返回 uncertain 且流程关闭；这里不是脚本模板插值或 DOM 执行。同一行的三条命中属于同一负向测试。 |
| SG-032 | `apps/web-admin/src/auth/mfaFlow.test.ts:196` | 误报/已有控制 | mfaFlow.test.ts:194-199 向 mock response 注入固定 <script> 字符串，断言 confirm 返回 uncertain 且流程关闭；这里不是脚本模板插值或 DOM 执行。同一行的三条命中属于同一负向测试。 |
| SG-033 | `apps/web-admin/src/auth/mfaFlow.test.ts:196` | 误报/已有控制 | mfaFlow.test.ts:194-199 向 mock response 注入固定 <script> 字符串，断言 confirm 返回 uncertain 且流程关闭；这里不是脚本模板插值或 DOM 执行。同一行的三条命中属于同一负向测试。 |
| SG-034 | `docs/architecture/tools/build_diagrams.py:7` | 本次修复 | 仅 XML 磁盘回读使用 defusedxml 并 forbid_dtd=True；标准库 ElementTree 保留生成职责，不使用全局 monkey patch。 |
| SG-035 | `docs/architecture/tools/build_diagrams.py:134` | 本次修复 | 仅 XML 磁盘回读使用 defusedxml 并 forbid_dtd=True；标准库 ElementTree 保留生成职责，不使用全局 monkey patch。 |
| SG-036 | `docs/architecture/tools/verify_delivery.py:7` | 本次修复 | 验收读取使用 defusedxml，拒绝 DTD 和实体引用，异常终止验收。固定依赖声明与安装/失败恢复说明已同步。 |
| SG-037 | `docs/architecture/tools/verify_delivery.py:31` | 本次修复 | 验收读取使用 defusedxml，拒绝 DTD 和实体引用，异常终止验收。固定依赖声明与安装/失败恢复说明已同步。 |
| SG-038 | `infra/docker-compose/nginx/nginx.conf:35` | 误报/已有控制 | infra/docker-compose/nginx/nginx.conf:30-40 在 /health 中显式重复 server 层全部五个安全头，注释也解释 add_header 的继承边界。规则报告的是存在 add_header，而不是遗漏防护。 |
| SG-039 | `infra/docker-compose/nginx/nginx.conf:36` | 误报/已有控制 | infra/docker-compose/nginx/nginx.conf:30-40 在 /health 中显式重复 server 层全部五个安全头，注释也解释 add_header 的继承边界。规则报告的是存在 add_header，而不是遗漏防护。 |
| SG-040 | `infra/docker-compose/nginx/nginx.conf:37` | 误报/已有控制 | infra/docker-compose/nginx/nginx.conf:30-40 在 /health 中显式重复 server 层全部五个安全头，注释也解释 add_header 的继承边界。规则报告的是存在 add_header，而不是遗漏防护。 |
| SG-041 | `infra/docker-compose/nginx/nginx.conf:38` | 误报/已有控制 | infra/docker-compose/nginx/nginx.conf:30-40 在 /health 中显式重复 server 层全部五个安全头，注释也解释 add_header 的继承边界。规则报告的是存在 add_header，而不是遗漏防护。 |
| SG-042 | `infra/docker-compose/nginx/nginx.conf:39` | 误报/已有控制 | infra/docker-compose/nginx/nginx.conf:30-40 在 /health 中显式重复 server 层全部五个安全头，注释也解释 add_header 的继承边界。规则报告的是存在 add_header，而不是遗漏防护。 |
| SG-043 | `infra/docker-compose/nginx/nginx.conf:40` | 误报/已有控制 | infra/docker-compose/nginx/nginx.conf:30-40 在 /health 中显式重复 server 层全部五个安全头，注释也解释 add_header 的继承边界。规则报告的是存在 add_header，而不是遗漏防护。 |
| SG-044 | `infra/docker-compose/nginx/nginx.conf:59` | 本次修复 | 固定 upstream Host $proxy_host，删除外部 Forwarded/X-Forwarded-Host |
| SG-045 | `infra/docker-compose/nginx/nginx.conf:76` | 本次修复 | 固定 upstream Host $proxy_host，删除外部 Forwarded/X-Forwarded-Host |
| SG-046 | `infra/docker-compose/nginx/story056-e2e.conf:16` | 误报/已有控制 | 对应 story056/story060 E2E Nginx 的 server 块没有 add_header 安全头，/health 中只有 Content-Type，不存在规则所称覆盖上级安全头。此配置仅用于 E2E。 |
| SG-047 | `infra/docker-compose/nginx/story056-e2e.conf:22` | 本次修复 | 固定 upstream Host $proxy_host，删除外部 Forwarded/X-Forwarded-Host |
| SG-048 | `infra/docker-compose/nginx/story056-e2e.conf:31` | 本次修复 | 固定 upstream Host $proxy_host，删除外部 Forwarded/X-Forwarded-Host |
| SG-049 | `infra/docker-compose/nginx/story060-e2e.conf:16` | 误报/已有控制 | 对应 story056/story060 E2E Nginx 的 server 块没有 add_header 安全头，/health 中只有 Content-Type，不存在规则所称覆盖上级安全头。此配置仅用于 E2E。 |
| SG-050 | `infra/docker-compose/nginx/story060-e2e.conf:25` | 本次修复 | 固定 upstream Host $proxy_host，删除外部 Forwarded/X-Forwarded-Host |
| SG-051 | `infra/docker-compose/nginx/story060-e2e.conf:34` | 本次修复 | 固定 upstream Host $proxy_host，删除外部 Forwarded/X-Forwarded-Host |
| SG-052 | `scripts/test_fujian_math_batch29.py:25` | 误报/已有控制 | 测试从固定同目录脚本读取 AST，只编译白名单函数定义，用合成输入检查生成逻辑。exec 输入是仓库维护代码，不来自 API、上传文件或不可信模型输出；可改为可导入辅助模块减少动态执行，但未证实命令注入入口。 |
| SG-053 | `services/api-gateway/internal/auth/cookies.go:10` | 误报/已有控制 | Cookie 已显式设置 Secure: h.cookieSecure、HttpOnly:true、SameSiteLax。配置默认按环境开启 Secure，productionSecurityProblems 在生产类似环境拒绝 SessionCookieSecure=false（config.go:497）；规则未解析动态布尔值，不应误判为缺少 Secure。 |
| SG-054 | `services/api-gateway/internal/auth/cookies.go:27` | 误报/已有控制 | Cookie 已显式设置 Secure: h.cookieSecure、HttpOnly:true、SameSiteLax。配置默认按环境开启 Secure，productionSecurityProblems 在生产类似环境拒绝 SessionCookieSecure=false（config.go:497）；规则未解析动态布尔值，不应误判为缺少 Secure。 |
| SG-055 | `services/api-gateway/internal/auth/cookies.go:54` | 误报/已有控制 | Cookie 已显式设置 Secure: h.cookieSecure、HttpOnly:true、SameSiteLax。配置默认按环境开启 Secure，productionSecurityProblems 在生产类似环境拒绝 SessionCookieSecure=false（config.go:497）；规则未解析动态布尔值，不应误判为缺少 Secure。 |
| SG-056 | `services/api-gateway/internal/auth/cookies.go:62` | 误报/已有控制 | Cookie 已显式设置 Secure: h.cookieSecure、HttpOnly:true、SameSiteLax。配置默认按环境开启 Secure，productionSecurityProblems 在生产类似环境拒绝 SessionCookieSecure=false（config.go:497）；规则未解析动态布尔值，不应误判为缺少 Secure。 |
| SG-057 | `services/api-gateway/internal/auth/mfa_http_test.go:36` | 误报/已有控制 | 命中的是 httptest 请求侧 AddCookie 构造器；HttpOnly/Secure 是 Set-Cookie 响应属性，浏览器发送 Cookie 请求头不携带这些属性。测试用来验证 CSRF 与无效 Authorization 回退，未改变真实服务设置 Cookie 的安全属性。 |
| SG-058 | `services/api-gateway/internal/auth/mfa_http_test.go:36` | 误报/已有控制 | 命中的是 httptest 请求侧 AddCookie 构造器；HttpOnly/Secure 是 Set-Cookie 响应属性，浏览器发送 Cookie 请求头不携带这些属性。测试用来验证 CSRF 与无效 Authorization 回退，未改变真实服务设置 Cookie 的安全属性。 |
| SG-059 | `services/api-gateway/internal/files/store_postgres.go:221` | 误报/已有控制 | files.transition 只拼接程序生成的 $7、$8 等占位符与固定 assetColumns，tenant、id、revision、状态和错误详情通过 args... 绑定。来源状态来自代码常量；未将外部内容拼入 SQL 语法。 |
| SG-060 | `services/api-gateway/internal/middleware/middleware_test.go:64` | 误报/已有控制 | 命中的是 httptest 请求侧 AddCookie 构造器；HttpOnly/Secure 是 Set-Cookie 响应属性，浏览器发送 Cookie 请求头不携带这些属性。测试用来验证 CSRF 与无效 Authorization 回退，未改变真实服务设置 Cookie 的安全属性。 |
| SG-061 | `services/api-gateway/internal/middleware/middleware_test.go:64` | 误报/已有控制 | 命中的是 httptest 请求侧 AddCookie 构造器；HttpOnly/Secure 是 Set-Cookie 响应属性，浏览器发送 Cookie 请求头不携带这些属性。测试用来验证 CSRF 与无效 Authorization 回退，未改变真实服务设置 Cookie 的安全属性。 |
| SG-062 | `services/api-gateway/internal/middleware/middleware_test.go:78` | 误报/已有控制 | 命中的是 httptest 请求侧 AddCookie 构造器；HttpOnly/Secure 是 Set-Cookie 响应属性，浏览器发送 Cookie 请求头不携带这些属性。测试用来验证 CSRF 与无效 Authorization 回退，未改变真实服务设置 Cookie 的安全属性。 |
| SG-063 | `services/api-gateway/internal/middleware/middleware_test.go:78` | 误报/已有控制 | 命中的是 httptest 请求侧 AddCookie 构造器；HttpOnly/Secure 是 Set-Cookie 响应属性，浏览器发送 Cookie 请求头不携带这些属性。测试用来验证 CSRF 与无效 Authorization 回退，未改变真实服务设置 Cookie 的安全属性。 |
| SG-064 | `services/api-gateway/internal/middleware/middleware_test.go:93` | 误报/已有控制 | 命中的是 httptest 请求侧 AddCookie 构造器；HttpOnly/Secure 是 Set-Cookie 响应属性，浏览器发送 Cookie 请求头不携带这些属性。测试用来验证 CSRF 与无效 Authorization 回退，未改变真实服务设置 Cookie 的安全属性。 |
| SG-065 | `services/api-gateway/internal/middleware/middleware_test.go:93` | 误报/已有控制 | 命中的是 httptest 请求侧 AddCookie 构造器；HttpOnly/Secure 是 Set-Cookie 响应属性，浏览器发送 Cookie 请求头不携带这些属性。测试用来验证 CSRF 与无效 Authorization 回退，未改变真实服务设置 Cookie 的安全属性。 |
| SG-066 | `services/api-gateway/internal/middleware/middleware_test.go:132` | 误报/已有控制 | 命中的是 httptest 请求侧 AddCookie 构造器；HttpOnly/Secure 是 Set-Cookie 响应属性，浏览器发送 Cookie 请求头不携带这些属性。测试用来验证 CSRF 与无效 Authorization 回退，未改变真实服务设置 Cookie 的安全属性。 |
| SG-067 | `services/api-gateway/internal/middleware/middleware_test.go:132` | 误报/已有控制 | 命中的是 httptest 请求侧 AddCookie 构造器；HttpOnly/Secure 是 Set-Cookie 响应属性，浏览器发送 Cookie 请求头不携带这些属性。测试用来验证 CSRF 与无效 Authorization 回退，未改变真实服务设置 Cookie 的安全属性。 |
| SG-068 | `services/api-gateway/internal/minioadapter/client_test.go:72` | 误报/已有控制 | TestNewClientRejectsTLSBeforeVersion12 故意建立 MaxVersion=TLS1.1 的测试端点，断言真实 MinIO 客户端拒绝该连接。命中代码是负向测试条件，不能建议把它改成 TLS1.2 使测试失去覆盖。 |
| SG-069 | `services/api-gateway/internal/modelgovernance/managed_config_probe.go:14` | 误报/已有控制 | math/rand 仅用于 retryManagedReadOnlyRequest 的毫秒级重试抖动（managed_config_probe.go:330），未用于令牌、密码、加密密钥或租约随机值。非密码学调度随机性允许使用该包。 |
| SG-070 | `services/api-gateway/internal/score/store_postgres.go:100` | 误报/已有控制 | score.ListExamGrades 拼接固定 WHERE 片段和由 len(args) 生成的占位符；Status、Query、Cursor、Limit 均单独绑定，tenant/exam 也在 WHERE 和 JOIN 中。未发现动态查询文本来自用户输入的 SQL 注入。 |
| SG-071 | `services/api-gateway/internal/server/e2e_business_command_test.go:333` | 误报/已有控制 | E2E 数据库测试使用固定表名白名单或本地 UUID 生成的隔离数据库、角色、触发器名，数据库/角色标识通过 pgx.Identifier.Sanitize 引用；故障触发条件拼入的命令 ID/题目 ID 来自测试固定字符串或 UUID fixture。不是生产 API 查询，也未找到可由外部输入控制的拼接值。 |
| SG-072 | `services/api-gateway/internal/server/e2e_business_command_test.go:341` | 误报/已有控制 | E2E 数据库测试使用固定表名白名单或本地 UUID 生成的隔离数据库、角色、触发器名，数据库/角色标识通过 pgx.Identifier.Sanitize 引用；故障触发条件拼入的命令 ID/题目 ID 来自测试固定字符串或 UUID fixture。不是生产 API 查询，也未找到可由外部输入控制的拼接值。 |
| SG-073 | `services/api-gateway/internal/server/e2e_business_command_test.go:342` | 误报/已有控制 | E2E 数据库测试使用固定表名白名单或本地 UUID 生成的隔离数据库、角色、触发器名，数据库/角色标识通过 pgx.Identifier.Sanitize 引用；故障触发条件拼入的命令 ID/题目 ID 来自测试固定字符串或 UUID fixture。不是生产 API 查询，也未找到可由外部输入控制的拼接值。 |
| SG-074 | `services/api-gateway/internal/server/e2e_business_command_test.go:344` | 误报/已有控制 | E2E 数据库测试使用固定表名白名单或本地 UUID 生成的隔离数据库、角色、触发器名，数据库/角色标识通过 pgx.Identifier.Sanitize 引用；故障触发条件拼入的命令 ID/题目 ID 来自测试固定字符串或 UUID fixture。不是生产 API 查询，也未找到可由外部输入控制的拼接值。 |
| SG-075 | `services/api-gateway/internal/server/e2e_compose_bootstrap_test.go:49` | 误报/已有控制 | E2E 数据库测试使用固定表名白名单或本地 UUID 生成的隔离数据库、角色、触发器名，数据库/角色标识通过 pgx.Identifier.Sanitize 引用；故障触发条件拼入的命令 ID/题目 ID 来自测试固定字符串或 UUID fixture。不是生产 API 查询，也未找到可由外部输入控制的拼接值。 |
| SG-076 | `services/api-gateway/internal/server/e2e_compose_bootstrap_test.go:53` | 误报/已有控制 | E2E 数据库测试使用固定表名白名单或本地 UUID 生成的隔离数据库、角色、触发器名，数据库/角色标识通过 pgx.Identifier.Sanitize 引用；故障触发条件拼入的命令 ID/题目 ID 来自测试固定字符串或 UUID fixture。不是生产 API 查询，也未找到可由外部输入控制的拼接值。 |
| SG-077 | `services/api-gateway/internal/server/e2e_compose_bootstrap_test.go:57` | 误报/已有控制 | E2E 数据库测试使用固定表名白名单或本地 UUID 生成的隔离数据库、角色、触发器名，数据库/角色标识通过 pgx.Identifier.Sanitize 引用；故障触发条件拼入的命令 ID/题目 ID 来自测试固定字符串或 UUID fixture。不是生产 API 查询，也未找到可由外部输入控制的拼接值。 |
| SG-078 | `services/api-gateway/internal/server/e2e_mfa_test.go:239` | 误报/已有控制 | E2E 数据库测试使用固定表名白名单或本地 UUID 生成的隔离数据库、角色、触发器名，数据库/角色标识通过 pgx.Identifier.Sanitize 引用；故障触发条件拼入的命令 ID/题目 ID 来自测试固定字符串或 UUID fixture。不是生产 API 查询，也未找到可由外部输入控制的拼接值。 |
| SG-079 | `services/api-gateway/internal/server/e2e_mfa_test.go:308` | 误报/已有控制 | E2E 数据库测试使用固定表名白名单或本地 UUID 生成的隔离数据库、角色、触发器名，数据库/角色标识通过 pgx.Identifier.Sanitize 引用；故障触发条件拼入的命令 ID/题目 ID 来自测试固定字符串或 UUID fixture。不是生产 API 查询，也未找到可由外部输入控制的拼接值。 |
| SG-080 | `services/api-gateway/internal/server/e2e_mfa_test.go:312` | 误报/已有控制 | E2E 数据库测试使用固定表名白名单或本地 UUID 生成的隔离数据库、角色、触发器名，数据库/角色标识通过 pgx.Identifier.Sanitize 引用；故障触发条件拼入的命令 ID/题目 ID 来自测试固定字符串或 UUID fixture。不是生产 API 查询，也未找到可由外部输入控制的拼接值。 |
| SG-081 | `services/api-gateway/internal/server/e2e_mfa_test.go:316` | 误报/已有控制 | E2E 数据库测试使用固定表名白名单或本地 UUID 生成的隔离数据库、角色、触发器名，数据库/角色标识通过 pgx.Identifier.Sanitize 引用；故障触发条件拼入的命令 ID/题目 ID 来自测试固定字符串或 UUID fixture。不是生产 API 查询，也未找到可由外部输入控制的拼接值。 |
| SG-082 | `services/api-gateway/internal/server/e2e_postgres_test.go:354` | 误报/已有控制 | E2E 数据库测试使用固定表名白名单或本地 UUID 生成的隔离数据库、角色、触发器名，数据库/角色标识通过 pgx.Identifier.Sanitize 引用；故障触发条件拼入的命令 ID/题目 ID 来自测试固定字符串或 UUID fixture。不是生产 API 查询，也未找到可由外部输入控制的拼接值。 |
| SG-083 | `services/api-gateway/internal/server/e2e_postgres_test.go:357` | 误报/已有控制 | E2E 数据库测试使用固定表名白名单或本地 UUID 生成的隔离数据库、角色、触发器名，数据库/角色标识通过 pgx.Identifier.Sanitize 引用；故障触发条件拼入的命令 ID/题目 ID 来自测试固定字符串或 UUID fixture。不是生产 API 查询，也未找到可由外部输入控制的拼接值。 |
| SG-084 | `services/api-gateway/internal/server/e2e_postgres_test.go:361` | 误报/已有控制 | E2E 数据库测试使用固定表名白名单或本地 UUID 生成的隔离数据库、角色、触发器名，数据库/角色标识通过 pgx.Identifier.Sanitize 引用；故障触发条件拼入的命令 ID/题目 ID 来自测试固定字符串或 UUID fixture。不是生产 API 查询，也未找到可由外部输入控制的拼接值。 |
| SG-085 | `services/api-gateway/internal/server/e2e_postgres_test.go:711` | 误报/已有控制 | E2E 数据库测试使用固定表名白名单或本地 UUID 生成的隔离数据库、角色、触发器名，数据库/角色标识通过 pgx.Identifier.Sanitize 引用；故障触发条件拼入的命令 ID/题目 ID 来自测试固定字符串或 UUID fixture。不是生产 API 查询，也未找到可由外部输入控制的拼接值。 |
| SG-086 | `services/api-gateway/internal/server/e2e_postgres_test.go:722` | 误报/已有控制 | E2E 数据库测试使用固定表名白名单或本地 UUID 生成的隔离数据库、角色、触发器名，数据库/角色标识通过 pgx.Identifier.Sanitize 引用；故障触发条件拼入的命令 ID/题目 ID 来自测试固定字符串或 UUID fixture。不是生产 API 查询，也未找到可由外部输入控制的拼接值。 |
| SG-087 | `services/api-gateway/internal/server/e2e_postgres_test.go:730` | 误报/已有控制 | E2E 数据库测试使用固定表名白名单或本地 UUID 生成的隔离数据库、角色、触发器名，数据库/角色标识通过 pgx.Identifier.Sanitize 引用；故障触发条件拼入的命令 ID/题目 ID 来自测试固定字符串或 UUID fixture。不是生产 API 查询，也未找到可由外部输入控制的拼接值。 |
| SG-088 | `services/api-gateway/internal/server/e2e_question_bank_import_test.go:166` | 误报/已有控制 | E2E 数据库测试使用固定表名白名单或本地 UUID 生成的隔离数据库、角色、触发器名，数据库/角色标识通过 pgx.Identifier.Sanitize 引用；故障触发条件拼入的命令 ID/题目 ID 来自测试固定字符串或 UUID fixture。不是生产 API 查询，也未找到可由外部输入控制的拼接值。 |
| SG-089 | `services/api-gateway/internal/server/e2e_question_bank_test.go:350` | 误报/已有控制 | E2E 数据库测试使用固定表名白名单或本地 UUID 生成的隔离数据库、角色、触发器名，数据库/角色标识通过 pgx.Identifier.Sanitize 引用；故障触发条件拼入的命令 ID/题目 ID 来自测试固定字符串或 UUID fixture。不是生产 API 查询，也未找到可由外部输入控制的拼接值。 |
| SG-090 | `services/api-gateway/tools/story056-postgres-e2e/Dockerfile:9` | 既有修复已核验 | 保留既有 USER 10001:10001；与本次 baseline 完全一致 |
| SG-091 | `services/api-gateway/tools/story056-real-worker-e2e/Dockerfile:18` | 本次修复 | 最终 USER 10001:10001 和可写 HOME；stdout 输出不需挂载改权 |
| SG-092 | `services/image-quality-worker/image_quality/api.py:94` | 本次修复 | 请求级 opener 拒绝所有自动跳转，关闭被拒绝响应；保留初始同源校验、令牌/租约、限额、超时与错误映射。 |
| SG-093 | `services/image-quality-worker/image_quality/api.py:121` | 本次修复 | 请求级 opener 拒绝所有自动跳转，关闭被拒绝响应；保留初始同源校验、令牌/租约、限额、超时与错误映射。 |
| SG-094 | `services/image-quality-worker/image_quality/api.py:145` | 本次修复 | 请求级 opener 拒绝所有自动跳转，关闭被拒绝响应；保留初始同源校验、令牌/租约、限额、超时与错误映射。 |
| SG-095 | `services/math-verification-worker/tests/test_timeout.py:34` | 误报/已有控制 | 这里只调用 pickle.dumps 对可信测试内存对象序列化，验证 multiprocessing 管道大结果场景；没有 pickle.loads/load 读取用户数据。序列化大小断言本身不是不安全反序列化。 |
| SG-096 | `services/ocr-worker/Dockerfile:6` | 误报/已有控制 | OCR Dockerfile:6 的 USER root 只用于安装依赖；文件末尾已切换 USER nobody，之后 CMD python -m ocr_worker。Semgrep 同时报告此 Dockerfile 的部分解析错误，不能把解析到的最后 root 当作真实最终用户。 |
| SG-097 | `services/ocr-worker/ocr_worker/api.py:266` | 本次修复 | 下载和 JSON 入口拒绝所有自动跳转，关闭被拒绝响应，保留现有认证与能力边界。 |
| SG-098 | `services/ocr-worker/ocr_worker/api.py:290` | 本次修复 | 下载和 JSON 入口拒绝所有自动跳转，关闭被拒绝响应，保留现有认证与能力边界。 |
| SG-099 | `services/ocr-worker/ocr_worker/math_runner.py:41` | 本次修复 | 数学验证复用同包拒绝跳转入口，内部令牌只发送到初始服务，原有 422/超时语义保留。 |
| SG-100 | `services/page-processing-worker/page_processing/api.py:71` | 本次修复 | 下载、上传和 JSON 入口拒绝所有自动跳转并释放原响应，保留普通 409 幂等回读与 Retry-After。 |
| SG-101 | `services/page-processing-worker/page_processing/api.py:102` | 本次修复 | 下载、上传和 JSON 入口拒绝所有自动跳转并释放原响应，保留普通 409 幂等回读与 Retry-After。 |
| SG-102 | `services/page-processing-worker/page_processing/api.py:160` | 本次修复 | 下载、上传和 JSON 入口拒绝所有自动跳转并释放原响应，保留普通 409 幂等回读与 Retry-After。 |
| SG-103 | `services/page-processing-worker/tests/test_main.py:47` | 误报/已有控制 | _config 位于 tests/test_main.py，固定测试字符串只传给 FakeClient/FakeRunner 与 monkeypatch，用来覆盖登录和配置边界，未用作真实 Worker 生产默认密码。报告不复述此合成值。 |
| SG-104 | `services/subjective-grading-worker/subjective_grading_worker/api.py:94` | 本次修复 | 主观题服务请求拒绝所有自动跳转并关闭原响应，保留会话与任务租约头。 |
| SG-105 | `tests/e2e/story060_grading_agent.py:19` | 本次修复 | E2E JSON 请求拒绝自动跳转；响应以 4 MiB 限额读取，校验存在的 Content-Length，缺失长度时仍最多读取限额加一个字节。主场景移入 main() 便于离线测试调用 helper。 |

[机器可读逐项结果](findings-remediation.json) · [增量独立评审](review-results.json) · [文件字节比对](file-byte-inventory.json)
