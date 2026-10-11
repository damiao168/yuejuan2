# Semgrep 本地工作区扫描详细报告

实际执行日期：2026-10-11（Asia/Shanghai）。报告目录沿用 2026-10-10 创建的审计目录。

使用官方 Semgrep 1.179.0 的 OSS 引擎完成真实本地扫描，发现 **105 条规则命中**。逐条结合调用和配置核验后：**68 条误报或已有防护，13 条条件性重定向风险待核实，24 条低风险加固、工具场景或部署检查项（含 1 条扫描后已解决）**。未确认可直接利用的生产高危漏洞；这不等于证明没有漏洞。

扫描包含 16 个 PartialParsing 警告，不能称为 100% 完整解析。退出码 0 表示扫描命令成功，不能当作零告警或安全门禁通过。

## 1. 执行与边界

| 项目 | 实际结果 |
| --- | --- |
| 扫描平台/引擎 | 官方 Semgrep CLI 1.179.0 / `engine_requested=OSS` |
| 官方镜像 | `semgrep/semgrep@sha256:93963d9295a366f59e4850127b1550400ee7b388f04fe144e4a1f6325d96e01b` |
| 分析开始/结束（UTC） | 2026-10-11T02:55:33.120657+00:00 → 2026-10-11T03:00:38.996552+00:00 |
| 实际分析耗时 | 304.487 秒 |
| 环境 | Docker，本次容器 2 CPU / 2 GiB 上限，2 jobs，单文件扫描内存上限 600 MB |
| 联网 | 扫描容器 `--network none`；`--metrics off`、`--disable-version-check`、空平台令牌 |
| 工作区 | `D:/project/yuejuan` 当前本地字节；含未提交修改与未跟踪维护源码 |
| 文件来源 | 本地 `git ls-files --cached --others --exclude-standard -z`，没有访问远端 GitHub |
| 快照文本文件 | 3196 个、34684912 字节；另加副本专用空 `.semgrepignore`，CLI 共扫描 3197 目标 |
| 快照 ZIP SHA-256 | `744ac73cfaed6c5767494d7f2d93146bac29632ff5418c710eaeb807aed78f2e` |
| 规则 | 7 个官方公开规则包；按 ID 去重加载 735 条，CLI 报告实际运行 434 条 |
| 原始结果 | ERROR 7、WARNING 87、MEDIUM 11；分布在 19 种规则中 |
| 扫描错误 | 16 条 PartialParsing；约 99.9% 解析行，无 raw skipped_rules 条目 |
| 工作区写入 | 仅审计目录、工具准备和独立本地快照；未修复业务代码 |

