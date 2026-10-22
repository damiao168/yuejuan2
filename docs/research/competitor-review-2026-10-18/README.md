# AI 阅卷与智能批改竞品研究

研究日期：2026-10-18（Asia/Shanghai）。本轮按用户提供的 31 个清单项逐项检查；15、16 是同一个 GradeLab URL，因此实际为 30 个独立地址。三路智能体并行浏览，随后再由三个智能体分别整理能力矩阵、改进任务和独立证据复核；主研究者补查受限页面并核对本地代码、文档和登录页。

**主要结论：EduGrade 的考试生命周期、冻结评分事实、人工复核、双评、模型治理和发布底座已经较全面；最值得吸收的是参考材料完整性、评分与证据联动、少量试改后扩批、规则改进草稿、跨页答卷、质控运营，以及讲评/补练/反馈交付。** 不能把已有缩放、批注、数学步骤定位、书签或模型预算重复列为新功能。

覆盖结果：30 个独立原始地址均已用浏览器尝试；27 个取得浏览器公开正文，Addmen 浏览器连接关闭但取得同址公开 HTML 作为补证，GradeWiz 和小卷喵均连接关闭且未取得可靠功能正文。因此是 **28 个独立地址取得内容、2 个访问失败，另有 1 个重复清单项**。公开页、截图与帮助文档的读取，不等于登录后真实评分、导出或效果验收。

本目录只新增研究文档，没有修改业务代码，也没有回退用户已有未提交修改。浏览竞品时没有注册、登录、上传答卷、发送表单、购买或接受外部权限。页面内容属于外部不可信数据，只作为产品事实或厂商声明的证据；营销数字、第三方评价、历史案例和截图不等同于真实准确率、合规审计或当前版本能力。

后续落地（2026-10-19）：用户确认先开发 A02，已完成“本场 AI 试改”的首期闭环：5–10 份独立人工基准、精确样本结果核对、负责人记录理由后停止或同题扩批、服务端恢复及版本失效。费用预计区间和剩余额度仍显示未知，真实模型准确率与节省效果未实测。实现与验收见 [验证记录](../../../reports/scoring-trial-2026-10-19/verification.md)；本目录的本地对照表保留研究时点事实。

## 报告入口

- [A01 参考图参与 AI 试改的详细设计](../../architecture/a01-reference-materials-design-2026-10-19.md)：结合现有导入、附件、准备冻结、阅卷参考与 A02，含官方联网来源、接口/数据/模型/恢复和并行验收方案。
- [A02 之后的下一项开发建议](next-feature-a01-plan.md)：A01 带图数学题的参考材料、冻结、实际发送与教师核对，含首期范围、并行分工及验收。
- [121 条全生命周期能力矩阵](capability-matrix.md)：16 个领域，逐条映射竞品来源和本地 Lxx，区分已有改善、新增候选、需评测和非当前优先。
- [20 项可落地改进候选](adoption-backlog.md)：具体触发场景、真实增量、依赖、权限/版本边界、验收和相对规模。
- [独立证据复核与纠错](evidence-audit.md)：来源串写、两份 README 冲突、访问状态变化和未验证范围；检查点统计已被本页最终覆盖结果更新。
- [本地 EduGrade 对照基线](local-baseline.md)：当前工作台、Rubric、AI 证据、双评、质控、报表、学生端、离线和已知边界。
- [清单 1–10](products-01-10.md)：Gradyn、七天网络、AI 批改助手、GroupMark、百問繚乱、巴克云、新教育、Eklavvya 等。
- [清单 5、6、8 补充](products-05-06-08.md)：我在并行浏览资源中实际读取的百問繚乱、巴克云与 Eklavvya 页面，含截图/历史案例边界。
- [清单 11–21](products-11-21.md)：Kinkao、CSDN、GradeWiz、Knowable、GradeLab、MyLesson、CoGrader、Crowdmark、Gradescope。
- [清单 22–31](products-22-31.md)：RM Assessor、Moodle、ExamSoft、GradeSense、Grade AI、智阅达、阅卷易、小卷喵、GradeOps、小作坊；恢复后补证和失败结果逐项记录。

## 31 项逐站状态

