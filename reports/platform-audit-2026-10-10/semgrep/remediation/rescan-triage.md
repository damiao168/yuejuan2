# 复扫命中与覆盖核验

完整扫描在补充 XML 修复前快照上完成：3236 个文本源文件，2605 个 paths.scanned，27 条命中、18 个 PartialParsing 诊断、退出码0。

CLI 记录586个路径被 .semgrepignore 模式忽略；其余45个源文件没有进入引擎 paths.scanned，不能逐文件推定过滤原因。与首次 Linux/Docker 的3197目标覆盖不等价，105→27不能全部归因于代码修复。

原报告37个命中文件中14个未被完整复扫实际扫描；随后显式传入46个文件，paths.scanned逐个核对全覆盖，原37个文件全部包括在内。定向结果65条命中、3个PartialParsing、退出码0。

定向计数变化：42个原rule/path命中减少=35个本次处置位置+SG-090既有修复1个+原生产Nginx重复安全头6个匹配差异。本次36条处置中的SG-034仍命中标准库生成import，但输入解析已加固，因此保留误报说明。新增2个subprocess命中在可信本地测试工具，详见[定向核验](rescan-report-scope/triage.md)。

全仓新增3条XML命中涉及两个工具，已补充安全解析和测试；这些不计入原报告36条。补充代码与该次全仓快照字节不同，见[补充证据](supplemental-xml-results.json)及[补充扫描](rescan-supplemental/scan-metadata.json)。另外2条child_process为固定本地审计/门禁命令，逐项如下。

扫描器部分解析涵盖两个E2E Dockerfile的最终指令；USER配置必须结合静态审核，不能用告警消失代替Docker运行身份验收。其余诊断为TS测试语法、Dockerfile/Bash片段和工作流嵌入脚本解析差异。未为了扫描器兼容而改写业务或测试语法。

## 全仓27条命中

