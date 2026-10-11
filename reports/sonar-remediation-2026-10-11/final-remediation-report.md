# SonarQube 本地工作区修复与最终复扫报告

交付生成时间：2026-10-11T19:55:07.627710+08:00。本次仅处理和扫描 `D:/project/yuejuan` 本地工作区；最终分析 `07c1210c-b44c-49f6-a3fe-c2bb211a0ce3`，项目 `yuejuan-local-workspace-20261010`。没有扫描远端 GitHub，也没有向云端上传、提交或推送代码。

原始 2223 项全部获得明确平台状态：2192 项由源码修复后分析器标为 FIXED，30 项经逐项证据审查标为 FALSE_POSITIVE，1 项保留明确的本机预览 HTTP 例外。

复扫新增 47 项也全部处理：43 项 FIXED，4 项 FALSE_POSITIVE。合计跟踪 2270 个唯一问题，当前未处置告警 0 项，安全热点 0 项。人工误报及接受事项没有计入源码修复数量。

**代码告警已全部处理，但质量门禁仍需按下面的实际状态解释；静态告警归零不能代表业务评测、覆盖率和集成测试全部达标。**

## 1. 扫描范围与证据完整性

- 最终 Scanner 实际退出 0；本地 Compute Engine 为 SUCCESS；导出完整。最终检查使用 full-checkpoint-04，之前失败及中间检查点全部保留。
- 使用官方 SonarQube Community Build 26.9.0.129388 与 SonarScanner CLI 8.1.0.6389，沿用原本机平台和独立私有审计项目。
- 冻结维护文件 2654 个，原问题涉及的 291 个文件全部包含；Sonar 生产文件度量为 1349 个、生产代码 227943 行。测试、配置、支持文件和生产度量的口径不同。
- 当前维护文件及类型解析支持文件与最终冻结输入逐字节一致；原排除策略、质量配置、活跃规则数量和规则更新时间均保持一致。没有用排除文件、禁用规则或降低门槛消除告警。
- 原始 issues.json 的 SHA256 与接手基线相同。原有未提交修改保留；额外已有改动按当前哈希复核，没有回滚其它工作。
- 自动文件清单不表示完整人工审读。各分工按实际审读范围记录；只读过局部或尚未审读的文件仍为 pending。
- 维护清单中的 SQL、Shell、PowerShell 和 YAML 等不表示获得与 Go/Python/TypeScript 相同的专用质量分析深度。依赖分析在本轮日志明确为 skipped，没有把这轮 Sonar 扫描当作完整依赖漏洞审计。
- 原始问题及新增问题均逐 key 对账，规则、项目和文件身份完整一致。详见 [最终平台与源码审计](D:/project/yuejuan/reports/sonar-remediation-2026-10-11/full-checkpoint-04/final-audit.json)、[冻结源码 SHA 清单](D:/project/yuejuan/reports/sonar-remediation-2026-10-11/full-checkpoint-04/source-scope.json)。

另一智能体独立核对全部 2270 个问题、2654 份维护源码和 2 份类型支持文件，检查无缺漏、无身份或严重度漂移，35 项人工处置理由及哈希均吻合；详见 [最终独立审计](D:/project/yuejuan/reports/sonar-remediation-2026-10-11/delivery-independent-checkpoint04-final-platform-audit-python-agent.md)。

## 2. 原始问题按语言的最终结果

| 规则语言 | 原始数 | FIXED | FALSE_POSITIVE | ACCEPTED | OPEN |
| --- | ---: | ---: | ---: | ---: | ---: |
| Web | 2 | 2 | 0 | 0 | 0 |
| css | 1648 | 1648 | 0 | 0 | 0 |
| go | 209 | 209 | 0 | 0 | 0 |
| javascript | 40 | 38 | 2 | 0 | 0 |
| python | 190 | 165 | 24 | 1 | 0 |
| typescript | 134 | 130 | 4 | 0 | 0 |

