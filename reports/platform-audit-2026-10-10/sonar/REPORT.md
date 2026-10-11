# SonarQube 本地扫描详细结果

本次真实执行 SonarQube Community Build 26.9.0.129388 + SonarScanner CLI 8.1.0.6389。开始：2026-10-11T11:41:41.118535+08:00；导出：2026-10-11T12:21:23.729040+08:00。扫描目录名沿用 2026-10-10，实际执行时间以此处和原始日志为准。

## 执行完成与边界

Compute Engine 状态 `SUCCESS`，原始 issue 全量导出 `2223` 条，热点 `0` 条。本次 Scanner 已完成全部分析器、重复代码分析和官方报告目录生成，但进程在 ZIP 压缩期间中断，未获得标准 Scanner 退出成功与 report-task.txt。随后验证目录 `12029` 文件、`45721127` 字节，重新压缩并逐条核对 SHA-256 和 ZIP CRC，经同版本本机官方 `POST /api/ce/submit` 提交；平台最终成功处理，因此指标和问题是真实 SonarQube 结果，不能写作原始 Scanner 标准发布成功。恢复 ZIP SHA-256：`224793c52cf5476eafdaf51c052befbfab3493d33ba56d6111f26f5836545307`。只向 `http://127.0.0.1:19000` 本机已有 SonarQube 的独立项目 `yuejuan-local-workspace-20261010` 提交，未访问远端 GitHub，未上传 SonarCloud。原中断 token 和恢复用项目分析 token 均已撤销；旧项目、全局配置与旧凭据未修改。

当前本地维护文件范围来自 `../source-manifest-resume-2026-10-11.csv`（含未提交和未跟踪源码），自动盘点不等于人工审读。`source-scope.json` 记录输入范围，`effective-scan.properties` 记录真正使用的包含/排除规则。第三方依赖、lab 下载/模型/运行目录、构建缓存、reports/output 目录、生成 SDK、真实环境密钥、测试夹具/数据集排除；维护清单包含的 docs/architecture HTML/CSS、辅助脚本和 frontend-review-report HTML 仍在本次扫描范围内；正式测试文件作为测试输入分析。SQL/Shell/PowerShell/YAML 等不受本 Community Build 完整分析支持的文件即使在维护清单，也不能据此称已完成同等深度检查。

本次独立本地项目在平台处理结束后已确认可见性 `private`，记录时间 2026-10-11T12:21:36.737214+08:00；此前进行中的可见性变更被平台拒绝，原始错误保留在 `project-visibility-change-error.json`。仅调整本项目可见性，服务监听 `127.0.0.1:19000`。

## 平台指标

| 指标 | 实际值 |
| --- | --- |
| 非注释代码行 | 227136 |
| 平台计数文件（主要源码指标范围） | 1339 |
| Bug 候选 | 1653 |
| 漏洞候选 | 10 |
| 可维护性问题 | 560 |
| 安全热点 | 0 |
| 可维护性评级（1=A） | 1.0 |
| 可靠性评级（4=D） | 4.0 |
| 安全评级（3=C） | 3.0 |
| 修复工作量估算（分钟） | 6898 |
| 圈复杂度总和 | 58301 |
| 认知复杂度总和 | 36400 |
| 重复代码行比例（%） | 1.5 |
| 重复代码行 | 3821 |
| 重复代码块 | 310 |
| 平台覆盖率（未导入覆盖率，不代表实际测试覆盖率） | 0.0 |
| 分语言代码行 | css=10503;docker=249;go=125947;js=7462;py=19113;rust=4426;ts=58107;web=1329 |

平台指标中的 1339 个文件和 227136 行是主要源码指标范围，索引和传感器还包含测试输入及其他语言文件；不能把索引 2495、维护清单 2502 与这一指标直接相减后宣布遗漏。部分配置虽被识别语言，仍没有该语言的专用质量规则，见下方配置表。

1653 个 Bug 候选中，1645 条来自同一 CSS 通用字体回退规则 `css:S4649`，其余 8 条来自 TypeScript、JavaScript 或 HTML 规则。`docs/architecture` 共 1741 个问题，占全量问题约 78.3%；本次维护清单包含文档中的 HTML/CSS 和审查辅助脚本，不能把这些数字当作 1653 个已证实业务运行缺陷。也不能因 CSS 告警占多数而忽略其余安全与可维护性候选。

可维护性 A、可靠性 D、安全 C 是当前平台评级，保留平台原始数据，不是人工最终风险评级。6898 分钟约 115 小时，是规则默认修复工作量的估算，不是经项目排期核实的工时承诺。

没有导入覆盖率文件，因此本报告不对真实测试覆盖率作零覆盖或达标判断；不执行测试也不能判断阅卷评分准确率、跨校访问是否绝对安全或并发正确性。

## 质量门禁

```json
{
  "status": "OK",
  "conditions": [],
  "ignoredConditions": false,
  "caycStatus": "compliant"
}
```

