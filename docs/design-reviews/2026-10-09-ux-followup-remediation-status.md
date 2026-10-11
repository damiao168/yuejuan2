# UX 复核后 17 项问题修复记录

本轮修复 2026-10-08 复核确认的 17 项残留或新增问题，按 P0 → P1 → P2 分批、批内并行。此前未提交修改保留。17 项（P0 4 项、P1 10 项、P2 3 项）均通过定向验证与最终集成检查。这里记录本轮修复，不把它表述为一次新的全仓审查。

| 优先级／编号 | 角色、页面与路由 | 修复后的操作和反馈 | 主要文件 |
| --- | --- | --- | --- |
| P0 · T03 | 考试负责人／资料核对；`#/admin/exams/:examId/questions`、`paper` | 保存同时核对资料代次和内容版本；另一人已保存时保留本地输入，读取最新结果、比较后由用户重新保存。 | [usePaperImportWorkflow.ts](../../apps/web-admin/src/features/paper-import/usePaperImportWorkflow.ts)、[paper_import_postgres.go](../../services/api-gateway/internal/paper/paper_import_postgres.go) |
| P0 · NEW-S01 | 受派教师／成绩复核；`#/teacher/appeals` | 可提交支持调整、不支持调整或补证的私有意见；显示已移交管理员。意见本身不改分、不结案，学生端不公开教师意见和身份。 | [QuestionAppealReviewOpinionPanel.tsx](../../apps/web-admin/src/components/QuestionAppealReviewOpinionPanel.tsx)、[published_question_opinion.go](../../services/api-gateway/internal/appeal/published_question_opinion.go) |
| P0 · D07-R1 | 离线阅卷教师；`workspace=offline` | 到期后在当前编辑器同身份联网续期，失败保留输入；续期成功后用新授权保存，不要求先放弃评分。 | [App.tsx](../../apps/desktop-client/src/App.tsx)、[useDesktopSession.ts](../../apps/desktop-client/src/features/session/useDesktopSession.ts) |
| P0 · DESKTOP-N1 | 扫描员／质量检查；`workspace=scan` | 切换考试、批次或答卷清除旧选择和结果；请求携带完整上下文及代次，迟到结果不能串到新对象。 | [useExamCaptureContext.ts](../../apps/desktop-client/src/features/capture/useExamCaptureContext.ts)、[useSubmissionQuality.ts](../../apps/desktop-client/src/features/quality/useSubmissionQuality.ts) |
| P1 · T01 | 考试负责人／题目与评分细则；`#/admin/exams/:examId/questions` | 保存成功但刷新失败时保留回执与内容；“重新读取保存结果”仅执行读取，不重复保存，也不覆盖后续编辑。 | [PaperRubricPage.tsx](../../apps/web-admin/src/pages/PaperRubricPage.tsx)、[PaperQuestionDetail.tsx](../../apps/web-admin/src/features/paper-rubric/PaperQuestionDetail.tsx) |
| P1 · S12 | 学生／文字反馈；`#/exams/:examId` | 按钮滚动并聚焦本页题目区，保持当前成绩详情路由；提交期间继续保护页面。 | [App.tsx](../../apps/student-portal/src/App.tsx)、[PaperViewer.tsx](../../apps/student-portal/src/features/questions/PaperViewer.tsx) |
| P1 · D08-R1 | 教师／答卷阅读；`workspace=offline` | 按旋转后的实际尺寸占位，适合窗口和缩放保留方向，四边均可滚动到达。 | [AnswerDocumentViewer.tsx](../../apps/desktop-client/src/components/AnswerDocumentViewer.tsx)、[styles.css](../../apps/desktop-client/src/styles.css) |
| P1 · D14-R1 | 扫描员／同步队列；`workspace=sync` → `scan` | 打开原考试和原批次的真实详情；结束批次可看结果但禁止导入上传，读取失败不跳到其他批次。 | [App.tsx](../../apps/desktop-client/src/App.tsx)、[useExamCaptureContext.ts](../../apps/desktop-client/src/features/capture/useExamCaptureContext.ts) |
| P1 · R01-R1 | 教师、管理员／全局待确认操作 | 从真实任务响应显示考试、题号与匿名答卷号；成功后移除待办仍保留业务对象和可进入的入口。 | [PendingBusinessCommands.tsx](../../apps/web-admin/src/components/PendingBusinessCommands.tsx) |
| P1 · R19 | 题库管理者／成员授权；`#/admin/question-bank` | 姓名与账号统一显示在选项和授权确认；同名且缺账号时禁止猜选，提示重新读取。普通读者不额外获得成员账号。 | [questionBankMembers.ts](../../apps/web-admin/src/features/question-bank/questionBankMembers.ts)、[members.go](../../services/api-gateway/internal/questionbank/members.go) |
| P1 · DESKTOP-N2 | 教师、扫描员／处理状态；`workspace=scan` | “检查页数完整性”与真实图像质量分别展示；各状态说明等待、人工核对或重试。页数通过不表示图像质量通过。 | [ScanWorkspace.tsx](../../apps/desktop-client/src/features/scan/ScanWorkspace.tsx)、[submissions.ts](../../apps/desktop-client/src/api/submissions.ts) |
| P1 · I06-R1 | 考试负责人／质量总览；`#/admin/exams/:examId/quality` | 事件与来源选项使用题号、中文原因、教师姓名／账号、时间；目录失败不隐藏事件，提示资料暂不可用。 | [QualityDashboardPage.tsx](../../apps/web-admin/src/pages/QualityDashboardPage.tsx) |
| P1 · I09-R1 | 考试负责人／相似答案抽检；考试质量页、`#/admin/arbitration` | 拒绝后显示真实已有人工任务、状态与责任；有读取权限时可进入任务。没有任务则明确请管理员安排，本次拒绝不自动创建任务。 | [AnswerGroupManualHandoff.tsx](../../apps/web-admin/src/features/answer-groups/AnswerGroupManualHandoff.tsx)、[member_context.go](../../services/api-gateway/internal/answergroup/member_context.go) |
| P1 · I10-R1 | 考试负责人／处理异常；`#/admin/exams/:examId/processing`、`#/admin/capture` | 列表显示同校处理人姓名／账号、本考试考号与页码；区分未指派与已指派但资料不可用，不另取完整人员目录。 | [ProcessingOperationsPanel.tsx](../../apps/web-admin/src/components/ProcessingOperationsPanel.tsx)、[store_postgres.go](../../services/api-gateway/internal/processing/store_postgres.go) |
| P2 · D10-R1 | 教师／同步冲突；`workspace=offline` | 五种不可提交终态统一使用中文，说明核对服务端评分或联系管理员；加密草稿保留，原版本、身份和提交检查不变。 | [OfflineWorkbench.tsx](../../apps/desktop-client/src/components/OfflineWorkbench.tsx)、[statusLabels.ts](../../apps/desktop-client/src/statusLabels.ts) |
| P2 · R27 | 考试负责人／解析进度；`#/admin/exams/:examId/paper` | 断线说明“当前任务结果尚未确认”和最后读取状态，重查原任务，不断言远端结果没有变化。 | [PaperImportWorkspace.tsx](../../apps/web-admin/src/features/paper-import/PaperImportWorkspace.tsx) |
| P2 · S06 | 学生／逐题均分、中位分与作答详情；`#/exams/:examId` | 显示本题实际参比人数和当前成绩版本范围，不挪用全卷／班级人数；保留未公开、小样本和缺失原因。 | [QuestionCohortScope.tsx](../../apps/student-portal/src/features/questions/QuestionCohortScope.tsx)、[AnswerReview.tsx](../../apps/student-portal/src/features/questions/AnswerReview.tsx) |

