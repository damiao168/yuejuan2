# 末次当前源码审读：桌面扫描、离线阅卷、成绩页与 Go 覆盖率

审读结束哈希核对时间：2026-10-10T15:12:25.5713958Z。先读取根 `AGENTS.md` 与 `docs/engineering/commenting-standard.md`，未发现上述路径适用的子目录 AGENTS。仅写本记录和 [final-reviewed-files.json](final-reviewed-files.json)；未改业务文件、图规格、图形、测试、工具指令或运行输入。

[workspace-preservation.json](workspace-preservation.json) 在本次开始时记录 36 个已有文件相对启动基线变化，早一轮 [frontend-current-source-recheck.md](frontend-current-source-recheck.md) 的 30 是当时快照。四个指定源码均全文分段读取，开始与结束 SHA256 相同；不能把目录枚举、截断输出或别组旧版本审读继承为本组完成。变化来源无法仅凭内容归因，本次未回退或覆盖。审计基线只有哈希，因此本记录核对当前事实，不声称已重建所有增量。

## 四个指定源码的读取范围

| 文件 | 全文读取范围（含首尾） | 结束 SHA256 | 状态 |
| --- | --- | --- | --- |
| `apps/desktop-client/src/components/OfflineWorkbench.tsx` | 1–180、181–360、361–540、541–730、731–930、931–1132 | `5c41e3e4d4ee974e8a49bc5c04a7204c168fb69347fd313d2c9674f531fedfa6` | complete，前后稳定 |
| `apps/desktop-client/src/features/scan/ScanWorkspace.tsx` | 1–70、71–140、141–210 | `dee947aedcab5b332ea9dbfcdc1ab42f06018379f9bf186e21deddfc15725454` | complete，前后稳定 |
| `apps/web-admin/src/pages/ScoreManagementPage.tsx` | 1–160、161–320、321–472 | `f5bd3cf4f3d5d590a07561604245bfcbf4fb31919fd04b81deaf2bcc78c8f98f` | complete，前后稳定 |
| `scripts/ci/check-go-coverage.mjs` | 1–89 | `df8909bbfdfc3b8049e003d982d63a3f71022c7de0b438bf93e8337674855a26` | complete，前后稳定 |

complete 表示这份当前源码的全文与本次架构事实审读完成，不表示运行测试、生产或硬件验收完成。四个文件的现有注释说明了本次涉及的非直观边界，无源码注释变更。

## 当前实现与边界

### 离线阅卷

`apps/desktop-client/src/components/OfflineWorkbench.tsx:150–180` 读取当前用户的实际任务；无 token/user 时清空列表并显示不能读取，未创建假任务。`:973–1014` 的任务包只从 `getReviewTaskContext` 映射冻结题目、frozen_rubric、原答卷文字和受控图片地址，不回查当前题库、提交片段或文件管理接口。`:940–945` 校验任务、题目和作答片段归属；冻结细则缺失仍显示缺失警告，不伪造可评分细则。

`:266–288` 的恢复图片只读取同任务上下文或其受控地址；`:403–404` 加载草稿后清掉 Blob 图片。`:115–117` 和 `:490–492` 以当前任务的实际可读图片/答卷文字阻止无材料的首次提交；`:485–486` 允许已保存原命令先核对回执。图片仅在本次会话显示，不随加密草稿恢复，不能把图 08 的本机 Spool 节点解释为离线阅卷图片已永久缓存。

`:554–583` 创建稳定 command ID，携带下载任务的 expected_revision、人工分数、采分点和文字，先加密保存原命令，再进行联网同步。`:473–475` 保留原命令对应包；`:587–598` 服务端已确认结果保留在内存，只有明确未接受/拒绝才解除原命令冻结。`:1017–1046` 的预检检测任务 revision、结束状态、分派、冻结材料来源与 rubric version；预检不是数据库锁，正式提交仍交给服务端版本校验。请求代次与本次会话检查见 `:140–148`、`:541–543`，仅约束旧回调对当前 UI 的影响。