本次返回 `OK`，但 `conditions=[]`，没有逐条件的评估记录。默认质量门禁主要用于新代码；首次本地扫描没有可比历史分支基线。本结果不能证明全仓质量条件全部达标，更不能抵消当前可靠性 D、安全 C 和未复核候选。


只读查询确认本项目使用内置默认 `Sonar way`，其定义包含以下 4 个新代码条件；定义与本次评估响应分别保存在 `quality-gate-definition.json` 和 `quality-gate.json`。

| 新代码指标 | 失败条件 |
| --- | --- |
| `new_violations` | `GT 0` |
| `new_coverage` | `LT 80` |
| `new_duplicated_lines_density` | `GT 3` |
| `new_security_hotspots_reviewed` | `LT 100` |

`GT` 表示大于，`LT` 表示小于。条件定义并不等于本次有可计算的新代码指标：本次响应没有任何条件条目，因此不能把 0.0% 总体覆盖率与 80% 新代码覆盖率门禁直接比较后宣布门禁有效通过。

## 问题分布

| 级别 | 数量 |
| --- | --- |
| MAJOR | 1829 |
| MINOR | 164 |
| CRITICAL | 230 |

| 类型 | 数量 |
| --- | --- |
| CODE_SMELL | 560 |
| VULNERABILITY | 10 |
| BUG | 1653 |

| 模块 | 数量 |
| --- | --- |
| `docs/architecture` | 1741 |
| `services/api-gateway` | 209 |
| `apps/web-admin` | 102 |
| `ai-services/tests` | 48 |
| `services/ocr-worker` | 33 |
| `services/page-processing-worker` | 22 |
| `tests/e2e` | 19 |
| `apps/desktop-client` | 10 |
| `apps/student-portal` | 9 |
| `ai-services/grading_agent` | 6 |
| `scripts/generate-fujian-2024-math-simulation.py` | 6 |
| `frontend-review-report/前端代码审查报告.html` | 3 |
| `lab/evals` | 3 |
| `scripts/check-story049-ocr-worker.mjs` | 2 |
| `tests/ai-evaluation` | 2 |
| `frontend-review-report/前端UI优化方案-对标成熟产品.html` | 1 |
| `frontend-review-report/前端UI设计分析.html` | 1 |
| `lab/scripts` | 1 |
| `scripts/check-story052-production-deployment.mjs` | 1 |
| `scripts/ci` | 1 |
| `services/image-quality-worker` | 1 |
| `services/math-verification-worker` | 1 |
| `services/subjective-grading-worker` | 1 |

## 高频规则

| 规则 | 数量 |
| --- | --- |
| `css:S4649` | 1645 |
| `go:S3776` | 201 |
| `python:S5778` | 79 |
| `typescript:S1874` | 65 |
| `python:S7498` | 23 |
| `typescript:S6582` | 19 |
| `python:S9073` | 18 |
| `python:S125` | 15 |
| `python:S3358` | 15 |
| `python:S3776` | 14 |
| `typescript:S6551` | 11 |
| `typescript:S9332` | 10 |
| `typescript:S5906` | 8 |
| `go:S107` | 8 |
| `javascript:S6594` | 7 |
| `python:S1192` | 7 |
| `typescript:S8783` | 7 |
| `python:S2245` | 6 |
| `typescript:S8980` | 5 |
| `javascript:S3776` | 5 |
| `javascript:S6582` | 4 |
| `python:S5332` | 3 |
| `javascript:S1874` | 3 |
| `javascript:S7766` | 3 |
| `javascript:S2681` | 3 |

## 高复杂度文件

以下为整文件复杂度总和，不是单函数复杂度，也不等于功能缺陷。多个短函数组成的文件仍可能总分较高；宜结合职责、事务边界、权限和测试分析，不只按行数或总分重构。Go 201 条 `go:S3776` 是真正的单函数认知复杂度规则命中，具体函数位置见完整问题表。

