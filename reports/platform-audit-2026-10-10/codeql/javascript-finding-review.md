# JavaScript / TypeScript CodeQL 候选人工复核

复核时间：2026-10-11T13:20:56.788370+08:00。平台原始结果 [javascript.sarif](javascript.sarif)，SHA-256 `f0543c1d15f9cb8a13153c0a454c6b51d80d40ad610224da102ba1484f61a0f1`。

203 条官方 security-and-quality 查询的 14 条候选全部逐条阅读：4 条安全类文件系统检查/使用竞态，10 条质量类。人工结论为 4 条有条件的本地脚本加固、5 条低优先级维护、3 条有意的覆盖/runner 输入、2 条标准 DOM 模型误报。没有把 7.7 的工具分数直接转换成四个已证实线上漏洞。

CodeQL 判断只用 2026-10-11 10:55:42 固定工作区副本，所列源文件及调用上下文 SHA-256 全部匹配清单。外部 runner 用途另参考当前本地文档，单独记录其哈希；未运行服务、未访问学生数据、未进行云端上传。告警上下文复核不代表相关文件全篇或全仓均已人工审读。

| 项 | 数量 |
| --- | ---: |
| 原始 security-high（四条均 7.7） | 4 |
| 原始质量候选 | 10 |
| 该 SARIF 未分配人工结论 | 0 |
| 复核质量候选中确认的运行缺陷 | 0 |

文件系统风险的前提是维护工作目录可被另一个非可信主体并发修改，并且维护脚本具有额外读写权限。当前固定调用链没有教师/学生 API 路径进入这几个函数。代码确有 TOCTOU 窗口，不能以开发脚本为由宣称绝对无风险；优先级随本机/CI 权限和目录隔离情况变化。

