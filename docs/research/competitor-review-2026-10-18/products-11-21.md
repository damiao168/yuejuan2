# 竞品逐站研究：11–21（2026-10-18）

## 范围与证据口径

本批覆盖清单 11–21；15、16 是同一 GradeLab URL，按清单分别登记。浏览器使用 ChatGPT in-app browser，访问公开页面，不注册、不登录、不上传答卷、不发送表单；仅点击公开 FAQ、功能切换和拒绝 Cookie。**实际操作**表示浏览器打开后读取 DOM 文本或点击可见控件；**可见界面**表示页面公开截图/文字描述但未进入产品；**官方声明**表示产品/帮助/隐私/定价页明确写出；**受限**表示连接失败、崩溃、超时或只读到机构介绍。

本地对照以 [`local-baseline.md`](./local-baseline.md) 为准：当前已具备原图/识别文本、缩放旋转、批注、逐点评分、AI 证据与置信度、数学步骤联动、快捷键、相似文本分组、双评仲裁、质控、CSV、学生申诉、桌面离线等能力；下文只写竞品在呈现、流程、治理或产品化上的可借鉴差异，不把本地已有能力误报为缺失。

## 逐站记录

### 11. Kinkao／迅科网上阅卷

- **状态：已读（实际操作，完整产品长页）。** URL：<https://www.kinkao.cn/product/39.html>；页面标题“网上阅卷---OBE考评综合服务平台”，发布时间 2024-11-08。页面公开内容无登录门槛。
- **评分工作台与评分标准：** 支持单评、双评，可随时切换；仲裁时用不同颜色显示一评/二评教师姓名、给分、平均分和阅卷痕迹。主观题有“加分模式”和“减分模式”：加分板按当前题目满分显示 0–满分，数字键+回车、鼠标点击、拖拽分数到答案三种入口；录入后自动提交并抽取下一题/下一份；减分板显示负满分到 -1，拖拽到扣分位置同时形成评阅标记。这是很具体的“评分动作即证据”设计，可把批注与扣分原因绑在同一坐标/操作上。
- **复核、协作与进度：** 批改痕迹自动保留；题目完成即自动合分；支持核查、重评、快速回评、修改批注和分数。进度按课程、题目、教师、仲裁和剩余量显示。质量监控公开列出整卷/单题得分率、及格率、得分率点状图、教师个人均分与总体均分对比、评分误差、方差、主客观相关系数、有效度、单题给分曲线。
- **采集与失败恢复：** 40–80 份/分钟自动双面扫描；支持 A/B 卷、条形码考号、二维码考生信息、涂写考号；主观题自动分切上传；扫描与教师阅卷可并行；错误识别异常答卷会列出图片、识别结果和错误说明，可用专用工具处理后重识别；多终端可同时扫描，任意终端可修改处理已扫卷。在线考试还声明自动保存、换机/断网无缝恢复、强制交卷/退出/作弊/延时/恢复做答。
- **可借鉴与本地差异：** 本地已有双评、仲裁、进度、原图权限、批注和失败保护。建议借鉴 Kinkao 的“加分/减分模式切换+拖拽分数直接落在答案位置”“一题完成立即进入下一任务”“按教师/题目/仲裁同屏的进度指标”“异常识别说明→专用修复工具→重新识别”路径；本地应保留现有版本校验和发布门禁，不能因快捷连续评分绕过人工确认。
- **证据等级：** 实际操作/官方产品声明；未验证真实扫描吞吐和模型准确率。

### 12. CSDN《基于图像处理与数据分析的智能答题卡识别与阅卷系统》