| 文件 | 代码行 | 圈复杂度 | 认知复杂度 |
| --- | --- | --- | --- |
| `apps/student-portal/src/App.tsx` | 971 | 517 | 428 |
| `services/api-gateway/internal/auth/store_memory.go` | 1164 | 441 | 355 |
| `services/api-gateway/internal/paper/paper_import_reconcile.go` | 1074 | 383 | 337 |
| `services/api-gateway/internal/paper/paper_import_runtime_postgres.go` | 1155 | 407 | 288 |
| `services/api-gateway/internal/review/store_memory.go` | 1207 | 337 | 276 |
| `apps/desktop-client/src/components/OfflineWorkbench.tsx` | 1048 | 502 | 268 |
| `lab/src/schemas/gradingSchema.js` | 387 | 203 | 202 |
| `services/api-gateway/internal/capture/store_memory.go` | 735 | 223 | 195 |
| `docs/architecture/tools/extract-frontend-api-recheck.mjs` | 193 | 175 | 191 |
| `docs/architecture/evidence/extract-frontend-api.mjs` | 193 | 175 | 191 |
| `services/api-gateway/internal/modelgovernance/store_memory.go` | 833 | 230 | 191 |
| `docs/architecture/tools/extract_database_catalog.py` | 140 | 84 | 186 |
| `services/api-gateway/internal/paper/store_memory.go` | 762 | 233 | 184 |
| `apps/web-admin/src/features/grading/workbench/hooks/useGradingQueue.ts` | 348 | 218 | 184 |
| `apps/web-admin/src/pages/PlatformModelConfigPage.tsx` | 737 | 254 | 182 |
| `services/api-gateway/tools/import-fujian-2024-math-simulation/main.go` | 1896 | 322 | 172 |
| `services/api-gateway/internal/score/store_memory.go` | 784 | 191 | 170 |
| `services/api-gateway/internal/modelcalibration/service.go` | 549 | 227 | 166 |
| `ai-services/grading_agent/paper_compact/anchors.py` | 538 | 171 | 164 |
| `services/api-gateway/internal/gradingevaluation/service.go` | 537 | 243 | 160 |
| `apps/web-admin/src/features/paper-import/usePaperImportWorkflow.ts` | 568 | 230 | 153 |
| `apps/desktop-client/src/features/session/useDesktopSession.ts` | 367 | 120 | 152 |
| `services/api-gateway/internal/paper/paper_import_postgres.go` | 1001 | 285 | 150 |
| `services/api-gateway/internal/subjective/store_memory.go` | 470 | 163 | 150 |
| `apps/desktop-client/src/features/scan-queue/useScanQueue.ts` | 373 | 198 | 150 |
| `services/api-gateway/internal/review/handlers.go` | 898 | 230 | 149 |
| `services/api-gateway/internal/scorerelease/store_postgres.go` | 1221 | 262 | 146 |
| `ai-services/grading_agent/contract.py` | 464 | 165 | 144 |
| `services/api-gateway/internal/regrade/store_memory.go` | 588 | 176 | 142 |
| `services/api-gateway/internal/mathunderstanding/rubric.go` | 377 | 149 | 141 |

## 重点候选问题与定位

以下保留平台消息，尚未逐项人工确认为可达生产缺陷。命中测试代码、既有保护或平台误报时应单独核实。完整清单见 `issues.csv` 和 `issues.json`。

