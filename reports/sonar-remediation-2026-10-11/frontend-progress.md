# 前端 SonarQube 整改进度

原始 JS/TS/TSX/MJS 172 项：166 项源码已实现待平台复扫，6 项保留必要语义待平台审定。HTML 内嵌问题由 root 处理。本账本不代表 SonarQube 已关闭问题。

7 项 CRITICAL 包括共享 API AST 扫描器、线性动态路径扫描、draw.io 几何阶段拆解及确定性排序；保留 405 个调用点完整 JSON 等价。其余修改覆盖键盘交互、React/AntD API、显示值类型边界、可选链、专用断言和单次正则匹配。ResponsiveTable 原 S6544 已按真实 Array.some 回调纠正为显式 Boolean，未改变原真假值条件。

最新 web-admin 串行 38 份测试 / 416 项全部通过、0 跳过，类型检查退出 0，测试及类型检查源码 SHA 均稳定，包含 ResponsiveTable 的 10 项现有测试。之前的 IPC、OOM 和 Windows 提交内存不足日志完整保留。其他已完成验证为 desktop 100、student 48、答题证据/查看器 10、Node 5、lab 194；AST 405 调用完整等价；STORY049/052 与 Biome 成功（15 个已有警告）。前端证据在 frontend-progress.json，各验证只覆盖相应冻结源码。

Playwright 的资源错误仍需条件允许时复验。独立只读图像及源码比对确认 390x844 的布局/文案差异早于本轮 Sonar 修复，原工作区已有相同移动布局；旧快照目前仍是待验收失败，没有更新基准图。详细归因见 frontend-e2e-independent-review-python-agent.json/md。本轮未将截图差异计为通过。

逐文件状态沿用 frontend-progress.json：完整审读才标完成，仅告警及调用上下文审读的文件保持 pending。统一平台复扫归 root 负责。

## 六项需要平台审定的语义证据

- `53d83cb5-de5b-4947-afec-13d86cc28adc` — `typescript:S8980`，`apps/student-portal/src/App.releaseConsistency.test.tsx` 原行 129。外层 act 合并同一事件批次，用于测试同步 ref 在 disabled 重绘前阻止提交或退出。拆为各自 fireEvent 的 act 会提前刷新 UI，失去该时序保证。 SHA256 `95c238c303b005740b70f697455bcbad2f83005f05f6486b7b47bca613e23469`。
- `e523e830-fd7b-4360-a4f2-b2d6260bf960` — `typescript:S8980`，`apps/student-portal/src/App.submissionReceipt.test.tsx` 原行 318。外层 act 合并同一事件批次，用于测试同步 ref 在 disabled 重绘前阻止提交或退出。拆为各自 fireEvent 的 act 会提前刷新 UI，失去该时序保证。 SHA256 `0e42cd9b976a1fd22fb236fa0df96962abbc5214b221a36b19cf361b2c6c5de3`。
- `ab84da33-c3ed-4e22-8690-fe7c1b35cc99` — `typescript:S8980`，`apps/student-portal/src/App.submissionReceipt.test.tsx` 原行 355。外层 act 合并同一事件批次，用于测试同步 ref 在 disabled 重绘前阻止提交或退出。拆为各自 fireEvent 的 act 会提前刷新 UI，失去该时序保证。 SHA256 `0e42cd9b976a1fd22fb236fa0df96962abbc5214b221a36b19cf361b2c6c5de3`。
- `3ea8c773-5a14-4880-bc36-8b0adf2fda7f` — `typescript:S6822`，`apps/web-admin/src/pages/ArbitrationTaskRail.tsx` 原行 31。listStyle:none 时 Safari 会从可访问性树移除原生列表；显式 role=list 保留列表语义，已有中文维护注释与 MDN 依据。 SHA256 `205bf807531e5ace73d596bdd071f4761ad7ab18e9cb3bd4282eef8eb59ce4ba`。
- `75ac3d77-53f0-47a5-9335-dc49df53ed13` — `javascript:S905`，`docs/architecture/tools/capture_preview_qa.js` 原行 2。playwright-cli run-code 接收整个文件中的函数表达式，由工具传入真实 page 并执行；不能改成无人调用的模块导出或自行执行脚本。 SHA256 `0606fe358d6e5a275781c02942c3207afb845616b21bf5f1d42dfbb419527eb1`。
- `e50a781b-1426-49d6-aa49-aa91cee217ae` — `javascript:S905`，`docs/architecture/tools/render_native.js` 原行 2。playwright-cli run-code 接收整个文件中的函数表达式，由工具传入真实 page 并执行；不能改成无人调用的模块导出或自行执行脚本。 SHA256 `0be6b4dbde9ebc8d7150140d5ae66a3aa755bd3376b43ed51ea13524dc43668a`。

`App.releaseConsistency` 保护图片失败后同一事件批次的直接 submit；两个 `App.submissionReceipt` 保护退出/提交的竞争顺序。每个 fireEvent 已封装 act，但外层 act 故意阻止事件间提前重绘；删掉会失去同步 ref 的覆盖。48 项学生端测试和当前 student 类型检查成功，尚须保存重跑日志供平台复核。

Safari 的列表兼容依据来自 [MDN list-style 可访问性说明](https://developer.mozilla.org/en-US/docs/Web/CSS/Reference/Properties/list-style#accessibility)。当前代码保留 `ul role="list"` 和 `listStyle:"none"`，附近注释直接说明原因。

两个工具文件供 `playwright-cli run-code` 读取。`DIAGRAM_MAINTENANCE.md` 与 `evidence/acceptance-report.md` 记录真实调用和 70 页验证；Node 测试把 render_native.js 的完整函数表达式交给 vm 并传入图形夹具执行，保护顶点/边界/端点/背景和悬空边。这个测试不等同真实浏览器渲染，原报告另有真实 EditorUi 证据。capture_preview_qa.js 的调用形式同样必须保留，未在本次重启浏览器。

最新证据补充：生产路由契约与 measure-build 的 17 项现有测试重新运行退出 0、0 跳过，测试前后源码 SHA 稳定；证据 frontend-production-contracts-current-01.json/tap。5 个历史元数据 SHA 与当前源码不同的文件已增加 latest_current_sha 和当前独立证据，详见 frontend-latest-sha-reconciliation.json；历史通过 SHA 保留，不把历史测试转记为后续源码通过。最终平台结果待 root 导出 checkpoint03。