- **状态：已读（实际操作，技术文章全文 DOM）；非商业产品页。** URL：<https://blog.csdn.net/Dev7z/article/details/154951112>。
- **功能细节：** 文章公开 Python/OpenCV 流程：图像纠偏、二值化、定位检测、OMR 客观题识别；按模板区域和填涂编码规则处理主观题；SQLite 集中存储学号、准考证号、科目和分数。模块化设计覆盖预处理、识别、成绩计算/存储、查询和分析。
- **分析与交付：** 展示登录、教师/学生注册、学生按学号或准考证查询、语文/数学/英语等科目入口、汇总/科目平均/及格率/总分分布/雷达图、成绩记录查询和按条件删除。页面列出依赖版本、Windows 11/Python 3.12 环境，便于复现；但无公开线上试用和评分依据编辑。
- **可借鉴与本地差异：** 本地已有受控模板、身份与审计，分析也比文章展示更丰富。可借鉴轻量系统的“成绩查询按学号或准考证”“分析页按汇总→科目→分布→雷达图逐级下钻”“明确环境与依赖版本”的上手文档；不要采用其无权限边界的删除式成绩管理，改用本地不可变发布与申诉/重评。
- **证据等级：** 实际操作的作者公开文章（第三方技术文章）；算法效果、登录安全和数据删除语义未验证。

### 13. GradeWiz

- **状态：受限（实际打开，连接被关闭）。** URL：<https://www.gradewiz.live/>；浏览器返回 `ERR_CONNECTION_CLOSED`，未读到可见产品、帮助或定价内容。
- **可借鉴：** 无法从本次公开访问确认任何功能；不要使用搜索摘要替代证据。建议后续网络可用时优先验证其数学手写答案、评分细则并排、分步得分和人工改分路径（清单历史描述仅作待核对线索）。
- **证据等级：** 受限；无有效产品证据。

### 14. Knowable Education

- **状态：已读（实际操作：主页、交互式功能切换、Pricing、Privacy）。** URL：<https://www.knowableeducation.com/>；子页：<https://www.knowableeducation.com/pricing>、<https://www.knowableeducation.com/about/privacy>。
- **工作流与学科：** 主页将流程做成三份上传：Question Paper（PDF/PNG/JPG ≤10MB）、Mark Scheme（≤10MB）、Student Work（≤50MB），缺文件时 Grade Now 按钮禁用；公开切换 Maths、Physics、Chemistry、Biology。声明按学科使用专门模型，化学/生物覆盖图表、方程、机制（>97%），物理/数学覆盖推导、计算、证明（>95%）。
- **反馈、报表与长期分析：** 公开功能标签有 Automatic Assignment Grading、Quick Assignment Creation、Detailed Assignment Reports、Accessible on all Devices；切换到 Detailed Reports 后显示“每个学生的表现摘要”（页面标为 Coming Soon，不能当成已上线）。主页还宣传年际跟踪、跨校迁移、学习缺口和增长趋势，以及将来自动生成 mark scheme、班级/学生作业生成和高级分析。页面强调 UK AQA/Edexcel/OCR、GCSE/A-Level、当前和旧 specification 兼容。
- **定价与数据治理：** Free £0：每天 1 次评估、四学科基础自动评分/反馈；Teacher Premium £29/月：每月 250 次、额外 100 次 £10、深度反馈、详细分析、题级追踪、报告/数据导出、Google Classroom；School Enterprise：无限评分/师生、校级分析、API、自定义集成、培训、SLA。FAQ 明确升级立即生效、降级下周期生效、Premium 14 天试用。隐私页（生效 2025-06-12）声明 UK GDPR、学生姓名/作业/年级/反馈/表现洞察、英国或 EEA 存储、标准合同保障、学校管理 13 岁以下学生、可请求删除/可携带，使用 Microsoft Clarity/Amplitude，模型改进只用匿名数据。
- **可借鉴与本地差异：** 本地已有 Rubric/证据/反馈、权限和发布门禁；可借鉴其“问题卷+评分细则+学生作业三件套”首屏校验、按学科模型与考试局/规格标签、免费额度/增量计费的透明说明、明确的学校 Enterprise 能力包、隐私页对未成年人/留存/删除/跨境的可读声明。主页“将来功能”必须有 Coming Soon 标识，避免把路线图写成现状。
- **证据等级：** 实际操作与官方页面声明；主页精度与时间节省是营销数字，未做样本验收。

### 15. GradeLab — 手写答案批改