| 类型/级别 | 规则 | 文件:行 | 平台消息 |
| --- | --- | --- | --- |
| VULNERABILITY/MAJOR | `python:S2245` | `scripts/generate-fujian-2024-math-simulation.py:270` | Make sure that using this pseudorandom number generator is safe here. |
| VULNERABILITY/MAJOR | `python:S2245` | `scripts/generate-fujian-2024-math-simulation.py:270` | Make sure that using this pseudorandom number generator is safe here. |
| VULNERABILITY/MAJOR | `python:S2245` | `scripts/generate-fujian-2024-math-simulation.py:270` | Make sure that using this pseudorandom number generator is safe here. |
| VULNERABILITY/MAJOR | `python:S2245` | `scripts/generate-fujian-2024-math-simulation.py:328` | Make sure that using this pseudorandom number generator is safe here. |
| VULNERABILITY/MAJOR | `python:S2245` | `scripts/generate-fujian-2024-math-simulation.py:448` | Make sure that using this pseudorandom number generator is safe here. |
| VULNERABILITY/MAJOR | `python:S2245` | `scripts/generate-fujian-2024-math-simulation.py:449` | Make sure that using this pseudorandom number generator is safe here. |
| VULNERABILITY/MINOR | `python:S5332` | `ai-services/grading_agent/server.py:291` | Using HTTP protocol is insecure. Use HTTPS instead. |
| VULNERABILITY/MINOR | `python:S5332` | `docs/architecture/tools/preview_server.py:36` | Using HTTP protocol is insecure. Use HTTPS instead. |
| VULNERABILITY/MINOR | `Web:S5725` | `frontend-review-report/前端代码审查报告.html:558` | Add integrity and crossorigin="anonymous" attributes to this element to enforce integrity checks. |
| VULNERABILITY/MINOR | `python:S5332` | `services/math-verification-worker/math_verification_worker/server.py:186` | Using HTTP protocol is insecure. Use HTTPS instead. |
| BUG/CRITICAL | `javascript:S2871` | `scripts/check-story052-production-deployment.mjs:70` | Provide a compare function to avoid sorting elements alphabetically. |
| BUG/CRITICAL | `javascript:S2871` | `scripts/ci/schema-version.mjs:18` | Provide a compare function that depends on "String.localeCompare", to reliably sort elements alphabetically. |
| BUG/MAJOR | `typescript:S6544` | `apps/web-admin/src/components/ResponsiveTable.tsx:93` | Expected a non-Promise value to be returned. |
| BUG/MAJOR | `typescript:S7727` | `apps/web-admin/src/features/grading/workbench/reviewContext.ts:37` | Do not pass function `normalizeRubricPoint` directly to `.map(…)`. |
| BUG/MAJOR | `Web:InputWithoutLabelCheck` | `docs/architecture/previews/index.html:3` | Associate a valid label to this input field. |
| BUG/MAJOR | `javascript:S905` | `docs/architecture/tools/capture_preview_qa.js:2` | Expected an assignment or function call and instead saw an expression. |
| BUG/MAJOR | `javascript:S905` | `docs/architecture/tools/render_native.js:2` | Expected an assignment or function call and instead saw an expression. |
| BUG/MAJOR | `javascript:S7727` | `lab/scripts/govern-dataset.js:76` | Do not pass function `questionGroupKey` directly to `.map(…)`. |
| CODE_SMELL/CRITICAL | `javascript:S3776` | `docs/architecture/evidence/extract-frontend-api.mjs:62` | Refactor this function to reduce its Cognitive Complexity from 18 to the 15 allowed. |
| CODE_SMELL/CRITICAL | `javascript:S3776` | `docs/architecture/evidence/extract-frontend-api.mjs:85` | Refactor this function to reduce its Cognitive Complexity from 69 to the 15 allowed. |
| CODE_SMELL/CRITICAL | `python:S3776` | `docs/architecture/tools/build_database_specs.py:53` | Refactor this function to reduce its Cognitive Complexity from 27 to the 15 allowed. |
| CODE_SMELL/CRITICAL | `python:S1192` | `docs/architecture/tools/build_diagrams.py:28` | Define a constant instead of duplicating this literal '</div>' 3 times. |
| CODE_SMELL/CRITICAL | `python:S3776` | `docs/architecture/tools/build_diagrams.py:62` | Refactor this function to reduce its Cognitive Complexity from 82 to the 15 allowed. |
| CODE_SMELL/CRITICAL | `python:S1192` | `docs/architecture/tools/build_diagrams.py:96` | Define a constant instead of duplicating this literal "dashed=1;" 3 times. |
| CODE_SMELL/CRITICAL | `python:S3776` | `docs/architecture/tools/build_module_catalog.py:12` | Refactor this function to reduce its Cognitive Complexity from 29 to the 15 allowed. |
| CODE_SMELL/CRITICAL | `javascript:S3776` | `docs/architecture/tools/extract-frontend-api-recheck.mjs:62` | Refactor this function to reduce its Cognitive Complexity from 18 to the 15 allowed. |
| CODE_SMELL/CRITICAL | `javascript:S3776` | `docs/architecture/tools/extract-frontend-api-recheck.mjs:85` | Refactor this function to reduce its Cognitive Complexity from 69 to the 15 allowed. |
| CODE_SMELL/CRITICAL | `python:S3776` | `docs/architecture/tools/extract_database_catalog.py:11` | Refactor this function to reduce its Cognitive Complexity from 16 to the 15 allowed. |
| CODE_SMELL/CRITICAL | `python:S1192` | `docs/architecture/tools/extract_database_catalog.py:68` | Define a constant instead of duplicating this literal 'CONSTRAINT ' 4 times. |
| CODE_SMELL/CRITICAL | `python:S3776` | `docs/architecture/tools/merge_review_ledger.py:23` | Refactor this function to reduce its Cognitive Complexity from 52 to the 15 allowed. |
| CODE_SMELL/CRITICAL | `python:S1186` | `docs/architecture/tools/preview_server.py:13` | Add a nested comment explaining why this method is empty, or complete the implementation. |
| CODE_SMELL/CRITICAL | `javascript:S3776` | `docs/architecture/tools/render_native.js:28` | Refactor this function to reduce its Cognitive Complexity from 26 to the 15 allowed. |
| CODE_SMELL/CRITICAL | `python:S3776` | `docs/architecture/tools/route_obstructions.py:21` | Refactor this function to reduce its Cognitive Complexity from 50 to the 15 allowed. |
| CODE_SMELL/CRITICAL | `python:S3776` | `docs/architecture/tools/scan_repository.py:10` | Refactor this function to reduce its Cognitive Complexity from 27 to the 15 allowed. |
| CODE_SMELL/CRITICAL | `python:S1192` | `docs/architecture/tools/simplify_diagrams.py:25` | Define a constant instead of duplicating this literal '本地 / 外部模型' 3 times. |
| CODE_SMELL/CRITICAL | `python:S3776` | `docs/architecture/tools/simplify_diagrams.py:54` | Refactor this function to reduce its Cognitive Complexity from 25 to the 15 allowed. |
| CODE_SMELL/CRITICAL | `python:S3776` | `docs/architecture/tools/simplify_diagrams.py:81` | Refactor this function to reduce its Cognitive Complexity from 64 to the 15 allowed. |
| CODE_SMELL/CRITICAL | `python:S3776` | `docs/architecture/tools/verify_delivery.py:22` | Refactor this function to reduce its Cognitive Complexity from 41 to the 15 allowed. |
| CODE_SMELL/CRITICAL | `python:S1192` | `docs/architecture/tools/write_frontend_audit.py:39` | Define a constant instead of duplicating this literal "ModulePage；模拟路由占位" 3 times. |
| CODE_SMELL/CRITICAL | `python:S3776` | `docs/architecture/tools/write_frontend_audit.py:132` | Refactor this function to reduce its Cognitive Complexity from 53 to the 15 allowed. |
| CODE_SMELL/CRITICAL | `python:S3776` | `docs/architecture/tools/write_frontend_specs.py:81` | Refactor this function to reduce its Cognitive Complexity from 19 to the 15 allowed. |
| CODE_SMELL/CRITICAL | `python:S1192` | `docs/architecture/tools/write_frontend_specs.py:145` | Define a constant instead of duplicating this literal "services/api-gateway/internal/server/router_guards.go" 9 times. |
| CODE_SMELL/CRITICAL | `python:S1192` | `docs/architecture/tools/write_frontend_specs.py:158` | Define a constant instead of duplicating this literal "services/api-gateway/internal/files/handlers.go" 4 times. |
| CODE_SMELL/CRITICAL | `python:S3776` | `docs/architecture/tools/write_root_review.py:56` | Refactor this function to reduce its Cognitive Complexity from 32 to the 15 allowed. |
| CODE_SMELL/CRITICAL | `python:S3776` | `docs/architecture/tools/write_root_specs.py:55` | Refactor this function to reduce its Cognitive Complexity from 24 to the 15 allowed. |
| CODE_SMELL/CRITICAL | `go:S3776` | `services/api-gateway/cmd/reliability-repair/main_batch50_test.go:159` | Refactor this method to reduce its Cognitive Complexity from 16 to the 15 allowed. |
| CODE_SMELL/CRITICAL | `go:S3776` | `services/api-gateway/cmd/reliability-repair/main_batch50_test.go:245` | Refactor this method to reduce its Cognitive Complexity from 24 to the 15 allowed. |
| CODE_SMELL/CRITICAL | `go:S3776` | `services/api-gateway/cmd/reliability-repair/main_batch51_coverage_test.go:105` | Refactor this method to reduce its Cognitive Complexity from 17 to the 15 allowed. |
| CODE_SMELL/CRITICAL | `go:S3776` | `services/api-gateway/cmd/reliability-repair/main_batch51_coverage_test.go:141` | Refactor this method to reduce its Cognitive Complexity from 16 to the 15 allowed. |
| CODE_SMELL/CRITICAL | `go:S3776` | `services/api-gateway/cmd/reliability-repair/main_batch51_coverage_test.go:190` | Refactor this method to reduce its Cognitive Complexity from 43 to the 15 allowed. |
| CODE_SMELL/CRITICAL | `go:S3776` | `services/api-gateway/cmd/reliability-repair/main_batch51_coverage_test.go:327` | Refactor this method to reduce its Cognitive Complexity from 33 to the 15 allowed. |
| CODE_SMELL/CRITICAL | `go:S3776` | `services/api-gateway/cmd/reliability-repair/main_batch51_coverage_test.go:373` | Refactor this method to reduce its Cognitive Complexity from 21 to the 15 allowed. |
| CODE_SMELL/CRITICAL | `go:S3776` | `services/api-gateway/internal/answergroup/store_test.go:46` | Refactor this method to reduce its Cognitive Complexity from 18 to the 15 allowed. |
| CODE_SMELL/CRITICAL | `go:S3776` | `services/api-gateway/internal/apicontract/a14_a24_openapi_test.go:11` | Refactor this method to reduce its Cognitive Complexity from 17 to the 15 allowed. |
| CODE_SMELL/CRITICAL | `go:S3776` | `services/api-gateway/internal/apicontract/exam_command_handler_schema_test.go:108` | Refactor this method to reduce its Cognitive Complexity from 46 to the 15 allowed. |
| CODE_SMELL/CRITICAL | `go:S3776` | `services/api-gateway/internal/apicontract/math_understanding_openapi_test.go:11` | Refactor this method to reduce its Cognitive Complexity from 44 to the 15 allowed. |
| CODE_SMELL/CRITICAL | `go:S3776` | `services/api-gateway/internal/apicontract/platform_schools_openapi_test.go:10` | Refactor this method to reduce its Cognitive Complexity from 16 to the 15 allowed. |
| CODE_SMELL/CRITICAL | `go:S3776` | `services/api-gateway/internal/appeal/handlers_test.go:82` | Refactor this method to reduce its Cognitive Complexity from 22 to the 15 allowed. |
| CODE_SMELL/CRITICAL | `go:S3776` | `services/api-gateway/internal/appeal/published_question_opinion_test.go:23` | Refactor this method to reduce its Cognitive Complexity from 24 to the 15 allowed. |
| BUG/MAJOR | `css:S4649` | `docs/architecture/previews/index.html:3` | Missing generic font family |