只从官方 Registry 下载规则并下载官方镜像。没有使用 `semgrep ci`、云端项目、托管扫描或上传功能。官方说明本地 YAML 规则可由 CLI 执行，metrics 可显式关闭，见 [本地规则](https://docs.semgrep.dev/running-rules)、[metrics 设置](https://docs.semgrep.dev/metrics)。

运行命令的完整参数在 `scan-metadata.json`，Windows 可复现入口是 `run-local.ps1`。原始 JSON 不经改写；`findings-triage.json` 与 `findings.csv` 是带人工结论的派生文件。工具安装、启动入口与范围纠正过程另见 `setup-notes.md`，失败准备步骤未冒充正式扫描。

### 规则覆盖的实际限制

735 条规则成功加载，不表示每条都作用于本仓库。按实际文件语言与 generic/regex 匹配，有 452 条具备目标语言、283 条没有对应语言；实际引擎汇总为 434 条运行。301 条加载规则未计入运行，其中剩余差额没有逐规则执行原因，不能全部归类为 Pro 跳过。原始 `skipped_rules=[]` 只是该字段未列出跳过项。

规则配置中 28 条带 interfile 选项，本次明确使用 OSS，引擎没有提供 Pro 跨文件分析结果。本次没有云端有效密钥验证、商业规则、SCA 依赖漏洞扫描或运行时渗透测试。Registry 响应的 `missed` 字段原样记录在 `rule-manifest.json`，这些未下载规则未计入已执行覆盖。

| 公开规则包 | 下载条数（去重前） | 响应 missed 字段 |
| --- | ---: | ---: |
| [golang](https://semgrep.dev/c/p/golang) | 42 | 71 |
| [javascript](https://semgrep.dev/c/p/javascript) | 74 | 242 |
| [owasp-top-ten](https://semgrep.dev/c/p/owasp-top-ten) | 560 | 1800 |
| [python](https://semgrep.dev/c/p/python) | 151 | 923 |
| [secrets](https://semgrep.dev/c/p/secrets) | 52 | 218 |
| [security-audit](https://semgrep.dev/c/p/security-audit) | 225 | 0 |
| [typescript](https://semgrep.dev/c/p/typescript) | 74 | 242 |

规则原文、各包 SHA-256、合并规则与可复现执行器均保存在本目录。未改变 `.gitignore`、原 `.semgrepignore` 或业务输入；空忽略文件只存在隔离副本，用于扫描测试与维护文档。

### 排除范围

排除 `.git`、node_modules、第三方下载/模型/运行时缓存（含 `lab/.downloads`、`lab/.models`、`lab/.runtime`）、虚拟环境、构建产物、覆盖率产物、reports、output、tmp、备份、证书、实际 `.env` 等运行秘密输入、二进制、超过 5 MB 的单个文件、生成 SDK 与 Tauri 生成 schema。`.env.example` 可扫描。历史 SQL 迁移只读扫描，没有改写。

这些排除项不是已经安全：未扫描实际 `.env`，因此本报告不能确认运行密钥不存在泄露；没有 secrets 规则命中也不能替代密钥轮换、运行环境检查或有效性验证。枚举与逐文件哈希详见 `source-inventory.json`。

## 2. 人工核验后的优先处理项

### 条件性中风险：Python 凭据请求自动重定向边界（13 条）

涉及 SG-013～015、SG-092～094、SG-097～102、SG-104。模型服务、图像质量、OCR、数学验证、页面处理与主观题 Worker 使用 `urllib.request.urlopen`。初始 URL 来自受信部署配置或经过同源校验，响应有 timeout、JSON/图像大小限制；没有证据支持把这些调用一律定为 SSRF。

剩余边界是默认重定向处理器：初始 URL 校验不会自动再次作用于跳转目标。对 Python 3.11.9 的无网络合成请求核验显示，跨域 302 GET 跳转会保留 `Authorization` 和 Worker Lease 等自定义头（`redirect-semantics.json`；脚本不打印凭据值、不启动应用、不发网络请求）。

**触发前提**：配置的 API、供应商或代理必须返回攻击者可控制的重定向。目前没有确认普通用户能控制这样的路由，未证明生产环境实际可利用，故归为待核实的条件性风险。

**影响**：一旦前提成立，可能向另一个 origin 泄露服务令牌或租约能力，并绕过初始的目标边界；不应把它直接表述为已发生跨学校越权。

**建议**：对带凭据的内部/供应商请求默认禁止自动跳转；确需跳转时逐跳校验 scheme、origin 和允许目的地址，并剥离所有敏感头。Go 受管模型客户端及 Python `public_json_transport` 已有相应限制，可参考其边界设计。之后用合成服务验证跨域跳转被拒绝、同源允许策略与过期租约拒绝，不能只改注释承诺所有请求始终同源。

| 位置 | 命中编号 |
| --- | --- |
| `ai-services/grading_agent/model.py:456,604,733` | SG-013～015 |
| `services/image-quality-worker/image_quality/api.py:94,121,145` | SG-092～094 |
| `services/ocr-worker/ocr_worker/api.py:266,290` | SG-097～098 |
| `services/ocr-worker/ocr_worker/math_runner.py:41` | SG-099 |
| `services/page-processing-worker/page_processing/api.py:71,102,160` | SG-100～102 |
| `services/subjective-grading-worker/subjective_grading_worker/api.py:94` | SG-104 |

### 低风险或场景受限事项（24 条，含 1 条扫描后已解决）

- SG-001～011：11 个 Dependabot 配置未设 cooldown，属于新版本引入缓冲建议，不能据此确认依赖存在漏洞；评估策略时不要延误必要的安全更新。
- SG-034～037：两个本地架构工具使用普通 XML 解析器，但当前只处理维护者固定生成的 drawio 产物。没有确认接受不可信 XML 的路径；今后支持外部文件时应限制来源并使用安全解析器。
- SG-044～045：生产 Nginx 转发 `$host`，目标 upstream 固定，未发现网关据此构造敏感重置链接或跳转。虚拟主机 allowlist 仍需部署检查，当前不算已确认 Host 注入。
- SG-047～048、050～051：只在 E2E Nginx 配置出现同类 Host 用法，测试范围，不能套用生产严重性。
- SG-090：原快照的 E2E Go 测试镜像最终未设置 USER，属于真实低风险加固项。报告核验时当前文件已经改为多阶段且最终 USER 10001:10001，扫描后已解决，不能反标原扫描误报。
- SG-091：E2E 客户端镜像最终未设置 USER，确实可以加固；受 E2E profile 和客户端用途限制，未发现公共服务入口，不能宣称生产容器以 root 运行。
- SG-105：E2E 工具的动态 URL 来自测试配置，可补拒绝跳转与限额读取，但未发现生产用户提供 URL 的路径。

### 重要误报示例（68 条）

- SG-059、070：生产 SQL 只拼固定查询片段和程序生成的 `$N` 占位符，tenant、id、状态、关键词、cursor 等通过参数绑定。不是已经确认的 SQL 注入。
- SG-053～056：Cookie 已有 `Secure: h.cookieSecure`、HttpOnly、SameSiteLax；生产配置拒绝 Secure=false，规则无法理解动态布尔值。其他 Cookie 命中是 httptest 请求构造器，其属性不代表服务 Set-Cookie。
- SG-069：math/rand 用于只读重试的毫秒级抖动，不生成认证令牌或加密材料。
- SG-095：调用 pickle.dumps 计算可信测试对象的序列化大小，没有从不可信输入反序列化。
- SG-096：OCR Dockerfile 最终阶段已有 USER nobody。规则命中前面的构建阶段不能代表运行身份；与扫描后才修复的 SG-090 需区分。
- Nginx header-redefinition 命中中，生产 health location 已重复完整安全头，测试配置没有需要继承的上级 add_header，不能直接报告响应头缺失。

## 3. 命中分布和逐条核验

原始 severity 保留工具值；ERROR/WARNING/MEDIUM 是规则作者给出的等级，不能机械映射为已确认漏洞的高/中/低。人工风险独立列出。

| 人工状态 | 条数 |
| --- | ---: |
| 加固建议 | 11 |
| 误报/已有控制 | 68 |
| 条件性风险待核实 | 13 |
| 工具场景受限 | 4 |
| 部署前提待核实 | 2 |
| 测试工具范围 | 7 |

以下每行是一个真实扫描命中，不合并重复告警。完整英文规则消息与 confidence 保存在 JSON/CSV，原始代码片段只留在本地原始扫描文件，不在报告中复制潜在秘密。

| ID | 文件及起始行 | 工具级别 | 人工状态 / 风险 | 核验依据 |
| --- | --- | --- | --- | --- |
| SG-001 | `.github/dependabot.yml:4` | MEDIUM | 加固建议 / 低 | 11 个 Dependabot 更新项确实没有 cooldown；已有每周检查和人工 PR 流程。属于依赖更新缓冲策略建议，不是已确认依赖漏洞或代码执行。可评估为新版本设置缓冲期；不应延迟必要的安全更新。 |
| SG-002 | `.github/dependabot.yml:13` | MEDIUM | 加固建议 / 低 | 11 个 Dependabot 更新项确实没有 cooldown；已有每周检查和人工 PR 流程。属于依赖更新缓冲策略建议，不是已确认依赖漏洞或代码执行。可评估为新版本设置缓冲期；不应延迟必要的安全更新。 |
| SG-003 | `.github/dependabot.yml:22` | MEDIUM | 加固建议 / 低 | 11 个 Dependabot 更新项确实没有 cooldown；已有每周检查和人工 PR 流程。属于依赖更新缓冲策略建议，不是已确认依赖漏洞或代码执行。可评估为新版本设置缓冲期；不应延迟必要的安全更新。 |
| SG-004 | `.github/dependabot.yml:31` | MEDIUM | 加固建议 / 低 | 11 个 Dependabot 更新项确实没有 cooldown；已有每周检查和人工 PR 流程。属于依赖更新缓冲策略建议，不是已确认依赖漏洞或代码执行。可评估为新版本设置缓冲期；不应延迟必要的安全更新。 |
| SG-005 | `.github/dependabot.yml:40` | MEDIUM | 加固建议 / 低 | 11 个 Dependabot 更新项确实没有 cooldown；已有每周检查和人工 PR 流程。属于依赖更新缓冲策略建议，不是已确认依赖漏洞或代码执行。可评估为新版本设置缓冲期；不应延迟必要的安全更新。 |
| SG-006 | `.github/dependabot.yml:49` | MEDIUM | 加固建议 / 低 | 11 个 Dependabot 更新项确实没有 cooldown；已有每周检查和人工 PR 流程。属于依赖更新缓冲策略建议，不是已确认依赖漏洞或代码执行。可评估为新版本设置缓冲期；不应延迟必要的安全更新。 |
| SG-007 | `.github/dependabot.yml:58` | MEDIUM | 加固建议 / 低 | 11 个 Dependabot 更新项确实没有 cooldown；已有每周检查和人工 PR 流程。属于依赖更新缓冲策略建议，不是已确认依赖漏洞或代码执行。可评估为新版本设置缓冲期；不应延迟必要的安全更新。 |
| SG-008 | `.github/dependabot.yml:67` | MEDIUM | 加固建议 / 低 | 11 个 Dependabot 更新项确实没有 cooldown；已有每周检查和人工 PR 流程。属于依赖更新缓冲策略建议，不是已确认依赖漏洞或代码执行。可评估为新版本设置缓冲期；不应延迟必要的安全更新。 |
| SG-009 | `.github/dependabot.yml:76` | MEDIUM | 加固建议 / 低 | 11 个 Dependabot 更新项确实没有 cooldown；已有每周检查和人工 PR 流程。属于依赖更新缓冲策略建议，不是已确认依赖漏洞或代码执行。可评估为新版本设置缓冲期；不应延迟必要的安全更新。 |
| SG-010 | `.github/dependabot.yml:85` | MEDIUM | 加固建议 / 低 | 11 个 Dependabot 更新项确实没有 cooldown；已有每周检查和人工 PR 流程。属于依赖更新缓冲策略建议，不是已确认依赖漏洞或代码执行。可评估为新版本设置缓冲期；不应延迟必要的安全更新。 |
| SG-011 | `.github/dependabot.yml:94` | MEDIUM | 加固建议 / 低 | 11 个 Dependabot 更新项确实没有 cooldown；已有每周检查和人工 PR 流程。属于依赖更新缓冲策略建议，不是已确认依赖漏洞或代码执行。可评估为新版本设置缓冲期；不应延迟必要的安全更新。 |
| SG-012 | `ai-services/grading_agent/managed_model.py:62` | WARNING | 误报/已有控制 / 不适用 | managed_model.public_json_transport 先限制 HTTPS，拒绝任何非公网 DNS 结果，再固定已校验 IP；HTTPSConnection 保留原 hostname 进行 SNI、证书链和主机名验证，响应有 4MB 上限并关闭连接。未发现由此调用直接引入的 SSRF/TLS 绕过；未来 Python 升级需关注 _create_connection 私有接口兼容性。 |
| SG-013 | `ai-services/grading_agent/model.py:456` | WARNING | 条件性风险待核实 / 中 | 模型 URL 来自受信部署配置，native adapter 还限定固定供应商 HTTPS 主机与路径，学校请求模型使用另一个固定公网地址且不自动跟随跳转的 transport。此处 urllib 默认允许自动重定向，合成无网络核验证明跨域跳转保留 Authorization；需供应商或配置的服务返回攻击者可控制的重定向才能触发，当前未证明存在此路径。建议带凭据客户端拒绝重定向，或逐跳校验同源并剥离敏感头。 |
| SG-014 | `ai-services/grading_agent/model.py:604` | WARNING | 条件性风险待核实 / 中 | 模型 URL 来自受信部署配置，native adapter 还限定固定供应商 HTTPS 主机与路径，学校请求模型使用另一个固定公网地址且不自动跟随跳转的 transport。此处 urllib 默认允许自动重定向，合成无网络核验证明跨域跳转保留 Authorization；需供应商或配置的服务返回攻击者可控制的重定向才能触发，当前未证明存在此路径。建议带凭据客户端拒绝重定向，或逐跳校验同源并剥离敏感头。 |
| SG-015 | `ai-services/grading_agent/model.py:733` | WARNING | 条件性风险待核实 / 中 | 模型 URL 来自受信部署配置，native adapter 还限定固定供应商 HTTPS 主机与路径，学校请求模型使用另一个固定公网地址且不自动跟随跳转的 transport。此处 urllib 默认允许自动重定向，合成无网络核验证明跨域跳转保留 Authorization；需供应商或配置的服务返回攻击者可控制的重定向才能触发，当前未证明存在此路径。建议带凭据客户端拒绝重定向，或逐跳校验同源并剥离敏感头。 |
| SG-016 | `ai-services/tests/test_http.py:29` | WARNING | 误报/已有控制 / 不适用 | 测试在 127.0.0.1 随机端口启动测试 HTTP 服务，URL 由 server_port 拼出，调用设置 timeout=2；测试覆盖认证、幂等和解析结果，未使用普通用户提供的目标 URL。此告警不代表生产 SSRF。 |
| SG-017 | `ai-services/tests/test_http.py:31` | WARNING | 误报/已有控制 / 不适用 | 测试在 127.0.0.1 随机端口启动测试 HTTP 服务，URL 由 server_port 拼出，调用设置 timeout=2；测试覆盖认证、幂等和解析结果，未使用普通用户提供的目标 URL。此告警不代表生产 SSRF。 |
| SG-018 | `ai-services/tests/test_http.py:36` | WARNING | 误报/已有控制 / 不适用 | 测试在 127.0.0.1 随机端口启动测试 HTTP 服务，URL 由 server_port 拼出，调用设置 timeout=2；测试覆盖认证、幂等和解析结果，未使用普通用户提供的目标 URL。此告警不代表生产 SSRF。 |
| SG-019 | `ai-services/tests/test_http.py:50` | WARNING | 误报/已有控制 / 不适用 | 测试在 127.0.0.1 随机端口启动测试 HTTP 服务，URL 由 server_port 拼出，调用设置 timeout=2；测试覆盖认证、幂等和解析结果，未使用普通用户提供的目标 URL。此告警不代表生产 SSRF。 |
| SG-020 | `ai-services/tests/test_http.py:64` | WARNING | 误报/已有控制 / 不适用 | 测试在 127.0.0.1 随机端口启动测试 HTTP 服务，URL 由 server_port 拼出，调用设置 timeout=2；测试覆盖认证、幂等和解析结果，未使用普通用户提供的目标 URL。此告警不代表生产 SSRF。 |
| SG-021 | `ai-services/tests/test_http.py:75` | WARNING | 误报/已有控制 / 不适用 | 测试在 127.0.0.1 随机端口启动测试 HTTP 服务，URL 由 server_port 拼出，调用设置 timeout=2；测试覆盖认证、幂等和解析结果，未使用普通用户提供的目标 URL。此告警不代表生产 SSRF。 |
| SG-022 | `ai-services/tests/test_http.py:117` | WARNING | 误报/已有控制 / 不适用 | 测试在 127.0.0.1 随机端口启动测试 HTTP 服务，URL 由 server_port 拼出，调用设置 timeout=2；测试覆盖认证、幂等和解析结果，未使用普通用户提供的目标 URL。此告警不代表生产 SSRF。 |
| SG-023 | `ai-services/tests/test_http.py:151` | WARNING | 误报/已有控制 / 不适用 | 测试在 127.0.0.1 随机端口启动测试 HTTP 服务，URL 由 server_port 拼出，调用设置 timeout=2；测试覆盖认证、幂等和解析结果，未使用普通用户提供的目标 URL。此告警不代表生产 SSRF。 |
| SG-024 | `ai-services/tests/test_math_v2_production.py:164` | WARNING | 误报/已有控制 / 不适用 | 测试在 127.0.0.1 随机端口启动测试 HTTP 服务，URL 由 server_port 拼出，调用设置 timeout=2；测试覆盖认证、幂等和解析结果，未使用普通用户提供的目标 URL。此告警不代表生产 SSRF。 |
| SG-025 | `apps/web-admin/nginx.conf:17` | WARNING | 误报/已有控制 / 不适用 | apps/web-admin/nginx.conf:14-24 的 /health location 已显式重复所有 server 安全头；17 行 Content-Type 并未造成 CSP、X-Frame-Options 等丢失。19-23 行正是修复继承边界的防护代码。 |
| SG-026 | `apps/web-admin/nginx.conf:19` | WARNING | 误报/已有控制 / 不适用 | apps/web-admin/nginx.conf:14-24 的 /health location 已显式重复所有 server 安全头；17 行 Content-Type 并未造成 CSP、X-Frame-Options 等丢失。19-23 行正是修复继承边界的防护代码。 |
| SG-027 | `apps/web-admin/nginx.conf:20` | WARNING | 误报/已有控制 / 不适用 | apps/web-admin/nginx.conf:14-24 的 /health location 已显式重复所有 server 安全头；17 行 Content-Type 并未造成 CSP、X-Frame-Options 等丢失。19-23 行正是修复继承边界的防护代码。 |
| SG-028 | `apps/web-admin/nginx.conf:21` | WARNING | 误报/已有控制 / 不适用 | apps/web-admin/nginx.conf:14-24 的 /health location 已显式重复所有 server 安全头；17 行 Content-Type 并未造成 CSP、X-Frame-Options 等丢失。19-23 行正是修复继承边界的防护代码。 |
| SG-029 | `apps/web-admin/nginx.conf:22` | WARNING | 误报/已有控制 / 不适用 | apps/web-admin/nginx.conf:14-24 的 /health location 已显式重复所有 server 安全头；17 行 Content-Type 并未造成 CSP、X-Frame-Options 等丢失。19-23 行正是修复继承边界的防护代码。 |
| SG-030 | `apps/web-admin/nginx.conf:23` | WARNING | 误报/已有控制 / 不适用 | apps/web-admin/nginx.conf:14-24 的 /health location 已显式重复所有 server 安全头；17 行 Content-Type 并未造成 CSP、X-Frame-Options 等丢失。19-23 行正是修复继承边界的防护代码。 |
| SG-031 | `apps/web-admin/src/auth/mfaFlow.test.ts:196` | WARNING | 误报/已有控制 / 不适用 | mfaFlow.test.ts:194-199 向 mock response 注入固定 <script> 字符串，断言 confirm 返回 uncertain 且流程关闭；这里不是脚本模板插值或 DOM 执行。同一行的三条命中属于同一负向测试。 |
| SG-032 | `apps/web-admin/src/auth/mfaFlow.test.ts:196` | WARNING | 误报/已有控制 / 不适用 | mfaFlow.test.ts:194-199 向 mock response 注入固定 <script> 字符串，断言 confirm 返回 uncertain 且流程关闭；这里不是脚本模板插值或 DOM 执行。同一行的三条命中属于同一负向测试。 |
| SG-033 | `apps/web-admin/src/auth/mfaFlow.test.ts:196` | WARNING | 误报/已有控制 / 不适用 | mfaFlow.test.ts:194-199 向 mock response 注入固定 <script> 字符串，断言 confirm 返回 uncertain 且流程关闭；这里不是脚本模板插值或 DOM 执行。同一行的三条命中属于同一负向测试。 |
| SG-034 | `docs/architecture/tools/build_diagrams.py:7` | ERROR | 工具场景受限 / 低 | build_diagrams.py:132 自行生成固定本地 drawio，134 行立即回读验证；未发现不可信上传 XML 进入此路径。不是已确认生产 XXE。若将来接受外部 XML，应限制解析资源并使用安全解析器。 |
| SG-035 | `docs/architecture/tools/build_diagrams.py:134` | ERROR | 工具场景受限 / 低 | build_diagrams.py:132 自行生成固定本地 drawio，134 行立即回读验证；未发现不可信上传 XML 进入此路径。不是已确认生产 XXE。若将来接受外部 XML，应限制解析资源并使用安全解析器。 |
| SG-036 | `docs/architecture/tools/verify_delivery.py:7` | ERROR | 工具场景受限 / 低 | verify_delivery.py:26-31 读取 EXPECTED 固定列表中的本地架构图，不接受网络请求或任意用户路径。维护者若主动导入不可信图纸仍有解析资源风险；本次没有证明生产接口存在 XXE。 |
| SG-037 | `docs/architecture/tools/verify_delivery.py:31` | ERROR | 工具场景受限 / 低 | verify_delivery.py:26-31 读取 EXPECTED 固定列表中的本地架构图，不接受网络请求或任意用户路径。维护者若主动导入不可信图纸仍有解析资源风险；本次没有证明生产接口存在 XXE。 |
| SG-038 | `infra/docker-compose/nginx/nginx.conf:35` | WARNING | 误报/已有控制 / 不适用 | infra/docker-compose/nginx/nginx.conf:30-40 在 /health 中显式重复 server 层全部五个安全头，注释也解释 add_header 的继承边界。规则报告的是存在 add_header，而不是遗漏防护。 |
| SG-039 | `infra/docker-compose/nginx/nginx.conf:36` | WARNING | 误报/已有控制 / 不适用 | infra/docker-compose/nginx/nginx.conf:30-40 在 /health 中显式重复 server 层全部五个安全头，注释也解释 add_header 的继承边界。规则报告的是存在 add_header，而不是遗漏防护。 |
| SG-040 | `infra/docker-compose/nginx/nginx.conf:37` | WARNING | 误报/已有控制 / 不适用 | infra/docker-compose/nginx/nginx.conf:30-40 在 /health 中显式重复 server 层全部五个安全头，注释也解释 add_header 的继承边界。规则报告的是存在 add_header，而不是遗漏防护。 |
| SG-041 | `infra/docker-compose/nginx/nginx.conf:38` | WARNING | 误报/已有控制 / 不适用 | infra/docker-compose/nginx/nginx.conf:30-40 在 /health 中显式重复 server 层全部五个安全头，注释也解释 add_header 的继承边界。规则报告的是存在 add_header，而不是遗漏防护。 |
| SG-042 | `infra/docker-compose/nginx/nginx.conf:39` | WARNING | 误报/已有控制 / 不适用 | infra/docker-compose/nginx/nginx.conf:30-40 在 /health 中显式重复 server 层全部五个安全头，注释也解释 add_header 的继承边界。规则报告的是存在 add_header，而不是遗漏防护。 |
| SG-043 | `infra/docker-compose/nginx/nginx.conf:40` | WARNING | 误报/已有控制 / 不适用 | infra/docker-compose/nginx/nginx.conf:30-40 在 /health 中显式重复 server 层全部五个安全头，注释也解释 add_header 的继承边界。规则报告的是存在 add_header，而不是遗漏防护。 |
| SG-044 | `infra/docker-compose/nginx/nginx.conf:59` | WARNING | 部署前提待核实 / 低 | 生产 Nginx 确实把请求 $host 转给固定上游；代理目的地不是由 Host 决定。未在当前网关非测试 Go 源码中找到使用请求 Host 构造重置链接/跳转的证据。公网允许域名与入口网关配置未在本次运行验证，建议结合部署评审，不标成已确认 Host 注入漏洞。 |
| SG-045 | `infra/docker-compose/nginx/nginx.conf:76` | WARNING | 部署前提待核实 / 低 | 生产 Nginx 确实把请求 $host 转给固定上游；代理目的地不是由 Host 决定。未在当前网关非测试 Go 源码中找到使用请求 Host 构造重置链接/跳转的证据。公网允许域名与入口网关配置未在本次运行验证，建议结合部署评审，不标成已确认 Host 注入漏洞。 |
| SG-046 | `infra/docker-compose/nginx/story056-e2e.conf:16` | WARNING | 误报/已有控制 / 不适用 | 对应 story056/story060 E2E Nginx 的 server 块没有 add_header 安全头，/health 中只有 Content-Type，不存在规则所称覆盖上级安全头。此配置仅用于 E2E。 |
| SG-047 | `infra/docker-compose/nginx/story056-e2e.conf:22` | WARNING | 测试工具范围 / 低 | 这些 $host 转发位于 story056/story060 E2E 配置，目的地为固定测试上游；未证实生产利用链。若复用为公网配置，需另审允许域名和 Host 消费方。 |
| SG-048 | `infra/docker-compose/nginx/story056-e2e.conf:31` | WARNING | 测试工具范围 / 低 | 这些 $host 转发位于 story056/story060 E2E 配置，目的地为固定测试上游；未证实生产利用链。若复用为公网配置，需另审允许域名和 Host 消费方。 |
| SG-049 | `infra/docker-compose/nginx/story060-e2e.conf:16` | WARNING | 误报/已有控制 / 不适用 | 对应 story056/story060 E2E Nginx 的 server 块没有 add_header 安全头，/health 中只有 Content-Type，不存在规则所称覆盖上级安全头。此配置仅用于 E2E。 |
| SG-050 | `infra/docker-compose/nginx/story060-e2e.conf:25` | WARNING | 测试工具范围 / 低 | 这些 $host 转发位于 story056/story060 E2E 配置，目的地为固定测试上游；未证实生产利用链。若复用为公网配置，需另审允许域名和 Host 消费方。 |
| SG-051 | `infra/docker-compose/nginx/story060-e2e.conf:34` | WARNING | 测试工具范围 / 低 | 这些 $host 转发位于 story056/story060 E2E 配置，目的地为固定测试上游；未证实生产利用链。若复用为公网配置，需另审允许域名和 Host 消费方。 |
| SG-052 | `scripts/test_fujian_math_batch29.py:25` | WARNING | 误报/已有控制 / 不适用 | 测试从固定同目录脚本读取 AST，只编译白名单函数定义，用合成输入检查生成逻辑。exec 输入是仓库维护代码，不来自 API、上传文件或不可信模型输出；可改为可导入辅助模块减少动态执行，但未证实命令注入入口。 |
| SG-053 | `services/api-gateway/internal/auth/cookies.go:10` | WARNING | 误报/已有控制 / 不适用 | Cookie 已显式设置 Secure: h.cookieSecure、HttpOnly:true、SameSiteLax。配置默认按环境开启 Secure，productionSecurityProblems 在生产类似环境拒绝 SessionCookieSecure=false（config.go:497）；规则未解析动态布尔值，不应误判为缺少 Secure。 |
| SG-054 | `services/api-gateway/internal/auth/cookies.go:27` | WARNING | 误报/已有控制 / 不适用 | Cookie 已显式设置 Secure: h.cookieSecure、HttpOnly:true、SameSiteLax。配置默认按环境开启 Secure，productionSecurityProblems 在生产类似环境拒绝 SessionCookieSecure=false（config.go:497）；规则未解析动态布尔值，不应误判为缺少 Secure。 |
| SG-055 | `services/api-gateway/internal/auth/cookies.go:54` | WARNING | 误报/已有控制 / 不适用 | Cookie 已显式设置 Secure: h.cookieSecure、HttpOnly:true、SameSiteLax。配置默认按环境开启 Secure，productionSecurityProblems 在生产类似环境拒绝 SessionCookieSecure=false（config.go:497）；规则未解析动态布尔值，不应误判为缺少 Secure。 |
| SG-056 | `services/api-gateway/internal/auth/cookies.go:62` | WARNING | 误报/已有控制 / 不适用 | Cookie 已显式设置 Secure: h.cookieSecure、HttpOnly:true、SameSiteLax。配置默认按环境开启 Secure，productionSecurityProblems 在生产类似环境拒绝 SessionCookieSecure=false（config.go:497）；规则未解析动态布尔值，不应误判为缺少 Secure。 |
| SG-057 | `services/api-gateway/internal/auth/mfa_http_test.go:36` | WARNING | 误报/已有控制 / 不适用 | 命中的是 httptest 请求侧 AddCookie 构造器；HttpOnly/Secure 是 Set-Cookie 响应属性，浏览器发送 Cookie 请求头不携带这些属性。测试用来验证 CSRF 与无效 Authorization 回退，未改变真实服务设置 Cookie 的安全属性。 |
| SG-058 | `services/api-gateway/internal/auth/mfa_http_test.go:36` | WARNING | 误报/已有控制 / 不适用 | 命中的是 httptest 请求侧 AddCookie 构造器；HttpOnly/Secure 是 Set-Cookie 响应属性，浏览器发送 Cookie 请求头不携带这些属性。测试用来验证 CSRF 与无效 Authorization 回退，未改变真实服务设置 Cookie 的安全属性。 |
| SG-059 | `services/api-gateway/internal/files/store_postgres.go:221` | WARNING | 误报/已有控制 / 不适用 | files.transition 只拼接程序生成的 $7、$8 等占位符与固定 assetColumns，tenant、id、revision、状态和错误详情通过 args... 绑定。来源状态来自代码常量；未将外部内容拼入 SQL 语法。 |
| SG-060 | `services/api-gateway/internal/middleware/middleware_test.go:64` | WARNING | 误报/已有控制 / 不适用 | 命中的是 httptest 请求侧 AddCookie 构造器；HttpOnly/Secure 是 Set-Cookie 响应属性，浏览器发送 Cookie 请求头不携带这些属性。测试用来验证 CSRF 与无效 Authorization 回退，未改变真实服务设置 Cookie 的安全属性。 |
| SG-061 | `services/api-gateway/internal/middleware/middleware_test.go:64` | WARNING | 误报/已有控制 / 不适用 | 命中的是 httptest 请求侧 AddCookie 构造器；HttpOnly/Secure 是 Set-Cookie 响应属性，浏览器发送 Cookie 请求头不携带这些属性。测试用来验证 CSRF 与无效 Authorization 回退，未改变真实服务设置 Cookie 的安全属性。 |
| SG-062 | `services/api-gateway/internal/middleware/middleware_test.go:78` | WARNING | 误报/已有控制 / 不适用 | 命中的是 httptest 请求侧 AddCookie 构造器；HttpOnly/Secure 是 Set-Cookie 响应属性，浏览器发送 Cookie 请求头不携带这些属性。测试用来验证 CSRF 与无效 Authorization 回退，未改变真实服务设置 Cookie 的安全属性。 |
| SG-063 | `services/api-gateway/internal/middleware/middleware_test.go:78` | WARNING | 误报/已有控制 / 不适用 | 命中的是 httptest 请求侧 AddCookie 构造器；HttpOnly/Secure 是 Set-Cookie 响应属性，浏览器发送 Cookie 请求头不携带这些属性。测试用来验证 CSRF 与无效 Authorization 回退，未改变真实服务设置 Cookie 的安全属性。 |
| SG-064 | `services/api-gateway/internal/middleware/middleware_test.go:93` | WARNING | 误报/已有控制 / 不适用 | 命中的是 httptest 请求侧 AddCookie 构造器；HttpOnly/Secure 是 Set-Cookie 响应属性，浏览器发送 Cookie 请求头不携带这些属性。测试用来验证 CSRF 与无效 Authorization 回退，未改变真实服务设置 Cookie 的安全属性。 |
| SG-065 | `services/api-gateway/internal/middleware/middleware_test.go:93` | WARNING | 误报/已有控制 / 不适用 | 命中的是 httptest 请求侧 AddCookie 构造器；HttpOnly/Secure 是 Set-Cookie 响应属性，浏览器发送 Cookie 请求头不携带这些属性。测试用来验证 CSRF 与无效 Authorization 回退，未改变真实服务设置 Cookie 的安全属性。 |
| SG-066 | `services/api-gateway/internal/middleware/middleware_test.go:132` | WARNING | 误报/已有控制 / 不适用 | 命中的是 httptest 请求侧 AddCookie 构造器；HttpOnly/Secure 是 Set-Cookie 响应属性，浏览器发送 Cookie 请求头不携带这些属性。测试用来验证 CSRF 与无效 Authorization 回退，未改变真实服务设置 Cookie 的安全属性。 |
| SG-067 | `services/api-gateway/internal/middleware/middleware_test.go:132` | WARNING | 误报/已有控制 / 不适用 | 命中的是 httptest 请求侧 AddCookie 构造器；HttpOnly/Secure 是 Set-Cookie 响应属性，浏览器发送 Cookie 请求头不携带这些属性。测试用来验证 CSRF 与无效 Authorization 回退，未改变真实服务设置 Cookie 的安全属性。 |
| SG-068 | `services/api-gateway/internal/minioadapter/client_test.go:72` | WARNING | 误报/已有控制 / 不适用 | TestNewClientRejectsTLSBeforeVersion12 故意建立 MaxVersion=TLS1.1 的测试端点，断言真实 MinIO 客户端拒绝该连接。命中代码是负向测试条件，不能建议把它改成 TLS1.2 使测试失去覆盖。 |
| SG-069 | `services/api-gateway/internal/modelgovernance/managed_config_probe.go:14` | WARNING | 误报/已有控制 / 不适用 | math/rand 仅用于 retryManagedReadOnlyRequest 的毫秒级重试抖动（managed_config_probe.go:330），未用于令牌、密码、加密密钥或租约随机值。非密码学调度随机性允许使用该包。 |
| SG-070 | `services/api-gateway/internal/score/store_postgres.go:100` | WARNING | 误报/已有控制 / 不适用 | score.ListExamGrades 拼接固定 WHERE 片段和由 len(args) 生成的占位符；Status、Query、Cursor、Limit 均单独绑定，tenant/exam 也在 WHERE 和 JOIN 中。未发现动态查询文本来自用户输入的 SQL 注入。 |
| SG-071 | `services/api-gateway/internal/server/e2e_business_command_test.go:333` | WARNING | 误报/已有控制 / 不适用 | E2E 数据库测试使用固定表名白名单或本地 UUID 生成的隔离数据库、角色、触发器名，数据库/角色标识通过 pgx.Identifier.Sanitize 引用；故障触发条件拼入的命令 ID/题目 ID 来自测试固定字符串或 UUID fixture。不是生产 API 查询，也未找到可由外部输入控制的拼接值。 |
| SG-072 | `services/api-gateway/internal/server/e2e_business_command_test.go:341` | WARNING | 误报/已有控制 / 不适用 | E2E 数据库测试使用固定表名白名单或本地 UUID 生成的隔离数据库、角色、触发器名，数据库/角色标识通过 pgx.Identifier.Sanitize 引用；故障触发条件拼入的命令 ID/题目 ID 来自测试固定字符串或 UUID fixture。不是生产 API 查询，也未找到可由外部输入控制的拼接值。 |
| SG-073 | `services/api-gateway/internal/server/e2e_business_command_test.go:342` | WARNING | 误报/已有控制 / 不适用 | E2E 数据库测试使用固定表名白名单或本地 UUID 生成的隔离数据库、角色、触发器名，数据库/角色标识通过 pgx.Identifier.Sanitize 引用；故障触发条件拼入的命令 ID/题目 ID 来自测试固定字符串或 UUID fixture。不是生产 API 查询，也未找到可由外部输入控制的拼接值。 |
| SG-074 | `services/api-gateway/internal/server/e2e_business_command_test.go:344` | WARNING | 误报/已有控制 / 不适用 | E2E 数据库测试使用固定表名白名单或本地 UUID 生成的隔离数据库、角色、触发器名，数据库/角色标识通过 pgx.Identifier.Sanitize 引用；故障触发条件拼入的命令 ID/题目 ID 来自测试固定字符串或 UUID fixture。不是生产 API 查询，也未找到可由外部输入控制的拼接值。 |
| SG-075 | `services/api-gateway/internal/server/e2e_compose_bootstrap_test.go:49` | WARNING | 误报/已有控制 / 不适用 | E2E 数据库测试使用固定表名白名单或本地 UUID 生成的隔离数据库、角色、触发器名，数据库/角色标识通过 pgx.Identifier.Sanitize 引用；故障触发条件拼入的命令 ID/题目 ID 来自测试固定字符串或 UUID fixture。不是生产 API 查询，也未找到可由外部输入控制的拼接值。 |
| SG-076 | `services/api-gateway/internal/server/e2e_compose_bootstrap_test.go:53` | WARNING | 误报/已有控制 / 不适用 | E2E 数据库测试使用固定表名白名单或本地 UUID 生成的隔离数据库、角色、触发器名，数据库/角色标识通过 pgx.Identifier.Sanitize 引用；故障触发条件拼入的命令 ID/题目 ID 来自测试固定字符串或 UUID fixture。不是生产 API 查询，也未找到可由外部输入控制的拼接值。 |
| SG-077 | `services/api-gateway/internal/server/e2e_compose_bootstrap_test.go:57` | WARNING | 误报/已有控制 / 不适用 | E2E 数据库测试使用固定表名白名单或本地 UUID 生成的隔离数据库、角色、触发器名，数据库/角色标识通过 pgx.Identifier.Sanitize 引用；故障触发条件拼入的命令 ID/题目 ID 来自测试固定字符串或 UUID fixture。不是生产 API 查询，也未找到可由外部输入控制的拼接值。 |
| SG-078 | `services/api-gateway/internal/server/e2e_mfa_test.go:239` | WARNING | 误报/已有控制 / 不适用 | E2E 数据库测试使用固定表名白名单或本地 UUID 生成的隔离数据库、角色、触发器名，数据库/角色标识通过 pgx.Identifier.Sanitize 引用；故障触发条件拼入的命令 ID/题目 ID 来自测试固定字符串或 UUID fixture。不是生产 API 查询，也未找到可由外部输入控制的拼接值。 |
| SG-079 | `services/api-gateway/internal/server/e2e_mfa_test.go:308` | WARNING | 误报/已有控制 / 不适用 | E2E 数据库测试使用固定表名白名单或本地 UUID 生成的隔离数据库、角色、触发器名，数据库/角色标识通过 pgx.Identifier.Sanitize 引用；故障触发条件拼入的命令 ID/题目 ID 来自测试固定字符串或 UUID fixture。不是生产 API 查询，也未找到可由外部输入控制的拼接值。 |
| SG-080 | `services/api-gateway/internal/server/e2e_mfa_test.go:312` | WARNING | 误报/已有控制 / 不适用 | E2E 数据库测试使用固定表名白名单或本地 UUID 生成的隔离数据库、角色、触发器名，数据库/角色标识通过 pgx.Identifier.Sanitize 引用；故障触发条件拼入的命令 ID/题目 ID 来自测试固定字符串或 UUID fixture。不是生产 API 查询，也未找到可由外部输入控制的拼接值。 |
| SG-081 | `services/api-gateway/internal/server/e2e_mfa_test.go:316` | WARNING | 误报/已有控制 / 不适用 | E2E 数据库测试使用固定表名白名单或本地 UUID 生成的隔离数据库、角色、触发器名，数据库/角色标识通过 pgx.Identifier.Sanitize 引用；故障触发条件拼入的命令 ID/题目 ID 来自测试固定字符串或 UUID fixture。不是生产 API 查询，也未找到可由外部输入控制的拼接值。 |
| SG-082 | `services/api-gateway/internal/server/e2e_postgres_test.go:354` | WARNING | 误报/已有控制 / 不适用 | E2E 数据库测试使用固定表名白名单或本地 UUID 生成的隔离数据库、角色、触发器名，数据库/角色标识通过 pgx.Identifier.Sanitize 引用；故障触发条件拼入的命令 ID/题目 ID 来自测试固定字符串或 UUID fixture。不是生产 API 查询，也未找到可由外部输入控制的拼接值。 |
| SG-083 | `services/api-gateway/internal/server/e2e_postgres_test.go:357` | WARNING | 误报/已有控制 / 不适用 | E2E 数据库测试使用固定表名白名单或本地 UUID 生成的隔离数据库、角色、触发器名，数据库/角色标识通过 pgx.Identifier.Sanitize 引用；故障触发条件拼入的命令 ID/题目 ID 来自测试固定字符串或 UUID fixture。不是生产 API 查询，也未找到可由外部输入控制的拼接值。 |
| SG-084 | `services/api-gateway/internal/server/e2e_postgres_test.go:361` | WARNING | 误报/已有控制 / 不适用 | E2E 数据库测试使用固定表名白名单或本地 UUID 生成的隔离数据库、角色、触发器名，数据库/角色标识通过 pgx.Identifier.Sanitize 引用；故障触发条件拼入的命令 ID/题目 ID 来自测试固定字符串或 UUID fixture。不是生产 API 查询，也未找到可由外部输入控制的拼接值。 |
| SG-085 | `services/api-gateway/internal/server/e2e_postgres_test.go:711` | WARNING | 误报/已有控制 / 不适用 | E2E 数据库测试使用固定表名白名单或本地 UUID 生成的隔离数据库、角色、触发器名，数据库/角色标识通过 pgx.Identifier.Sanitize 引用；故障触发条件拼入的命令 ID/题目 ID 来自测试固定字符串或 UUID fixture。不是生产 API 查询，也未找到可由外部输入控制的拼接值。 |
| SG-086 | `services/api-gateway/internal/server/e2e_postgres_test.go:722` | WARNING | 误报/已有控制 / 不适用 | E2E 数据库测试使用固定表名白名单或本地 UUID 生成的隔离数据库、角色、触发器名，数据库/角色标识通过 pgx.Identifier.Sanitize 引用；故障触发条件拼入的命令 ID/题目 ID 来自测试固定字符串或 UUID fixture。不是生产 API 查询，也未找到可由外部输入控制的拼接值。 |
| SG-087 | `services/api-gateway/internal/server/e2e_postgres_test.go:730` | WARNING | 误报/已有控制 / 不适用 | E2E 数据库测试使用固定表名白名单或本地 UUID 生成的隔离数据库、角色、触发器名，数据库/角色标识通过 pgx.Identifier.Sanitize 引用；故障触发条件拼入的命令 ID/题目 ID 来自测试固定字符串或 UUID fixture。不是生产 API 查询，也未找到可由外部输入控制的拼接值。 |
| SG-088 | `services/api-gateway/internal/server/e2e_question_bank_import_test.go:166` | WARNING | 误报/已有控制 / 不适用 | E2E 数据库测试使用固定表名白名单或本地 UUID 生成的隔离数据库、角色、触发器名，数据库/角色标识通过 pgx.Identifier.Sanitize 引用；故障触发条件拼入的命令 ID/题目 ID 来自测试固定字符串或 UUID fixture。不是生产 API 查询，也未找到可由外部输入控制的拼接值。 |
| SG-089 | `services/api-gateway/internal/server/e2e_question_bank_test.go:350` | WARNING | 误报/已有控制 / 不适用 | E2E 数据库测试使用固定表名白名单或本地 UUID 生成的隔离数据库、角色、触发器名，数据库/角色标识通过 pgx.Identifier.Sanitize 引用；故障触发条件拼入的命令 ID/题目 ID 来自测试固定字符串或 UUID fixture。不是生产 API 查询，也未找到可由外部输入控制的拼接值。 |
| SG-090 | `services/api-gateway/tools/story056-postgres-e2e/Dockerfile:9` | ERROR | 测试工具范围 / 低 | 原扫描快照是单阶段 E2E Go 测试镜像，最终确实没有 USER，属于低风险测试工具加固项。扫描后的当前工作区已改为多阶段并设置 USER 10001:10001，已解决运行身份问题；不能把扫描后修复反标成原扫描误报。快照/当前哈希差异见 snapshot-current-check.json，未由本次审计修改。 |
| SG-091 | `services/api-gateway/tools/story056-real-worker-e2e/Dockerfile:18` | ERROR | 测试工具范围 / 低 | 独立 story056-real-worker-e2e 测试客户端镜像最终没有 USER，使用 alpine 默认 root；Compose 仅在 e2e profile 启动它，没有公网服务入口。这是确实存在的测试镜像加固项，建议独立非 root 用户；不是已确认生产越权漏洞。 |
| SG-092 | `services/image-quality-worker/image_quality/api.py:94` | WARNING | 条件性风险待核实 / 中 | 初始 URL 通过 _trusted_service_url/_trusted_url 限制配置 API 同源，具备 timeout 和受限响应读取。但 request.urlopen 使用默认重定向处理器，跳转后不重新执行同源校验；合成无网络验证显示 Authorization 与 Worker Lease 头会保留至跨域 GET 跳转。风险前提是 API/反向代理返回攻击者可控重定向，未在本次静态检查中证明普通用户能触发。建议凭据请求禁止自动跳转，或逐跳校验 origin 且去掉敏感头。 |
| SG-093 | `services/image-quality-worker/image_quality/api.py:121` | WARNING | 条件性风险待核实 / 中 | 初始 URL 通过 _trusted_service_url/_trusted_url 限制配置 API 同源，具备 timeout 和受限响应读取。但 request.urlopen 使用默认重定向处理器，跳转后不重新执行同源校验；合成无网络验证显示 Authorization 与 Worker Lease 头会保留至跨域 GET 跳转。风险前提是 API/反向代理返回攻击者可控重定向，未在本次静态检查中证明普通用户能触发。建议凭据请求禁止自动跳转，或逐跳校验 origin 且去掉敏感头。 |
| SG-094 | `services/image-quality-worker/image_quality/api.py:145` | WARNING | 条件性风险待核实 / 中 | 初始 URL 通过 _trusted_service_url/_trusted_url 限制配置 API 同源，具备 timeout 和受限响应读取。但 request.urlopen 使用默认重定向处理器，跳转后不重新执行同源校验；合成无网络验证显示 Authorization 与 Worker Lease 头会保留至跨域 GET 跳转。风险前提是 API/反向代理返回攻击者可控重定向，未在本次静态检查中证明普通用户能触发。建议凭据请求禁止自动跳转，或逐跳校验 origin 且去掉敏感头。 |
| SG-095 | `services/math-verification-worker/tests/test_timeout.py:34` | WARNING | 误报/已有控制 / 不适用 | 这里只调用 pickle.dumps 对可信测试内存对象序列化，验证 multiprocessing 管道大结果场景；没有 pickle.loads/load 读取用户数据。序列化大小断言本身不是不安全反序列化。 |
| SG-096 | `services/ocr-worker/Dockerfile:6` | ERROR | 误报/已有控制 / 不适用 | OCR Dockerfile:6 的 USER root 只用于安装依赖；文件末尾已切换 USER nobody，之后 CMD python -m ocr_worker。Semgrep 同时报告此 Dockerfile 的部分解析错误，不能把解析到的最后 root 当作真实最终用户。 |
| SG-097 | `services/ocr-worker/ocr_worker/api.py:266` | WARNING | 条件性风险待核实 / 中 | 初始 URL 通过 _trusted_service_url/_trusted_url 限制配置 API 同源，具备 timeout 和受限响应读取。但 request.urlopen 使用默认重定向处理器，跳转后不重新执行同源校验；合成无网络验证显示 Authorization 与 Worker Lease 头会保留至跨域 GET 跳转。风险前提是 API/反向代理返回攻击者可控重定向，未在本次静态检查中证明普通用户能触发。建议凭据请求禁止自动跳转，或逐跳校验 origin 且去掉敏感头。 |
| SG-098 | `services/ocr-worker/ocr_worker/api.py:290` | WARNING | 条件性风险待核实 / 中 | 初始 URL 通过 _trusted_service_url/_trusted_url 限制配置 API 同源，具备 timeout 和受限响应读取。但 request.urlopen 使用默认重定向处理器，跳转后不重新执行同源校验；合成无网络验证显示 Authorization 与 Worker Lease 头会保留至跨域 GET 跳转。风险前提是 API/反向代理返回攻击者可控重定向，未在本次静态检查中证明普通用户能触发。建议凭据请求禁止自动跳转，或逐跳校验 origin 且去掉敏感头。 |
| SG-099 | `services/ocr-worker/ocr_worker/math_runner.py:41` | WARNING | 条件性风险待核实 / 中 | 数学验证目标来自受信服务配置，操作名是内部固定调用，设置 timeout 与 JSON 大小上限，未见直接用户 URL 输入。仍使用默认 urllib 重定向，X-EduGrade-Internal-Token 等自定义凭据头不会自动清除；需服务返回不可信重定向才会触发，当前缺少可利用路径证据。建议禁用凭据请求重定向。 |
| SG-100 | `services/page-processing-worker/page_processing/api.py:71` | WARNING | 条件性风险待核实 / 中 | 初始 URL 通过 _trusted_service_url/_trusted_url 限制配置 API 同源，具备 timeout 和受限响应读取。但 request.urlopen 使用默认重定向处理器，跳转后不重新执行同源校验；合成无网络验证显示 Authorization 与 Worker Lease 头会保留至跨域 GET 跳转。风险前提是 API/反向代理返回攻击者可控重定向，未在本次静态检查中证明普通用户能触发。建议凭据请求禁止自动跳转，或逐跳校验 origin 且去掉敏感头。 |
| SG-101 | `services/page-processing-worker/page_processing/api.py:102` | WARNING | 条件性风险待核实 / 中 | 初始 URL 通过 _trusted_service_url/_trusted_url 限制配置 API 同源，具备 timeout 和受限响应读取。但 request.urlopen 使用默认重定向处理器，跳转后不重新执行同源校验；合成无网络验证显示 Authorization 与 Worker Lease 头会保留至跨域 GET 跳转。风险前提是 API/反向代理返回攻击者可控重定向，未在本次静态检查中证明普通用户能触发。建议凭据请求禁止自动跳转，或逐跳校验 origin 且去掉敏感头。 |
| SG-102 | `services/page-processing-worker/page_processing/api.py:160` | WARNING | 条件性风险待核实 / 中 | 初始 URL 通过 _trusted_service_url/_trusted_url 限制配置 API 同源，具备 timeout 和受限响应读取。但 request.urlopen 使用默认重定向处理器，跳转后不重新执行同源校验；合成无网络验证显示 Authorization 与 Worker Lease 头会保留至跨域 GET 跳转。风险前提是 API/反向代理返回攻击者可控重定向，未在本次静态检查中证明普通用户能触发。建议凭据请求禁止自动跳转，或逐跳校验 origin 且去掉敏感头。 |
| SG-103 | `services/page-processing-worker/tests/test_main.py:47` | WARNING | 误报/已有控制 / 不适用 | _config 位于 tests/test_main.py，固定测试字符串只传给 FakeClient/FakeRunner 与 monkeypatch，用来覆盖登录和配置边界，未用作真实 Worker 生产默认密码。报告不复述此合成值。 |
| SG-104 | `services/subjective-grading-worker/subjective_grading_worker/api.py:94` | WARNING | 条件性风险待核实 / 中 | 初始 URL 通过 _trusted_service_url/_trusted_url 限制配置 API 同源，具备 timeout 和受限响应读取。但 request.urlopen 使用默认重定向处理器，跳转后不重新执行同源校验；合成无网络验证显示 Authorization 与 Worker Lease 头会保留至跨域 GET 跳转。风险前提是 API/反向代理返回攻击者可控重定向，未在本次静态检查中证明普通用户能触发。建议凭据请求禁止自动跳转，或逐跳校验 origin 且去掉敏感头。 |
| SG-105 | `tests/e2e/story060_grading_agent.py:19` | WARNING | 测试工具范围 / 低 | E2E 脚本目标来自测试环境变量，默认 Compose 内部固定服务名，不接受生产 API 用户 URL。通用 urlopen 规则只发现动态地址；测试工具可同样采用拒绝重定向与受限响应读取，但当前不构成已确认生产 SSRF。 |

### 按规则汇总

| 规则 ID | 命中数 |
| --- | ---: |
| `audit.package_managers.dependabot.dependabot-missing-cooldown.dependabot-missing-cooldown` | 11 |
| `audit.python.lang.security.audit.httpsconnection-detected.httpsconnection-detected` | 1 |
| `audit.python.lang.security.audit.dynamic-urllib-use-detected.dynamic-urllib-use-detected` | 23 |
| `audit.generic.nginx.security.header-redefinition.header-redefinition` | 14 |
| `audit.javascript.lang.security.audit.unknown-value-with-script-tag.unknown-value-with-script-tag` | 3 |
| `audit.python.lang.security.use-defused-xml.use-defused-xml` | 2 |
| `audit.python.lang.security.use-defused-xml-parse.use-defused-xml-parse` | 2 |
| `audit.generic.nginx.security.request-host-used.request-host-used` | 6 |
| `audit.python.lang.security.audit.exec-detected.exec-detected` | 1 |
| `audit.go.lang.security.audit.net.cookie-missing-secure.cookie-missing-secure` | 9 |
| `audit.go.lang.security.audit.net.cookie-missing-httponly.cookie-missing-httponly` | 5 |
| `audit.go.lang.security.audit.database.string-formatted-query.string-formatted-query` | 21 |
| `audit.go.lang.security.audit.crypto.missing-ssl-minversion.missing-ssl-minversion` | 1 |
| `audit.go.lang.security.audit.crypto.math_random.math-random-used` | 1 |
| `audit.dockerfile.security.missing-user.missing-user` | 1 |
| `audit.dockerfile.security.missing-user-entrypoint.missing-user-entrypoint` | 1 |
| `audit.python.lang.security.deserialization.pickle.avoid-pickle` | 1 |
| `audit.dockerfile.security.last-user-is-root.last-user-is-root` | 1 |
| `audit.python.lang.security.audit.hardcoded-password-default-argument.hardcoded-password-default-argument` | 1 |

## 4. 解析缺口

全部 16 条错误均为 PartialParsing，具体位置如下；文件其余部分可能已经扫描，但不能视为全部语法均被理解。特别是 `.github/workflows/ci.yml`、Web Dockerfile 和 OCR Dockerfile 的相关段落需要结合本地 CI/构建检查。

| 文件 | 受影响起始行 |
| --- | --- |
| `.github/workflows/ci.yml` | 60 |
| `.github/workflows/ci.yml` | 186 |
| `apps/student-portal/src/App.reauthentication.test.tsx` | 10 |
| `apps/web-admin/Dockerfile` | 23 |
| `apps/web-admin/src/api/reports.batch48.test.ts` | 8 |
| `apps/web-admin/src/components/ScoringExamples.ux.test.tsx` | 21, 31 |
| `apps/web-admin/src/features/answer-sheet-template/answerSheetTemplatePage.batch7.test.tsx` | 118, 128, 229 |
| `apps/web-admin/src/features/exams/management/ManagementExamArchiveDialog.test.tsx` | 11 |
| `apps/web-admin/src/features/question-bank/QuestionBankACL.ux.test.tsx` | 18, 22, 25 |
| `apps/web-admin/src/features/question-bank/QuestionBankPage.schemaRecovery.test.tsx` | 27, 33, 36 |
| `apps/web-admin/src/features/question-bank/questionBank.complexity.batch50.test.tsx` | 29 |
| `apps/web-admin/src/pages/ExamManagementPage.test.tsx` | 11 |
| `apps/web-admin/src/pages/PaperRubricPage.batch42.test.tsx` | 20, 43 |
| `apps/web-admin/src/pages/PaperRubricPage.test.tsx` | 18 |
| `apps/web-admin/src/pages/PlatformModelConfigPage.test.tsx` | 29, 35 |
| `services/ocr-worker/Dockerfile` | 29 |

## 5. 不能从本次结果推出的结论

- 规则扫描未自动证明学校/租户数据隔离、数据库 RLS、业务状态机、并发成绩写入或阅卷准确率正确；这些仍需专项代码审查、数据库集成测试与业务评测。
- 已核验的是 105 条告警的位置、输入来源和相关保护控制，不是逐个文件完成全面人工审读。清单中全部文件人工审读状态仍为 pending，不能用自动枚举或扫描完成替代逐文件审读。
- 无网络合成重定向核验只证明标准库请求构造行为，没有向现有服务发请求，也没有证明生产部署可被利用。
- 没有自动修复或重新格式化业务代码；所有已有未提交修改均保留。

## 6. 交付文件

| 文件 | 用途 |
| --- | --- |
| `semgrep-raw.json` | 平台原始结果：105 命中、16 解析警告、OSS 标记、扫描路径 |
| `scan.stderr.log` / `docker-run.log` | 引擎状态、434 实际运行规则、命令执行结果 |
| `scan-metadata.json` | 版本、完整参数、耗时、退出码、隔离条件 |
| `findings-triage.json` / `findings.csv` | 105 条逐条人工核验，全部非 pending |
| `root-triage.json` / `backend-triage.json` | 并行审查分工证据 |
| `source-inventory.json` | 本地快照逐文件 SHA-256、排除项、pending 人工文件状态 |
| `snapshot-current-check.json` | 报告生成时对照快照检查文件漂移 |
| `rule-manifest.json` / `rule-capabilities.json` | 官方规则来源、哈希、语言能力和明确限制 |
| `redirect-semantics.json` / `check_redirect_semantics.py` | 无网络合成凭据重定向边界核验 |
| `run-local.ps1` / `prepare_snapshot.py` / `local_scan.py` | 用当前工作区再次执行真实本地扫描 |
| `setup-notes.md` / `pip-install.log` / `docker-pull.log` | 准备步骤、失败与范围纠正说明 |

报告生成时检查 3196 个快照文件，发现当前字节变化或缺失 21 个。正式引擎结果对应 10:55 的固定快照，不能冒充扫描后的最新文件。变化影响 SG-090、SG-105 所在文件：SG-090 当前已设置非 root USER；SG-105 默认 Agent URL 已由 HTTP 改为 HTTPS，动态 URL 的测试工具范围结论不变。其他变化文件没有对应本次告警，仍不等于已经按新字节扫描。

变化文件：

- `.dockerignore`
- `.github/workflows/ci.yml`
- `ai-services/README.md`
- `ai-services/grading_agent/config.py`
- `ai-services/grading_agent/server.py`
- `ai-services/tests/test_config.py`
- `ai-services/tests/test_server_tls.py`
- `docs/architecture/evidence/audit-coverage.json`
- `docs/architecture/evidence/workspace-preservation.json`
- `infra/docker-compose/.env.example`
- `infra/docker-compose/README.md`
- `infra/docker-compose/docker-compose.story060-e2e.yml`
- `infra/docker-compose/docker-compose.yml`
- `infra/docker-compose/scripts/preflight.ps1`
- `services/api-gateway/tools/story056-postgres-e2e/Dockerfile`
- `services/math-verification-worker/Dockerfile`
- `services/math-verification-worker/math_verification_worker/server.py`
- `services/math-verification-worker/tests/test_server.py`
- `services/ocr-worker/ocr_worker/config.py`
- `services/ocr-worker/tests/test_config.py`
- `tests/e2e/story060_grading_agent.py`