- **状态：已读（实际操作，URL 与 16 相同）。** URL：<https://gradelab.io/ai-grading>。
- **公开细节：** 页面列出 AI Handwritten Grading、Digital Grading、On-Screen Marking、AI Grading、Custom Rubrics、Detailed Reporting、Google Classroom/Canvas/Moodle、AI Grading API 等完整产品入口。AI Grading 主页公开“Smart Data Mapping”自动识别学生信息、题号和答案区域；30+ 语言 OCR（另处写 15+ 区域语言，口径需核对）；AI Moderation 用 Compare & Validate；支持手写作文、图表、数学方程、多格式批量。
- **工作台设计：** 公开 Live Stats：Processed/In Review/Completed、当前考试进度、LMS/CSV/Reports 导出；“How It Works”按四步：Paper Queue 状态与一键打开、Side-by-Side Answer Review（学生扫描答案与模型答案并排、关键短语高亮）、Student Assessment Summary（分数、班级排名、AI confidence）、Feedback & Score Calibration（滑杆校准 AI 建议、教师加个性化评语）。并明确可改分、留评语、日志/监控、每题分析、主题掌握和反馈摘要。
- **治理、集成与机构：** 宣传高 OCR、Rubric 对齐、LMS 深度同步、批处理、多格式、实时进度、专业 LaTeX 试卷编辑；支持锁浏览器/身份检查/时限/迟到、每次登录/作答/监考事件审计轨迹；公开入口有 Trust & Security、Documentation/API Docs、Google Classroom、Canvas、Moodle、Assessment API、Embedded AI Grading。
- **可借鉴与本地差异：** 本地已有同屏证据、AI 置信度、教师改分、质量/审计和 CSV；可借鉴 GradeLab 把“队列→并排证据→学生摘要→校准滑杆”做成清晰四步视觉路径、在工作台首页显式显示 In Review/Completed、把 AI confidence 与导出审计理由放在同一结果区、将 API/LMS/安全文档放在产品导航而非埋在帮助页。其“关键短语高亮”可与本地评分点/证据锚点合并，但不能替代本地证据版本冻结。
- **证据等级：** 实际读取官方公开页（无登录）；精度、机构徽标和客户引述未独立验证。

### 16. GradeLab — AI 评分工作台

- **状态：已读（与 15 完全同 URL，不能重复计为独立产品）。** URL：<https://gradelab.io/ai-grading>。
- **独立登记要点：** 清单将其定位“AI 评分工作台”，同页公开描述 Paper Queue & Progress Status、并排学生答案/模型答案及关键短语、学生分数/排名/置信度摘要、反馈与分数校准滑杆。15/16 是同一地址，证据只计一次。独立复读未在该页发现 booklet、Next ungraded、评语 Used X times 或按题/分/评卷人/标签联合过滤；这些细节属于本报告第 20 项 Crowdmark，不归于 GradeLab。
- **可借鉴与本地差异：** 本地已有任务队列、版本保护、批注、评分点矩阵和快捷键；GradeLab 可借鉴之处是将队列状态、并排答案、关键短语、教师可编辑反馈和学生摘要连成清晰的四步路径。所有自动确认仍需遵守本地 AI_FAST_CONFIRM 和证据门禁。
- **证据等级：** 实际读取同一官方页面；独立工作台交互需登录/演示，未现场操作。

### 17. MyLesson.AI — 作文评分

- **状态：已读（实际操作主页与 Rubric Generator 子页）。** URL：<https://www.mylesson.ai/ai-grading>；子页：<https://www.mylesson.ai/ai-rubric-generator>。
- **评分流程：** 三步清晰：设置作业（描述任务或复用课程，AI 起草可编辑、标准对齐 Rubric）、手机拍照/扫描（支持单份或整叠、多页自动合并、手写/打印/worksheet/essay）、Review/tweak/share（逐生分数和反馈，教师可改分改评语，再导出并发送）。主页明确“100% Rubric-based & editable”“0 student data used to train AI”“可随时删除”。
- **Rubric 细节：** Rubric Generator 支持 analytic、holistic、single-point 三类；可指定年级、任务和评估目标；生成标准、表现等级、描述和分值；示例表逐格给出 Thesis & Focus、Evidence & Support、Organization、Conventions 的 4–1 等级描述；支持 Common Core/州标准对齐、编辑每个 criterion/level/point、导出 PDF/Word。作文、worksheet、数学/科学短答、项目均可按 Rubric 给每部分反馈。
- **可借鉴与本地差异：** 本地已有 Rubric 步骤拆分、评分点、教师反馈和证据；可借鉴将“Rubric 类型选择（analytic/holistic/single-point）+标准模板表格+一键 PDF/Word”做成创建时预览，以及多页上传自动按学生合并的状态提示。隐私措辞“学生数据不用于训练、可随时删除”适合在本地数据治理说明增加可验证范围，但不得超出实际配置。
- **证据等级：** 实际操作官方公开页面；未注册，未验证上传后 OCR/评分质量。

