# 竞品研究证据复核与纠错

复核日期：2026-10-18（Asia/Shanghai）。本文件只新增独立审查记录，不修改业务代码，不覆盖其他智能体的研究文档。已阅读全文：根目录 `AGENTS.md`、`docs/engineering/commenting-standard.md` 以及本目录六份既有报告；原始 31 项清单也已逐项对照。15/16 是同一地址，实际只有 30 个独立 URL。

## 本次直接核对的来源

| 来源 | 本次方式 | 实际取得的内容 | 边界 |
| --- | --- | --- | --- |
| [GradeLab 原始页](https://gradelab.io/ai-grading) | CUA 浏览器直接打开，读取完整可见正文与 AX | 产品导航、状态示意、四步流程、FAQ 标题、页脚 | 没有登录工作台；FAQ 展开尝试超时，未把题目标题当成答案；没有从图片推断隐藏控件 |
| [Crowdmark 灵活阅卷公告](https://www.crowdmark.com/new-feature-flexible-grading) | 备用网页读取 | 2017-12-14 公告全文，尤其导航与附加功能段 | 本次独立复核为抓取；前轮分组报告记录过浏览器读取；不是当前版本实操 |
| [GradeOps 根 README](https://github.com/exharmonic/gradeops/blob/main/README.md) | 备用网页读取 | 流程、模型、架构与开发说明 | 不执行仓库代码；不审定实现、模型可用性或恢复保证 |
| [GradeOps backend README](https://github.com/exharmonic/gradeops/blob/main/backend/README.md) | 备用网页读取 | 两阶段模型、状态持久化、技术栈与配置说明 | 与根 README 有不一致，见下文 |
| [Moodle Essay 原文](https://docs.moodle.org/en/Essay_question_type) | 备用网页读取；当前重定向到 [5.3 文档](https://docs.moodle.org/503/en/Essay_question_type) | 作答配置、模板、教师说明、人工评分与截图叙述 | 文档不是运行中的 Moodle 实例；不能把截图示例写成自动检测 |
| [ExamSoft 评分原文](https://support.examsoft.com/hc/en-us/articles/11166571442829-Legacy-Portal-Grade-Essay-Questions) | 备用网页读取 | 2023-02-07 Legacy Portal 文档全文 | 只验证文档归属，不证明当前新门户完全相同 |

CUA 首次导航超时后，第二次成功读取 GradeLab。随后 FAQ 交互又发生超时，因此本次其他来源采用网页只读工具，并明确区分；没有注册、登录、上传、填写表单或运行外部代码。

## 1. GradeLab 与 Crowdmark 的工作台细节混入

原 `products-11-21.md` 的第 16 项将以下细节写成 GradeLab 同页已验证元素。独立实读 GradeLab 正文没有取得这些依据，而 Crowdmark 公告逐条明确写出，归属应修正。**未在公开正文取得依据不等于断言 GradeLab 产品绝不支持；这些项在 GradeLab 仍属未验证。**

| 原第 16 项细节 | 可靠来源与位置 | 修正方式 |
| --- | --- | --- |
| 右栏固定当前题、分数键盘、学生 booklet | Crowdmark 公告“阅卷界面重做”后的导航说明 | 从 GradeLab 已验证列表移出，归到第 20 项历史公告 |
| 下一份未评，并按顺序遍历 | Crowdmark 导航说明及附加功能第 1 条，按钮名为 `Next ungraded` | 归到 Crowdmark；不重复计为两个厂商的证据 |
| 按题目、分数/区间、评卷人、标签筛选 | Crowdmark 导航说明末条 | 归到 Crowdmark；GradeLab 联合过滤待验证 |
| 点评语使用次数筛出使用该评语的评价 | Crowdmark 附加功能第 2 条，控件为 `Used X times` | 归到 Crowdmark；不能描述为 GradeLab 评语库实操 |
| 题级进度条、隐藏页缩略图抽屉、超满分黄色警告 | 同一 Crowdmark 公告 | 可保留在第 20 项，并标历史公开文档；不能扩大成现行 SaaS 验证 |

GradeLab 本次正文能支持的是：队列与状态进度、学生扫描答案对照模型答案、关键短语高亮、分数/排名/AI 置信度摘要、滑杆校准和个性评语。首屏状态与 LMS/CSV/Reports 为公开示意；本次未完成导出。FAQ 有扣分解释、学生质疑成绩、审计理由导出的提问，但本次没有成功展开，不据此补写其具体实现或导出字段。

可用于第 16 项的保守表述：

> 与第 15 项为同一公开 URL。本页以队列和进度、并排答案核对、学生摘要、人工校准四步介绍工作流。右栏题目/分数键盘、booklet、下一份未评、联合过滤和评语使用次数过滤的证据来自 Crowdmark 第 20 项历史公告；这些不能计入本页已验证细节。GradeLab 登录后的实际操作仍待验证。

主智能体收到复核结果后已通知修正第 16 项；本节保留发现原因和来源，避免后续汇总重新带入。

## 2. Moodle 混入 ExamSoft 阅读工具；相同答案示例被扩大

`products-22-31.md` 第 23 项原文写了“左侧题目/回答、右侧分数/评论、调字体和间距、隐藏题干、打印 Essay”。本次读取的 Moodle 原页没有这些文字；它们出现在 ExamSoft 评分文档的评分屏说明中，且与第 24 项表述一致。

| 事实 | Moodle 原文能支持的程度 | 修正 |
| --- | --- | --- |
| 字体/间距、隐藏题干、打印与左右布局 | 本页没有取得依据 | 从第 23 项删去；借鉴建议保留在第 24 项，并标 Legacy Portal 文档 |
| 同题遍历所有学生 | 评分段明确说明从 Results 选择人工评分后，可选一道题批阅所有学生 | 可保留 |
| 分数和自定义评语、附件/音视频反馈 | 人工评分段明确说明 | 可保留；不要因此补写固定布局 |
| Responses 近乎相同答案 | 原文说浏览所有学生回答时“可能发现”相同答案，并以图片举例 | 改为教师并排/集中浏览时可发现线索；本页不证明内置自动检测或相似度算法 |
| 文本必填 | 原文专门提醒该项并不强迫学生输入，仍可留空继续 | 不应把配置名直接当作强制校验保证；本地若实现强校验，应独立定义需求 |

可替换第 23 项“评分体验与治理细节”的文字：

> 教师可从测验 Results 的人工评分入口，选择一道题连续批阅全部学生，也可从 Responses 浏览所有学生的回答。人工评分可填写分数和自定义评语，并添加图片或录制音视频。文档用相似答案截图说明集中浏览可能帮助教师发现疑点；没有证明自动相似检测，不能自动据此判作弊。学生作答模板与教师评分说明分开呈现。字体/间距、隐藏题干、打印与固定左右布局的依据属于 ExamSoft 第 24 项，不能计入本页。

相应地，第 23 项可借鉴列表中“文本排版密度/打印友好模式”应移到 ExamSoft 来源；“借助 Responses 提示作弊”应改为“教师集中查阅疑似相同答案”。建议仍可研究这些设计，但来源与产品事实必须准确。

## 3. GradeOps 两份 README 的口径没有统一

| 问题 | 根 README | backend README | 应采用的写法 |
| --- | --- | --- | --- |
| OCR 模型 | Gemini 负责 OCR，Gemini 再评分 | Nemotron 经 OpenRouter 负责 OCR，Gemini 评分 | 两份公开文档存在不一致，实际模型路由待核对源码和配置；不能写“后端进一步说明”使读者以为一致 |
| 人审持久化组件 | 提到异步 SQLite checkpointer，开发说明写 `AsyncSqliteSaver` | 写 `SqliteSaver`，强调跨请求保留队列 | 文档都描述人审等待持久化，但具体组件/异步路径未核实 |
| 重启恢复 | 作者在开发说明中声称待人审状态可在服务重启后保留 | 主要描述磁盘保存和跨请求，而非完整故障恢复试验 | 只能记为仓库声明；不能由 README 验证“保证恢复”或零丢失 |

可替换第 30 项模型和持久化段落：

> 两份 README 都描述 OCR、按 Rubric 评分、人工 approve/override/flag 后发布的分段流程，并声称将人审等待状态保存到 SQLite。根 README 使用 Gemini OCR/评分并提到异步持久化组件；backend README 则写 Nemotron/OpenRouter OCR、Gemini 评分及另一组件名称，文档口径尚未统一。本轮未审读模型路由、持久化调用和恢复测试，也未运行该项目，不能确认真实模型、异步实现或重启恢复保证。

“OCR 与评分分阶段、等待人审可持久化、模型来源可追溯”仍可作为设计借鉴；具体双模型组合、认证可靠性、抄袭检测效果和生产成熟度继续为 pending。

## 4. 30 个独立地址的覆盖核对

下表已由主研究者按最终补查结果更新；独立审查者最初统计只是中途检查点。B 表示有浏览器公开正文记录；F 表示取得同址网页正文，但浏览器读取失败；X 表示没有可靠产品正文；D 表示重复 URL。各表只说明列明的公开范围，不等于所有子页或实际业务功能均已测试。

| 原编号 | 独立地址/产品 | 检查点 | 已读范围或缺口 |
| ---: | --- | --- | --- |
| 1 | [Gradyn](https://www.gradyn.ai/) | B | 主页、样例报告、FAQ、隐私；无真实评分 |
| 2 | [七天网络](https://www.7net.cc/build/product/yuejuan/index.html) | B | 产品、生态与数据产品页；无账号阅卷 |
| 3 | [AI 批改助手](https://aimarking.five-plus-one.com/) | B | 主页和公开配置/纠错/历史文档；无外部平台提交 |
| 4 | [GroupMark](https://groupmark.com.au/) | B | 主页/截图/隐私；客户端未安装 |
| 5 | [百問繚乱教师记录](https://note.com/umeducation_jp/n/n274bdb4ec849) | B | 2023 第三方全文与图注，不能代表现行产品 |
| 6 | [巴克云案例](https://news.bakclass.com/news/newsArticle?newsId=555) | B | 2017 厂商案例全文，不能代表现行产品 |
| 7 | [新教育](https://hc.xinjiaoyu.com/read-over/test-marking/0814) | B | 主研究者恢复后浏览器读取官方帮助全文，补本人/多人剩余口径与问题卷副作用 |
| 8 | [Eklavvya/Capterra](https://www.capterra.in/software/1019111/eklavvya-) | B | 目录和截图弹窗、评价第一页五条；评价下一页 pending |
| 9 | [慧阅卷](https://cms.17yuejuan.cn/) | B | 主研究者恢复后读主页与 privacy.html；未安装客户端 |
| 10 | [Addmen](https://www.addmengroup.com/exam-software/on-screen-manual-scoring.htm) | F | 同址公开 HTML；主研究者浏览器直连确认连接关闭 |
| 11 | [Kinkao](https://www.kinkao.cn/product/39.html) | B | 官方公开长页；无实际扫描/评分 |
| 12 | [CSDN 技术文章](https://blog.csdn.net/Dev7z/article/details/154951112) | B | 作者文章；非商业产品实测 |
| 13 | [GradeWiz](https://www.gradewiz.live/) | X | 连接关闭，没有可靠正文 |
| 14 | [Knowable](https://www.knowableeducation.com/) | B | 主页切换、价格、隐私；详细报告标将来功能 |
| 15 | [GradeLab](https://gradelab.io/ai-grading) | B | 公开 AI grading 页；本审查又独立浏览读取 |
| 16 | [GradeLab 同页](https://gradelab.io/ai-grading) | D | 与 15 完全相同，不新增独立证据 |
| 17 | [MyLesson](https://www.mylesson.ai/ai-grading) | B | AI grading、Rubric Generator；无真实上传 |
| 18 | [CoGrader](https://links.cograder.com/ai-grading/) | B | 主页及 FAQ；无真实 LMS 回写 |
| 19 | [Crowdmark 双盲](https://crowdmark.com/help/double-blind-grading/) | B | 官方帮助；无登录切权限 |
| 20 | [Crowdmark 灵活阅卷](https://crowdmark.com/new-feature-flexible-grading) | B | 历史公告；本审查又抓取核实归属 |
| 21 | [迈阿密 Gradescope 支持页](https://lpt.it.miami.edu/platforms/supported/gradescope/index.html) | B | 主研究者展开机构页全部功能页签，并读官方 get_started 全文 |
| 22 | [RM Assessor](https://www.rm.com/assessment/what-is-emarking) | B | 官方介绍及服务页；无真实工作台 |
| 23 | [Moodle Essay](https://docs.moodle.org/en/Essay_question_type) | B | 官方文档；本审查又抓取核实，见纠错 |
| 24 | [ExamSoft](https://support.examsoft.com/hc/en-us/articles/11166571442829-Legacy-Portal-Grade-Essay-Questions) | B | 官方旧门户帮助及分配/报告页；本审查又核实主文档 |
| 25 | [GradeSense](https://gradesense.in/) | B | 首页、功能/安全/价格，含截图与滑块；无真实评分 |
| 26 | [Grade AI demo](https://www.gradeai.us/demo) | B | 分组报告已记主智能体浏览 demo；其他子页仍为抓取 |
| 27 | [智阅达](https://zhiyueda.com/product/ai-grading/) | B | 主研究者读原页全文，子方案页仍为备用抓取 |
| 28 | [阅卷易](https://yuejuanyi.cn/) | B | 主研究者读主页功能与模式，折叠问答部分为备用抓取 |
| 29 | [小卷喵](https://xiaojuanmiao.com/) | X | 主研究者浏览器直连连接关闭，抓取也超时；不推断功能 |
| 30 | [GradeOps](https://github.com/exharmonic/gradeops) | B | 主研究者读仓库首页与根 README；后端 README 独立抓取核对；实现与运行 pending |
| 31 | [小作坊](https://jxxzf.cn/) | B | 主研究者读产品矩阵、AI阅卷工具与AI评测系统公开详情，两款产品分开 |

最终合计：27 个 B、1 个 F、2 个 X，共30个独立地址，另1个D。浏览器已逐一尝试30个独立原始地址，**27个取得浏览器正文、1个用同址HTML补证、2个无可靠正文**；不能写成“30个产品全部实测完成”或“所有页面和功能无遗漏”。即使标B，也只覆盖已列出的公开范围，登录后、折叠未展开处、未读评价页和真实效果继续 pending。

上述补证已同步到对应分组报告与总报告。GradeWiz重新直连仍关闭；小卷喵直连也关闭。小作坊后续展开另一个AI评测系统，确认两款产品主题数不同，应分别记录。

## 5. 汇总文档同步与未验证边界

1. 总报告原26–31受限状态已按更晚的逐站证据更新；第16项过滤/booklet等归属已修正为Crowdmark，不再计入GradeLab。
2. `products-22-31.md` 的统一“26–31 浏览器 pending”已拆开：浏览器补证不使安全/隐私/价格、下载客户端或账号内交互自动完成，子页证据仍分别保留。
3. 小作坊原主题数混写已纠正：AI阅卷工具10套主题/10套评分预设，另一款AI评测系统20套主题。主题数量不作为效果或验收指标。
4. Moodle 的 5.3 是本次 URL 的重定向版本标签，不能据此声称已测 5.3 运行实例；源页面自身更新时间也可能更旧。ExamSoft 明确是旧门户，Crowdmark 是 2017 公告，巴克云是 2017 案例，百問繚乱是 2023 第三方记录，时间与来源性质应跟随每次引用。
5. 所有“准确率、节时、合规、安全、零训练、数据驻留、恢复、免费额度、价格、容量”均区分页面声明与实证；本轮没有真实评分样本、合同、安全审计、备份恢复试验或容量试验，不能升级成已验证保证。
6. 本地能力比较沿用 [local-baseline.md](local-baseline.md) 的 L-UI/L-CODE/L-DOC 边界：真实业务界面未登录，部分源码仍为 pending；不能把公开竞品演示与本地未读部分进行确定性优劣排名，也不能把已存在的双评、批注、评分点、快捷键、个人书签与 CSV 再列为新增。

本次发现的混入与口径问题影响研究事实归属，不涉及业务代码。优先修正事实，再根据本地需求决定借鉴哪些设计；修正来源不会使这些设计本身失去参考价值。