原始分类为 Bug 1653、代码异味 560、安全漏洞候选 10；严重级别为 CRITICAL 230、MAJOR 1829、MINOR 164。这些是平台候选分类，并不表示原来存在相同数量的生产漏洞。

主要改动包括生成模板和静态页面的通用字体后备、页面可访问名称及外部资源完整性；Go/Python 复杂流程和测试的职责拆分；测试异常输入的求值边界；TypeScript 异步回调及类型表达；SDK 文件操作的竞争、关闭责任和重试边界。生成文件通过权威模板或既有流程更新，历史 SQL 迁移和锁文件没有直接补写注释。

完整逐项结果见 [2270 项最终 CSV](D:/project/yuejuan/reports/sonar-remediation-2026-10-11/final-issues.csv) 和 [2270 项最终 JSON](D:/project/yuejuan/reports/sonar-remediation-2026-10-11/final-issues.json)。逐项记录保留原行号、当前行号、原分类、规则、平台状态和源码处理说明。

## 3. 人工误报与明确接受的例外

- 原始 30 项误报：15 条把中文维护说明识别成注释掉的代码、6 条将固定种子的合成绘图噪声当成安全随机数、1 条测试比较顺序、3 条有意的 React act 外层批处理、1 条兼容 Safari 的列表角色、2 条 Playwright 工具函数输入、2 条已在 serve 前完成 TLS 包装的监听入口。
- 新增 4 项误报：2 条 page.evaluate 浏览器执行单元中的几何函数作用域、1 条字体后备中文维护说明、1 条 playwright-cli run-code 接收的完整 async 函数输入。实际安装的工具 loader 已只读核对，不根据函数外形推测用途。
- 唯一 ACCEPTED 是固定 `127.0.0.1:8766` 的本地图纸预览 HTTP 服务。已有路径范围、解析后路径边界及请求体限制保留；这不等于改成 HTTPS，也不作为生产 HTTP 部署许可。
- TLS 两项仍允许显式开发环境、真实 bool 授权及字面 IPv4 回环的 HTTP 例外；生产、缺省及未知环境在 bind 前要求成对证书，最低 TLS1.2，证书包装在 serve 前完成。真实握手和负例证据支持误报处置，但不声称所有环境都走 HTTPS。
- 每次处置都检查当前源码 SHA 与对应冻结扫描 SHA，相同后在本地平台附依据、转换状态并 GET 回查；没有用人工 resolve 替代源码 FIXED。

处置证据：[root-platform-disposition-results.json](D:/project/yuejuan/reports/sonar-remediation-2026-10-11/root-platform-disposition-results.json)、[root-platform-disposition-phase02-results.json](D:/project/yuejuan/reports/sonar-remediation-2026-10-11/root-platform-disposition-phase02-results.json)、[root-platform-disposition-phase03-results.json](D:/project/yuejuan/reports/sonar-remediation-2026-10-11/root-platform-disposition-phase03-results.json)、[full03-fp-independent-review-python-agent.md](D:/project/yuejuan/reports/sonar-remediation-2026-10-11/full03-fp-independent-review-python-agent.md)。

## 4. 实际验证、跳过与失败记录

以下列出对本次改动有直接意义的成功批次；批次间存在重复测试及子测试，不相加成一个虚假的全仓测试总数。