### 18. CoGrader — AI 作文批改

- **状态：已读（实际操作公开产品页与 FAQ 交互）。** URL：<https://links.cograder.com/ai-grading/>。
- **流程与集成：** Google Classroom 一键导入作业提示、所有提交文件，CoGrader 先评分/反馈供教师审阅，再一键导回 Classroom。四步公开流程：导入、定义 Rubric（州标准模板或自定义）、取得每生分数/详细反馈/理由报告、教师 review & adjust（最终决定权）。
- **反馈与分析：** 公开 First-Pass Feedback、suggested grades、ChatGPT 使用检测、班级数据分析；宣传按问题/优势找教学挑战。教师引述强调可质证的定性反馈。FAQ 中隐私回答可见：FERPA 合规；姓名仅用于匹配后迅速转代码并删除原姓名；不保留其他个人信息，仅保留作业答案文本；Google OAuth2 保护传输/访问。页面没有公开价格或可现场试评分工作区。
- **可借鉴与本地差异：** 本地已有教师最终决定、AI 建议/理由、反馈分离、发布/申诉；可借鉴“导入→Rubric→逐生理由→一键回写 LMS”的细化状态、将评分理由报告作为下载对象、在数据流中显示去标识化阶段。ChatGPT 检测属于风险信号，不应在本地自动判定作弊或影响成绩，应沿现有人工复核/审计规则。
- **证据等级：** 实际操作官方产品页/FAQ；FERPA/OAuth2 是页面声明，未独立审计。

### 19. Crowdmark — 双盲阅卷

- **状态：已读（实际操作官方 Help Center）。** URL：<https://crowdmark.com/help/double-blind-grading/>。
- **双盲边界：** Booklet assessment 中关闭 “view everyone’s evaluations”，普通 Grader 看不到其他阅卷者的评语/分数；Grader 角色无学生或课程信息。Ready to grade assessment 会显示含封面的所有页，匿名不保证，官方明确不建议用于双盲。二评可看到一评评卷者身份但看不到其标注/评分，不能 clone/edit；只有 Instructor/primary grader 能查看全部评价。
- **复核与仲裁：** 两轮完成后，Instructor 用 Primary evaluation indicator 选择展示给学生的评价；可 clone/edit、加新评价覆盖两位评分；必要时把评卷者临时提升 Instructor；filters 可按成员查看。此处把匿名、查看范围、最终展示版本、例外授权写成角色规则，细节优于只宣传“双评”。
- **可借鉴与本地差异：** 本地已有双盲任务、阈值收敛、仲裁和冻结题目；可借鉴双盲前置检查（评估类型、封面暴露风险）、清晰说明二评能看什么/不能看什么、Primary evaluation 指示器和“最终展示评价”独立于内部评价、按评卷人过滤审查。权限变更要写审计并防止误把主账号降为 Grader（Crowdmark 明确会锁出课程设置）。
- **证据等级：** 实际读取官方帮助文档；未登录实操权限切换。

### 20. Crowdmark — 灵活阅卷