| ID | 快照位置 | 处置 | 依据 |
| --- | --- | --- | --- |
| RS-001 | `ai-services/grading_agent/managed_model.py:62` | existing_control | managed_model.public_json_transport 先限制 HTTPS，拒绝任何非公网 DNS 结果，再固定已校验 IP；HTTPSConnection 保留原 hostname 进行 SNI、证书链和主机名验证，响应有 4MB 上限并关闭连接。未发现由此调用直接引入的 SSRF/TLS 绕过；未来 Python 升级需关注 _create_connection 私有接口兼容性。 |
| RS-002 | `apps/web-admin/nginx.conf:17` | existing_control | apps/web-admin/nginx.conf:14-24 的 /health location 已显式重复所有 server 安全头；17 行 Content-Type 并未造成 CSP、X-Frame-Options 等丢失。19-23 行正是修复继承边界的防护代码。 |
| RS-003 | `apps/web-admin/nginx.conf:19` | existing_control | apps/web-admin/nginx.conf:14-24 的 /health location 已显式重复所有 server 安全头；17 行 Content-Type 并未造成 CSP、X-Frame-Options 等丢失。19-23 行正是修复继承边界的防护代码。 |
| RS-004 | `apps/web-admin/nginx.conf:20` | existing_control | apps/web-admin/nginx.conf:14-24 的 /health location 已显式重复所有 server 安全头；17 行 Content-Type 并未造成 CSP、X-Frame-Options 等丢失。19-23 行正是修复继承边界的防护代码。 |
| RS-005 | `apps/web-admin/nginx.conf:21` | existing_control | apps/web-admin/nginx.conf:14-24 的 /health location 已显式重复所有 server 安全头；17 行 Content-Type 并未造成 CSP、X-Frame-Options 等丢失。19-23 行正是修复继承边界的防护代码。 |
| RS-006 | `apps/web-admin/nginx.conf:22` | existing_control | apps/web-admin/nginx.conf:14-24 的 /health location 已显式重复所有 server 安全头；17 行 Content-Type 并未造成 CSP、X-Frame-Options 等丢失。19-23 行正是修复继承边界的防护代码。 |
| RS-007 | `apps/web-admin/nginx.conf:23` | existing_control | apps/web-admin/nginx.conf:14-24 的 /health location 已显式重复所有 server 安全头；17 行 Content-Type 并未造成 CSP、X-Frame-Options 等丢失。19-23 行正是修复继承边界的防护代码。 |
| RS-008 | `apps/web-admin/src/auth/mfaFlow.test.ts:196` | existing_control | mfaFlow.test.ts:194-199 向 mock response 注入固定 <script> 字符串，断言 confirm 返回 uncertain 且流程关闭；这里不是脚本模板插值或 DOM 执行。同一行的三条命中属于同一负向测试。 |
| RS-009 | `apps/web-admin/src/auth/mfaFlow.test.ts:196` | existing_control | mfaFlow.test.ts:194-199 向 mock response 注入固定 <script> 字符串，断言 confirm 返回 uncertain 且流程关闭；这里不是脚本模板插值或 DOM 执行。同一行的三条命中属于同一负向测试。 |
| RS-010 | `apps/web-admin/src/auth/mfaFlow.test.ts:196` | existing_control | mfaFlow.test.ts:194-199 向 mock response 注入固定 <script> 字符串，断言 confirm 返回 uncertain 且流程关闭；这里不是脚本模板插值或 DOM 执行。同一行的三条命中属于同一负向测试。 |
| RS-011 | `docs/architecture/tools/build_diagrams.py:6` | fixed_boundary_residual | 标准库 ElementTree 只生成节点；磁盘回读已改用 defusedxml，显式拒绝 DTD。import 规则仍命中生成依赖，保留此残余误报，不删除生成代码。 |
| RS-012 | `docs/architecture/tools/build_preview.py:5` | supplemental_fixed | 这次全仓快照发现的同类解析入口；复扫后补充迁移到 defusedxml 并拒绝 DTD，正常及恶意 XML 通过临时文件回归。此条描述的是修复前快照，当前字节另见补充复扫。 |
| RS-013 | `docs/architecture/tools/verify_typography.py:6` | supplemental_fixed | 这次全仓快照发现的同类解析入口；复扫后补充迁移到 defusedxml 并拒绝 DTD，正常及恶意 XML 通过临时文件回归。此条描述的是修复前快照，当前字节另见补充复扫。 |
| RS-014 | `docs/architecture/tools/verify_typography.py:20` | supplemental_fixed | 这次全仓快照发现的同类解析入口；复扫后补充迁移到 defusedxml 并拒绝 DTD，正常及恶意 XML 通过临时文件回归。此条描述的是修复前快照，当前字节另见补充复扫。 |
| RS-015 | `infra/docker-compose/nginx/story056-e2e.conf:18` | existing_control | 对应 story056/story060 E2E Nginx 的 server 块没有 add_header 安全头，/health 中只有 Content-Type，不存在规则所称覆盖上级安全头。此配置仅用于 E2E。 |
| RS-016 | `infra/docker-compose/nginx/story060-e2e.conf:18` | existing_control | 对应 story056/story060 E2E Nginx 的 server 块没有 add_header 安全头，/health 中只有 Content-Type，不存在规则所称覆盖上级安全头。此配置仅用于 E2E。 |
| RS-017 | `lab/scripts/run-graduation-audit.js:14` | trusted_local_tool | 本地审计脚本只调用代码内固定 npm/node 命令和固定参数；Windows cmd 分支同样没有请求数据或可变业务字段拼接，ComSpec 来自运行者环境。拥有本地执行权限是工具前提，未见服务端命令注入入口。未运行会覆盖实验账本的审计脚本。 |
| RS-018 | `scripts/check-lab-integration.mjs:16` | trusted_local_tool | 本地门禁只运行固定 npm test/eval/gate 和固定 go test 包；命令参数不由API或报告字段提供，cwd 由仓库路径确定。Windows cmd 分支接受本地工具环境，未见外部请求到命令执行的路径。未运行会覆盖实验报告的完整门禁。 |
| RS-019 | `scripts/test_fujian_math_batch29.py:25` | existing_control | 测试从固定同目录脚本读取 AST，只编译白名单函数定义，用合成输入检查生成逻辑。exec 输入是仓库维护代码，不来自 API、上传文件或不可信模型输出；可改为可导入辅助模块减少动态执行，但未证实命令注入入口。 |
| RS-020 | `services/api-gateway/internal/auth/cookies.go:10` | existing_control | Cookie 已显式设置 Secure: h.cookieSecure、HttpOnly:true、SameSiteLax。配置默认按环境开启 Secure，productionSecurityProblems 在生产类似环境拒绝 SessionCookieSecure=false（config.go:497）；规则未解析动态布尔值，不应误判为缺少 Secure。 |
| RS-021 | `services/api-gateway/internal/auth/cookies.go:27` | existing_control | Cookie 已显式设置 Secure: h.cookieSecure、HttpOnly:true、SameSiteLax。配置默认按环境开启 Secure，productionSecurityProblems 在生产类似环境拒绝 SessionCookieSecure=false（config.go:497）；规则未解析动态布尔值，不应误判为缺少 Secure。 |
| RS-022 | `services/api-gateway/internal/auth/cookies.go:54` | existing_control | Cookie 已显式设置 Secure: h.cookieSecure、HttpOnly:true、SameSiteLax。配置默认按环境开启 Secure，productionSecurityProblems 在生产类似环境拒绝 SessionCookieSecure=false（config.go:497）；规则未解析动态布尔值，不应误判为缺少 Secure。 |
| RS-023 | `services/api-gateway/internal/auth/cookies.go:62` | existing_control | Cookie 已显式设置 Secure: h.cookieSecure、HttpOnly:true、SameSiteLax。配置默认按环境开启 Secure，productionSecurityProblems 在生产类似环境拒绝 SessionCookieSecure=false（config.go:497）；规则未解析动态布尔值，不应误判为缺少 Secure。 |
| RS-024 | `services/api-gateway/internal/files/store_postgres.go:221` | existing_control | files.transition 只拼接程序生成的 $7、$8 等占位符与固定 assetColumns，tenant、id、revision、状态和错误详情通过 args... 绑定。来源状态来自代码常量；未将外部内容拼入 SQL 语法。 |
| RS-025 | `services/api-gateway/internal/modelgovernance/managed_config_probe.go:14` | existing_control | math/rand 仅用于 retryManagedReadOnlyRequest 的毫秒级重试抖动（managed_config_probe.go:330），未用于令牌、密码、加密密钥或租约随机值。非密码学调度随机性允许使用该包。 |
| RS-026 | `services/api-gateway/internal/score/store_postgres.go:100` | existing_control | score.ListExamGrades 拼接固定 WHERE 片段和由 len(args) 生成的占位符；Status、Query、Cursor、Limit 均单独绑定，tenant/exam 也在 WHERE 和 JOIN 中。未发现动态查询文本来自用户输入的 SQL 注入。 |
| RS-027 | `services/ocr-worker/Dockerfile:6` | existing_control | OCR Dockerfile:6 的 USER root 只用于安装依赖；文件末尾已切换 USER nobody，之后 CMD python -m ocr_worker。Semgrep 同时报告此 Dockerfile 的部分解析错误，不能把解析到的最后 root 当作真实最终用户。 |