`:948–966` 只投影任务上下文允许预填的已有 AI 第二意见；R3/HUMAN_PRIMARY 或未获预填许可不生成可采用分数。`:849–853` 的展开按钮只显示辅助意见；`:1083–1091` 初始人工总分与反馈为空，`:917–918` 显式确认后才采用已有建议。该文件没有创建 A/B/C Panel 或自动公开成绩。

### 扫描工作站

`apps/desktop-client/src/features/scan/ScanWorkspace.tsx:39–53` 从真实 exams/captureBatches 取得当前考试和批次，只有相同考试/批次的 ready 模板、已恢复 ready 队列和 150–1200 整数 DPI 才放行新导入。`:62–70` 选择文件并调用现有队列和上传动作；当前批次上传限 pending/failed。`:75–88` 展示实际恢复/导入结果，恢复未成功不会显示成空队列；全账号批次上传需明确确认。`:173–177` 将 loading/error/locked 的空态与 ready 区分。

`:118–128` 读取本批实际模板布局并说明布局不是打印原件。`:146–160` 把设备检查作为可选路径，界面明确 WIA 用于发现 Windows 设备、直接采集尚未启用；文件导入不要求 WIA，不能据此宣称真实设备采集已经验收。`:91–104`、`:194–209` 将服务端页数完整性、图像质量与答卷匹配分别显示，上传确认不等于形成答卷或质量通过。

补充定位的 `useScanQueue.ts:169–198` 也检查真实考试、可继续批次、当前 ready 模板和纸张/DPI，再保留本次导入上下文；`:202–237` 在 Spool 成功后才保留句柄与队列项，`:284–330` 逐件检查并在新原件入队后才尝试移除旧件。此依赖只做选定段落定位，不计入本组全文完成。

### 成绩管理

`apps/web-admin/src/pages/ScoreManagementPage.tsx:199–221` 同时使用旧 quality/grades 与 releaseGate/scoreReleases/currentPublishedRelease。当前门户版本取服务端当前指针，不能按最高创建序号推断最后发布版本。`:213–214` 数据重读未成功或已有动作进行中时禁用写入；这个前端条件不能替代服务端门禁。

旧动作 `:274–305` 检查旧 `quality.can_publish` 与发布原因，再经 `:297–300` 的 runWithStepUp 调用 `publishExamGrades`。补充定位 `apps/web-admin/src/api/scores.ts:170–175` 仍发送 POST `/api/v1/exams/{examId}/publish`，带稳定业务命令键。`ScoreManagementPage.tsx:423–436` 的新 ScoreReleaseWorkspace 与 `:450–457` 的旧 ScoreActionsPanel 同时存在；`:378–388` 也分别展示基础成绩与学生门户发布状态。

新工作流补充定位 `useScoreReleaseWorkflow.tsx:91–96` 创建带可见策略的草稿，`:104–134` 检查新 releaseGate 并经近期认证委托 `publishScoreRelease(release.id)`。两个入口并存没有因当前成绩页变化而被统一；本组未重新审读发布事务或学生后端，不独立扩大或取消 [pipeline-audit.md](pipeline-audit.md) 第 8 节的旧发布门禁缺口结论。

### Go 覆盖率检查

`scripts/ci/check-go-coverage.mjs:11–34` 解析 ok 包前缀和当前内容行最后一个 coverage 标记，只有合法数值后缀才纳入结果；`:36–47` 的预算包无结果或低于门槛都失败。`:60–72` 使用 resolveTrustedExecutable 定位 Go，传 `test -cover -count=1`，`shell:false` 与 `GOTOOLCHAIN:local`；`:75–86` 处理运行错误、非零退出与预算失败。当前文件职责仍是 CI 覆盖率门禁，无新增业务 API、存储通道或部署组件；未据此宣称所有工具路径验证均正确，resolver 和 coverage-floor 未全文复核。

## 与图 01、图 08 和流水线证据的对应