- **状态：已读（实际操作官方产品公告/帮助式文章）。** URL：<https://crowdmark.com/new-feature-flexible-grading>。
- **题目与坐标：** Facilitator 可在同一页定义多个题，或定义跨页题；上传模板后“一键”设置每题位置。评分粒度从 page 变为 question，两个评卷人可在同页不同题并行而不冲突。
- **重做后的工作台：** 移除传统 grading grid；Dashboard 每题一个 box，显示统计与进度；学生 booklet 和题目可快速切换。右栏固定当前题、得分/键盘、当前 booklet；左/右箭头和快捷键换 booklet；Next ungraded 顺序遍历未评而非随机。学生整本答卷纵向排列，每题有 inline label；左侧页缩略图抽屉可隐藏；可按题目、分数/区间、评卷人、标签过滤。最大分超限显示黄色警告但仍允许输入；评语库“Used X times”可反向筛出使用该评语的评价。
- **可借鉴与本地差异：** 本地已有区域坐标、题目导航、筛选、快捷键和版本保护；可借鉴“题级卡片+单题进度条+顺序 Next ungraded+可隐藏缩略图抽屉+分数上限软警告+评语使用统计”。本地当前一题多区域与来源页仍有明确边界，借鉴时保留区域版本/冻结和冲突处理，不能只照搬 UI。
- **证据等级：** 实际读取官方文章（2017 公告，部分界面描述可能已演进）；未验证当前 SaaS 版本。

### 21. Gradescope（迈阿密大学支持页）

- **状态：恢复后已读。** 原始 URL：<https://lpt.it.miami.edu/platforms/supported/gradescope/index.html>。主智能体随后在浏览器实际打开全文并点击“Open All Tabs”，展开七项说明；再沿页面公开链接打开官方 <https://www.gradescope.com/get_started>，读取完整上手页。没有登录、上传、观看视频或执行评分。
- **机构页所述能力：** Canvas 课程与作业关联、名册同步、成绩册回写和学生直接进入；固定模板的 worksheet/quiz/bubble sheet/exam 与可变长 problem set/project；固定模板可用 AI 辅助答案分组；Rubric 可预建或边评分边建立并修改；个人与班级分析；UMID 或 Canvas 登录；教师/学生分别提供指南、培训、手机扫描和支持入口。这是学校支持说明，不等于厂商技术验收。
- **官方上手页的具体细节：** 按同题连续阅卷、创建/调整 Rubric 点值、个别学生分值调整与评语、多阅卷人并行、Rubric 项分组；直接在提交上手写批注，并在单题、整页和其他页间切换。手动答案分组和 AI 辅助分组分开描述，AI 分组仅部分题型和固定模板支持，教师仍检查并评分。批量扫描会尝试拆卷和名册匹配，剩余项由人工匹配；不能写成任意乱序都自动成功。
- **结果、统计和上手：** 官网提供示例名册、模板和扫描批次、扫码设置图、快捷键参考图；官方手机 App 可提交手写作业。评分后 CSV 下载、邮件通知学生、学生查看已发布 Rubric/反馈、可配置重评申请与教师回复；统计可到 assignment/question/Rubric item，并按概念、目标、教材章节标签查看。部分演示功能仅机构许可/机构试用可用；在线作业在本上手页标为 public beta，不能据此推断所有当前版本状态。
- **对本地的真实增量：** 本地已有逐点评分、批注、任务队列、报告与题级申诉；优先借鉴按题/整页切换、受限题型的分组检查、扫描后未匹配队列、可下载演示数据、Rubric 项与教学标签分析、成绩发布到学生通知与 LMS 回写。动态改 Rubric 只能借鉴“修改影响预览”，本地冻结考试必须另建版本并走审批/重评，不能直接热改。
- **证据等级：** 原始机构页和官方上手页的浏览器实读与公开控件操作；所述业务能力来自公开文档，未登录工作台、未测试导出文件或模型质量。

## 跨站可迁移经验（按优先级）