## 原报告未进入全仓引擎扫描的文件

- `ai-services/tests/test_http.py`（已由46文件显式扫描补齐）
- `ai-services/tests/test_math_v2_production.py`（已由46文件显式扫描补齐）
- `services/api-gateway/internal/auth/mfa_http_test.go`（已由46文件显式扫描补齐）
- `services/api-gateway/internal/middleware/middleware_test.go`（已由46文件显式扫描补齐）
- `services/api-gateway/internal/minioadapter/client_test.go`（已由46文件显式扫描补齐）
- `services/api-gateway/internal/server/e2e_business_command_test.go`（已由46文件显式扫描补齐）
- `services/api-gateway/internal/server/e2e_compose_bootstrap_test.go`（已由46文件显式扫描补齐）
- `services/api-gateway/internal/server/e2e_mfa_test.go`（已由46文件显式扫描补齐）
- `services/api-gateway/internal/server/e2e_postgres_test.go`（已由46文件显式扫描补齐）
- `services/api-gateway/internal/server/e2e_question_bank_import_test.go`（已由46文件显式扫描补齐）
- `services/api-gateway/internal/server/e2e_question_bank_test.go`（已由46文件显式扫描补齐）
- `services/math-verification-worker/tests/test_timeout.py`（已由46文件显式扫描补齐）
- `services/page-processing-worker/tests/test_main.py`（已由46文件显式扫描补齐）
- `tests/e2e/story060_grading_agent.py`（已由46文件显式扫描补齐）

## 部分解析诊断

| 文件 | 起始行 | 类型 |
| --- | --- | --- |
| `.github/workflows/ci.yml` | 60 | PartialParsing |
| `.github/workflows/ci.yml` | 186 | PartialParsing |
| `apps/student-portal/src/App.reauthentication.test.tsx` | 10 | PartialParsing |
| `apps/web-admin/Dockerfile` | 23 | PartialParsing |
| `apps/web-admin/src/api/reports.batch48.test.ts` | 8 | PartialParsing |
| `apps/web-admin/src/components/ScoringExamples.ux.test.tsx` | 21, 31 | PartialParsing |
| `apps/web-admin/src/features/answer-sheet-template/answerSheetTemplatePage.batch7.test.tsx` | 118, 128, 229 | PartialParsing |
| `apps/web-admin/src/features/exams/management/ManagementExamArchiveDialog.test.tsx` | 11 | PartialParsing |
| `apps/web-admin/src/features/question-bank/QuestionBankACL.ux.test.tsx` | 18, 22, 25 | PartialParsing |
| `apps/web-admin/src/features/question-bank/QuestionBankPage.schemaRecovery.test.tsx` | 27, 33, 36 | PartialParsing |
| `apps/web-admin/src/features/question-bank/questionBank.complexity.batch50.test.tsx` | 29 | PartialParsing |
| `apps/web-admin/src/pages/ExamManagementPage.test.tsx` | 11 | PartialParsing |
| `apps/web-admin/src/pages/PaperRubricPage.batch42.test.tsx` | 20, 43 | PartialParsing |
| `apps/web-admin/src/pages/PaperRubricPage.test.tsx` | 18 | PartialParsing |
| `apps/web-admin/src/pages/PlatformModelConfigPage.test.tsx` | 29, 35 | PartialParsing |
| `services/api-gateway/tools/story056-postgres-e2e/Dockerfile` | 24, 33 | PartialParsing |
| `services/api-gateway/tools/story056-real-worker-e2e/Dockerfile` | 18 | PartialParsing |
| `services/ocr-worker/Dockerfile` | 29 | PartialParsing |

[机器结果](rescan-triage.json) · [全仓扫描元数据](rescan/scan-metadata.json) · [定向元数据](rescan-report-scope/scan-metadata.json)