## 验证与交付边界

- 最终全量 Web 1962/1962、学生端 198/198、桌面前端 304/304，合计 2464/2464。Web 首轮唯一失败为历史测试任务缺少新增 CAS 所需的 `updated_at`；补真实时间及参数断言后，定向 7/7 和全量重跑均通过，未修改生产校验。重跑不累加为独立用例。
- 最终全工作区 lint、类型检查、生产构建通过。lint 保留 112 条非阻塞警告，构建保留包体积提示；最后测试数据调整另经单文件 lint。
- 后端 paper、appeal、answergroup、processing、questionbank、apicontract 六包通过，799 条测试通过记录含子测试。默认跳过的 Node 共享客户端契约已显式启用并单独通过。OpenAPI／SDK 生成一致性、接口兼容性与授权契约门禁通过。
- 真实 Chromium 使用生产组件及样式检查 0/90/180/270° × 100/200/400% 共 12 组答卷几何，四边可达并目视截图；资料冲突比较也经真实 AntD 浏览器交互验证。接口使用模拟数据，不等同生产多角色端到端验收。
- 本轮数据库回归使用内存或脚本驱动，未执行真实 PostgreSQL 锁竞争验收；未验收原生扫描仪、打包窗口与凭据库完整冷启动。没有部署、提交或推送。

接口变更和前后端一起发布的要求见[接口说明](2026-10-09-ux-followup-contract-notes.md)。新增必填保存版本是有意收紧契约，旧页面需要刷新，不能为了兼容旧客户端放开无版本写入。

逐文件实际审读和注释维护见[本轮文件台账](2026-10-09-ux-followup-file-status.csv)。大型共享文件仅审读相关段的记录保持 `pending`；生成产物仅记录正式生成及相关差异核对。功能验证通过与整文件审读分别记录，不用自动枚举替代人工审读。前一轮 82 项的历史实现记录见[原修复记录](2026-10-08-ux-remediation-status.md)。