| 验证 | 实际结果 | 边界与证据 |
| --- | --- | --- |
| Web Admin 38 个测试文件 | 416 pass、0 skip、退出 0 | 单 worker 重试成功，前后源码 SHA 一致；[frontend-web-retry-01-execution.json](D:/project/yuejuan/reports/sonar-remediation-2026-10-11/frontend-web-retry-01-execution.json) |
| Web Admin 类型检查 | 退出 0、无诊断 | 1280 MiB 堆重试成功；此前真实 OOM 退出 134 保留；[frontend-web-current-typecheck-02.json](D:/project/yuejuan/reports/sonar-remediation-2026-10-11/frontend-web-current-typecheck-02.json) |
| 前端生产契约及构建测量脚本 | 17 pass、0 skip、退出 0 | 没有据此宣称完成浏览器或生产 bundle 验收；[frontend-production-contracts-current-01.json](D:/project/yuejuan/reports/sonar-remediation-2026-10-11/frontend-production-contracts-current-01.json) |
| 六个最新 Go 关联包 | 1158 Test/subtest pass、2 条件 skip、退出 0 | 真实本地 AI/共享客户端契约的环境条件保留；[go-final-validation-audit.md](D:/project/yuejuan/reports/sonar-remediation-2026-10-11/go-final-validation-audit.md) |
| server 包 | 188 Test/subtest pass、62 条件 skip、退出 0 | 61 条 PostgreSQL 环境跳过、1 条 crash-helper 辅助入口跳过；[go-server-batch04-tests.json](D:/project/yuejuan/reports/sonar-remediation-2026-10-11/go-server-batch04-tests.json) |
| 题库五个 PostgreSQL 场景 | 编译及所属包回归通过；PG 0 pass、5 skip | 缺真实隔离数据库配置，不算题库业务集成通过；[go-server-question-bank-progress.md](D:/project/yuejuan/reports/sonar-remediation-2026-10-11/go-server-question-bank-progress.md) |
| 模型治理重构 | baseline/current 各 366 Test/subtest pass、0 skip | 真实 117 编译输入未变化，逐测试身份及 TRACE 顺序核对；[delivery-independent-fresh-followup-audit-python-agent.md](D:/project/yuejuan/reports/sonar-remediation-2026-10-11/delivery-independent-fresh-followup-audit-python-agent.md) |
| 最新 Python 最小补验证 | 43+2 pass、0 skip，两次退出 0 | 变化的 OCR 批次/符号验证/主观题 API 测试按真实依赖 SHA 绑定；[execution.json](D:/project/yuejuan/reports/sonar-remediation-2026-10-11/fresh-python-dynamic-01/execution.json) |
| SDK 文件竞争与生成契约 | 18 pass、0 skip、退出 0 | 短写、截断、竞争重试、权限错误及 FD 关闭责任；[root-new-js-tests-02.json](D:/project/yuejuan/reports/sonar-remediation-2026-10-11/root-new-js-tests-02.json) |
| 前端 API 扫描工具的固定输入对比 | 输出 1176650 字节完全一致 | 485 TS 源、405 调用、536 后端路由等清单一致；生成写入在内存截获；[frontend-api-scope-equivalence-02.json](D:/project/yuejuan/reports/sonar-remediation-2026-10-11/frontend-api-scope-equivalence-02.json) |
| TLS/传输与 XML 加固关联检查 | 已执行真实握手负例及恶意 XML/无网络测试 | 独立 SHA 绑定和输出等价检查；[python-tls-platform-evidence.md](D:/project/yuejuan/reports/sonar-remediation-2026-10-11/python-tls-platform-evidence.md) |

最后两个 Go 复杂度项使用单独的接手备份、baseline/current 包测试和完整业务证据比较；结果及逐项保护边界见 [root-go-final-02-progress.md](D:/project/yuejuan/reports/sonar-remediation-2026-10-11/root-go-final-02-progress.md)。另一个智能体全文读审并反向内联新 helper，整文件 token 与 checkpoint03 一致，详见 [go-import-final-independent-review.md](D:/project/yuejuan/reports/sonar-remediation-2026-10-11/go-import-final-independent-review.md)；可靠性重构的全文审查见 [go-server-reliability-independent-review.md](D:/project/yuejuan/reports/sonar-remediation-2026-10-11/go-server-reliability-independent-review.md)。

历史编译失败、工具真实执行失败、OOM、IPC 故障及重试日志全部保留，没有改写成成功。早期部分 Go 测试没有单独记录测试时 SHA，最终审计明确保留这一证据局限。

