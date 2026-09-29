# 前端审查结论（2026-09-26）

本轮快速复核确认 2 项 P2。未修改业务源码；只补充审查目录内的复现用例和报告。

## 1. [P2] 提交返回后应核对当前任务，再清理界面或跳转

- 定位：`apps/web-admin/src/features/grading/workbench/hooks/useGradingReviewActions.ts:196-206`，建议行内评论锚点为 199-206。
- 入口证据：`TaskRail.tsx:121` 的列表项始终允许选择；`GradingWorkbench.tsx:528-532` 直接切换 selectedTaskId，未对正在提交的 actioning 加锁。
- 触发：在 A 点击提交，请求尚未返回时切到并加载 C；A 的提交成功回调仍使用开始时捕获的 nextTask=B，无条件清空当前 ctx/draft 并把 selectedTaskId 改成 B。代码接收了 activeTask/activeUserId，但这一回调未检查它们。
- 更常见的变体：用户提前切到 A 的下一题 B，并且 B 已加载。A 返回后将 B 的 ctx 清空，再次设置 selectedTaskId=B。`GradingWorkbench.tsx:341-343` 的上下文加载 effect 仅依赖稳定的 loadContext 和 selectedTaskId，两者此时都未改变，因此留在“请选择一份答卷”空态；再次点击已选 B 也不会重新加载，需要刷新或切换其他题再返回。已核实 loadContext 所依赖的 viewer 回调在 `useAnswerViewer.ts:32-53` 保持稳定。
- 影响边界：确认的是当前界面草稿被重置、意外跳题、空态阻断工作流。不能称为永久草稿丢失：`GradingWorkbench.tsx:257-261` 会把编辑写入本地 fallback，并另行保存服务端草稿；提交成功仅删除 A 的 fallback。此次复现也未证明服务端提交错题或持久化数据损坏。
- 最小修复方向：保留对已提交 A 的缓存/队列处理；将清理活动上下文、重置草稿、跳转下一题等界面副作用限制为提交所属任务和用户仍然活动的情况。
- 复现：`grading-submit-race.test.ts`，两个 case 均通过并断言了现有错误行为。第一例调用实际生产 action，验证 C → B 和当前草稿重置；第二例以真实 React state/effect 的最小 harness 验证 B → B 不会再次运行上下文加载 effect。第二例模拟上下文加载，不是完整浏览器 E2E。
- 命令：`npx vitest run reports/code-review-2026-09-26/grading-submit-race.test.ts`；结果：1 file / 2 tests passed，详见 `grading-submit-race.log`。

## 2. [P2] 同步未保存的离线草稿前，应建立本地记录

- 定位：`apps/desktop-client/src/components/OfflineWorkbench.tsx:243-252`，建议行内评论锚点为 243-245。
- 入口证据：下载任务包在 121-125 行仅更新 React 状态；本地草稿创建只在显式保存的 146-169 行进行；365 行“同步”按钮只检查 pkg 和已同步状态，不要求存在 currentEnvelope，因此“下载任务包 → 输入分数 → 直接同步”是可到达路径。
- 失败链路：服务端 submitHumanGrade 成功后，245 行更新本地记录。Native 路径经 `offlineStore.ts:86-89` / `durableStore.ts:178-180` 调用 Rust UPDATE；`src-tauri/src/durable_store/drafts.rs:90-96` 在不存在 draft 行时返回 `offline draft was not found`。catch 随后把界面从 synced 改为 failed，252 行再次更新同一不存在的记录而抛出；按钮使用 `void syncDraft()`，没有接住该异常。
- 影响：服务端已接收评分，但界面显示同步失败，且抛出未处理 rejection；此前阻止重复同步的 synced 状态也被覆盖。未据此声称后台会重复写入评分，实际重试仍可能被版本/状态检查拒绝。
- 影响范围：上述缺失行异常属于 Tauri Native 持久层路径。浏览器开发 fallback 的数组 map 对缺失条目不会抛相同错误，不能把两种实现混为一谈。
- 最小修复方向：同步前确保本地持久记录存在，或明确禁止直接同步未保存草稿；服务端提交成功后，本地状态写入失败应作为独立错误处理，避免误报评分提交失败和在 catch 再次抛同一持久层异常。
- 复现：`desktop-unsaved-sync-repro.mjs` 从生产 TSX 提取 syncDraft 函数并执行，后端提交和持久层均使用 mock；持久层 mock 按 Rust “UPDATE 缺失行报错”契约实现。观测到 backendSubmissionsAccepted=1、状态依次 syncing → synced → failed、escapedRejection 为 offline draft was not found。该证据是生产 action + Native 源码契约复核，不是实机 Tauri/SQLite 集成测试。
- 命令：`node reports/code-review-2026-09-26/desktop-unsaved-sync-repro.mjs`；既有 `desktop-unsaved-sync-repro.log` 与本次重跑输出一致。

## 验证范围

- 使用已有前端全量测试日志 `frontend-tests.log`（81 files / 366 tests passed），本轮没有重复执行全量测试。
- 未进一步添加未证实的问题；上述复现均只运行本地 mock，不向真实服务提交评分。
