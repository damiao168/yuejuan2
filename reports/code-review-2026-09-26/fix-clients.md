# F06 / F07 客户端修复与验证

验证日期：2026-09-27。此文件记录修复后的结果；原始审查复现记录保留原样。

## F06：迟到的评分提交响应覆盖新任务

`apps/web-admin/src/features/grading/workbench/hooks/useGradingReviewActions.ts` 在服务端确认后清理原用户、原任务的本地草稿；更新队列、查询缓存前核对当前用户。仅当编辑器仍然显示提交的任务时，才清空编辑器、失效图片、切换下一题或打开金标准提名。切换到 B 或 C 后收到 A 的结果，会保留当前上下文、评分草稿和保存状态，同时正确将 A 标记为已提交。

新增 `useGradingReviewActions.test.tsx` 的 5 项 React 生命周期回归覆盖：切到 C、切到既定下一题 B、切换用户、正常完成及提名、离开 A 后禁止迟到分页跳转。

## F07：未保存离线草稿直接同步及本地回执失败

`apps/desktop-client/src/components/OfflineWorkbench.tsx` 在任何服务端检查或提交前，先加密持久化当前任务包和精确评分草稿。保存失败会停止网络操作；离线状态仍保留可重试草稿。浏览器模式无密钥不能同步。

服务端评分结果与本地回执、列表、日志更新分开处理。本地更新失败不会把已确认的服务端成功改写成失败，不会进行第二次失败状态写入，也不会让同步事件返回未处理的 Promise 拒绝。本次会话保存已确认任务集合，配合互斥引用与按钮状态，阻止双击、旧事件回调、同步期间的手工保存/任务替换，以及重新下载已确认任务后的重复提交。

重新加载草稿时同时读取最新 envelope 元数据，以元数据状态覆盖加密负载中的旧状态。测试明确将加密负载和状态元数据分开模拟，验证成功回执落盘后重新挂载、加载草稿仍显示成功并禁止重复提交。若回执本身未能落盘，当前会话的确认记录会阻止重复操作；重启后的提交仍需通过既有的服务端版本和任务状态检查。

补充处理初始化列表、清理过期缓存，以及任务/下载错误后的二次日志失败，避免这些本地存储错误变成未处理的事件拒绝。

新增 `OfflineWorkbench.test.tsx` 共 17 项回归。模拟持久层遵守原生 SQLite 的 update-only 契约，缺少草稿行时更新会失败；不会通过 mock 自动创建缺失行掩盖原问题。

## 已执行验证

所有下列命令均在最终客户端改动后执行，退出码为 0：

| 命令 | 实际结果 | 日志 |
| --- | --- | --- |
| `npm --workspace apps/desktop-client test -- --maxWorkers=1` | 17 个测试文件，68 项通过；含 OfflineWorkbench 17 项；无未处理拒绝报告 | `fix-desktop-tests.log` |
| `npm --workspace apps/web-admin test -- src/features/grading/workbench --maxWorkers=1` | 13 个测试文件，71 项通过；含提交生命周期 5 项 | `fix-web-workbench-tests.log` |
| `npm --workspace apps/desktop-client run typecheck` | 通过 | `fix-desktop-typecheck.log` |
| `npm --workspace apps/web-admin run typecheck` | 通过 | `fix-web-typecheck.log` |
| `git diff --check -- <上述四个生产/测试文件>` | 通过 | 终端退出码 0 |

边界：这是实际 React 组件/动作与模拟 API、模拟持久层的回归验证；本轮未运行真实 Tauri GUI、原生 SQLite/Windows 凭据库或真实后端的端到端提交。不将已有的全仓库构建结果冒充最新客户端改动后的原生集成验证。
