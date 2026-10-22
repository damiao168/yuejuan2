# EduGrade 本地对照基线

核对日期：2026-10-18。工作目录为 `D:\project\yuejuan`，读取时 HEAD 为 `242ea44`。本地已有 478 个已跟踪文件处于修改状态；这些修改属于用户现有工作，本轮只新增研究文档，不回退、不覆盖，不将本轮研究描述为代码交付。

## 证据口径

- **L-UI**：本轮浏览器直接看到的本地界面。
- **L-CODE**：本轮读取的当前工作树代码，只证明实现与接线存在。
- **L-DOC**：仓库既有说明，可能早于当前工作树，只作为背景与待核对依据。
- **待核对**：没有足够证据判断，不等于没有实现。

浏览器实际打开 `http://localhost:8088/`，看到 EduGrade 登录页。未提供业务账号，本轮没有进入真实教师会话，也未向外部竞品传输本地文件或答卷。后续的工作台比较主要依据当前源码，不能写成真实教师操作、模型效果或学校现场验收。

## 当前能力与可改进边界

| 编号 | 领域 | 本轮核对到的事实 | 对比时的边界 | 依据 |
| --- | --- | --- | --- | --- |
| L01 | 账号入口 | 学校代码、手机号/教职工号/原账号、密码；账号/微信扫码页签；常用电脑与学校公共电脑选项；忘记密码指向管理员一次性恢复流程；点击微信页签后可见学校代码和生成二维码按钮，并说明五分钟有效、不读取好友聊天信息 | 实际切换了登录页签；没有生成二维码或完成扫码、恢复和跨设备验证 | L-UI；`http://localhost:8088/` |
| L02 | 初次启用 | 组织、学年、年级、班级、学生 CSV 字段映射与错误行、创建业务用户、首场考试已有使用说明 | 向导属于学校管理员流程；教师能否在几分钟内试用完整样例仍待核对 | L-DOC；[启用说明](../../user-guides/organization-onboarding-and-exam-workspace.md) |
| L03 | 考试上下文 | 工作台接受 `initialExamId`；查询域同时包含考试、身份、原图权限；避免旧异步结果回填新上下文 | 已有上下文与并发保护，不能把“考试切换”列为全新功能 | L-CODE；[工作台](../../../apps/web-admin/src/features/grading/workbench/GradingWorkbench.tsx) 前 160 行 |
| L04 | 教师与管理者界面 | 管理者显示处理原因、冻结材料和证据；普通教师用折叠材料与任务抽屉减少信息量 | 已区分岗位；更简洁的默认布局、入口命名仍需用户任务验证 | L-CODE；[任务内容](../../../apps/web-admin/src/features/grading/workbench/components/WorkbenchTaskContent.tsx)、工作台 |
| L05 | 任务队列 | 按考试、学科、题目、来源/状态、我的/全部、未分配过滤；存在分页、整体统计和任务预取接线 | 批量“当前已加载”和“全部匹配”语义已区分，不能再建议只做表头全选 | L-CODE；[队列](../../../apps/web-admin/src/features/grading/workbench/components/TaskRail.tsx) 前 140 行、工作台 |
| L06 | 任务分配 | 分配预览；处理中、已提交、取消及固定双评任务不参与普通改派 | 跨题负载均衡与现场排班效率未据此证明 | L-CODE；队列中的禁用与说明分支 |
| L07 | 答卷阅读 | 答题区域/原图/识别文本切换；原图受独立权限控制；缩放百分比、旋转、适配、拖动、键盘平移 | 缩放平移不是缺失项；原图和批注是否能在同一画布连续工作可继续优化 | L-CODE；[答卷窗格](../../../apps/web-admin/src/features/grading/workbench/components/AnswerEvidencePane.tsx)、[查看器](../../../apps/web-admin/src/features/grading/workbench/hooks/useAnswerViewer.ts) 相关分支 |
| L08 | 原卷批注 | 答卷窗格下有单独折叠的批注工作区；批注可关联评分点，存在常用评语与几何编辑 | 不能说没有原卷批注；当前呈现为额外的图像工作区，可比较竞品同画布联动的价值 | L-CODE；答卷窗格；[批注工作区](../../../apps/web-admin/src/components/features/review-annotation/ReviewAnnotationWorkspace.tsx) 相关检索分支 |
| L09 | 逐点评分 | 共享评分控件按点录入并合计；允许总分人工调整，出现差异时要求说明；本题满分、快捷分与评分点展示 | 作文的多等级维度量表和扣分型 Rubric 不能仅由普通采分点推断已完整支持 | L-CODE；[评分编辑](../../../apps/web-admin/src/features/grading/workbench/components/ScoreEditor.tsx) 前 190 行；[评分控件](../../../apps/web-admin/src/features/grading/SharedScoreControl.tsx) 相关分支 |
| L10 | AI 分数与反馈 | 采用 AI 建议分先预览改分；逐点映射不全时只改总分并提示核对；AI 学生反馈与教师内部备注分开审读和采用 | 已有教师决定权、反馈编辑和替换预览；不能建议直接自动最终定分 | L-CODE；评分编辑 |
| L11 | AI 依据展示 | 建议分、置信度、需人工复核、模拟标记、采分点、缺失点、证据和风险；缺建议时可人工评分；存在证据校验入口 | 显示一个数值不证明置信度已校准；真实可靠性需额外评测 | L-CODE；[证据面板](../../../apps/web-admin/src/features/grading/workbench/components/EvidenceInspector.tsx) |
| L12 | 数学评分联动 | “评分点 × 步骤”矩阵；点步骤可定位答题框；已验证/不成立/待确认；已验证分值、待确认分值和建议区间；建议历史 | 数学联动已存在；跨学科通用联动和更直观的扣分叙事仍可比较，不能把数学功能重复立项 | L-CODE；[数学矩阵](../../../apps/web-admin/src/features/grading/workbench/components/MathRubricMatrix.tsx)、答卷窗格 |
| L13 | 数学事实绑定 | 校正未保存、证据不可用或版本冲突时建议不可采用；重新生成受权限与证据就绪约束；高风险独立评分不从普通入口预填 AI 分 | 界面和代码约束不等于真实模型质量；旧建议失效保护应保留 | L-CODE；数学矩阵、评分编辑、工作台；L-DOC [验证状态](../../verification-status.md) |
| L14 | 快捷键与连续阅卷 | Ctrl/⌘+Enter；可选 Enter 提交；数字键按评分点或总分语境使用；A/F/R/Z、加减缩放；输入框、输入法、重复按键与弹窗阻断保护 | 不是“缺快捷键”；可研究首用提示、个性化与按题保持视图的体验 | L-CODE；[快捷键](../../../apps/web-admin/src/features/grading/workbench/keyboardShortcuts.ts) |
| L15 | 批量快捷复核 | 前 20 份已分配候选；必须看到原图并明确勾选；提交前重新读取任务版本和 AI 建议；部分成功有独立反馈；进入质检而非直接发布 | 仅 AI_FAST_CONFIRM、R1/R2、非数学、无评分点、无风险、完整文本证据、置信度至少 0.95 等条件；不能扩写成所有题型可整批采纳 | L-CODE；[快捷确认](../../../apps/web-admin/src/features/grading/workbench/components/FastConfirmDrawer.tsx) 前 135 行；[准入条件](../../../apps/web-admin/src/features/grading/workbench/fastConfirm.ts) 全文 |
| L16 | 草稿与失败恢复 | 工作台存在草稿保存状态、服务端 revision 冲突、保存队列、未保存离开保护与本地回退接线 | 本轮未完整审读这些 hook，也未实测断网恢复；只记已有接线，不承诺任意断电恢复 | L-CODE；工作台前 160 行导入及状态 |
| L17 | 相似答案分组 | 当前管理界面明确限可可靠文本化的精确文本/短构造题；代表、边界与异常样本抽检；生成可追溯候选；支持回滚 | 不等于手写图像任意语义聚类；增加分组类型需数据和评测，不只改名称 | L-CODE；[分组抽屉](../../../apps/web-admin/src/features/answer-groups/AnswerGroupingDrawer.tsx) 相关分支检索 |
| L18 | 双评仲裁 | 两个独立任务；双盲普通任务不返回另一人评分；按阈值与策略收敛或仲裁；仲裁使用冻结题目与评分标准 | 依据 API 文档，未重跑服务端；竞品可以启发管理进度、差异解释、盲态呈现，不能把双评写成缺失 | L-DOC；[双评仲裁](../../api/arbitration.md) 前 120 行 |
| L19 | 人工质控 | 质量面板包含标定、双评一致、样本量、分组、回标、发布门禁；已有 Gold Papers、Seed、漂移等模块说明 | 结构较全面；样本量不足、负责人处理入口和质量事件闭环可继续研究 | L-CODE；[质量面板](../../../apps/web-admin/src/pages/QualityDashboardViews.tsx) 前 100 行；L-DOC 验证状态 A07–A13 |
| L20 | Rubric 建立 | 步骤划分、拆分合并/撤销、AI 从解答给候选采分点、分值核对、旧草稿替换确认、保存后锁定；高级项已有结构化扣分条件、负分值与评分示例编辑 | 不能把扣分规则或评分示例写成完全没有；可进一步比较作文等级量表、跨题共享模板与带图锚定样例的成熟程度 | L-CODE；[Rubric 助手](../../../apps/web-admin/src/features/paper-rubric/PaperRubricAssistant.tsx) 前 120 行；[评分配置](../../../apps/web-admin/src/features/paper-rubric/PaperScoringConfiguration.tsx) 相关分支 |
| L21 | 答题卡建立和采集 | 已有多页来源、模板候选、人工确认、锁定与考试绑定；后续质量、配准、ROI、条码与 OMR 有能力地图 | 10-16 地图不能覆盖 10-18 新代码；本轮当前代码仍明确一题多区域受限，自动页序不等于验证学生身份 | L-DOC；[能力地图](../../engineering/answer-sheet-capability-map.md)；L-CODE `regionLifecycle.ts`、`orderAnswerSheetSourcePages.ts`、后端 `answer_sheet_page_order.go` 相关检索 |
| L22 | 成绩和分析 | 概览、班级对比、题目、知识点、错因、难度/区分度、选项分布、阅卷质量；缺数据有空状态 | 不是“缺报表”；可研究从统计下钻到原卷/错答/讲评，以及形成教学行动的路径 | L-CODE；[学情页](../../../apps/web-admin/src/pages/LearningReportsPage.tsx) 前 160 行及图表/导出相关分支；L-DOC [报告 API](../../api/reports.md) |
| L23 | 导出 | 报告 API 与当前学情页面提供 CSV，导出有确认、审计和追溯水印 | 不能据此认为没有其他模块的 PDF；当前核对的“学情报告导出”没有发现 PDF/XLSX 路径 | L-CODE；学情页 367–378 行；[报告 API 客户端](../../../apps/web-admin/src/api/reports.ts) 导出相关分支；L-DOC 报告 API |
| L24 | 发布和学生反馈 | 版本化发布、学生逐题查看、只看失分题、教师反馈、申诉窗口/理由、圈选争议区域与重复申请处理 | 学生回执和申诉并非缺失；针对性练习、可打印反馈单、消息通知要单独核对实现 | L-CODE；[学生端](../../../apps/student-portal/src/App.tsx) 相关检索；L-DOC 验证状态 A18–A22 |
| L25 | 桌面离线 | 本机加密草稿、账号绑定、最长七天离线恢复授权、同步队列和续期提示 | 代码明确图片恢复和下载仍需联网，不能宣传完整答卷永久离线；真实设备与断电恢复仍需验收 | L-CODE；[桌面入口](../../../apps/desktop-client/src/App.tsx) 相关检索；L-DOC 验证状态 A23–A26 |
| L26 | 集成 | 本轮在 apps/services/docs/api/packages 的业务代码检索没有发现明确命名的 Google Classroom、Moodle 或 LTI 集成入口 | 仅是限定检索结果；大写 LTI 子串误命中常量不计证据；未全文审读所有接口适配器 | 限定范围文本检索，状态为待核对 |
| L27 | 定价和轻量试用 | 当前本地面向学校部署；登录需要学校上下文；README 有安装、服务账号和模型配置说明 | 没有据此判断商业能力缺失；个人教师试用、免费额度、成本预测的产品入口仍待核对 | L-UI；L-DOC [README](../../../README.md) 前 200 行、product scope |
| L28 | 数据治理与模型能力 | PRD 强调租户隔离、最小权限、审计、受治理模型；验证文档区分代码/自动验证/外部证据，数学功能旗标和真实样本门禁有专门边界 | 本轮没有进行安全评审或重跑门禁；不能用竞争对手营销指标推断本地或竞品真实准确率 | L-DOC；[范围](../../prd/product-scope.md)、验证状态前 130 行 |
| L29 | 个人稍后复查 | 当前工作台已有个人书签入口、可选说明、单独的只读回看；说明与评分反馈分开；个人清单不是评分业务状态 | 当日旧比较报告 A4 已落地，不能再列成待新增功能；提醒、共享等扩展仍需独立需求 | L-CODE；[个人复查](../../../apps/web-admin/src/features/grading/workbench/components/ReviewBookmarks.tsx) 前 85 行；L-DOC [当日设计记录](../../architecture/personal-review-bookmarks-design-2026-10-18.md) 前 115 行 |
| L30 | 模型策略与预算 | 当前管理端契约已有 local_only、shadow_compare、cloud_suggestion、hybrid_escalation、dual_provider_review，文字/图片外发开关、允许部署、每题/整考成本上限和 manual_only/approved_deployment_only 回退 | 是当前类型契约事实；本轮未实测所有服务端执行、预算扣减或供应商调用。面向教师的试改/扩批和成本预估可改善，不应把这些策略重复立项 | L-CODE；[模型治理契约](../../../apps/web-admin/src/api/modelGovernance.ts) 前55行；[改进任务](adoption-backlog.md) 的定向源码阅读表 |