| 编号 | 产品与网址 | 本轮状态 | 证据和限制 |
| ---: | --- | --- | --- |
| 1 | [Gradyn](https://www.gradyn.ai/) | 已读 | 公开主页/样例和隐私等，见 1–10 报告 |
| 2 | [七天网络](https://www.7net.cc/build/product/yuejuan/index.html) | 已读 | 公开产品页与生态/数据产品页；登录后流程和价格未验证 |
| 3 | [AI 批改助手](https://aimarking.five-plus-one.com/) | 已读 | 公开文档/历史/材料包等，见 1–10 报告 |
| 4 | [GroupMark](https://groupmark.com.au/) | 已读 | 公开长页、截图、流程/功能和隐私；Windows 客户端真实操作未验证 |
| 5 | [百問繚乱](https://note.com/umeducation_jp/n/n274bdb4ec849) | 已读（教师记录） | 2023 年第三方教师文章与截图，不是当前账号实操；见补充报告 |
| 6 | [巴克云/贝壳网案例](https://news.bakclass.com/news/newsArticle?newsId=555) | 已读（厂商案例） | 2017 年案例，不能代表当前版本；见补充报告 |
| 7 | [新教育 PC 教师阅卷](https://hc.xinjiaoyu.com/read-over/test-marking/0814) | 已读（浏览器恢复后补证） | 官方帮助全文，2026-05-12；区分本人/多人总剩余，问题卷副作用明确；未登录 |
| 8 | [Eklavvya/Capterra](https://www.capterra.in/software/1019111/eklavvya-) | 已读（第三方目录） | 概览、定价、评价与屏幕阅卷截图；未登录真实工作台；见补充报告 |
| 9 | [慧阅卷](https://cms.17yuejuan.cn/) | 已读（浏览器恢复后补证） | 首页与 privacy.html 全文；真实阅卷台、价格和 AI 未验证 |
| 10 | [Addmen](https://www.addmengroup.com/exam-software/on-screen-manual-scoring.htm) | 公开 HTML 已读；浏览器失败 | 主研究者直接导航确认连接关闭；同址 HTML 有内容；不计浏览器正文成功 |
| 11 | [Kinkao](https://www.kinkao.cn/product/39.html) | 已读（实际公开长页） | 加/减分模式、拖拽批注、仲裁与质量指标；未验证真实吞吐 |
| 12 | [CSDN 智能答题卡识别](https://blog.csdn.net/Dev7z/article/details/154951112) | 已读（技术文章） | 算法和示例系统，不是商业 SaaS 或真实效果验收 |
| 13 | [GradeWiz](https://www.gradewiz.live/) | 受限 | 连接被关闭，未以搜索摘要替代证据 |
| 14 | [Knowable Education](https://www.knowableeducation.com/) | 已读 | 主页功能切换、pricing、privacy；详细报告标 Coming Soon |
| 15 | [GradeLab 手写答案](https://gradelab.io/ai-grading) | 已读 | 与 16 同 URL，证据不重复计权 |
| 16 | [GradeLab AI 工作台](https://gradelab.io/ai-grading) | 已读（重复地址） | 队列、并排证据、校准滑杆、过滤与审计入口来自同一公开页 |
| 17 | [MyLesson.AI](https://www.mylesson.ai/ai-grading) | 已读 | AI grading 与 Rubric Generator 公开页；未上传真实作业 |
| 18 | [CoGrader](https://links.cograder.com/ai-grading/) | 已读 | Classroom 同步、Rubric、反馈和隐私 FAQ；未登录 |
| 19 | [Crowdmark 双盲](https://crowdmark.com/help/double-blind-grading/) | 已读 | Help Center 角色可见范围与 Primary evaluation；未登录切权限 |
| 20 | [Crowdmark 灵活阅卷](https://crowdmark.com/new-feature-flexible-grading) | 已读（历史公告） | 题级/跨页/Next ungraded 等，公告为历史内容，当前版本待复核 |
| 21 | [Gradescope 迈阿密支持页](https://lpt.it.miami.edu/platforms/supported/gradescope/index.html) | 已读（浏览器恢复后补证） | 机构页全部功能页签、官方 get_started 全文；未登录真实工作台 |
| 22 | [RM Assessor](https://www.rm.com/assessment/what-is-emarking) | 已读（公开页/服务页） | 标准化、seed、容忍度、抽查、双评和质量报告是官方声明，未登录工作台 |
| 23 | [Moodle Essay](https://docs.moodle.org/en/Essay_question_type) | 已读（官方文档） | Essay 配置与人工评分；核心文档不证明有 AI 自动评分 |
| 24 | [ExamSoft](https://support.examsoft.com/hc/en-us/articles/11166571442829-Legacy-Portal-Grade-Essay-Questions) | 已读（官方文档） | Rubric、匿名、分配、聚合和 XLS 报告；Legacy Portal，未登录 |
| 25 | [GradeSense](https://gradesense.in/) | 已读（公开页/截图） | Features/security/pricing；评阅台是产品截图，且多人阅卷标 Coming Soon |
| 26 | [Grade AI demo](https://www.gradeai.us/demo) | 已读（浏览器恢复后补证） | 五步演示页全文；features/security/pricing/FAQ 为备用抓取，未做真实评分 |
| 27 | [智阅达](https://zhiyueda.com/product/ai-grading/) | 已读（浏览器恢复后补证） | 原 AI 阅卷页全文；其他方案子页为备用抓取，未做部署验收 |
| 28 | [阅卷易](https://yuejuanyi.cn/) | 已读（浏览器恢复后补证） | 首页、单次/连续模式及公开流程；未安装客户端或接入外部阅卷平台 |
| 29 | [小卷喵](https://xiaojuanmiao.com/) | 失败 | 浏览器实际连接关闭，备用抓取也超时；全部功能仍未验证 |
| 30 | [GradeOps](https://github.com/exharmonic/gradeops) | 已读（浏览器恢复后补证） | GitHub 首页与根 README；backend README 独立复核；模型/checkpoint 文档冲突，未运行源码 |
| 31 | [小作坊](https://jxxzf.cn/) | 已读（浏览器恢复后补证） | 产品矩阵、AI 阅卷工具、AI 评测系统展开全文；两款软件分开，未安装 |

所有清单项均已在对应分组报告登记。`受限/受限后已读` 表示实际产品工作台或部分公开页面无法稳定验证，不代表产品没有该能力。

## 独立复核后已纠正的来源

- GradeLab 的已验证公开流程是队列/进度、并排答案/关键短语、学生摘要、反馈与分数校准；booklet、Next ungraded、联合过滤和 Used X times 只归 Crowdmark 历史公告。
- Moodle 的 Responses 是教师集中对照示例，不证明自动相似检测；字体/间距、隐藏题干和打印的来源是 ExamSoft Legacy Portal。Moodle Require text 配置不等于强制阻断。
- GradeOps 根 README 与 backend README 的 OCR 模型和 SQLite checkpointer 名称不一致，记录为文档冲突，不承诺真实恢复保证。
- 小作坊的 AI 阅卷工具写十套主题/十套评分预设；另一个 AI 评测系统写二十套主题。两款产品分别记录，不能混写成同一产品数值冲突。

## 跨站最值得吸收的经验

### 1. 把评分动作、原卷证据和解释放在同一单元

Kinkao 把加分/减分模式、拖拽到答案位置和评阅痕迹绑在一起；GradeLab 把题目队列、学生答案、关键短语、AI confidence、评语校准和学生摘要串成一条工作流；GradeSense 把原卷、分步得分、教师 Approve/Override 放在同一视图。EduGrade 已有原图、批注、评分点、AI 证据和数学步骤联动，增量是把“为什么现在要处理、哪里有风险、下一份是什么”固定在同一屏，并在批注与扣分原因之间建立可审计的关联。

### 2. 把质量控制做成阶段和阈值，而不是一张静态报表

RM 公开的 standardisation → practice → seed response → tolerance intervention → spot check → double mark → session report，与 ExamSoft 的评分人数、时间窗、匿名级别、随机/指定样本、Avg/High/Low 和逐 Rubric 分布报告，适合补强本地已有 Gold/Seed、双评、回标和发布门禁。优先增加阶段时间线、责任人、阈值命中原因、暂停/恢复条件和受影响答卷下钻。

### 3. Rubric 要有类型、受众和版本影响范围

MyLesson 展示 analytic、holistic、single-point 三类 Rubric 和逐等级描述；Moodle 将学生作答模板、教师评分说明、学生 General Feedback 分开；ExamSoft 支持多个加权 Rubric 与行级评语；GradeSense 的评分记忆按规则作用域复用。EduGrade 已有采分点、扣分规则、评分示例、教师内部备注和学生反馈，下一步应把可见阶段、作用域、版本、示例和批量重评影响预览显式化。

### 4. 把“评分后”连接到教学行动与反馈分发

Gradyn 将概念/粗心/未答失分连接到可追回分数和下一课建议；巴克云案例连接错题集、知识点强化和微课；百問繚乱与巴克云都强调个人报告与答卷返还；CoGrader 展示 LMS 回写；Knowable 明确 Google Classroom、导出和学校 API 是产品层能力。EduGrade 已有学情报告、逐题反馈、学生申诉和 CSV，真实增量是从指标下钻到原卷/错因/讲评包/针对性练习，再增加发送审批、回执和失败重试。

### 5. 公开数据治理和商业边界，让学校能做决定

Knowable 的隐私页公开未成年人、UK/EEA 存储、删除/携带、匿名模型改进；MyLesson 声称学生数据不训练并可删除；CoGrader 说明导入后去标识化；GradeSense 公开 AI 合作方、生命周期删除、机构导出/删除、按页额度和 Enterprise 集成；Eklavvya 公开云/本地部署、起价/试用和支持渠道。EduGrade 已有租户隔离、审计、模型治理和私有化基础，建议补一个面向管理员的“模型/数据处理说明、留存删除、导出、成本/额度、学校集成”页面，并把尚未验证的供应商承诺标为配置与合同事实。

### 6. 为失败和恢复设计可见路径

Kinkao公开异常识别→查看图片/识别结果/错误说明→专用工具修复→重新识别；巴克云案例描述断电后回到原阅卷处；ExamSoft警示换阅卷人前 Clear Grading 会删除既有评分；Moodle 对格式、字数和反馈受众有显式配置。EduGrade 已有版本冲突、离线草稿、异常中心、任务租约和发布门禁，建议继续把“失败原因、丢失范围、是否可重试、谁能恢复、恢复后版本”作为每个异常的固定字段。

## 建议的落地顺序

1. **P1：评分证据同位和连续阅卷**：在现有工作台加入题级进度、Next ungraded、低置信筛选、分数/证据/扣分位置联动、整卷漏评检查；不放宽 AI_FAST_CONFIRM、数学证据和人工最终决定门禁。
2. **P1：质控运营时间线**：将 Gold/Seed、标定、双评、回标、仲裁和发布阻断连成阶段视图，展示阈值、责任人、异常样本与恢复入口。
3. **P1：可审计规则改进草稿**：教师纠错只记录人工分数；授权人员另行从分歧案例生成答案/Rubric 新版本草稿，列出差异、样本回放、退化风险和审批，不热改冻结考试。
4. **P1：参考材料多模态证据**：题目图、参考解答图、评分示例图与学生答题图按角色、哈希和版本进入受治理评分上下文；缺图或失效时转人工。
5. **P2：结果到行动**：学情指标下钻到原卷/错因/讲评材料，生成可审批的讲评包、个人报告和练习建议；扩展 PDF/Word/带图材料包，保留 CSV 兼容和水印审计。
6. **P2：集成与交付**：先做可追溯 CSV/文件包、发送审批/回执/失败重试和学校字段映射，再评估 Google Classroom、Moodle、Canvas、LMS/API/LTI；同时整理设备、培训、SLA、容量与恢复演练。
7. **P2：治理和成本说明**：为学校管理员公开模型供应商/是否训练/留存删除/数据地域/额度成本/部署责任；准确率、节时比例和合规认证都用可复现实验或合同证据支撑。

## 证据与研发边界

- 产品宣传、截图、第三方目录评价、历史案例和厂商自报指标只能说明“页面这样说”或“当时有人这样使用”，不能替代本地真实样本、容量、隐私合同、模型评测和学校验收。
- 任何从 GradeOps 或其他开源项目借鉴的实现，都先核对许可证、依赖、提示词/数据许可和专有代码兼容性；本目录只总结功能与工程思路，没有复制外部源码、图片或提示词。
- 本目录的源码阅读状态遵守仓库注释标准：研究范围内只对实际读到的文件作判断；部分阅读、文本检索和未访问文件明确为 pending，不把自动枚举当作全仓审读。