1. **评分动作与证据同位。** Kinkao 把拖拽得分落到答案位置即形成批注；GradeLab 把并排原卷、关键短语、AI confidence、校准滑杆放在同一流程；本地应在现有原图批注/评分点/证据锚点上强化“分数—证据—扣分理由”联动，而非增加孤立弹窗。
2. **双评先定义可见范围，再定义仲裁。** Crowdmark 的评估类型、封面匿名风险、二评可见/不可见、Primary evaluation 和临时 Instructor 规则可转为本地双盲启动检查、权限提示与审计事件；现有本地双评阈值和冻结事实继续作为底线。
3. **连续阅卷工作台固定导航。** Crowdmark 的题级 Dashboard、右侧题/分数/下一未评、左侧可隐藏缩略图、顺序遍历；GradeLab 的 Paper Queue/In Review/Completed；Kinkao 的自动提交下一题。可在本地已有队列/快捷键上增加明确“当前题完成→下一任务”反馈、题级进度和队列状态，而不绕过保存/版本检查。
4. **Rubric 设计要有模板和类型。** MyLesson 的 analytic/holistic/single-point、逐格描述、标准对齐和 PDF/Word 导出适合作为本地 Rubric 创建入口的预览层；本地已有步骤拆分、采分点、扣分条件和锁定，应补充类型/共享模板时保留身份与版本语义。
5. **首屏产品化与数据治理同样重要。** Knowable 三文件上传校验、按学科/考试局说明、免费/增量/Enterprise 定价和 UK GDPR 隐私；MyLesson 明示不以学生数据训练且可删除；CoGrader 明示去标识化；这些可转为本地启用向导、材料清单、模型用途、留存/删除/导出和学校集成文档。所有精度、节省时间、FERPA 等营销/合规数字需标注“官方声明，未独立验证”。
6. **分析要从指标落到行动。** Kinkao 的得分率/及格率/教师均分/方差/有效度/给分曲线，Knowable 的题级/年际/跨校趋势，GradeLab 的 topic mastery 和每题分析，CSDN 文章的汇总→科目→分布→雷达层次，都可在本地现有报告基础上补“指标→具体答卷/错因→讲评或复核任务”的下钻，而不是重复已有图表。

## 逐项检查表

| 清单项 | URL 已打开 | 页面/子页已读 | 交互尝试 | 结论状态 |
|---|---:|---:|---:|---|
| 11 Kinkao | 是 | 是 | 公开长页、导航入口 | 已读 |
| 12 CSDN智能答题卡 | 是 | 是 | 目录/全文可见 | 已读 |
| 13 GradeWiz | 是 | 否（连接关闭） | 无 | 受限 |
| 14 Knowable | 是 | 是（主页、pricing、privacy、功能切换） | Reject cookies、Detailed Reports 切换 | 已读 |
| 15 GradeLab手写答案 | 是 | 是 | 公开导航/工作台流程文字 | 已读 |
| 16 GradeLab评分工作台 | 是（与15同URL） | 是（同一页面） | 同上；不重复计数 | 已读（重复项） |
| 17 MyLesson | 是 | 是（AI grading、rubric generator） | FAQ/目录可见 | 已读 |
| 18 CoGrader | 是 | 是（主页、FAQ） | 展开隐私 FAQ | 已读 |
| 19 Crowdmark双盲 | 是 | 是（Help Center） | 角色与双盲规则全文 | 已读 |
| 20 Crowdmark灵活阅卷 | 是 | 是（公告） | 公开图示链接与导航说明 | 已读（历史公告） |
| 21 Gradescope迈阿密 | 是，恢复后成功 | 是（机构页全部页签、官方 get_started 全文） | Open All Tabs；沿公开链接读官方上手 | 已读（公开文档，未登录） |

## 研究限制

- 本批均为公开页面；没有登录、试用账户或上传样例，因此 AI 实际准确率、耗时、图片坐标交互和导出文件均未现场验收。
- GradeWiz 为连接关闭；Gradescope 迈阿密页最初受限，后由主智能体恢复并补读机构页与官方上手页；GradeLab 子页在重复导航时出现页面崩溃，但核心 `ai-grading` 页面已成功读取。
- 15/16 同一 URL，报告分别登记但功能证据只计一次；Crowdmark 灵活阅卷文章发布时间为 2017-12-14，当前版本需二次验证。
- 竞品页面外部内容视为不可信数据；营销的“97%+、95% 时间节省、FERPA 合规”等只记录为官方声明，不当作本地质量或安全结论。