**浏览器 E2E 没有全部通过。** 原请求 47 用例的运行在 23 个启动标记后中断，没有最终成功摘要。记录的 10 个失败包括 2 个原有 UI/快照不一致、2 个有资源不足证据的断言超时、6 个浏览器启动/worker/导航失败。预扫描本地基线证明两项 UI 差异已存在；未更新快照或删断言，仍需要健康环境重跑及产品验收。详细证据见 [frontend-e2e-independent-review-python-agent.md](D:/project/yuejuan/reports/sonar-remediation-2026-10-11/frontend-e2e-independent-review-python-agent.md)。

本批没有实际运行图纸字号浏览器 QA，也没有构建或拉取 Docker 镜像；相关小改动通过源码、语法和最终规则复扫验证，不能改写为对应运行验收已经完成。

## 5. 最终平台度量与质量门禁

| 度量 | 原始扫描 | 最终扫描 |
| --- | ---: | ---: |
| ncloc | 227136 | 227943 |
| bugs | 1653 | 0 |
| vulnerabilities | 10 | 0 |
| code_smells | 560 | 0 |
| security_hotspots | 0 | 0 |
| duplicated_lines_density | 1.5 | 1.4 |
| sqale_index | 6898 | 0 |
| sqale_rating | 1.0 | 1.0 |
| reliability_rating | 4.0 | 1.0 |
| security_rating | 3.0 | 1.0 |

评级数值 1=A、2=B、3=C、4=D、5=E；sqale_index 为平台估算维护成本，单位分钟，不是已耗工时。

**质量门禁实际状态为 ERROR。** 条件逐项列出：

| 条件 | 实际值 | 失败条件 | 状态 |
| --- | ---: | --- | --- |
| new_coverage | 0.0 | LT 80 | ERROR |
| new_duplicated_lines_density | 0.48845 | GT 3 | OK |
| new_violations | 0 | GT 0 | OK |

本次没有导入测试覆盖率，平台 new_coverage/coverage 为 0 是缺少导入数据，不能解释为实测覆盖率 0%。没有放宽 80% 覆盖率门槛或改写质量门禁。本次门禁仅新增覆盖率项失败，新增问题数及新增重复率均满足门槛；交付结论是“代码告警全部处理、覆盖率门禁仍未满足”。

Rust 语言传感器保留原范围，但本次出现 No Cargo manifest found / skipping Clippy analysis，因此不能声称本轮完成 Rust Clippy 构建检查。既有 CodeQL Rust 完整查询属于另一个有独立哈希的历史扫描，不能替代当前工作区的 Clippy 或业务测试。

## 6. 其它平台与业务评测的范围

本轮最终当前工作区复扫是 SonarQube。最初按用户要求完成的 Semgrep、CodeQL、本地 Actions 检查和覆盖率附件保持原样；它们对应各自历史冻结版本，后续源码改变后没有在本轮重新扫描，所以不把历史安全结果当作当前全仓复检结果。

历史完整入口：[全部平台原始详细报告](D:/project/yuejuan/reports/platform-audit-2026-10-10/README.md)。Actions 云端 run 和 Codecov 云端上传均没有执行，遵守仅本地要求；最初六包的 49.472% 覆盖率及 releasegate 41.315% 门槛失败也没有被本轮覆盖掉。

SonarQube 告警归零仍不能证明学校间授权隔离、成绩事务并发、裁切/识别准确率、主观题一致性或三智能体仲裁正确。数据库、真实 AI 服务、压力测试和专门业务评测须使用对应环境与数据集独立验证。

## 7. 原始规则逐项统计

