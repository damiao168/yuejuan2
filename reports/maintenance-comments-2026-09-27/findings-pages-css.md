# 页面审读中发现的独立问题

本批仅补维护注释，以下问题未改业务逻辑。

## P1：申诉详情乱序返回可能让提交目标与高亮记录不一致

- 文件：`apps/web-admin/src/pages/AppealCenterPage.tsx`，依赖 `selectedId` 的详情加载 effect。
- 触发：先选 A，再选 B，令 `getAppeal(A)` 比 `getAppeal(B)` 更晚返回。该 effect 没有请求序号、当前 ID 校验或 cleanup。
- 结果：列表仍高亮 B，但较迟的 A 回包覆盖 `selected`、分派人、建议与分数。重新分派、教师建议和管理员结论均通过 `selected.id` 提交，会处理 A。
- 依据：静态追踪请求回调和三个提交函数；尚未运行延迟网络复现。
- 建议：详情响应与错误/loading 都按请求代次和当前选择隔离，加载新记录时清空旧详情，并为提交再次核验目标。

## P1：仲裁切换任务期间仍能提交上一任务

- 文件：`apps/web-admin/src/pages/ArbitrationPage.tsx`。
- 触发：A 详情已载入，点击 B；`loadDetail` 设置 loading 但未清空 `detail` / `draft`。侧栏标题立即使用 `selectedTask` 的 B，输入及提交按钮仅按 `detail` 和 submitted 状态禁用，没有考虑 `detailLoading` 或 `selectedTaskId !== detail.task.id`。
- 结果：在 B 详情返回前，仍可把 A 草稿通过 `detail.task.id` 提交给 A。已有请求序号只保护响应乱序，不保护这个加载窗口。
- 依据：静态追踪，尚未运行 UI 复现。建议切换时清空草稿并禁止旧详情操作，提交时再次检查目标一致。

## P2：未填仲裁分数被转换为 0

- 同文件 `submitDecision` 先执行 `Number(draft.finalScore)`，草稿初值为 null；`Number(null) === 0`，随后有限数和区间校验均通过。
- 填写说明但未填写分数时会提交 0 分，建议在转换前显式拒绝 null。本批未改逻辑。
