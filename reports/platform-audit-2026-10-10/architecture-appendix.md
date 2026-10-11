# 仓库架构门禁附件

本附件来自仓库自带的 `scripts/check-architecture-boundaries.mjs`，不是 SonarQube、Semgrep 或 CodeQL 的扫描结果。该脚本采用文本规则检查约定，不等同于完整依赖图或人工架构审查。

2026 年 10 月 11 日实际执行 `node scripts/check-architecture-boundaries.mjs`，工具退出码 1，发现三个阻断项：

| 文件 | 当前物理行 | 既有上限 | 结果 |
| --- | ---: | ---: | --- |
| `apps/web-admin/src/features/question-bank/QuestionBankPage.tsx` | 827 | 700 | 超出 |
| `apps/web-admin/src/pages/CaptureBatchPage.tsx` | 748 | 700 | 超出 |
| `apps/web-admin/src/pages/PlatformModelConfigPage.tsx` | 773 | 700 | 超出 |

这些结果表示页面超过仓库当前规定的维护规模，并不单凭行数证明存在运行错误。重构时应按职责提取交互、状态与展示组件，同时保留解释业务边界的必要注释；不宜通过删除注释或直接放宽预算使门禁通过。

脚本还报告大型接口和复杂 hooks 等非阻断信号。例如 `capture.Store` 含 49 个方法，`questionbank.Store` 含 26 个方法，桌面 `useDesktopSession` 含 20 处 `useState`。这些信号适合帮助定位进一步审查范围；部分 hook 行数告警命中了测试文件，不能全部解释为生产 hook 复杂度。

原始输出见 `architecture-boundary.log`，工具退出状态见 `architecture-boundary-exit.json`。本次仅检查和报告，没有修改页面、接口或门禁预算。
