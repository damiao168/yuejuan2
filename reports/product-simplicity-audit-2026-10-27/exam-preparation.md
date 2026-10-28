# 考试准备、试卷配置、答题卡与答卷导入的精简审计

日期：2026-10-27。审计方式：按教师首次创建考试、补齐资料、配置评分、制作答题卡、上传学生答卷的任务路径阅读当前源码。本文记录静态核实的设计问题，未在浏览器中点击，也未使用外部搜索。优先级是体验整改次序，不等同于代码缺陷严重级别。未修改业务代码或已有工作区文件。

补审记录：在首轮报告后，继续完整审读全仓清单中分配给本智能体的 44 个未完成 TSX 文件。新增 EX-25 至 EX-33；撤回正常上传需手动创建模板的早期判断 EX-16。源代码未修改。逐文件状态同时更新；没有发现新增问题的文件也按实际全文阅读结果记录。

逐文件实际阅读范围见 [exam-preparation-files.json](./exam-preparation-files.json)。`reviewed` 表示该文件全文已读且已核对相关展示/调用；`pending` 包括仅搜索命中、仅读片段和未读文件，不能计入完成。这里的阅读状态只描述本次体验审计，不代表完成该文件所有技术或注释审计。

## 先处理的六项

1. EX-07：「保存本题全部修改」与页面真实保存范围不一致。
2. EX-08：数值题误差有两个入口、两套保存对象、两种单位。
3. EX-17：答题卡的最终确认藏在「更多操作 → 模板设置」里。
4. EX-20：「导入答卷」先要求给技术批次命名和选采集方式，才能选文件。
5. EX-21：普通文件上传登记后仍要求另点「开始处理」。
6. EX-22：要求教师预先排列文件，却缺少上传前的可见页序/学生分组预览。

## 逐项发现

### EX-01 · P2 · 创建之前先选择两种实际很接近的创建方式

