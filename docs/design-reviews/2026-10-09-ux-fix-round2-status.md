# 2026-10-09 第二轮体验修复

本轮承接 [修复效果复核](2026-10-09-ux-fix-effect-review.md) 确认的 3 项 P0 和 12 项 P1。按 P0 验收后再开始 P1 的顺序执行，每批按互不重叠的文件并行。工作区原有未提交修改保留；本记录只描述本轮增量，不能据此认定全仓文件已经审读。

| 编号 | 优先级 | 用户任务 | 状态 |
| --- | --- | --- | --- |
| F01 | P0 | 相似答案分组切题后保持操作对象一致 | verified；36 项定向测试通过 |
| F02 | P0 | 普通教师从任务权限读取桌面答卷，材料缺失禁止新提交 | verified；142 项桌面定向测试通过 |
| F03 | P0 | 分类规则读失败禁止写入空规则 | verified；25 项题库恢复测试通过 |
| F04 | P1 | 模型绑定范围贯穿正式评分使用检查 | verified；42 项前端测试、web-admin typecheck 通过 |
| F05 | P1 | 题库冲突两侧完整比较答案与评分 | verified；5 项冲突恢复测试通过 |
| F06 | P1 | 本机扫描队列恢复失败同页可重试 | verified；扫描队列恢复与失败重试测试通过 |
| F07 | P1 | 人工边界校正材料读取失败可恢复 | verified；31 项校正工作区测试通过 |
| F08 | P1 | 分区评分保存后保留回执与快照 | verified；24 项评分配置测试通过 |
| F09 | P1 | 回标差异读取失败显示错误且刷新有效 | verified；质量看板定向测试通过 |
| F10 | P1 | 回标与复评选择器区分同名教师 | verified；质量看板定向测试通过 |
| F11 | P1 | 导入答题卡自动读取真实模板 | verified；模板缺失/失效/重读测试通过 |
| F12 | P1 | 扫描队列显示原考试与批次名称 | verified；跨批次名称与权限重读测试通过 |
| F13 | P1 | 回标历史与差异使用题号及中文状态 | verified；质量看板定向测试通过 |
| F14 | P1 | 受派成绩复核教师能辨认考试名称 | verified；46 项前端测试、appeal/server 授权测试通过 |
| F15 | P1 | 教师解析页隐藏技术模型用量 | verified；14 项解析页测试通过 |

本轮没有新增确认的 P2。P0/P1 的“verified”只表示对应问题的修复目标和定向失败路径已验证，不代表真实学校数据库、实体扫描设备或全仓所有文件已经现场验收；未全文审读的关联文件仍按逐文件台账保留 pending。

## 本轮统一门禁

- `npm run typecheck`：通过，用户可见文案检查、desktop、student、web-admin、SDK 均通过。
- `npm run lint`：通过（无 error；仓库既有及本轮相关文件仍有 122 条 warning，未用自动修复改动无关 effect 依赖）。
- `npm --workspace apps/web-admin run build`、`npm --workspace apps/desktop-client run build`：通过；student-portal 随 workspace build 通过。构建仅保留已有大 chunk warning。
- `go test ./internal/appeal ./internal/capture ./internal/server`：通过。
- `node scripts/ci/check-generated-contracts.mjs`、`npm run check:openapi-breaking`、`npm run check:authorization-contract`：通过；SDK 与路由覆盖已重新生成并一致。

逐文件审读状态和每条测试命令保存在本轮外部证据目录 `C:/Users/pc/.codex/visualizations/2026/10/07/01a1155f-d593-7153-9595-8c482e7ddecf/ux-remediation-20261009-round2/`；本文件不把自动枚举当作全仓审读。
