# 考试准备与导入小字实施记录

2026-10-28。MC-E01–26 全部已实施；修改 15 份源码与 6 份对应测试，无 deferred 项。未修改后端、提交、冻结、登记、锁定或失败恢复逻辑。

本分区 12 个相关测试文件共 312 个不同场景通过（含修订文案断言后的定向重跑）。根任务统一执行 typecheck/lint/build；本分区未运行浏览器视觉回归。

| 编号 | 状态 | 实施结果 |
| --- | --- | --- |
| MC-E01 | implemented | 删除创建页两处复述标签的导语；保留示例、必填、范围与错误。 |
| MC-E02 | implemented | 创建方式按真实先后顺序说明创建、上传和方案带入范围。 |
| MC-E03 | implemented | 上传预告集中第4节一次；删除逐科与页脚重复，保留学科和满分。 |
| MC-E04 | implemented | 压缩展开后的组织方式说明，保留逐题策略冻结与教师确认。 |
| MC-E05 | implemented | 方案抽屉保留科目、满分、时长、题目结构及选择后可调整。 |
| MC-E06 | implemented | 标题下按冻结前后只显示一条名单说明，权限和同步/保存禁用不变。 |
| MC-E07 | implemented | 准备标题下按匹配确认、导入、可编辑、只读记录不匹配显示单一状态，保留错误和下一步按钮。 |
| MC-E08 | implemented | 最终确认保留全部冻结对象、在读学籍和不可直接修改；删除导航教程。 |
| MC-E09 | implemented | 仅advisories显示“仅提醒，不影响准备确认”，不修改阻断检查。 |
| MC-E10 | implemented | 文本输入前常显模型发送范围，仅公式帮助折叠；文件上传入口独立常显新增原文/图片发送范围。 |
| MC-E11 | implemented | 上传任务提示要求核对匹配结果；格式列明确docx和多文件，不改accept。 |
| MC-E12 | implemented | 连接异常明确下方是上次结果，保留重查原任务动作。 |
| MC-E13 | implemented | 按真实资料状态压缩详情，保留结构、完整试卷、正式配置和资料登记区别。 |
| MC-E14 | implemented | 结构摘要和最终警告分别说明当前任务与完整性风险，保留计数和阻断。 |
| MC-E15 | implemented | 两个业务节点分别保留完整试卷结构/题目范围勾选，不改校验和代次重置。 |
| MC-E16 | implemented | 首次答题卡上传只展示任务与格式；来源与复用限制放入原生可展开帮助。 |
| MC-E17 | implemented | 页序处理中去掉模型术语，不改条件和进度。 |
| MC-E18 | implemented | 模板库说明和收录成功反馈压缩，保留本场还需确认的边界。 |
| MC-E19 | implemented | 收录确认保留空白来源、无个人信息、逐页完整区域两个勾选；重复信息Alert改为辅助文本。 |
| MC-E20 | implemented | 草稿锁定、未本场使用、自动识别待确认分别说明，不改usageConfirmed。 |
| MC-E21 | implemented | 压缩坐标帮助，保留整页/题区参照系、百分比和一个填涂点，保留原高级折叠。 |
| MC-E22 | implemented | 压缩OMR模式与校准状态文案，保留98%和其他自动确认条件及盲标不可改。 |
| MC-E23 | implemented | 打印和补打分别保留身份条码、每人每页唯一、签发范围、旧卡作废及核验后果。 |
| MC-E24 | implemented | 文件选择前常显每人页数和完整答卷要求；详细规则使用原生details，保留1–20页、顺序和不能跨选择补页。 |
| MC-E25 | implemented | 按取消整批、替代整组、排除整组、名册调整分支说明准确后果，保留登记/处理成功时点和有评分不可移除。 |
| MC-E26 | implemented | 普通说明去重；最终确认保留实时人数、封存和普通导入/补传不同阶段后果。 |

## 关键呈现调整

- 准备与学生范围根据真实冻结/确认状态只显示对应的一条提示；记录不匹配时保持异常说明。
- 粘贴与文件上传入口分别常显模型资料发送范围；只有公式格式帮助折叠。
- 答题卡来源复用和答卷分组规则使用原生 details；普通上传任务、完整答卷要求和每人页数仍常显。
- 收录空白来源、逐页题区、唯一条码、旧卡作废、整组替代和最终冻结/封存后果均保留。

## 验证

- 第一轮测试因旧文案精确断言失败；按新文案含义更新，未删除业务保护断言。
- 第二轮低并发验证：12 文件 307 场景，仅答题卡页 2 处旧文案断言待更新。
- 最终定向重跑：准备页、答题卡页 2 文件 129 场景全部通过；结合其他 10 文件通过结果，共 312 个不同场景通过。
- git diff --check 通过；29 处最终源码锚点全部匹配。
- 初始 pnpm --filter 误匹配输出目录同名包，缺少 vitest，未形成有效验证；随后改为在 apps/web-admin 下直接调用根 vitest，未再递归工作区。

## 修改文件

源码：

- apps/web-admin/src/features/exams/create/CreateExamComposer.tsx
- apps/web-admin/src/pages/ExamPreparationPage.tsx
- apps/web-admin/src/features/paper-import/PaperImportTextModal.tsx
- apps/web-admin/src/features/paper-import/PaperImportWorkspace.tsx
- apps/web-admin/src/features/paper-import/PaperMaterialsConfirmation.tsx
- apps/web-admin/src/features/paper-import/PaperStructurePanel.tsx
- apps/web-admin/src/features/answer-sheet-template/view/TemplateSourceToolbarView.tsx
- apps/web-admin/src/features/answer-sheet-template/library/TemplateLibraryPanel.tsx
- apps/web-admin/src/features/answer-sheet-template/view/TemplateBindingView.tsx
- apps/web-admin/src/features/answer-sheet-template/view/templateViewRules.ts
- apps/web-admin/src/features/answer-sheet-template/view/TemplateRegionSettingsView.tsx
- apps/web-admin/src/features/answer-sheet-template/view/OMRCalibrationView.tsx
- apps/web-admin/src/features/answer-sheet-template/view/StudentPrintingView.tsx
- apps/web-admin/src/features/capture/CaptureBatchPanels.tsx
- apps/web-admin/src/features/capture/CaptureCollectionPanel.tsx

测试：

- apps/web-admin/src/pages/ExamReadinessFreeze.test.tsx
- apps/web-admin/src/features/paper-import/PaperImportWorkspace.test.tsx
- apps/web-admin/src/features/paper-import/PaperMaterialsConfirmation.test.tsx
- apps/web-admin/src/features/answer-sheet-template/answerSheetTemplatePage.batch7.test.tsx
- apps/web-admin/src/features/capture/CaptureCollectionPanel.test.tsx
- apps/web-admin/src/pages/CaptureBatchPage.batch16.test.tsx

准确证据行号、每项测试对应关系和边界见 [JSON](exam-preparation.json)。