## 安全热点

热点表示需要人工判断安全上下文，不是已确认漏洞。`hotspots.json` 含全部原始详情、规则说明、位置和状态。

| 优先级 | 类别 | 文件:行 | 消息 |
| --- | --- | --- | --- |

## 本次分析器与规则配置

以下版本直接取自本机平台 `plugins.json`。

| 分析器 | 版本 |
| --- | --- |
| Go Code Quality and Security | `1.43.0.7704` |
| HTML Code Quality and Security | `3.31 (build 8334)` |
| IaC Code Quality and Security | `2.16.0.22905` |
| JavaScript/TypeScript/CSS Code Quality and Security | `13.8 (build 44569)` |
| Python Code Quality and Security | `5.31 (build 36502)` |
| Rust Code Quality and Security | `1.8.1.3395` |
| Text Code Quality and Security | `2.49.0.12346` |

扫描日志实际选用以下 10 个语言的内置 Sonar way 配置；`quality-profiles.json` 还包含服务器安装的其他语言配置，不能把其他语言配置计作本次已执行规则。触发的规则类型为 51，不等于本次仅运行 51 条规则。

| 语言 | 配置 | 启用规则数 | 规则更新时间（平台原值） |
| --- | --- | --- | --- |
| `css` | Sonar way | 40 | `2026-10-02T11:33:23+0000` |
| `docker` | Sonar way | 25 | `2026-10-02T11:33:27+0000` |
| `go` | Sonar way | 22 | `2026-10-02T11:33:36+0000` |
| `js` | Sonar way | 420 | `2026-10-02T11:33:25+0000` |
| `json` | Sonar way | 0 | `2026-10-02T11:33:32+0000` |
| `py` | Sonar way | 398 | `2026-10-02T11:33:26+0000` |
| `rust` | Sonar way | 78 | `2026-10-02T11:33:28+0000` |
| `ts` | Sonar way | 435 | `2026-10-02T11:33:41+0000` |
| `web` | Sonar way | 61 | `2026-10-02T11:33:30+0000` |
| `yaml` | Sonar way | 0 | `2026-10-02T11:33:36+0000` |