| 已核对规格节点 | 当前依据 | 本次有限结论 |
| --- | --- | --- |
| 图 01：F05.02、F10.01 / F10-boundary | ScanWorkspace 当前模板/队列/质量门禁及设备能力说明 | 实际文件导入与持久上传队列归属不变；不能增加硬件已验收、假考试、未知模板自动建模等事实。 |
| 图 01：F10.02；图 08：files-workers/local、download | OfflineWorkbench 冻结上下文、受控图像、先持久化命令与回执恢复 | 任务包/加密草稿与同步边界不变；Blob 图片会话限制继续成立。 |
| 图 01：F08.01.features、F08-boundary；图 08：grading-release/row2-api、row2-handler、row2-data | 旧 publishExamGrades 与新 release 工作区并存 | 两套发布路径与当前指针事实仍成立；不能改写为全部发布统一走新版 gate。 |
| 图 01：F07.03.features、F07-boundary；图 08：评分人工提交链 | 仅使用服务端既有意见、人工总分和 review command | 这四个文件没有新增默认生产 Panel 接入或直接公开 AI 分数的通道；后端 adapter/Panel 边界继续依赖已审读后端证据。 |
| 图 08：capture-ocr/row0-page、row0-api 及 api-overview/desktop | 现有采集 hook、scan-context 与任务 API 包装 | 页面→现有 API→后端的调用方向不变。本组未重新枚举全部 API 或核验全图统计。 |

本次没有发现需修改图 01 / 图 08 节点事实或核心调用方向的证据，也没有发现需改变 [pipeline-current-ui-subread.md](pipeline-current-ui-subread.md) 所限定的冻结、人工租约、Panel 与后端发布边界的证据。该结论只覆盖四个当前源码和上述定位段，不是其它全部源码或图页再次审读完成。

## 依赖与未审读测试

依赖定位范围单列在 JSON 的 `supporting_callsite_reads`：`api/review.ts` 1–72、`offlineReviewCommand.ts` 1–71（全文用于有限调用定位，未结合全部依赖/测试完成模块审读）；`useScanQueue.ts` 164–238、284–331；`api/exams.ts` 64–69；Web `api/scores.ts` 156–180、`useScoreReleaseWorkflow.tsx` 69–137、`api/scoreReleases.ts` 45–56。局部架构分析均保持 partial/pending，不覆盖别组有效全文记录。

以下三项本组未人工审读，均为 pending：`apps/desktop-client/src/components/OfflineWorkbench.test.tsx`、`apps/desktop-client/src/features/scan/ScanWorkspace.batch34.test.tsx`、`scripts/ci/go-coverage.test.mjs`。它们的当前哈希只用于盘点，不能表示测试已理解或已运行。Go 覆盖率测试在本组期间继续变化：早一次核对仍等于启动哈希，结束核对已不同，因此开始时的 36 项保存快照不能当作实时全仓变化数；变化不归因作者。

未运行 Vitest、Go 测试、浏览器、Tauri、数据库联调、供应商或扫描硬件验收。规格抽取的两次合并工具输出曾截断，已对本次需要的完整节点标签和调用段单独重读；截断内容未作为源码完成依据。

## 交付前指纹复核

全仓指纹截止于 2026-10-10T15:24:00.314645+00:00：原始 3396 个文件中有 39 项相对启动基线变化，HEAD 未变。四个全文审读源码在 2026-10-10T15:26:14.609810+00:00 再次核对，SHA256 全部仍与上表一致；这里只续认同一源码版本，不虚构第二次全文审读。

末次观察的 `.github/workflows/ci.yml`、`scripts/ci/lint-workflows.mjs` 和 `apps/web-admin/src/pages/LearningReportsPage.tsx` 当前版本均未重读，记为 pending。学习报告页原 complete 声明和 CI YAML 原 partial 声明的 SHA 已失效，已保留旧 SHA 与旧状态并降级，不计入当前完成项。所有变化来源仍未归因，未回退或覆盖。截止后的并行变动不属于该快照证明范围。