- **用户场景与现状**：教师要建一场考试，先遇到「从试卷资料创建」「快速创建」「使用考试方案」。前两种使用同一个完整表单；提交时非模板模式的试卷分区均为空，主要前端区别是创建后分别进入资料页、学生范围页。用户必须先理解产品内部的入口差别，才能开始共同任务。
- **证据**：[CreateExamComposer.tsx:22](../../apps/web-admin/src/features/exams/create/CreateExamComposer.tsx#L22)、同文件 147–159；[CreateExamPage.tsx:19](../../apps/web-admin/src/features/exams/create/CreateExamPage.tsx#L19)、同文件 176–186；[createExamValidation.ts:40](../../apps/web-admin/src/features/exams/create/createExamValidation.ts#L40)。
- **建议**：默认直接创建；「套用方案」作为可选快捷入口。创建完成后提供「上传资料」及「稍后继续」，把区别放到真正发生分叉的位置。此结论不表示服务端两个模式完全等价，合并实现前应核对后端对 `creation_mode` 的业务约束。
- **核实**：静态核实；首次创建时的实际主观负担待用户走查验证。

### EX-02 · P3 · 创建表单里的“第 4 步”没有任何可执行动作

- **用户场景与现状**：「4. 分学科导入考试资料」实际只展示已经选过的学科和分值，每行重复「创建后上传试卷与答案」。教师滚过一整块，看起来像尚未完成的步骤，但此时不能上传。空状态还将第 2 节写成「第 2 题」。
- **证据**：[CreateExamComposer.tsx:227](../../apps/web-admin/src/features/exams/create/CreateExamComposer.tsx#L227)，229、251–252。
- **建议**：移除该编号分节，在提交按钮旁保留一句「创建后上传试卷与答案」。若确实要成为一步，就把真实上传控件放进来。修正文案「第 2 步」。
- **核实**：静态核实。

### EX-03 · P3 · 成绩发布下拉框只有一个选项

- **用户场景与现状**：展开高级设置后，「成绩发布」是可点击下拉框，但只有「阅卷完成并由管理员确认后发布」；折叠标题已经重复显示管理员确认后发布。
- **证据**：[presentation.ts:12](../../apps/web-admin/src/features/exams/create/presentation.ts#L12)；[CreateExamComposer.tsx:234](../../apps/web-admin/src/features/exams/create/CreateExamComposer.tsx#L234)、243。
- **建议**：改成说明文字「成绩由管理员确认后发布」，保留既有服务端策略，消除没有选择意义的控件。
- **核实**：静态核实。

### EX-04 · P2 · 同一个阅卷方式被拆成两组单选

- **用户场景与现状**：「阅卷方式」列出三种选项，「更多阅卷方式」又列出双评、盲双评；两组 Radio.Group 操作同一个 `draft.gradingMode`。选中后另一组看起来未选，用户要自行理解它们互斥。同时创建页的组织方式、逐题判分方式又是不同层级。
- **证据**：[CreateExamComposer.tsx:239](../../apps/web-admin/src/features/exams/create/CreateExamComposer.tsx#L239)–245；[presentation.ts:4](../../apps/web-admin/src/features/exams/create/presentation.ts#L4)；[PaperScoringConfiguration.tsx:31](../../apps/web-admin/src/features/paper-rubric/PaperScoringConfiguration.tsx#L31)。
- **建议**：用一组选择表达互斥关系；默认显示教师常用方式，双评作为同组的展开选项。用「阅卷组织方式」与「本题判分方式」区分层级，避免让人以为选过一次又要重复选择。
- **核实**：静态核实。两组实际共用同一值、逻辑互斥，不存在同时选中两个模式的结论；本项仅评价同一决策被拆成两块的理解成本。没有建议删除双评、盲评及其权限边界。

### EX-05 · P3 · “考试时间”实际要求填写时长

- **用户场景与现状**：创建页每科字段叫「考试时间」，实际输入分钟且校验 `durationMinutes`。教师容易以为还要安排考试日期、开始时间。
- **证据**：[CreateExamComposer.tsx:101](../../apps/web-admin/src/features/exams/create/CreateExamComposer.tsx#L101)–103；[createExamValidation.ts:33](../../apps/web-admin/src/features/exams/create/createExamValidation.ts#L33)。
- **建议**：直接写「考试时长」，保留分钟单位。
- **核实**：静态核实。

### EX-06 · P2 · 保存之后仍需手工触发另一套“检查”界面

- **用户场景与现状**：题目工具有「检查题目配置」按钮，点击打开抽屉并 GET readiness；抽屉内还可「重新检查」。页头五步导航也读取同一个 readiness，最终又有准备检查页。教师要理解“已保存、检查通过、准备确认”的区别，但其中只读检查不必都由人触发。
- **证据**：[PaperQuestionConfirmation.tsx:69](../../apps/web-admin/src/features/paper-rubric/PaperQuestionConfirmation.tsx#L69)–83、97–108；[ExamWorkspaceLayout.tsx:79](../../apps/web-admin/src/features/exams/workspace/ExamWorkspaceLayout.tsx#L79)–96；[PaperRubricView.tsx:149](../../apps/web-admin/src/features/paper-rubric/PaperRubricView.tsx#L149)–155。
- **建议**：保存并回读成功后自动更新检查结果；将问题清单并入页头状态和题目待办。保留失败时的重试及最终准备确认，不能把只读检查通过等同于用户已经确认冻结。
- **核实**：静态核实。

### EX-07 · P1 · “保存本题全部修改”没有涵盖同一页的全部设置

- **用户场景与现状**：题目页同时提供「保存题目与答案」「保存本题全部修改」，评分页再提供规则/细则草稿与发布/锁定。同一评分标签内的「判分方式」拥有独立「保存判分方式」，参考图也有「保存参考图清单」。`saveAllQuestionChanges` 实际只写题目、rubric 和固定评分规则，不包含这两者。用户无法从“全部”判断还有哪些本题修改尚未保存。
- **证据**：[PaperQuestionDetail.tsx:90](../../apps/web-admin/src/features/paper-rubric/PaperQuestionDetail.tsx#L90)、98–120；[PaperRubricPage.tsx:430](../../apps/web-admin/src/pages/PaperRubricPage.tsx#L430)–464；[AssessmentProfileEditor.tsx:555](../../apps/web-admin/src/components/features/assessment/AssessmentProfileEditor.tsx#L555)；[ReferenceMaterialEditor.tsx:158](../../apps/web-admin/src/features/paper-rubric/ReferenceMaterialEditor.tsx#L158)。
- **建议**：明确一个本题草稿保存范围，并统一展示每个区域的未保存状态；不能统一写入的区域要改名和明确标注独立保存。把正式发布/锁定保留为单独的显式动作，草稿保存成功后再引导确认。
- **核实**：保存范围及文案不一致已静态核实；切题时是否实际丢失判分方式草稿，需浏览器验证，本文不将其当作已复现的数据丢失缺陷。

### EX-08 · P1 · 数值题误差设置重复，且相对误差单位不同

- **用户场景与现状**：「题目与答案 → 高级答案设置」可填写绝对误差及相对误差（%）；「评分配置 → 固定判分规则」又填写绝对误差及相对误差，但后者取值 0–1，没有百分比单位。前者写 `answer_key.tolerance`，后者写独立的 `scoring_rule.config`。同一道题的同名规则可分别编辑和保存，页面没有告诉教师以哪一份为准。
- **证据**：[PaperQuestionForm.tsx:28](../../apps/web-admin/src/features/paper-rubric/PaperQuestionForm.tsx#L28)–31；[PaperScoringConfiguration.tsx:82](../../apps/web-admin/src/features/paper-rubric/PaperScoringConfiguration.tsx#L82)–85；[useQuestionEditor.ts:96](../../apps/web-admin/src/features/paper-rubric/hooks/useQuestionEditor.ts#L96)–99、163–166；[useScoringRules.ts:22](../../apps/web-admin/src/features/paper-import/useScoringRules.ts#L22)–26、111–128。
- **建议**：只保留一个权威误差编辑入口，统一为百分比并显示示例，如「允许误差 1%」。历史答案容差与正式规则的继承/优先关系由系统处理并可查看，不让教师同步两份。
- **核实**：双入口、保存对象和单位差异已静态核实。后端 [store_postgres.go:47](../../services/api-gateway/internal/grading/store_postgres.go#L47)–79 使用已发布规则整体替代答案容差；冻结运行则在 [scoring_run_rules_snapshot.go:139](../../services/api-gateway/internal/grading/scoring_run_rules_snapshot.go#L139)–151 先继承答案容差，再由冻结规则的同名字段覆盖。两种路径均存在规则覆盖答案容差的实际优先关系，页面没有表达。仍未运行具体数值用例，不能直接据此断言错分。

### EX-09 · P2 · 点击“来源”后主要看到的是来源计数和另一页入口

- **用户场景与现状**：题目页按钮叫「来源与答题区域」，打开后只说明是否由资料导入、有多少来源引用、应去模板框选；并未提供已经记录的来源原文或页图，唯一跳转是答题卡模板。用户为了核对题干，需要关闭抽屉再回资料页找来源。
- **证据**：[PaperQuestionDetail.tsx:85](../../apps/web-admin/src/features/paper-rubric/PaperQuestionDetail.tsx#L85)；[PaperRubricView.tsx:159](../../apps/web-admin/src/features/paper-rubric/PaperRubricView.tsx#L159)–169。资料侧已有可用来源打开能力：[PaperImportSourceList.tsx:21](../../apps/web-admin/src/features/paper-import/PaperImportSourceList.tsx#L21)、[usePaperImportMaterials.ts:198](../../apps/web-admin/src/features/paper-import/usePaperImportMaterials.ts#L198)。
- **建议**：在本题抽屉直接展示/打开真实引用页并定位；答题区域改为一句当前状态加「编辑区域」。去掉只有计数、不能核对内容的中间界面。
- **核实**：静态核实当前抽屉内容。

### EX-10 · P2 · AI 配分有多层草稿和相近“分段/分值”路径

- **用户场景与现状**：用户可在「按解答步骤配分」「AI 建议分值划分」之间切换；手工步骤内又有「让 AI 建议采分段」。生成后要区分“采用分段但不采用分值”“采用建议到评分细则”“应用后再保存”“保存后锁定”。这些动作各有真实数据边界，但把中间处理阶段都变成用户需要管理的工作。
- **证据**：[PaperRubricAssistant.tsx:119](../../apps/web-admin/src/features/paper-rubric/PaperRubricAssistant.tsx#L119)–146、182–191、264–285；[PaperScoringConfiguration.tsx:98](../../apps/web-admin/src/features/paper-rubric/PaperScoringConfiguration.tsx#L98)–116。
- **建议**：默认呈现一份可编辑采分点草稿；「AI 建议」把结果展示为同一编辑区内可接受/撤销的差异。解答单元拆合放入高级修正；教师仍需核对总分和显式锁定，不自动采纳模型输出。
- **核实**：静态核实；采用建议后的实际点击数需按有无已有草稿分别体验。

### EX-11 · P2 · 题目问题分散在筛选、待处理抽屉和检查抽屉

- **用户场景与现状**：左侧有「待补答案」「待确认细则」筛选与色点；顶部另有「待处理题目」抽屉，检查按钮再打开一份按检查项组织的问题。这些有不同覆盖范围，但教师需要记住从哪个入口找到哪类问题。
- **证据**：[PaperQuestionIndex.tsx:21](../../apps/web-admin/src/features/paper-rubric/PaperQuestionIndex.tsx#L21)–32；[PaperQuestionIssues.tsx:8](../../apps/web-admin/src/features/paper-rubric/PaperQuestionIssues.tsx#L8)–26；[PaperQuestionConfirmation.tsx:10](../../apps/web-admin/src/features/paper-rubric/PaperQuestionConfirmation.tsx#L10)–18、105–108；[PaperRubricView.tsx:139](../../apps/web-admin/src/features/paper-rubric/PaperRubricView.tsx#L139)–155。
- **建议**：合成一个按题排列的待办列表，每题带答案、分值、细则状态，并支持「下一项待办」。总分、资料结构等考试级问题保留在同一列表的顶部，避免删除它们而漏检。
- **核实**：静态核实入口重复；不认定三套检查集合完全等价。

### EX-12 · P2 · 资料页和小题页都可逐题编辑识别候选

- **用户场景与现状**：资料页识别结果直接挂载整套 `PaperImportReviewPanel`，含逐题保存；同时有「逐题核对」按钮跳小题页。小题页的候选详情又挂载同一个编辑器，并再次提供「确认并保存本题」。教师要选择在概览里改还是进入专门页面改。
- **证据**：[PaperImportResults.tsx:24](../../apps/web-admin/src/features/paper-import/PaperImportResults.tsx#L24)、85–100；[PaperImportWorkspace.tsx:42](../../apps/web-admin/src/features/paper-import/PaperImportWorkspace.tsx#L42)–49；[PaperQuestionDetail.tsx:47](../../apps/web-admin/src/features/paper-rubric/PaperQuestionDetail.tsx#L47)–60；[PaperImportReviewPanel.tsx:423](../../apps/web-admin/src/features/paper-import/PaperImportReviewPanel.tsx#L423)。
- **建议**：资料页聚焦文件、进度、识别摘要和结构确认；把逐题修改集中到小题页，主按钮直接到第一项待核对内容。保留资料页的只读预览即可。
- **核实**：静态核实。

### EX-13 · P2 · 粘贴文本弹窗把解析实现细节放在主要任务前面

- **用户场景与现状**：入口叫「粘贴文本」，弹窗却叫「粘贴 Markdown 资料」，在输入框上方讲公式分隔符、本地解析、是否调用大模型、新增原文片段、图片页面、历史资料是否重发。对只想粘贴答案的教师，这些文字先制造了格式和技术门槛。
- **证据**：[PaperImportWorkspace.tsx:55](../../apps/web-admin/src/features/paper-import/PaperImportWorkspace.tsx#L55)；[PaperImportTextModal.tsx:12](../../apps/web-admin/src/features/paper-import/PaperImportTextModal.tsx#L12)–23。
- **建议**：标题统一「粘贴试卷或答案」，默认输入区加一个简短示例；公式写法放「格式帮助」。保留真实数据发送告知，但折叠为「资料如何处理」，临近确有影响的操作时提供可读说明。
- **核实**：静态核实；不建议删除隐私和数据用途告知。

### EX-14 · P2 · 排资料顺序时每点一下就触发一次后端更新和重新匹配

- **用户场景与现状**：每份资料常驻上移、下移、类型下拉、来源、删除；上移、下移和类型变化立即调用 `replaceImportSources`。想把最后一份移到前面，需要多次提交、等候、刷新，也可能反复遇到尚未保存草稿的拦截。
- **证据**：[PaperImportSourceList.tsx:20](../../apps/web-admin/src/features/paper-import/PaperImportSourceList.tsx#L20)–25；[usePaperImportMaterials.ts:132](../../apps/web-admin/src/features/paper-import/usePaperImportMaterials.ts#L132)–152。
- **建议**：正常状态只展示识别结果和来源；「调整资料」内允许本地重排、批量改类型，一次保存并重新匹配。保留代次校验与脏草稿保护。
- **核实**：前端每次点击立即提交，无本地批量重排暂存或统一提交按钮。后端 [paper_import_postgres.go:219](../../services/api-gateway/internal/paper/paper_import_postgres.go#L219)–310 的 `ReplacePaperImportSources` 每次有效替换增加 `generation` 并建立分发记录；[document_import.go:93](../../services/api-gateway/internal/paper/document_import.go#L93)–111 继续处理该代次。因此本项不是“已批处理却误报”；仍未推断每次请求都会重跑全量 OCR。

### EX-15 · P2 · 恢复失败时让教师选择“解析”还是“识别全部”

- **用户场景与现状**：AI 解析失败和取消后同时给「仅重新解析 / 仅解析本次补充」「重新识别全部」。内部阶段差别是真实的，但按钮只表达处理机制，没表达教师能否保留已经核对的内容、耗时和应该默认选哪个。
- **证据**：[PaperImportResults.tsx:31](../../apps/web-admin/src/features/paper-import/PaperImportResults.tsx#L31)–35；[usePaperImportMaterials.ts:180](../../apps/web-admin/src/features/paper-import/usePaperImportMaterials.ts#L180)–194；[paperImportPresentation.ts:66](../../apps/web-admin/src/features/paper-import/paperImportPresentation.ts#L66)–71。
- **建议**：主操作叫「继续处理本次资料」，描述保留已识别结果；「全部重新识别」放次级菜单并说明影响。沿用现有失败恢复边界，不能用新任务冒充原任务重试。
- **核实**：静态核实。

### EX-17 · P1 · 答题卡的核心完成按钮被埋在高级设置中

- **用户场景与现状**：页面工具栏显眼操作是「识别区域」「保存草稿」；完成本环节必需的「确认答题卡设置」在 `TemplateBindingPanel`，该面板仅挂载于「更多操作 → 模板设置」抽屉里。教师框好全部题区后，仍可能找不到怎么完成。
- **证据**：[TemplateSourceToolbarView.tsx:181](../../apps/web-admin/src/features/answer-sheet-template/view/TemplateSourceToolbarView.tsx#L181)–220；[TemplateSettingsView.tsx:23](../../apps/web-admin/src/features/answer-sheet-template/view/TemplateSettingsView.tsx#L23)–46；[TemplateBindingView.tsx:21](../../apps/web-admin/src/features/answer-sheet-template/view/TemplateBindingView.tsx#L21)–32。现有交互测试也通过先点更多、再点模板设置才能找到绑定面板：[TemplateWorkspaceView.interaction.test.tsx:82](../../apps/web-admin/src/features/answer-sheet-template/view/TemplateWorkspaceView.interaction.test.tsx#L82)附近。
- **建议**：把「确认答题卡设置」置于工具栏或固定页脚，旁边显示尚缺区域/异常原因。模板命名、校准和校验码继续放设置；保存→锁定→绑定的实际状态边界继续保留。
- **核实**：静态及现有测试调用核实；尚未实机点击。

### EX-18 · P3 · 同一题目选择同时提供下拉、列表和“创建或定位”

- **用户场景与现状**：答题卡侧栏已有可点题目列表，顶部再给一个包含全部题目的 Select，下面又有「创建或定位答题区域」。Select 与列表都调用 `locateQuestion`，而额外按钮把创建、定位两种意图混为一名。
- **证据**：[TemplateQuestionPane.tsx:16](../../apps/web-admin/src/features/answer-sheet-template/view/TemplateQuestionPane.tsx#L16)–25、42–81。
- **建议**：保留可搜索的题号列表；已配置题点击定位，未配置题在行内给「添加区域」。很长试卷可用搜索输入替代第二份完整下拉列表。
- **核实**：静态核实。

### EX-19 · P2 · 参考图添加暴露像素坐标和多级暂存

- **用户场景与现状**：为本题加一张参考图，需要进入抽屉、选资料页、拖框或填写像素、点「生成实际裁图」、等图加载、选角色、点「已核对，加入本题」、再点「保存参考图清单」。坐标和“实际裁图”更像图像处理工具的术语。
- **证据**：[ReferenceMaterialEditor.tsx:133](../../apps/web-admin/src/features/paper-rubric/ReferenceMaterialEditor.tsx#L133)–158，尤其 149–151、156、158。
- **建议**：默认用可视化框选，像素输入收入高级设置；框选结束自动生成实际裁图预览，用户核对后用「添加并保存」完成本张。支持多图编辑时明确显示未保存张数。必须继续让教师看见实际发送图片、限制身份信息泄漏并保留材料授权/冻结规则。
- **核实**：静态核实；网络失败恢复不可因为减少按钮而跳过。

### EX-20 · P1 · “导入答卷”实际先要求创建一个有名字的技术批次

- **用户场景与现状**：教师点击「导入答卷」后，先看到新建采集批次弹窗，必须填批次名称并选择采集方式（网页、扫描站、文件夹、离线同步）。提交并刷新后才显示具体批次的文件上传。网页里无论选哪种来源，当前前端文件选择和 `uploadSources` 调用仍走同一套控件。
- **证据**：[CaptureBatchPage.tsx:436](../../apps/web-admin/src/pages/CaptureBatchPage.tsx#L436)–445、198–228、525–567；[CaptureBatchPanels.tsx:235](../../apps/web-admin/src/features/capture/CaptureBatchPanels.tsx#L235)–238。
- **建议**：主入口直接选文件，系统默认生成日期/顺序批次名及实际来源；名称在导入记录中可改。只有真正从扫描站/同步工作站导入时才显示对应来源。补传原因、批次审计、幂等恢复继续保留。合并前需核对后端是否针对来源设置额外约束。
- **核实**：静态核实前端路径，未推断后端来源字段没有意义。

### EX-21 · P1 · 上传成功后还有一个容易漏掉的“开始处理”

- **用户场景与现状**：`useCaptureUpload` 上传并登记后提示「等待拆页校验」，并不在该路径调用 `processCaptureBatch`；工具栏另有「开始处理」，只有点击后才将 uploaded/failed 文件提交处理。教师可能以为文件选完即可离开。
- **证据**：[useCaptureUpload.ts:47](../../apps/web-admin/src/features/capture/useCaptureUpload.ts#L47)–89；[CaptureBatchPage.tsx:257](../../apps/web-admin/src/pages/CaptureBatchPage.tsx#L257)–269；[CaptureBatchPanels.tsx:240](../../apps/web-admin/src/features/capture/CaptureBatchPanels.tsx#L240)–243。
- **建议**：一次完整选择全部登记后自动进入处理队列；展示可见进度和失败重试。若业务确实要求上传前核对分组，把主流程改成「选择文件 → 预览分组 → 确认导入并处理」，不让“上传成功”与“开始工作”分成两次模糊承诺。
- **核实**：前端及对应后端入口已静态核实。[capture/handlers.go:528](../../services/api-gateway/internal/capture/handlers.go#L528)–558 将登记与排队分成两个接口；[capture/store_postgres.go:81](../../services/api-gateway/internal/capture/store_postgres.go#L81)–135 的登记只写入 `uploaded` 并更新批次，任务创建在同文件 183–272 的 `QueueBatch`。未运行真实上传，不能据此保证所有部署都没有额外调度器。

### EX-24 · P2 · 单科考试也要经过一页只有一行学科的总览

- **用户场景与现状**：教师在考试列表点击一场独立单科考试，先进入「全部学科」概览，再点唯一学科才能进入当前阶段。列表名和整行均调用 `onOpenWorkspace`；路由始终用 `managementExamRoute`，没有针对独立单科直达的分支。总览页再通过 `defaultExamWorkspaceRoute` 打开学科。
- **证据**：[ExamManagementPage.tsx:109](../../apps/web-admin/src/pages/ExamManagementPage.tsx#L109)、140；[pageRendering.tsx:159](../../apps/web-admin/src/router/pageRendering.tsx#L159)；[managementPresentation.ts:25](../../apps/web-admin/src/features/exams/management/managementPresentation.ts#L25)；[ManagementExamOverviewPage.tsx:58](../../apps/web-admin/src/features/exams/management/ManagementExamOverviewPage.tsx#L58)。主审计智能体已浏览器复现，截图位于 `output/playwright/product-simplicity-audit-2026-10-27/source-exam-list.png` 与 `source-exam-overview.png`。
- **建议**：独立单科默认直达当前业务阶段；在工作区保留「整场总览」。多科考试继续先给学科概览。仅有一科授权的多科场次应保留授权语义，不能据此改成整场管理权限。
- **核实**：动作链静态核实，单科多一跳由主审计智能体实机确认。

### EX-22 · P1 · 要求用户保证页序，却缺少提交前的分组确认

- **用户场景与现状**：导入规则依赖文件选择顺序、文件内页序及每 N 页一名学生；每次选择独立分组，不能跨两次选取补齐。当前上传控件 `showUploadList: false`，选择文件后立即登记，因此教师难以在提交前发现顺序错误、调换正反面或查看每一组对应哪些页。
- **证据**：[CaptureBatchPage.tsx:233](../../apps/web-admin/src/pages/CaptureBatchPage.tsx#L233)–254；[useCaptureUpload.ts:91](../../apps/web-admin/src/features/capture/useCaptureUpload.ts#L91)–108；[CaptureBatchPanels.tsx:249](../../apps/web-admin/src/features/capture/CaptureBatchPanels.tsx#L249)。
- **建议**：增加轻量导入预检，展示文件/页缩略图、每人 N 页的分组边界，支持提交前排序；页数默认取已确认模板。保持现有完整性规则，不通过取消跨组隔离来减少报错。
- **核实**：静态核实当前直接上传路径；大 PDF 预览与真实浏览器选择顺序需运行验证。

### EX-23 · P2 · 正常导入页常驻大段边界说明

- **用户场景与现状**：每个已读批次都显示一块说明，包含 N 页、单面/双面、1–20 页、PDF/TIFF 解码、每次选择独立分组、缺页不处理、不跨两次选择凑页。遇到上传组问题时，下方又重复一段分组边界说明。正常任务里大量例外说明挤占了进度与待办。
- **证据**：[CaptureBatchPanels.tsx:149](../../apps/web-admin/src/features/capture/CaptureBatchPanels.tsx#L149)–161、249–253。
- **建议**：正常状态只显示「每人 N 页，按学生顺序连续排列」及「查看导入规则」；缺页、重复或身份冲突发生时就地显示相应限制和可执行修复。规则不删除，改为按触发条件展示。
- **核实**：静态核实。

### EX-25 · P2 · 已指出具体问题的“去处理”仍把用户送回通用入口

- **用户场景与现状**：准备检查已经知道「第几题」有评分方案问题，但每一题的「去处理」均跳同一个 `/exams/:id/questions`，没有携带 `question_id` 或目标字段；考试级问题的「核对考试信息」则跳整张考试列表。用户到了下一页还要再次找原题或原考试。题目页内部其实已经有定位某题某字段的能力。
- **证据**：[ExamScoringPreparation.tsx:23](../../apps/web-admin/src/components/features/assessment/ExamScoringPreparation.tsx#L23)、33–38；[PaperRubricPage.tsx:253](../../apps/web-admin/src/pages/PaperRubricPage.tsx#L253)–280；[ExamPreparationPage.tsx:13](../../apps/web-admin/src/pages/ExamPreparationPage.tsx#L13)–16。
- **建议**：题目待办直接定位原题并展开判分方式/缺项字段；考试级待办打开当前考试的对应编辑界面。无权限时解释原因并显示明确交接对象，保留服务端读取与权限检查。
- **核实**：路由参数和题目页接收方式已静态核实；未将所有准备检查都误认为有可定位的单题。

### EX-26 · P2 · 图像质检把完整算法诊断当作默认阅读内容

- **用户场景与现状**：教师主要需要判断“能否阅卷、是否重扫、是否放行”。质检页在结论与操作之外默认列出七项分值、处理条件、算法名称/版本、毫秒耗时、局部坏块比例、有效短边像素；问题说明再显示实测数和规则阈值。这些均直接挂在检查栏，并未收进诊断详情。
- **证据**：[ImageQualityWorkspace.tsx:432](../../apps/web-admin/src/features/capture/ImageQualityWorkspace.tsx#L432)–477、522–529；[capture.css:44](../../apps/web-admin/src/styles/features/capture.css#L44)–59。
- **建议**：默认保留结论、受影响位置、是否建议重扫与人工放行操作；算法、阈值、历史指标收入「检测详情」。保留原图查看门禁、真实记录和放行审计，不以精简界面降低质量标准。
- **核实**：完整组件及相关样式片段静态核实；属于默认信息量问题，不主张删除诊断能力。

### EX-27 · P2 · 异常处理仍要求学校用户理解工程级别和资源标识

- **用户场景与现状**：异常列表有独立“级别”列与筛选，选项直接是 P0、P1、P2、P3，没有用户可理解的影响说明；打开异常材料后又常驻完整答卷 ID、页面 ID。已有考号、页码与具体异常原因，但这些工程标识仍占主要内容区。
- **证据**：[ProcessingOperationsPanel.tsx:262](../../apps/web-admin/src/components/ProcessingOperationsPanel.tsx#L262)–265、394–402；[ProcessingExceptionEvidence.tsx:14](../../apps/web-admin/src/components/ProcessingExceptionEvidence.tsx#L14)–16。
- **建议**：默认按是否阻断阅卷、需要补扫/匹配/重试等业务后果组织待办；若保留级别，显示「阻断阅卷（P1）」等与真实后端含义一致的标签。UUID 收入可复制的「排障信息」。不能编造 P0–P3 的含义或改变优先级规则。
- **核实**：静态核实；等级中文解释须结合服务端定义确认，本文不替后端定义等级含义。

### EX-28 · P2 · 版面对齐默认露出八个 0–1 坐标输入

- **用户场景与现状**：电脑上已提供可拖动的四角点和方向键微调，右栏仍默认显示四组答卷 x/y 数字，共八个输入框。值是 0–1 的页面比例，界面并未把每个数字明确标为横向/纵向位置。普通用户要在可视化动作之外理解坐标表示；开启模板点修正后输入量再翻倍。
- **证据**：[RegistrationCorrectionWorkspace.tsx:158](../../apps/web-admin/src/components/RegistrationCorrectionWorkspace.tsx#L158)–229、574–649。手机样式本来已隐藏精确操作，本项仅针对电脑界面：[correction.css:44](../../apps/web-admin/src/styles/features/correction.css#L44)–54。
- **建议**：默认用拖点、键盘微调和校正预览；精确数字放「精确坐标」中，并明确横向/纵向、统一为百分比或清楚解释比例。保留可访问的键盘/数字替代操作与实际预览后才能应用的边界。
- **核实**：完整文件静态核实；不把必要四角校正、原因填写或应用确认当作冗余。

### EX-29 · P2 · 已识别的答案对教师显示成 JSON

- **用户场景与现状**：资料识别预览的 `answerText` 对所有对象和数组都输出 JSON 代码块；多选答案数组、逐空答案对象及等价答案因此以程序数据结构显示。教师为了核对 AC 或各空答案，要读方括号、字段名和引号。
- **证据**：[PaperImportRecognitionPreview.tsx:15](../../apps/web-admin/src/features/paper-import/PaperImportRecognitionPreview.tsx#L15)–18、49–51、72。
- **建议**：按题型渲染「答案 A、C」「第 1 空……」「允许写法……」；未知结构保留可展开的原始数据，避免丢失语义。直接复用已有结构化答案展示规则，不让展示层改写保存数据。
- **核实**：静态核实该只读预览路径；不是声称所有答案编辑器都暴露 JSON。

### EX-30 · P2 · 同一题有两个必须相等的“满分”编辑框

- **用户场景与现状**：识别候选编辑已有题目“分值”，评分细则另有可编辑“满分”。两者必须相等、采分点合计也必须相等，否则阻断确认。修改题目分值只改本题分数字段，不会自动同步细则满分，用户需要手工维护两个相同事实。
- **证据**：[PaperImportReviewPanel.tsx:173](../../apps/web-admin/src/features/paper-import/PaperImportReviewPanel.tsx#L173)；[RubricReviewEditor.tsx:33](../../apps/web-admin/src/features/paper-import/RubricReviewEditor.tsx#L33)–43、56；[usePaperImportWorkflow.ts:280](../../apps/web-admin/src/features/paper-import/usePaperImportWorkflow.ts#L280)–287；[materials.ts:235](../../apps/web-admin/src/features/paper-import/materials.ts#L235)–242。
- **建议**：细则满分从本题分值派生，只显示不编辑；修改题目分值后保留各采分点并提示总分差异，由教师调整采分点。不能自动按比例改评分点，不能取消总分一致性检查。
- **核实**：完整相关展示和字段更新函数静态核实；未修改数据契约或历史已锁定细则。

### EX-31 · P2 · 识别处理页把内部状态转换做成用户按钮

- **用户场景与现状**：除本场批次导入外，`/capture` 与考试工作区 `/processing` 仍挂载另一套答题卡处理页。其“下一步”可以是「检查图片质量」「转入待识别」「开始识别」「生成题目区域」。其中「转入待识别」只更新状态，成功后明确再要求点击「开始识别」。页面总说明却承诺自动完成图片检查和文字识别，教师难以判断还应等待还是必须继续点。
- **证据**：[submissionColumns.tsx:30](../../apps/web-admin/src/features/capture/submissionColumns.tsx#L30)–40、112–140；[SubmissionCapturePage.tsx:453](../../apps/web-admin/src/pages/SubmissionCapturePage.tsx#L453)–456；[pageRendering.tsx:105](../../apps/web-admin/src/router/pageRendering.tsx#L105)–106、182–189。
- **建议**：正常处理由系统编排，用户操作表达「继续处理」「重试失败步骤」或具体修复；把纯状态切换吸收到服务端受控命令。若该页只是保留历史入口，应清楚定位并引导当前流程。不得绕过完整性、质量、身份与模板检查。
- **核实**：前端分支及当前路由挂载静态核实；不宣称每一份正常答卷都必然需要逐个点击，也未据此断言服务端没有自动调度。

### EX-32 · P2 · 相似答案抽检先反复“打开”，再反复阅读同题评分依据

- **用户场景与现状**：每个样本初始只有「查看实际答题样本」，必须单独打开才能读内容并启用接受/拒绝；打开后每个样本都在答案前重复渲染本题完整题干、选项、参考答案、解析、细则、扣分和示例。多个样本展开时，相同评分依据反复撑长列表。顶部另有同质性、回滚率、事后审计误差等五项指标，进一步分散抽检注意力。
- **证据**：[AnswerGroupSampleEvidence.tsx:10](../../apps/web-admin/src/features/answer-groups/AnswerGroupSampleEvidence.tsx#L10)、52；[AnswerGroupingDrawer.tsx:312](../../apps/web-admin/src/features/answer-groups/AnswerGroupingDrawer.tsx#L312)–337、395–402；[TaskScoringReference.tsx:11](../../apps/web-admin/src/components/TaskScoringReference.tsx#L11)–25。
- **建议**：改为逐份样本审阅区，默认显示当前样本并提供“接受/拒绝并下一份”；同一冻结版本的评分依据集中展示一次，随时可查看。统计诊断收进详情。必须继续实际显示当前样本材料、核验 hash、完成最低及异常样本抽检，不能用自动勾选已读代替人工审阅。
- **核实**：完整组件及评分依据组件静态核实；不认定接口的 `members` 一定包含全部组成员，也不建议降低抽检量。

### EX-33 · P2 · 上传队列常驻完成记录，却把较早失败藏在第九项以后

- **用户场景与现状**：答题卡处理页每上传一个文件就在队列头部加入记录；队列面板只展示 `queue.slice(0, 8)`，没有展开、分页、清除完成项或只看失败入口。上传多批后，已完成的最新记录占位置，较早失败只计入“X 个失败”，用户却无法在该队列里找到对应文件和原因。
- **证据**：[SubmissionCapturePage.tsx:233](../../apps/web-admin/src/pages/SubmissionCapturePage.tsx#L233)–241；[SubmissionCaptureViews.tsx:73](../../apps/web-admin/src/pages/SubmissionCaptureViews.tsx#L73)–104。
- **建议**：默认保留进行中和失败项，完成项归入可展开历史；允许查看全部失败并定位原答题卡/原操作。只有隐藏完成记录，不删除服务端事实，不诱导用户重复上传已有成功文件。
- **核实**：静态核实；需用超过八条且包含较早失败的合成队列实机验证可发现性。

## 撤回的早期判断

- **原 EX-16：上传后需要手动创建区域模板——撤回，不计整改项。** 首轮只看了 `TemplateWorkspaceView` 与工具栏的手动兜底分支。完整补读 [AnswerSheetTemplatePage.tsx:149](../../apps/web-admin/src/pages/AnswerSheetTemplatePage.tsx#L149)–182 后确认：正常未知来源会自动匹配学校模板、创建草稿，并在草稿就绪后自动识别区域；匹配失败/冲突保留手动核对和重试。不能把失败恢复入口当作正常流程的必经负担。

## 本轮补审中保留的合理设计

- 模板库收录默认折叠，空白来源与版式两项核对有具体隐私/复用边界；未新增“勾选太多”的问题。
- OMR 校准隐藏机器结果以便独立盲标，样本覆盖、独立审批、撤销原因与校验信息各有必要性；审计校验码已折叠，未重复建议折叠。
- 成功的自动题区允许直接编辑；仅异常区域才需要追加核对，未误报为全部区域逐一确认。
- 打印包避免重复签发、补打同时作废旧卡；生成成功但下载失败可恢复下载，这些不是应删除的功能。
- 题目批量分值、候选采用、草稿保存及版本锁定保留了真实状态边界；本文建议集中保存和指引，并不合并不同业务事实。
- 共享 UI 的 `WorkspaceLayout` 只负责插槽，考试工作区明确 `stageRail={null}`，未发现同时挂两套共享阶段栏的证据。

## 不应被当作冗余删除的边界

- AI 建议需要教师核对；规则发布、细则锁定、模板绑定和最终准备确认是不同事实。可以统一界面和引导，不能把保存草稿变为自动确认。
- 资料完整性确认防止把部分页面误认为完整试卷；不能为了少一勾而允许静默删减已有题目。
- 结束本场导入前，必须核对学生、材料完整性、缺考、重复答卷及处理状态。审计原因、修订号和幂等恢复不能移除。
- 锁定模板后克隆版本、异常区域核对、参考图实际内容和使用授权均有明确业务目的。改进应降低日常路径负担，保留这些必要约束。

## 后续验证重点

1. 先按实际学校管理员账户走通新建单科、多科考试，记录从进入创建到第一份学生答卷可阅的点击、跳页和必须理解的术语。
2. 用合成数值题比较两处误差配置，按已核实的后端覆盖优先级验证界面承诺；将实际判分问题与这里的入口设计问题分开报告。
3. 修改判分方式但不点该区域保存，再点「保存本题全部修改」并切题，检查未保存提示是否覆盖该区域。
4. 上传无学校库命中的空白答题卡，验证已实现的自动匹配、建稿和识别链及失败恢复，再观察最终确认按钮的可发现性；不再预设正常流程需要手工建稿。
5. 上传合成双面答卷，在不点「开始处理」的情况下观察服务端实际是否自动调度；再验证预检方案不会削弱分组和重试边界。
