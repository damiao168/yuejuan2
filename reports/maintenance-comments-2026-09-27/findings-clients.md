# 客户端审读发现（本轮不改业务逻辑）

- `apps/desktop-client/src-tauri/src/scanner.rs` 的 `available_disk_bytes` 使用所有文件系统盘中最大的空闲空间，而预检展示为 spool 可用容量。spool 所在盘容量不足、其他盘空间充足时可能误判可采集。根代理已在总发现中登记；本轮仅增加准确说明。
- `apps/web-admin/src/components/model-governance/ManagedGovernanceWorkspace.tsx` 的 `load` 等待租户相关请求后直接更新 `policy` 和表单，没有租户代次或清理检查。如果父组件切学校时复用该组件，旧学校的迟到结果可覆盖新学校界面，随后 `savePolicy` 使用新 `tenantID` 与旧策略版本/表单。需核实父层挂载方式与服务端版本保护；本轮未改。
- `apps/web-admin/src/features/answer-sheet-template/printing/useStudentPrinting.ts` 的签发幂等键签名只包含排序后的学生 ID，模板切换时不清空 `issueRequestRef`。上次签发未确认、切换模板且选择相同学生后可能沿用原键。需核实服务端幂等键作用域及模板切换交互；本轮未改。
- `apps/web-admin/src/features/gold-papers/GoldPaperManagerDrawer.tsx` 的覆盖缺口初值为空，覆盖查询失败也写入空数组；渲染仅判断 `coverageGaps.length`，因此加载中或请求失败仍显示“当前题目标准卷覆盖规则已满足”。建议后续独立区分加载中、查询失败与已确认无缺口；本轮未改。
- `apps/web-admin/src/features/score-management/components/ScoreReleaseWorkspace.tsx` 在 `releaseGate` 为 `null` 时，门禁列表条件落入成功分支，显示“当前发布门禁通过”，而同页状态标签仍为待处理。发布按钮仍禁用，但加载中或无管理权限时展示互相矛盾；应独立处理未知门禁状态，本轮未改。