JSON/YAML 配置启用规则数为 0；文本/密钥扫描及适用的 IaC 传感器可能另行处理文件，但不能宣称已对所有 JSON/YAML 配置完成专用语义安全检查。

## 本次配置与重现

Scanner 引擎堆上限 1536 MiB，Node 堆设置 2048 MiB，ActiveProcessorCount=2。TypeScript 配置只使用 `apps/desktop-client/tsconfig.json`、`apps/student-portal/tsconfig.json`、`apps/web-admin/tsconfig.json`、`packages/sdk/tsconfig.json`，避免默认发现 output 和其他工作树的历史配置。

```powershell
python reports/platform-audit-2026-10-10/sonar/run_local_sonar.py
python reports/platform-audit-2026-10-10/sonar/fingerprint_after.py
python reports/platform-audit-2026-10-10/sonar/summarize_results.py
```

重现依赖本机现有 SonarQube 26.9 服务和本地凭据文件；凭据内容不在报告中。独立 19100 新实例启动失败后已自行退出，本次不停止其他会话的 19000 服务。恢复脚本 `resume_submission.py` 处理已有 Scanner 报告目录；有本次接收回执时只等待/导出，不重复提交分析。

第 2 次完整 Scanner 通过 Go/Python/Rust/Text，但默认发现 22 个 TypeScript 配置时 Node 内存上限 960 MiB 耗尽；`scanner-attempt2.log` 是失败证据。该尝试未提交分析，不能计入已完成问题数量。之后采用 4 个真实配置并将 Node 设为 2048 MiB。

## 扫描期间本地内容变化

本次 Scanner 在开始至服务器接收报告的区间内读取 live 工作区。输入清单仅用于盘点和限定范围，未冻结内容。扫描后 2026-10-11T12:07:15.242302+08:00 再次计算哈希，与恢复盘点相比 23 文件内容变化、0 文件缺失；其他本地任务的改动未回滚。`source-manifest-after-scan.csv` 和 `source-drift-after-scan.json` 保存具体哈希和路径。

扫描后哈希不能证明每个文件在 Scanner 实际读取时的内容，因此本次结果不能声称与 Semgrep/CodeQL 使用完全相同冻结快照；关键问题定位仍需按当前代码核实。

