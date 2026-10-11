# 2026-10-09 修复效果复核

按 P0→P1→P2 逐项复核：原82项 76 项有效、6 项部分有效；最新17项 17 项有效。当前确认 3 个P0、12 个P1，无新增P2。有效只表示该项原目标已闭合，邻近新问题单列。

## A. 最严重的10个问题

| 顺序 | 优先级 | 角色／页面 | 当前问题 | 优先修改 |
| --- | --- | --- | --- | --- |
| 1 | P0 | 学校管理员／考试负责人／相似答案分组 | 切题后回滚实际作用于上一题 | [apps/web-admin/src/features/answer-groups/AnswerGroupingDrawer.tsx](D:/project/yuejuan/apps/web-admin/src/features/answer-groups/AnswerGroupingDrawer.tsx:101) |
| 2 | P0 | 仅有 review:work 的普通阅卷教师／任务列表→离线阅卷→答卷预览/恢复图片 | 普通教师打开阅卷后看不到答案，仍可进入同步提交 | [apps/desktop-client/src/components/OfflineWorkbench.tsx](D:/project/yuejuan/apps/desktop-client/src/components/OfflineWorkbench.tsx:862) |
| 3 | P0 | 题库管理者、学校管理员／题目分类与知识点 | 分类规则读取失败仍能把空表单存为当前新版 | [apps/web-admin/src/features/question-bank/QuestionBankDialogs.tsx](D:/project/yuejuan/apps/web-admin/src/features/question-bank/QuestionBankDialogs.tsx:155) |
| 4 | P1 | 平台管理员／模型管理→三智能体配置→正式评分准备→验证评分准入 | 从高中当前评分组合进入准入检查，实际查初中数学默认范围 | [apps/web-admin/src/pages/PlatformModelConfigPage.tsx](D:/project/yuejuan/apps/web-admin/src/pages/PlatformModelConfigPage.tsx:739) |
| 5 | P1 | 教师、题库维护者／题库版本冲突恢复 | 冲突比较缺本地答案和评分点，仍无法判断选哪一侧 | [apps/web-admin/src/features/question-bank/QuestionBankPage.tsx](D:/project/yuejuan/apps/web-admin/src/features/question-bank/QuestionBankPage.tsx:751) |
| 6 | P1 | 重启客户端的教师/扫描员／扫描上传/同步队列→本机队列恢复 | 本机队列恢复失败被显示成尚未选择文件 | [apps/desktop-client/src/features/scan-queue/useScanQueue.ts](D:/project/yuejuan/apps/desktop-client/src/features/scan-queue/useScanQueue.ts:92) |
| 7 | P1 | 学校管理员／考试负责人／答题卡人工边界校正 | 初始校正材料读失败后永久显示加载 | [apps/web-admin/src/components/RegistrationCorrectionWorkspace.tsx](D:/project/yuejuan/apps/web-admin/src/components/RegistrationCorrectionWorkspace.tsx:300) |
| 8 | P1 | 考试负责人、具有考试管理权限的教师／小题与分值／评分配置 | 分区评分已存而读取失败，仅留下通用错误缺持久已存回执 | [apps/web-admin/src/features/paper-rubric/hooks/useRubricEditor.ts](D:/project/yuejuan/apps/web-admin/src/features/paper-rubric/hooks/useRubricEditor.ts:115) |
| 9 | P1 | 学校管理员／考试负责人／回标差异与复评交接 | 回标差异读失败仍显示正在加载且刷新失效 | [apps/web-admin/src/pages/QualityDashboardPage.tsx](D:/project/yuejuan/apps/web-admin/src/pages/QualityDashboardPage.tsx:279) |
| 10 | P1 | 学校管理员／考试负责人／创建回标／回标转正式复评 | 回标和复评分派无法区分同名教师 | [apps/web-admin/src/pages/QualityDashboardPage.tsx](D:/project/yuejuan/apps/web-admin/src/pages/QualityDashboardPage.tsx:396) |

## B. 完整问题清单