StorageEvent 两条误报的依据包括工具自身 `externs/web/webstorage.js:106` 零参数声明与 [WHATWG 标准构造器](https://html.spec.whatwg.org/multipage/webstorage.html#the-storageevent-interface)。这里允许 type 和初始化字典，删除参数会损坏测试场景。

## CQ-JS-001 · js/file-system-race

[scripts/ci/check-generated-contracts.mjs:16](<D:/tools/audit-codeql-20261010/source-20261011/scripts/ci/check-generated-contracts.mjs:16>)

人工状态：`reviewed_alert_context`；结论 `conditional_local_hardening`；人工优先级 `low`。原始安全分数：7.7。

检查路径存在/类型之后，后续读写再次按路径打开，确有检查与使用之间的窗口。本次调用位于维护者本地/CI 的契约生成与对账脚本，路径由工作区根和固定产物路径组成；未找到教师/学生请求控制文件系统目标或切换链接的入口。只有另一进程能在该维护工作区改名、删除或替换链接，且脚本具有该进程没有的权限时，才形成安全攻击前提。不据此认定四个线上高危漏洞；同权限并发维护仍可能导致读写失败或产物不一致。

建议：后续维护可用打开后的同一文件描述符做读取/校验，明确符号链接政策；生成写入考虑同目录临时文件和原子替换，并保留现有 Windows 权限/文件锁兼容约束。在普通可信工作区作为低优先级加固；若 CI 使用共享不可信可写目录或提升权限运行，再提高优先级。

固定源文件 SHA-256：`ef97c30e3604fead0c39f9cb8c0ddfa34395219b0990cb07e282569eeeac5381`。相关证据与所读范围见 [javascript-finding-review.json](javascript-finding-review.json)。

## CQ-JS-002 · js/file-system-race

[scripts/lib/openapi-sdk.mjs:252](<D:/tools/audit-codeql-20261010/source-20261011/scripts/lib/openapi-sdk.mjs:252>)

人工状态：`reviewed_alert_context`；结论 `conditional_local_hardening`；人工优先级 `low`。原始安全分数：7.7。

检查路径存在/类型之后，后续读写再次按路径打开，确有检查与使用之间的窗口。本次调用位于维护者本地/CI 的契约生成与对账脚本，路径由工作区根和固定产物路径组成；未找到教师/学生请求控制文件系统目标或切换链接的入口。只有另一进程能在该维护工作区改名、删除或替换链接，且脚本具有该进程没有的权限时，才形成安全攻击前提。不据此认定四个线上高危漏洞；同权限并发维护仍可能导致读写失败或产物不一致。

建议：后续维护可用打开后的同一文件描述符做读取/校验，明确符号链接政策；生成写入考虑同目录临时文件和原子替换，并保留现有 Windows 权限/文件锁兼容约束。在普通可信工作区作为低优先级加固；若 CI 使用共享不可信可写目录或提升权限运行，再提高优先级。

固定源文件 SHA-256：`059bffebe15e3aaa8a0129aff8575ed047142adccf7c96376b6dc9a5ab3823db`。相关证据与所读范围见 [javascript-finding-review.json](javascript-finding-review.json)。

## CQ-JS-003 · js/file-system-race

[scripts/lib/openapi-sdk.mjs:257](<D:/tools/audit-codeql-20261010/source-20261011/scripts/lib/openapi-sdk.mjs:257>)

人工状态：`reviewed_alert_context`；结论 `conditional_local_hardening`；人工优先级 `low`。原始安全分数：7.7。

检查路径存在/类型之后，后续读写再次按路径打开，确有检查与使用之间的窗口。本次调用位于维护者本地/CI 的契约生成与对账脚本，路径由工作区根和固定产物路径组成；未找到教师/学生请求控制文件系统目标或切换链接的入口。只有另一进程能在该维护工作区改名、删除或替换链接，且脚本具有该进程没有的权限时，才形成安全攻击前提。不据此认定四个线上高危漏洞；同权限并发维护仍可能导致读写失败或产物不一致。

建议：后续维护可用打开后的同一文件描述符做读取/校验，明确符号链接政策；生成写入考虑同目录临时文件和原子替换，并保留现有 Windows 权限/文件锁兼容约束。在普通可信工作区作为低优先级加固；若 CI 使用共享不可信可写目录或提升权限运行，再提高优先级。

固定源文件 SHA-256：`059bffebe15e3aaa8a0129aff8575ed047142adccf7c96376b6dc9a5ab3823db`。相关证据与所读范围见 [javascript-finding-review.json](javascript-finding-review.json)。

## CQ-JS-004 · js/file-system-race

[scripts/update-api-route-coverage.mjs:120](<D:/tools/audit-codeql-20261010/source-20261011/scripts/update-api-route-coverage.mjs:120>)

人工状态：`reviewed_alert_context`；结论 `conditional_local_hardening`；人工优先级 `low`。原始安全分数：7.7。

检查路径存在/类型之后，后续读写再次按路径打开，确有检查与使用之间的窗口。本次调用位于维护者本地/CI 的契约生成与对账脚本，路径由工作区根和固定产物路径组成；未找到教师/学生请求控制文件系统目标或切换链接的入口。只有另一进程能在该维护工作区改名、删除或替换链接，且脚本具有该进程没有的权限时，才形成安全攻击前提。不据此认定四个线上高危漏洞；同权限并发维护仍可能导致读写失败或产物不一致。

建议：后续维护可用打开后的同一文件描述符做读取/校验，明确符号链接政策；生成写入考虑同目录临时文件和原子替换，并保留现有 Windows 权限/文件锁兼容约束。在普通可信工作区作为低优先级加固；若 CI 使用共享不可信可写目录或提升权限运行，再提高优先级。

固定源文件 SHA-256：`46157193d9dd449115e17f670c1de9baf7881739d6712ed8e64d03ec065fae1f`。相关证据与所读范围见 [javascript-finding-review.json](javascript-finding-review.json)。

## CQ-JS-005 · js/unused-local-variable

[apps/web-admin/scripts/check-production-routes.mjs:27](<D:/tools/audit-codeql-20261010/source-20261011/apps/web-admin/scripts/check-production-routes.mjs:27>)

人工状态：`reviewed_alert_context`；结论 `low_maintenance`；人工优先级 `low`。原始安全分数：无。

examWorkspace 只初始化读入源码，变量在该固定快照脚本中未被后续断言使用。不是生产页面的未使用状态。

建议：后续删除无用途的读取，或补上原计划的路由契约断言；不要把读取本身当作已经检查了该页面。

固定源文件 SHA-256：`396aac28dba8ce350c1d4d1838fcc647aec282746a08132b03bf212f140e090b`。相关证据与所读范围见 [javascript-finding-review.json](javascript-finding-review.json)。

## CQ-JS-006 · js/unused-local-variable

[apps/web-admin/scripts/check-production-routes.mjs:168](<D:/tools/audit-codeql-20261010/source-20261011/apps/web-admin/scripts/check-production-routes.mjs:168>)

人工状态：`reviewed_alert_context`；结论 `low_maintenance`；人工优先级 `low`。原始安全分数：无。

allUiSource 对 src 做递归读入并 join，随后没有使用；其他断言使用各自具体源码或 gradingFeature。存在无用途的扫描开销。

建议：后续删除无用途的全 src 读取或明确其实际断言用途。

固定源文件 SHA-256：`396aac28dba8ce350c1d4d1838fcc647aec282746a08132b03bf212f140e090b`。相关证据与所读范围见 [javascript-finding-review.json](javascript-finding-review.json)。

## CQ-JS-007 · js/duplicate-property

[lab/tests/helpers.js:29](<D:/tools/audit-codeql-20261010/source-20261011/lab/tests/helpers.js:29>)

人工状态：`reviewed_alert_context`；结论 `intentional_behavior`；人工优先级 `none`。原始安全分数：无。

baseInput 测试夹具先取 overrides.rubric 或默认量规，...overrides 后再次写 rubric，确保最终使用同一个已选择量规；重复键是夹具覆盖次序，未发现值丢失。

建议：可删除 spread 前面的第一个 shorthand rubric 简化对象；必须保留最终覆盖规则及 max_score 来源，不能只删除最后一个 rubric。

固定源文件 SHA-256：`1a5ef501acbc7bcd8896b744da3f7eb070df78dbac03e51c94e573a220c4594e`。相关证据与所读范围见 [javascript-finding-review.json](javascript-finding-review.json)。

## CQ-JS-008 · js/useless-expression

[docs/architecture/tools/capture_preview_qa.js:2](<D:/tools/audit-codeql-20261010/source-20261011/docs/architecture/tools/capture_preview_qa.js:2>)

人工状态：`reviewed_alert_context`；结论 `intentional_behavior`；人工优先级 `none`。原始安全分数：无。

该文件保存 async(page)=>{...} 给 Playwright runner 作为函数输入，函数体由 runner 调用；作为独立 node 脚本会只生成未使用的函数值。现有架构维护文档记录该运行方式。

建议：如需要独立 Node 入口，另外提供显式 runner；在质量扫描中用精确路径标记此类工具输入，不全局关闭 useless-expression。

固定源文件 SHA-256：`3d38695dbbf3c4cc578611fb3174face4de812e037d35f7acb51ffeb652c2c07`。相关证据与所读范围见 [javascript-finding-review.json](javascript-finding-review.json)。

## CQ-JS-009 · js/useless-expression

[docs/architecture/tools/render_native.js:2](<D:/tools/audit-codeql-20261010/source-20261011/docs/architecture/tools/render_native.js:2>)

人工状态：`reviewed_alert_context`；结论 `intentional_behavior`；人工优先级 `none`。原始安全分数：无。

该文件是 Playwright run-code 使用的 async(page) 函数输入，并非自动调用的 Node 入口；函数由外部浏览器 runner 注入 page 后执行。文档和本地证据记有 render_native.js 的运行方式。

建议：在文件说明和扫描配置中明确 runner 输入用途；不要为了消除告警直接加自调用而缺少 page。

固定源文件 SHA-256：`55871091d09172fc45a7c5dc493109fcbea53f3bf44e7a5e7061799843e5f6a4`。相关证据与所读范围见 [javascript-finding-review.json](javascript-finding-review.json)。

## CQ-JS-010 · js/superfluous-trailing-arguments

[apps/web-admin/src/features/grading/workbench/hooks/useGradingKeyboard.test.tsx:14](<D:/tools/audit-codeql-20261010/source-20261011/apps/web-admin/src/features/grading/workbench/hooks/useGradingKeyboard.test.tsx:14>)

人工状态：`reviewed_alert_context`；结论 `false_positive_context`；人工优先级 `none`。原始安全分数：无。

StorageEvent 的标准构造器接收 type 和可选 StorageEventInit；此处 storage + {key:null} 合法。SARIF relatedLocations 指向 CodeQL 自带 externs/web/webstorage.js:106 的旧零参数模型 function StorageEvent() {}，不是业务声明。

建议：保留测试语义；可向 CodeQL 反馈标准 DOM 模型过时，并对两条精确告警记录误报理由。

固定源文件 SHA-256：`a36906fe170935c1495c8b0421940627997129906526f27db110a35895d0461c`。相关证据与所读范围见 [javascript-finding-review.json](javascript-finding-review.json)。

## CQ-JS-011 · js/superfluous-trailing-arguments

[apps/web-admin/src/features/grading/workbench/hooks/useGradingKeyboard.test.tsx:59](<D:/tools/audit-codeql-20261010/source-20261011/apps/web-admin/src/features/grading/workbench/hooks/useGradingKeyboard.test.tsx:59>)

人工状态：`reviewed_alert_context`；结论 `false_positive_context`；人工优先级 `none`。原始安全分数：无。

StorageEvent 的两个参数符合标准接口；第二个字典中 key 用于模拟跨标签页快捷键设置变更。CodeQL 将标准构造器建模为零参数导致多余参数告警。

建议：保留测试的 key；不要删除参数破坏跨标签页设置变化场景。记录精确误报。

固定源文件 SHA-256：`a36906fe170935c1495c8b0421940627997129906526f27db110a35895d0461c`。相关证据与所读范围见 [javascript-finding-review.json](javascript-finding-review.json)。

## CQ-JS-012 · js/trivial-conditional

[apps/web-admin/src/pages/PanelModelBindingsSection.tsx:161](<D:/tools/audit-codeql-20261010/source-20261011/apps/web-admin/src/pages/PanelModelBindingsSection.tsx:161>)

人工状态：`reviewed_alert_context`；结论 `low_maintenance`；人工优先级 `low`。原始安全分数：无。

同一 save 回调 line154 在无 await 之前已因 !tenantID 或 loadingConfigs 返回；line161 再查这两个闭包值确为冗余。后续 !config/canDisableMissing/prompt/strength_rank/capability 检查仍有效。

建议：后续可去掉第二处重复子条件，保留入口同步 ref/state 边界与其他字段校验。不是缺失鉴权或异步竞态的证据。

固定源文件 SHA-256：`a6ce3383703de544a469a4e71c7bb692df2d77003e16a0c2f22cc0ce6be66c0a`。相关证据与所读范围见 [javascript-finding-review.json](javascript-finding-review.json)。

## CQ-JS-013 · js/trivial-conditional

[apps/web-admin/src/pages/PanelModelBindingsSection.tsx:161](<D:/tools/audit-codeql-20261010/source-20261011/apps/web-admin/src/pages/PanelModelBindingsSection.tsx:161>)

人工状态：`reviewed_alert_context`；结论 `low_maintenance`；人工优先级 `low`。原始安全分数：无。

该 !tenantID 子表达式在 line154 提前 guard 后恒 false，原因是正常入口前置拒绝；不表示租户校验失效。

建议：与同一行另外两条冗余条件一并整理，入口租户 guard 应保留。

固定源文件 SHA-256：`a6ce3383703de544a469a4e71c7bb692df2d77003e16a0c2f22cc0ce6be66c0a`。相关证据与所读范围见 [javascript-finding-review.json](javascript-finding-review.json)。

## CQ-JS-014 · js/trivial-conditional

[apps/web-admin/src/pages/PanelModelBindingsSection.tsx:161](<D:/tools/audit-codeql-20261010/source-20261011/apps/web-admin/src/pages/PanelModelBindingsSection.tsx:161>)

人工状态：`reviewed_alert_context`；结论 `low_maintenance`；人工优先级 `low`。原始安全分数：无。

该 loadingConfigs 子表达式在 line154 提前 guard 后恒 false，两次检查之间无 await；同步入口仍使用 loadingRef 防 state 未重绘的窗口。

建议：可去掉第二次 loadingConfigs 检查，保留 loadingRef 和入口 guard 的业务说明。

固定源文件 SHA-256：`a6ce3383703de544a469a4e71c7bb692df2d77003e16a0c2f22cc0ce6be66c0a`。相关证据与所读范围见 [javascript-finding-review.json](javascript-finding-review.json)。