| 规则 | 原始数 | FIXED | FALSE_POSITIVE | ACCEPTED | OPEN |
| --- | ---: | ---: | ---: | ---: | ---: |
| css:S4649 | 1645 | 1645 | 0 | 0 | 0 |
| go:S3776 | 201 | 201 | 0 | 0 | 0 |
| python:S5778 | 79 | 79 | 0 | 0 | 0 |
| typescript:S1874 | 65 | 65 | 0 | 0 | 0 |
| python:S7498 | 23 | 23 | 0 | 0 | 0 |
| typescript:S6582 | 19 | 19 | 0 | 0 | 0 |
| python:S9073 | 18 | 18 | 0 | 0 | 0 |
| python:S125 | 15 | 0 | 15 | 0 | 0 |
| python:S3358 | 15 | 15 | 0 | 0 | 0 |
| python:S3776 | 14 | 14 | 0 | 0 | 0 |
| typescript:S6551 | 11 | 11 | 0 | 0 | 0 |
| typescript:S9332 | 10 | 10 | 0 | 0 | 0 |
| go:S107 | 8 | 8 | 0 | 0 | 0 |
| typescript:S5906 | 8 | 8 | 0 | 0 | 0 |
| javascript:S6594 | 7 | 7 | 0 | 0 | 0 |
| python:S1192 | 7 | 7 | 0 | 0 | 0 |
| typescript:S8783 | 7 | 7 | 0 | 0 | 0 |
| python:S2245 | 6 | 0 | 6 | 0 | 0 |
| javascript:S3776 | 5 | 5 | 0 | 0 | 0 |
| typescript:S8980 | 5 | 2 | 3 | 0 | 0 |
| javascript:S6582 | 4 | 4 | 0 | 0 | 0 |
| css:S7924 | 3 | 3 | 0 | 0 | 0 |
| javascript:S1874 | 3 | 3 | 0 | 0 | 0 |
| javascript:S2681 | 3 | 3 | 0 | 0 | 0 |
| javascript:S7766 | 3 | 3 | 0 | 0 | 0 |
| python:S5332 | 3 | 0 | 2 | 1 | 0 |
| python:S7494 | 3 | 3 | 0 | 0 | 0 |
| javascript:S2871 | 2 | 2 | 0 | 0 | 0 |
| javascript:S6557 | 2 | 2 | 0 | 0 | 0 |
| javascript:S7744 | 2 | 2 | 0 | 0 | 0 |
| javascript:S7765 | 2 | 2 | 0 | 0 | 0 |
| javascript:S7780 | 2 | 2 | 0 | 0 | 0 |
| javascript:S8786 | 2 | 2 | 0 | 0 | 0 |
| javascript:S905 | 2 | 0 | 2 | 0 | 0 |
| typescript:S6606 | 2 | 2 | 0 | 0 | 0 |
| typescript:S6819 | 2 | 2 | 0 | 0 | 0 |
| Web:InputWithoutLabelCheck | 1 | 1 | 0 | 0 | 0 |
| Web:S5725 | 1 | 1 | 0 | 0 | 0 |
| javascript:S7727 | 1 | 1 | 0 | 0 | 0 |
| python:S1172 | 1 | 1 | 0 | 0 | 0 |
| python:S1186 | 1 | 1 | 0 | 0 | 0 |
| python:S3415 | 1 | 0 | 1 | 0 | 0 |
| python:S5843 | 1 | 1 | 0 | 0 | 0 |
| python:S5906 | 1 | 1 | 0 | 0 | 0 |
| python:S8786 | 1 | 1 | 0 | 0 | 0 |
| python:S8997 | 1 | 1 | 0 | 0 | 0 |
| typescript:S6544 | 1 | 1 | 0 | 0 | 0 |
| typescript:S6822 | 1 | 0 | 1 | 0 | 0 |
| typescript:S6845 | 1 | 1 | 0 | 0 | 0 |
| typescript:S6847 | 1 | 1 | 0 | 0 | 0 |
| typescript:S7727 | 1 | 1 | 0 | 0 | 0 |

最终报告以真实平台状态为准；分工账本中的待复扫文字保留历史来源，其最新平台状态及分析 ID 另存于总账，不把历史证据时间改成最终扫描时间。