| 优先级 | 角色 | 页面/功能 | 路由 | 文件 | 当前问题 | 为什么看不懂 | 推荐修改 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| P0 | 学校管理员／考试负责人 | 相似答案分组（load / 选题 / rollback） | #/admin/arbitration；#/admin/exams/:examId/quality | [apps/web-admin/src/features/answer-groups/AnswerGroupingDrawer.tsx](D:/project/yuejuan/apps/web-admin/src/features/answer-groups/AnswerGroupingDrawer.tsx:101) | 第2题 · short_answer；回滚候选；选择器已切第2题，迟到的第1题读取结果仍替换groups；按钮按selectedGroup发送第1题group_id。 | 页面题号和操作对象不一致，教师点击前无从发现。 | 按考试/题目/请求序号隔离读取，切题清空旧组、材料、决定草稿和弹窗；确认框明确本题题号/份数，提交前再次检查group.question_id与当前题目。反馈显示实际题号与影响份数，返回同一题。（参考M1） |
| P0 | 仅有 review:work 的普通阅卷教师 | 任务列表→离线阅卷→答卷预览/恢复图片（OfflineWorkbench/buildTaskPackage/restorePackageImage） | workspace=tasks → workspace=offline | [apps/desktop-client/src/components/OfflineWorkbench.tsx](D:/project/yuejuan/apps/desktop-client/src/components/OfflineWorkbench.tsx:862) | 任务包不完整；未定位答案页面；未记录 OCR 文本；恢复答卷图片；同步提交；任务冻结 context 请求成功且 answer_artifact 内已有授权文字及 segment_image_url；组件忽略这些答案材料，调用 submission/segment/OCR/grade 管理入口遭 403。包只展示题干细则及技术缺失警告，恢复按钮再次走同管理入口失败；填写合法分数后同步没有答案核对门禁。 | 教师已能进入任务，却不知道为什么答案缺失；反复恢复仍是权限失败，技术警告不能指示实际可恢复方式。 | 任务包与图片恢复统一读取任务范围 context.answer_artifact 的文字/图像入口及 context.ai_candidates；缓存到本账号草稿时保留来源和版本。未得到任何可核对答案时保留草稿编辑，但禁用提交并提供同页重读材料、联系管理员和返回任务列表。无需授予教师广泛管理权限。（参考M2） |
| P0 | 题库管理者、学校管理员 | 题目分类与知识点（QuestionBankDialogs / QuestionBankPage.saveSchema） | #/admin/question-bank；#/teacher/question-bank | [apps/web-admin/src/features/question-bank/QuestionBankDialogs.tsx](D:/project/yuejuan/apps/web-admin/src/features/question-bank/QuestionBankDialogs.tsx:155) | 题目分类与知识点 · 第 — 版；保存新的分类规则；首次currentSchema读取失败且无缓存时，表单只禁isLoading，isError未禁保存，也无弹窗内读错提示；回填无数据即返回，saveSchema把未填fields/taxonomies转[]。后端normalizeSchema接受空数组，expected_revision匹配即新增空规则并推进当前版。 | 空白像尚未配置，保存却意味着移除当前全部分类；用户不知道读取失败及真实变更范围。 | 读失败显示持久错误和重新读取分类规则；无可核实规则禁保存。提交展示增删摘要，非空改全空需明确移除全部规则确认，不能把未读取当空。（参考M3） |
| P1 | 平台管理员 | 模型管理→三智能体配置→正式评分准备→验证评分准入（PanelModelBindingsSection / ManagedGovernanceWorkspace / ScoringAssuranceWorkspace / EligibilityPolicyPanel） | #/admin/platform/model-config?tenant_id=:id&tab=bindings → tab=governance&view=assurance&stage=senior&subject=english&archetype=essay | [apps/web-admin/src/pages/PlatformModelConfigPage.tsx](D:/project/yuejuan/apps/web-admin/src/pages/PlatformModelConfigPage.tsx:739) | 上方待核对评分范围为高中/english/essay，题型准入选择却显示数学/初中/数值表达式/R2。；源页把当前stage/subject/archetype写入hash；治理页打印这些值，准入组件未读取或接收范围，useState(defaultAxis)后自动loadPolicy(axis)。 | 管理员需要猜测两套范围关系并重新选择；直接维护策略会处理与刚配置组合不同的对象，入口连续性失效。 | 页面名称：正式评分使用检查；按钮名称：检查当前评分组合；辅助说明：以当前学校、高中、英语和实际题型为准；默认全题型必须先选具体题型，风险级别需说明或由题目事实确定。；布局：同一个中文范围摘要和实际查询控件同步，无法映射时先显示需选择且不自动查其他默认范围。；点击反馈：明确已核对哪个具体范围，以及缺少哪项准入事实。；下一步：同范围补齐评测、批准或策略；不得通过打印hash文本代替真实范围传递。（参考M1） |
| P1 | 教师、题库维护者 | 题库版本冲突恢复（比较本地修改与最新版本 Modal） | #/teacher/question-bank；#/admin/question-bank | [apps/web-admin/src/features/question-bank/QuestionBankPage.tsx](D:/project/yuejuan/apps/web-admin/src/features/question-bank/QuestionBankPage.tsx:751) | 本地修改 · 原修订；最新版本 · 修订；采用最新版本；本地侧只有满分/题干，最新侧另列标准答案/评分点；本地评分虽在下载备份里，不能直接在比较弹窗看到。 | 弹窗称比较，正文相同也看不出答案/评分的具体差异。 | 两侧对齐显示题干、答案、逐评分点/分值/条件和附件，突出变动；继续保留本地输入，采用前明确二次确认。（参考M3） |
| P1 | 重启客户端的教师/扫描员 | 扫描上传/同步队列→本机队列恢复（useScanQueue restore effect/ScanWorkspace/QueueList） | 冷启动登录/解锁 → workspace=scan 或 workspace=sync | [apps/desktop-client/src/features/scan-queue/useScanQueue.ts](D:/project/yuejuan/apps/desktop-client/src/features/scan-queue/useScanQueue.ts:92) | 本地耐久队列无法恢复（仅诊断页）；尚未选择文件；暂无队列项目；恢复前 setQueue([])；listDurableScanQueue 失败仅 setDiagnosticError，不给扫描页或同步页 queue loading/error/retry。扫描页收到 fileImportError 而未接 diagnosticError；同步页也只接评分草稿列表错误。 | 真正的读取失败与合法空队列使用相同空态，没有同页失败原因或下一步，诊断错误与原任务割裂。 | 为本机队列设置恢复中/失败/成功空态；失败时明确原队列状态尚未核实、请先重读再决定是否重新导入，提供同页重新读取队列、检查授权/磁盘及打开诊断，保留当前已入队项。只在恢复成功且列表为空时展示尚未选择文件。（参考M3） |
| P1 | 学校管理员／考试负责人 | 答题卡人工边界校正（初始context和PointCanvas） | #/admin/exams/:examId/capture | [apps/web-admin/src/components/RegistrationCorrectionWorkspace.tsx](D:/project/yuejuan/apps/web-admin/src/components/RegistrationCorrectionWorkspace.tsx:300) | 预览校正；原图与模板处Spin；原图/模板文件503只短暂Toast，随后两幅画布继续Spin，预览禁用，无本页重读。 | 没有持续错误与下一步操作，失败状态看起来像未完成加载。 | 区分正在读取/已失败；各材料内联说明失败原因并提供重新读取，保留点位，读取到当前版本图像后再开放预览。（参考M3） |
| P1 | 考试负责人、具有考试管理权限的教师 | 小题与分值／评分配置（useRubricEditor.persistRubricSave / PaperRubricView.ErrorState） | #/admin/exams/:examId/questions | [apps/web-admin/src/features/paper-rubric/hooks/useRubricEditor.ts](D:/project/yuejuan/apps/web-admin/src/features/paper-rubric/hooks/useRubricEditor.ts:115) | 评分细则新版本已提交；读取失败提示；分区保存评分细则先POST成功Toast，再loadConfig默认捕获GET错误置configError而不抛，随后清dirty；编辑区卸载成通用ErrorState。已有onRetry只读loadConfig，但无持久评分已保存事实/快照，未使用整体保存的acceptedSaveReceipt。 | 错误页没有告诉教师哪一步已完成，无法决定重新保存还是重新读取。 | 分区也保存接受回执和该版快照；持久显示评分已保存、页面暂不能读取、无需再保存。复用已有只读重试，防旧缓存回填，并保留后续编辑。（参考M3） |
| P1 | 学校管理员／考试负责人 | 回标差异与复评交接（openBackmarkSummary / BackmarkSummaryContent） | #/admin/exams/:examId/quality | [apps/web-admin/src/pages/QualityDashboardPage.tsx](D:/project/yuejuan/apps/web-admin/src/pages/QualityDashboardPage.tsx:279) | 正在加载回标差异；刷新；打开先清summary，三个必要请求任一失败只Toast；finally loading=false但summary仍空，内容固定LoadingState。刷新按钮须有summary才调用读取。 | 显示尚在加载且看似有刷新按钮，实际点击没有请求。 | 保留所选batch作为独立对象；错误状态内联说明哪个数据失败，刷新按所选batch重读，目录/版本失败不要遮住已成功的差异证据。（参考M3） |
| P1 | 学校管理员／考试负责人 | 创建回标／回标转正式复评（reassigned_to Select / BackmarkRegradeOptions） | #/admin/exams/:examId/quality | [apps/web-admin/src/pages/QualityDashboardPage.tsx](D:/project/yuejuan/apps/web-admin/src/pages/QualityDashboardPage.tsx:396) | 指定回标阅卷员：display_name ／／ username；原阅卷教师用姓名+账号，指定回标阅卷员和复评阅卷员仍只展示姓名，两位同名教师选项相同。 | 同一个流程的三个人员选择器身份信息不一致。 | 全部人员选择统一姓名（账号）或姓名＋学科/部门；无法核对身份时停用该选项并重读目录。确认与成功回执显示实际承接教师和题号。（参考M1） |
| P1 | 普通教师/扫描员 | 扫描上传→导入已有 PDF/图片（ScanWorkspace/importReady + useScanQueue.handleFileSelection） | workspace=scan | [apps/desktop-client/src/features/scan/ScanWorkspace.tsx](D:/project/yuejuan/apps/desktop-client/src/features/scan/ScanWorkspace.tsx:63) | 本批答题卡模板标识；模板标识由学校教务处从本次考试的答题卡模板详情提供；选择真实考试/批次后仍须手填模板字符串，否则选文件按钮不可用；设备档案也保留手填模板字符串。 | 模板标识不是教师熟悉的模板名称，前端已选择考试及批次却继续要求复制同一业务对象的内部值。 | 从当前批次锁定模板读取名称、版本、纸张和清晰度，主流程展示答题卡模板名称及预览，参数自动填充；批次尚未绑定模板时显示缺模板及由学校管理员配置的具体下一步，内部标识只留高级诊断。（参考M1） |
| P1 | 跨多个考试批次的教师/扫描员 | 扫描上传全部队列/同步队列（ScanQueueTable/QueueList） | workspace=scan / workspace=sync | [apps/desktop-client/src/components/DesktopStatusViews.tsx](D:/project/yuejuan/apps/desktop-client/src/components/DesktopStatusViews.tsx:104) | 考试 / 采集批次；采集批次：{captureBatchId}；导入选择器显示真实批次名称，但已入队后表格与同步卡片只输出 captureBatchId，缺批次名称与采集时间。打开队列后确实定位到原批次，但点击前必须猜想是哪一批。 | UUID 无法与教师记忆中的批次名称对应；新增列只有内部键不能解决首次使用理解。 | 队列入库记录并刷新批次名称/创建时间，表格显示考试名、批次名和时间/文件数量；ID 放详情或复制诊断。遇到名称读取失败显示名称未确认及重读入口，仍维持既有原批次定位和不可改属边界。（参考M1） |
| P1 | 学校管理员／考试负责人 | 已有回标批次及差异详情（BackmarkBatchHistory / BackmarkSummaryContent） | #/admin/exams/:examId/quality | [apps/web-admin/src/pages/QualityDashboardViews.tsx](D:/project/yuejuan/apps/web-admin/src/pages/QualityDashboardViews.tsx:128) | 批次UUID；题目UUID；ready_for_confirmation；arbitrate；历史表id与question_id直接展示，status直接Tag；差异去向直接显示policy.disposition。 | 中文表头下的数据仍是开发者语言，创建页与历史页对象名称断开。 | 主列用题号、创建时间、受影响份数与中文状态；去向用记录差异/进入仲裁/进入题目重评，UUID只放高级详情，查看差异后显示本题和下一步。（参考M1） |
| P1 | 受派复核教师，appeal:work且无exam:manage | 成绩复核→学生门户申请（QuestionAppealCenterPage / QuestionAppealQueue / QuestionAppealIdentityHeader） | #/teacher/appeals | [apps/web-admin/src/pages/QuestionAppealCenterPage.tsx](D:/project/yuejuan/apps/web-admin/src/pages/QuestionAppealCenterPage.tsx:36) | 考试 11111111-2222-3333-4444-555555555555 · 第3题 / 申请内部标识；只有exam:manage才读取考试目录；普通教师examLabels恒空。获授权的appeal及context都不返回考试名，队列和详情统一退回考试UUID。 | 教师无法辨认哪次考试的哪份复核，需找管理员对号；盲评隐藏学生身份不要求隐藏考试名称。 | 页面名称：我的成绩复核；按钮名称：查看第3题复核材料；辅助说明：高一第一次月考·英语·第3题·原公布第1版；本任务只供受派教师查看。；布局：获授权任务投影返回考试名称/学科，队列与Drawer共用同一摘要；内部编号折叠到记录信息。；点击反馈：保存/冲突/移交同样显示该业务摘要。；下一步：保留私有教师意见→管理员处理，不为读取名称授予整场考试管理权，也不解除学生身份盲评。（参考M1） |
| P1 | 普通教师、考试负责人 | 考试资料／自动识别结果（PaperImportReviewPanel / paper-import-model-usage） | #/teacher/exams/:examId/paper；#/admin/exams/:examId/paper | [apps/web-admin/src/features/paper-import/PaperImportReviewPanel.tsx](D:/project/yuejuan/apps/web-admin/src/features/paper-import/PaperImportReviewPanel.tsx:371) | 本次模型用量；输入；输出；缓存命中；合计；Token 数量由模型厂商返回；showOverview默认true且有total_tokens即显示，DraftResults正常调用未传管理员权限或折叠状态；共用资料总览主区。 | 输入/输出/合计易误认为题数、答案数或待处理数量，缓存命中不能帮助核对题目。 | 教师主区保留题目、答案、需核对数量和下一步；技术用量放权限限定的模型用量明细或高级用量详情，明确Token单位和估算性质。（参考M2） |