| 变化文件 |
| --- |
| `.github/workflows/ci.yml` |
| `ai-services/grading_agent/config.py` |
| `ai-services/grading_agent/server.py` |
| `ai-services/tests/test_config.py` |
| `ai-services/tests/test_server_tls.py` |
| `apps/desktop-client/src/components/OfflineWorkbench.test.tsx` |
| `apps/desktop-client/src/components/OfflineWorkbench.tsx` |
| `docs/architecture/previews/index.html` |
| `docs/architecture/tools/simplify_diagrams.py` |
| `docs/architecture/tools/verify_delivery.py` |
| `infra/docker-compose/docker-compose.story060-e2e.yml` |
| `infra/docker-compose/docker-compose.yml` |
| `infra/docker-compose/scripts/preflight.ps1` |
| `scripts/ci/trusted-executable.mjs` |
| `scripts/ci/trusted-executable.test.mjs` |
| `services/api-gateway/internal/releasegate/service.go` |
| `services/api-gateway/tools/story056-postgres-e2e/Dockerfile` |
| `services/math-verification-worker/Dockerfile` |
| `services/math-verification-worker/math_verification_worker/server.py` |
| `services/math-verification-worker/tests/test_server.py` |
| `services/ocr-worker/ocr_worker/config.py` |
| `services/ocr-worker/tests/test_config.py` |
| `tests/e2e/story060_grading_agent.py` |

## 分析器局限与执行警告

维护清单 2502 个文件，官方预处理 2498、索引 2495，识别 10 种语言。JS/TS/CSS 传感器记录 1015/1015；其中 JS/TS 缓存统计为 954 个，不能把两者混为同一数量。Text/Secrets 2293/2293、Docker 10/10、重复代码计算 1200 个文件。不同传感器可能处理同一文件，不能相加当作文件总数。

Python 日志记录 182/182，但明确警告 `services/ocr-worker/tests/test_config.py` 因行偏移异常无法分析，不能称全部 Python 文件检查无遗漏。该文件在扫描后哈希与盘点不一致，期间工作区变化可能相关；现有证据不足以确定根因。Rust `queue.rs` 与 `spool.rs` 的注释高亮偏移异常影响高亮，不能直接归类为业务缺陷。根目录没有 Cargo manifest，Clippy 传感器明确跳过；因此未完成 Rust 编译器/Clippy 检查。

以下来自最后完成分析器的原始日志。

```text
11:44:00.586 WARN  No Cargo manifest found, skipping Clippy analysis
11:44:02.807 ERROR Invalid highlighting: HighlightTokens[tokenType=COMMENT, location=Location[startLine=18, startColumn=4, endLine=18, endColumn=33]]. Reason: 33 is not a valid line offset for pointer. File apps/desktop-client/src-tauri/src/durable_store/queue.rs has 32 character(s) at line 18
11:44:02.808 ERROR Invalid highlighting: HighlightTokens[tokenType=COMMENT, location=Location[startLine=143, startColumn=4, endLine=143, endColumn=43]]. Reason: 43 is not a valid line offset for pointer. File apps/desktop-client/src-tauri/src/durable_store/queue.rs has 42 character(s) at line 143
11:44:02.808 ERROR Invalid highlighting: HighlightTokens[tokenType=COMMENT, location=Location[startLine=21, startColumn=4, endLine=21, endColumn=31]]. Reason: 31 is not a valid line offset for pointer. File apps/desktop-client/src-tauri/src/durable_store/queue.rs has 30 character(s) at line 21
11:44:02.809 ERROR Invalid highlighting: HighlightTokens[tokenType=COMMENT, location=Location[startLine=19, startColumn=4, endLine=19, endColumn=51]]. Reason: 51 is not a valid line offset for pointer. File apps/desktop-client/src-tauri/src/durable_store/queue.rs has 50 character(s) at line 19
11:44:02.809 ERROR Invalid highlighting: HighlightTokens[tokenType=COMMENT, location=Location[startLine=80, startColumn=4, endLine=80, endColumn=45]]. Reason: 45 is not a valid line offset for pointer. File apps/desktop-client/src-tauri/src/durable_store/queue.rs has 44 character(s) at line 80
11:44:02.810 ERROR Invalid highlighting: HighlightTokens[tokenType=COMMENT, location=Location[startLine=20, startColumn=4, endLine=20, endColumn=31]]. Reason: 31 is not a valid line offset for pointer. File apps/desktop-client/src-tauri/src/durable_store/queue.rs has 30 character(s) at line 20
11:44:02.810 ERROR Invalid highlighting: HighlightTokens[tokenType=COMMENT, location=Location[startLine=216, startColumn=4, endLine=216, endColumn=37]]. Reason: 37 is not a valid line offset for pointer. File apps/desktop-client/src-tauri/src/durable_store/queue.rs has 36 character(s) at line 216
11:44:02.810 ERROR Invalid highlighting: HighlightTokens[tokenType=COMMENT, location=Location[startLine=17, startColumn=4, endLine=17, endColumn=35]]. Reason: 35 is not a valid line offset for pointer. File apps/desktop-client/src-tauri/src/durable_store/queue.rs has 34 character(s) at line 17
11:44:02.895 ERROR Invalid highlighting: HighlightTokens[tokenType=COMMENT, location=Location[startLine=37, startColumn=4, endLine=37, endColumn=43]]. Reason: 43 is not a valid line offset for pointer. File apps/desktop-client/src-tauri/src/durable_store/spool.rs has 42 character(s) at line 37
11:44:02.896 ERROR Invalid highlighting: HighlightTokens[tokenType=COMMENT, location=Location[startLine=343, startColumn=8, endLine=343, endColumn=32]]. Reason: 32 is not a valid line offset for pointer. File apps/desktop-client/src-tauri/src/durable_store/spool.rs has 31 character(s) at line 343
11:44:02.897 ERROR Invalid highlighting: HighlightTokens[tokenType=COMMENT, location=Location[startLine=180, startColumn=4, endLine=180, endColumn=46]]. Reason: 46 is not a valid line offset for pointer. File apps/desktop-client/src-tauri/src/durable_store/spool.rs has 45 character(s) at line 180
11:44:02.897 ERROR Invalid highlighting: HighlightTokens[tokenType=COMMENT, location=Location[startLine=342, startColumn=8, endLine=342, endColumn=33]]. Reason: 33 is not a valid line offset for pointer. File apps/desktop-client/src-tauri/src/durable_store/spool.rs has 32 character(s) at line 342
11:44:02.898 ERROR Invalid highlighting: HighlightTokens[tokenType=COMMENT, location=Location[startLine=236, startColumn=4, endLine=236, endColumn=44]]. Reason: 44 is not a valid line offset for pointer. File apps/desktop-client/src-tauri/src/durable_store/spool.rs has 43 character(s) at line 236
11:45:25.733 WARN  Unable to analyze file: services/ocr-worker/tests/test_config.py
```