## 本轮实际阅读范围

本轮属于产品对照研究，不是全仓注释整理或代码评审。上表引用的实现有全文阅读、部分阅读和文本检索三种深度；没有全文审读的源码继续视为 **pending**，不能算作整个文件已验收。

| 阅读方式 | 文件/范围 | 状态 |
| --- | --- | --- |
| 全文 | `AGENTS.md`、`docs/engineering/commenting-standard.md`、`docs/prd/product-scope.md`、`apps/teacher-portal/README.md`、`docs/api/reports.md`、`package.json`、`apps/web-admin/package.json` | 研究背景已读；未修改 |
| 全文 | `WorkbenchTaskContent.tsx`、`AnswerEvidencePane.tsx`、`EvidenceInspector.tsx`、`MathRubricMatrix.tsx`、`keyboardShortcuts.ts`、`fastConfirm.ts` | 已读对应组件，未验证所有下游依赖和运行场景 |
| 部分 / pending | `GradingWorkbench.tsx` 前 160 行、`ScoreEditor.tsx` 前 190 行、`TaskRail.tsx` 前 140 行、`FastConfirmDrawer.tsx` 前 135 行、`PaperRubricAssistant.tsx` 前 120 行、`LearningReportsPage.tsx` 前 160 行及导出/图表分支、`QualityDashboardViews.tsx` 前 100 行、`ReviewBookmarks.tsx` 前 85 行、`WorkbenchHeader.tsx` 前 95 行 | 只对已读取部分作产品事实判断 |
| 相关分支检索 / pending | `ReviewAnnotationWorkspace.tsx`、`useAnswerViewer.ts`、`SharedScoreControl.tsx`、`PaperScoringConfiguration.tsx`、`AnswerGroupingDrawer.tsx`、`AnswerGroupSampleEvidence.tsx`、`apps/student-portal/src/App.tsx`、`apps/desktop-client/src/App.tsx`、`reports.ts`、答题卡区域与页序相关源码 | 不构成文件完整审读或缺陷结论 |
| 既有文档 | README 前 200 行、verification-status 前 130 行、机构启用说明、arbitration 前 120 行、answer-sheet-capability-map 前 170 行、个人复查设计前 115 行、当日 AI-Marker-Suite 比较前 155 行 | 文档日期和当前工作树可能不同；不重用历史测试为本轮测试结果 |
| 浏览器 | 本地账号与微信登录页签 DOM；未登录进入业务界面 | 只能确认登录页呈现 |

教师门户 `apps/teacher-portal` 的 README 明确为预留目录，教师业务当前优先放在 Web 管理后台和桌面端。不能因为独立教师门户为空就判断本地没有教师工作台。

## 研究结论的使用方式

后续建议先复用现有考试上下文、冻结评分事实、任务版本、原图授权、人工确认和发布门禁。竞品优势若只是呈现与入口差异，优先改善当前功能；确属新增领域时，再明确数据模型、权限、失败恢复和验收样本。浏览器宣传截图、公开文档和真实操作应分别记录，不能混写成同等证据。