原82项中仍部分有效：R23、A03、D02、D06、D08、D14。其余逐项结论、API证据与限制见[99项复核台账](C:/Users/pc/.codex/visualizations/2026/10/07/01a1155f-d593-7153-9595-8c482e7ddecf/ux-recheck-20261009-final/checks-99.csv)和[完整证据](C:/Users/pc/.codex/visualizations/2026/10/07/01a1155f-d593-7153-9595-8c482e7ddecf/ux-recheck-20261009-final/final-review.json)。新增发现是否由上一轮修复引入均未确认，不能据邻近关系归因。

M1 当前内部编号/切换后对象断裂 → Vercel先选团队和项目，再在其部署列表按日期、环境、状态筛选 → yuejuan2以考试/题号/名称保持整条流程的对象范围，ID移入详情。这里只借鉴公开交互，不断言其异步实现。[Vercel部署管理](https://vercel.com/docs/deployments/managing-deployments)。

M2 教师取材依赖管理权限、解析页展示技术用量 → Stripe按职责开放争议与相关付款等业务资料，同时限制账号/API密钥管理 → yuejuan2用受派任务范围取材，技术诊断仅给授权管理员。[Stripe角色权限](https://docs.stripe.com/get-started/account/teams/roles)。

M3 失败显示成加载/空态、成功事实被读取错误淹没 → NN/G要求错误可见、具体、提供恢复建议并尊重已有输入 → yuejuan2保留草稿与写入回执，同页区分读取失败和未配置并可重读。[NN/G错误提示指南](https://www.nngroup.com/articles/error-message-guidelines/)。

## C. 应删除／隐藏／改名的技术术语

以下仅列本轮当前确认的残留，已修术语不重复算问题。

| 当前名称 | 位置 | 推荐处理 | 推荐名称 |
| --- | --- | --- | --- |
| 本批答题卡模板标识（内部串） | [apps/desktop-client/src/features/scan/ScanWorkspace.tsx](D:/project/yuejuan/apps/desktop-client/src/features/scan/ScanWorkspace.tsx:63) | 主流程从批次取实际模板；内部ID仅诊断 | 答题卡模板：名称＋版本＋预览 |
| Rubric 版本／answer_segment／OCR 文本 | [apps/desktop-client/src/components/OfflineWorkbench.tsx](D:/project/yuejuan/apps/desktop-client/src/components/OfflineWorkbench.tsx:896) | 教师界面改名，内部字段不变 | 评分标准版本／本题答题材料／识别文字 |
| Token／输入／输出／缓存命中 | [apps/web-admin/src/features/paper-import/PaperImportReviewPanel.tsx](D:/project/yuejuan/apps/web-admin/src/features/paper-import/PaperImportReviewPanel.tsx:371) | 普通教师隐藏；授权管理员诊断可见 | 教师只看解析进度、待核对题数；管理员可看模型调用用量 |
| 题目UUID／批次UUID | [apps/web-admin/src/pages/QualityDashboardViews.tsx](D:/project/yuejuan/apps/web-admin/src/pages/QualityDashboardViews.tsx:128) | 主列表替换，详情保留复制内部编号 | 第X题／X月X日回标·N份 |
| 采集批次 UUID | [apps/desktop-client/src/components/DesktopStatusViews.tsx](D:/project/yuejuan/apps/desktop-client/src/components/DesktopStatusViews.tsx:104) | 入队保留批次名称；ID放详情 | 考试名·采集批次名·时间 |
| 考试 UUID | [apps/web-admin/src/components/QuestionAppealViews.tsx](D:/project/yuejuan/apps/web-admin/src/components/QuestionAppealViews.tsx:31) | 授权任务投影提供名称，不扩教师管理权 | 考试名·学科·第X题 |
| ready_for_confirmation／arbitrate／regrade | [apps/web-admin/src/pages/QualityDashboardViews.tsx](D:/project/yuejuan/apps/web-admin/src/pages/QualityDashboardViews.tsx:131) | 状态与差异去向均中文映射 | 待确认／达到分差进入仲裁／进入题目重评 |
| short_answer 等原始题型代码 | [apps/web-admin/src/features/answer-groups/AnswerGroupingDrawer.tsx](D:/project/yuejuan/apps/web-admin/src/features/answer-groups/AnswerGroupingDrawer.tsx:271) | 用统一学科题型标签 | 简答题（具体题号） |
| junior／numeric_expression／R2范围与摘要 | [apps/web-admin/src/components/model-governance/ScoringAssuranceWorkspace.tsx](D:/project/yuejuan/apps/web-admin/src/components/model-governance/ScoringAssuranceWorkspace.tsx:91) | 同步真实查询范围；风险需说明，高级原始值折叠 | 初中／数值作答；当前实际风险级别及含义 |

## D. 有问题的核心流程

| 当前流程 | 问题 | 推荐流程 |
| --- | --- | --- |
| 任务列表→下载任务包→无答案→恢复仍403→可填写并点同步 | 普通教师取材依赖管理接口，已授权答案未采用 | 任务级context取材→文字/图片可核对→编辑与保存→同步；材料缺失只允许保存草稿及重读 |
| 分类规则读取失败→空表单→保存新规则 | 读取失败被当成未配置，可覆盖当前分类 | 先成功读取当前规则→编辑→变更摘要→保存新版；失败禁保存并同页重读，明确空目录变更影响 |
| 第1题读取中→切第2题→旧结果覆盖→操作第1题组 | 题号和实际操作对象断裂 | 切题立即失效旧请求/对象/弹窗→仅采用当前题响应→确认题号及份数→显示该题回执 |
| 高中英语作文模型组合→验证评分准入→默认初中数学 | 入口摘要和实际查询范围不一致 | 同一学校/学段/学科/具体题型贯穿绑定、评测、准入；无法映射时先选范围，禁止查别的默认值 |
| 保存评分细则成功→回读失败→只剩通用错误 | 教师无法确认究竟是否已保存；已有重读但无持久回执 | 持久显示评分已保存及版本→保留编辑快照→单独重读已存结果→恢复成功后开放下一步 |
| 打开校正/回标差异/恢复本机队列→读取失败→加载或空态 | 短暂报错消失或错误只在诊断页，原页无有效恢复 | 恢复中→持久具体错误→同页重读；回标保存所选批次，恢复成功再显示真实空态 |
| 评分标准冲突→比较→本地侧只有题干/分值 | 不能比较本地答案和评分点，难决定保留哪边 | 本地与最新均展示答案/评分点/扣分规则；差异高亮→选择保留与合并→新版本再保存 |

## E. 开发修改顺序

第一批 P0 并行：相似答案分组请求/对象隔离、桌面任务级取材、题库规则读取失败保护。分别修改 [apps/web-admin/src/features/answer-groups/AnswerGroupingDrawer.tsx](D:/project/yuejuan/apps/web-admin/src/features/answer-groups/AnswerGroupingDrawer.tsx:98)、[apps/desktop-client/src/components/OfflineWorkbench.tsx](D:/project/yuejuan/apps/desktop-client/src/components/OfflineWorkbench.tsx:862)、[apps/desktop-client/src/api/review.ts](D:/project/yuejuan/apps/desktop-client/src/api/review.ts:26)、[apps/web-admin/src/features/question-bank/QuestionBankDialogs.tsx](D:/project/yuejuan/apps/web-admin/src/features/question-bank/QuestionBankDialogs.tsx:153)、[apps/web-admin/src/features/question-bank/QuestionBankPage.tsx](D:/project/yuejuan/apps/web-admin/src/features/question-bank/QuestionBankPage.tsx:601)。每线验证各自真实角色与失败场景，完成后共同检查权限与对象作用域。

第二批 P1 并行：范围贯穿／冲突比较／保存回执、读取失败恢复、可读对象与同名人员、教师技术文案。需按B表逐条对应文件，关联 [apps/web-admin/src/components/model-governance/ManagedGovernanceWorkspace.tsx](D:/project/yuejuan/apps/web-admin/src/components/model-governance/ManagedGovernanceWorkspace.tsx:132)、[apps/web-admin/src/components/model-governance/ScoringAssuranceWorkspace.tsx](D:/project/yuejuan/apps/web-admin/src/components/model-governance/ScoringAssuranceWorkspace.tsx:158)、[apps/web-admin/src/features/paper-rubric/hooks/useRubricEditor.ts](D:/project/yuejuan/apps/web-admin/src/features/paper-rubric/hooks/useRubricEditor.ts:114)、[apps/desktop-client/src/features/scan-queue/useScanQueue.ts](D:/project/yuejuan/apps/desktop-client/src/features/scan-queue/useScanQueue.ts:74)、[apps/web-admin/src/components/RegistrationCorrectionWorkspace.tsx](D:/project/yuejuan/apps/web-admin/src/components/RegistrationCorrectionWorkspace.tsx:264)、[apps/web-admin/src/pages/QualityDashboardPage.tsx](D:/project/yuejuan/apps/web-admin/src/pages/QualityDashboardPage.tsx:279)、[apps/web-admin/src/pages/QualityDashboardViews.tsx](D:/project/yuejuan/apps/web-admin/src/pages/QualityDashboardViews.tsx:128)；同一文件只分给一个修改者，其他线给接口/调用侧配合。

第三批 P2：本轮没有新增确认的P2。等P0/P1完成后再验窄屏、键盘操作、视觉层级及现场桌面工作流，避免掩盖实际任务阻断。

本轮现有前端测试共执行951次，全部通过（按执行次数合计，不宣称独立场景数）；学生198、桌面304、教师206、管理与根交互243。后端3包、CAS和任务权限定向证据已归档。上述通过不覆盖新发现的所有故障路径。

生产组件浏览器证据：[切题对象错位](D:/project/yuejuan/output/playwright/ux-recheck-20261009-final/group-question-mismatch.png)、[普通教师无答案](D:/project/yuejuan/output/playwright/ux-recheck-20261009-final/desktop-teacher-no-answer.png)、[校正加载失败](D:/project/yuejuan/output/playwright/ux-recheck-20261009-final/correction-source-failure.png)；前三个P0有[独立源码复核](C:/Users/pc/.codex/visualizations/2026/10/07/01a1155f-d593-7153-9595-8c482e7ddecf/ux-recheck-20261009-final/independent-p0-review.json)。截图来自受控组件夹具，不用于断言整页生产视觉。

本轮实际文件全文审读105份，未全文审读2493份保持pending，见[逐文件台账](C:/Users/pc/.codex/visualizations/2026/10/07/01a1155f-d593-7153-9595-8c482e7ddecf/ux-recheck-20261009-final/file-review-status.csv)。99修复检查项已逐项完成，未把文件枚举当审读。生产代码只读，未回退已有修改；期间基线2597文件中13处发生其他写入、0缺失，归因未确认且已重核相关结论，见[变化记录](C:/Users/pc/.codex/visualizations/2026/10/07/01a1155f-d593-7153-9595-8c482e7ddecf/ux-recheck-20261009-final/source-hash-comparison.json)。旧测试早于部分外部改动；真实学校多账号、数据库写入、打包Tauri及现场扫描仪仍待验收。