## 原始证据与重现

- `scanner.log`：官方 Scanner 日志，全部语言分析器结束，最终停在报告生成后压缩阶段。
- `scanner-command.json`、`effective-scan.properties`：真实命令、资源上限、范围；token 值不输出。
- `compute-engine-task.json`、`recovery-submission.json`：官方本机接口接收和计算成功证据；原始运行没有新 report-task.txt。
- `recovery-integrity.json`、`official-submit-api.json`、`resume_submission.py`：官方接口定义、重新压缩 ZIP 与全部条目哈希、恢复过程。
- `metrics.json`、`quality-gate.json`、`issues-*-page-*.json`、`hotspots-page-*.json`：平台 API 分页原始响应。
- `plugins.json`、`quality-profiles.json`、`languages.json`、`rules.json`：本次规则版本和配置依据。
- `issues.csv`、`summary.json`：全量问题表及程序可读摘要。

Docker 官方服务器镜像首次下载发生 EOF；随后独立原生实例启动发现 H2 默认端口 9092 与已有实例冲突。本次最终改用已有 localhost 实例中的独立新项目完成分析；`pull-server.log`、`server-native.*.log` 是失败路径证据，不能作为最终分析成功证据。第一次 Scanner 使用 384 MiB 堆，在 Go auth 包转换 AST 时发生 Java heap space；`scanner-attempt1.log` 保留失败证据。随后提高为 1536 MiB 重新完整执行，最终状态以上方报告恢复和 Compute Engine 证据为准。

本次只扫描并写审计产物，不自动修复或改动业务代码。扫描并非全文件人工评审；所有未核实文件和问题仍为 pending。

## Compute Engine 官方任务警告

本次 task API 返回 2 条警告；以下保留完整原值。平台成功处理不保证所有文件高亮、符号均完整。

```text
Inconsistent highlighting data detected on some files (2 in total). File source may have been modified while analysis was running.
   ° docs/architecture/tools/verify_delivery.py
   ° services/api-gateway/internal/releasegate/service.go
Inconsistent symbol data detected on file 'docs/architecture/tools/verify_delivery.py'. File source may have been modified while analysis was running.
```

## Compute Engine 执行警告

平台成功处理仍可能带有局部限制；以下只截取本次 task 的后台警告。符号数据不一致时该文件的符号会被忽略，不能因此称该文件完成全部功能检查。

```text
2026.10.11 12:18:19 WARN  ce[14bfcea9-251b-472e-bc01-1ece81c736f0][o.s.c.t.p.s.l.SymbolsLineReader] Inconsistency detected in Symbols data. Symbols will be ignored for file 'yuejuan-local-workspace-20261010:docs/architecture/tools/verify_delivery.py'
```

高复杂度前三文件的局部职责核对见 [complexity-context-review.md](complexity-context-review.md)。该核对只覆盖明确列出的行段，文件级审读状态仍为 pending。

10 个 VULNERABILITY、8 个非 CSS BUG 和 4 个 CSS 代表的人工结论见 [finding-review.md](finding-review.md)，逐项证据及未审 2201 条清单见 [finding-review.json](finding-review.json)。该附录使用本机平台 sources/raw 保存的实际分析源码，记录独立哈希；其余告警仍为 pending。
