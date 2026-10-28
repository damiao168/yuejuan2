# 产品繁琐与冗余体验审计

日期：2026-10-27。对象：当前工作区的 EduGrade Web、学生端、扫描工作站、题库、模型管理及本地安装流程。主智能体加 3 个并行智能体协作；只新增审计报告、阅读清单和合成浏览器夹具，未修改业务代码，未操作真实成绩或申请。

**两轮共整理出 120 项观察：119 项体验精简建议和 1 项条件性配置风险，按整改次序分为 P1 22 项、P2 90 项、P3 8 项。另单列 6 个功能或界面疑点。** 它们是带定位与适用条件的审计发现，不是经过目标用户研究确认的 120 个缺陷。

产品最明显的负担是：教师、学生和教务要理解多套保存、技术批次、不同公开入口、任务与版本关联，才能完成自己的目标。精简应把这些编排交给系统，保留用户真正需要判断的内容。

用户要求继续后，补齐了剩余交互组件，新增25项、撤回1项，净增加24项。原 EX-16“上传后必须手工创建区域模板”在完整调用链中被自动匹配/创建流程推翻，已移出整改清单。新发现集中在：准备待办不定位具体题目、失败上传记录被截断、任务清单缺少上下文、复杂恢复暴露 JSON，以及可选安全试点与内部编号录入。

## 最值得先处理的 12 组

| 次序 | 我作为用户遇到的事 | 具体问题 | 建议达到的体验 | 详细编号 |
| --- | --- | --- | --- | --- |
| 1 | 修改一道题，然后保存 | “保存本题全部修改”没有覆盖判分方式与参考图；同名误差又在两处设置，单位不同、优先级不直观。 | 一份明确的题目草稿；统一保存状态与误差入口，保存与发布仍分开。 | [EX-07、EX-08](exam-preparation.md) |
| 2 | 学生只想看自己怎么答错 | 点“查看作答”就展开整张复核申请表，还重复显示答卷和可选圈选工具。 | 默认看作答与反馈，主动申请时才出现原因和可选圈选。 | [S03、S04](portals-ops.md) |
| 3 | 在题库同时改题干和答案 | 修改题干后，答案区立即禁用；必须先保存一块，才能修改另一块。 | 可以连续修改本题内容和答案，再保存本题草稿；冲突由系统准确提示。 | [R11](navigation-question-bank.md) |
| 4 | 导入学生答卷 | 点“导入答卷”先要命名采集批次，创建后才选文件；登记上传后还要“开始处理”。 | 先选文件、检查页序与分组；批次自动命名，明确一个“导入并处理”主动作。 | [EX-20、EX-21、EX-22](exam-preparation.md) |
| 5 | 处理一份学生复核 | 创建复评、关联申请、查找任务、新版本发布、回申请选择结果，跨区反复对账。 | 一个申请详情串联所有阶段，自动带入题目/答卷/原版本，保留独立教师与正式发布判断。 | [G02、G12—G15](grading-release.md) |
| 6 | 扫描员准备上传 | 模板和设备设置在先，真正先选的考试/批次反而在下方；多套刷新让用户判断哪个阶段完成。 | 按“选考试和批次→核对模板→上传→统一进度”排列，失败才显示对应修复动作。 | [D03—D05](portals-ops.md) |
| 7 | 学校导入日常学生名单 | 初始化有中文列映射，日常导入却强制英文 CSV 表头。 | 同一套中文列识别与映射；支持学校已有 Excel 名单，歧义在预检中解决。 | [O03](portals-ops.md) |
| 8 | 管理员登记模型评测 | 已有结构化报告，还要手填哈希、版本和 20 余项元数据/指标。 | 导入报告并校验，人只核对证据与审批结论。 | [O10](portals-ops.md) |
| 9 | 教师完成答题卡设置 | 主操作区能保存草稿，最终确认却藏在“更多操作→模板设置”抽屉。 | “确认答题卡设置”与剩余问题直接出现在主操作区，高级参数仍放设置。 | [EX-17](exam-preparation.md) |
| 10 | 教师离线阅卷 | 准备、保存、授权多项状态并存，答卷图片重启后仍需联网恢复；评分点与总分还要手工汇总。 | 明确区分“文字草稿”和“完整离线任务”；按评分点自动汇总，例外才调整总分。 | [D02、D06—D09](portals-ops.md) |
| 11 | 按题意在题库找题 | 普通列表没有题干，普通搜索只查编号，题干搜索藏进另一张弹窗表。 | 一张有题干摘要的结果表，直接搜题干和编号，筛选按需展开。 | [R06—R10、R14](navigation-question-bank.md) |
| 12 | 首次安装并长期使用 | 默认准备本地模型，人工/外部 API 模式不明显；两天证书到期后日常启动中断，续期路径很深。 | 首次选能力模式，按需安装；显示证书期限，给受控续期与失败回退入口。 | [DP01、DP03—DP10](deployment.md) |

另应尽早核实 [DP02](deployment.md)：默认本地模型启动会同步模型 API key；若同一个环境键已配置外部模型，可能被本地 key 覆盖。此项为条件性配置风险，未运行启动脚本验证当前部署是否符合触发条件。

## 全部结果

| 报告 | 数量 | 范围 |
| --- | --- | --- |
| [考试准备](exam-preparation.md) | 32 | 创建考试、资料识别、题目/答案/评分、答题卡、答卷导入；EX-16已撤回 |
| [阅卷与发布](grading-release.md) | 28 | 阅卷、AI方案、成绩公开、复核/复评、学情、错误恢复；另有服务端必要边界矩阵和5个功能疑点 |
| [学生、扫描与管理](portals-ops.md) | 31 | 学生7项、桌面10项、组织/模型/治理/审计14项 |
| [登录、导航与题库](navigation-question-bank.md) | 19 | 登录策略、命名、题库表单/搜索/交接、助手局部信息负担；另单列1个合成浏览器复现的界面疑点 |
| [安装与升级](deployment.md) | 10 | 能力选择、配置保留、证书生命周期、启动器、版本与升级 |
| [120 项完整索引](findings-index.md) | 120 | 按人工发现编号汇总，另附6个疑点与撤回记录；机器可读版本见 [JSON](findings-index.json) |

同一根因跨模块会在不同报告出现，例如分区保存、比例单位和多套刷新；这代表不同页面的具体落点，实施时应按上面的改造组归并，不应启动 120 个互不相关的改造项目。

## 本次继续后值得立即核对的落点

- [EX-25](exam-preparation.md)：待办已经知道哪道题有问题，“去处理”仍只打开通用题目页，用户要再找一遍。
- [EX-33](exam-preparation.md)：上传队列只显示最新8项，较早失败会被新完成记录挤出，没有查看全部失败入口。
- [G22—G26](grading-release.md)：回标清单缺考试/题号；参考资料重复排在答图前；冲突恢复暴露 JSON；成绩表每行铺开全部小题；质量待办多层展开。
- [O11—O14](portals-ops.md)：未保护日常登录的 TOTP 试点位于账户安全首块；学校详情错误没有就地重试；治理关联手填内部编号；两种报告登记重复输入共同元数据。
- [R16—R19](navigation-question-bank.md)：引用全文网址与按钮打断阅读、题库比较不收起相同字段、全局回执逐条手动查询、冲突备份缺少恢复或逐项采用入口。

六个疑点中，最值得先复现的是 [grading-D05](grading-release.md)：同一复评表先改两条分数，提交其中一条后，另一条未提交分数可能被远端候选原值重置。另有 [F01](navigation-question-bank.md) 已在合成浏览器确认：没有编辑、读取同一版本，空解析的两种空值表示被误判为差异。它们都未混入“删文字/减步骤”的建议，也没有在本次审计中修改实现。

## 浏览器证据与验证限制

实际部署 `http://127.0.0.1:8088` 只体验未登录页面；未使用真实学校账号。登录后 Web 流程使用当前源码在 `4173` 运行，学生端在 `4174` 运行，API 由已有仓库夹具及本次补充的合成数据响应。没有把“接口未覆盖导致的合成夹具报错”记为产品缺陷。

| 操作路径 | 确认的现象 | 证据 |
| --- | --- | --- |
| 部署登录→微信扫码 | 两个互斥复选框；切换扫码后还要点击生成二维码 | [登录截图](../../output/playwright/product-simplicity-audit-2026-10-27/deployed-login.png)、[扫码前截图](../../output/playwright/product-simplicity-audit-2026-10-27/deployed-wechat-before-qr.png) |
| Web首页→创建考试→展开阅卷设置 | 三种创建入口，第四节只是预告；发布策略呈现为下拉 | [创建页](../../output/playwright/product-simplicity-audit-2026-10-27/source-exam-create.png)、[设置展开](../../output/playwright/product-simplicity-audit-2026-10-27/source-exam-create-settings.png) |
| 考试列表→单科考试→数学→答卷导入 | 单科也经过只有一行学科的概览；导入按钮先开批次表单 | [单科概览](../../output/playwright/product-simplicity-audit-2026-10-27/source-exam-overview.png)、[导入弹窗](../../output/playwright/product-simplicity-audit-2026-10-27/source-capture-import-modal.png) |
| 题库→新建题目→打开题目→修改题干 | 编号必填、次要字段常驻；改题干后评分区禁用 | [列表](../../output/playwright/product-simplicity-audit-2026-10-27/source-question-bank-list.png)、[新建题目](../../output/playwright/product-simplicity-audit-2026-10-27/source-question-bank-create.png)、[修改后快照](../../output/playwright/product-simplicity-audit-2026-10-27/question-bank-dirty-snapshot.txt) |
| 题库→直接查看同一最新版本 | 18个固定比较项全展开；空解析两边均“未配置”却被标成差异 | [比较页截图](../../output/playwright/product-simplicity-audit-2026-10-27/continued-question-bank-conflict.png)、[完整快照](../../output/playwright/product-simplicity-audit-2026-10-27/continued-conflict-snapshot.txt) |
| 学生首页→查看考试分析→查看本题作答 | 最近考试重复、逐题七列与重复参比说明；只看答案也展开完整复核表单 | [学生首页](../../output/playwright/product-simplicity-audit-2026-10-27/source-student-exam-list.png)、[成绩详情](../../output/playwright/product-simplicity-audit-2026-10-27/source-student-result.png)、[作答展开](../../output/playwright/product-simplicity-audit-2026-10-27/source-student-answer-open.png)、[展开快照](../../output/playwright/product-simplicity-audit-2026-10-27/student-answer-open-snapshot.txt) |

浏览器初始视口约 1035×703，题库编辑后另核实 1440×900；学生题目详情另留 390×844 的窄屏截图。截图是指定合成状态，不能据此量化真实学校的平均任务耗时或用户流失。复核后确认百分比坐标本来已折叠，S04 已精确限定为重复图像与默认出现的可选圈选工具。

现有 4 个测试文件、15 项测试通过，针对 AI 方案、发布工作区、题目复评入口、个人标记；它们确认当前交互合同，不证明精简方案已实现。文案静态检查 `node scripts/check-user-facing-copy.mjs` 通过。本轮未运行全仓测试，未做真实模型推理、评分发布、申诉提交或扫描仪硬件测试。

上述测试命令为：`npm --workspace apps/web-admin run test -- src/features/grading/workbench/components/GradingAIPlanSettings.test.tsx src/features/score-management/components/ScoreReleaseWorkspace.test.tsx src/components/QuestionAppealRegradeLaunch.test.tsx src/features/grading/workbench/components/ReviewBookmarks.test.tsx`。原始完成输出只在智能体工具记录中，未保存独立日志；报告没有提供不存在的日志链接。继续补审没有修改业务代码，因此未无理由重跑这四份测试。

## 必须保留的边界

独立阅卷/复核、原评教师回避、权限与学校隔离、原成绩不可变版本、发布前锁内再次检查、关键操作确认和不确定请求的回执恢复都有实现依据。这里建议合并的是人的查找、重复填写和重复展示；多次 API 写入能否合成一个动作，必须设计好部分成功与重试。

模型结果仍是建议，不能为了减少操作而自动成为最终成绩。历史复核数据、基础成绩查询和门户发布采用不同模型，也不能仅因名称近似就删表或合并事实。

## 全仓覆盖状态

本轮全仓检索与文件盘点覆盖 **5,402 个已跟踪文件及当前未跟踪文件**；最终自动清单共 15,828 项，其中大量是既有历史报告和临时输出。全文审读 **383** 份、片段审读 **37** 份；分区重叠已去重。

**281份应用端非测试 TSX（Web255、学生17、桌面9）及14份共享 UI TSX 均已全文审读，共295份。** 这个范围包括开发/演示辅助组件，只说明界面源码的静态覆盖完成，不表示295个独立页面，也不表示所有条件状态都已运行。样式、控制器与后端按相关调用深入核对，未完整读完的仍为 pending。

[全仓逐文件状态](repository-files.json) 中 `reviewed` 只表示本次体验相关全文审读，`pending` 包含未读和未读完，`protected` 为历史迁移/生成文件/锁文件等，`excluded` 为历史输出或二进制资源。**没有把文件枚举当作审读，也不声称已经穷尽整个仓库的所有问题。** AI Worker、算法、部分样式、测试和后端仍有 pending；没有交互入口的文件不因为搜过名字就标为完成。

工作区初始状态保存在 [worktree-baseline.txt](worktree-baseline.txt)。最终核对 `git diff --stat` 为空，原有未跟踪内容保留。各分区清单可追溯阅读者与方法，总清单还记录已审读内容的 SHA-256；报告引用行号对应本次工作区，后续改动需重新核对。

## 判断依据

“只问当前任务所需信息、不要重复询问已有信息”参考 [GOV.UK 问题页面指南](https://design-system.service.gov.uk/patterns/question-pages/)；短表单与必要输入参考 [W3C 表单教程](https://www.w3.org/WAI/tutorials/forms/)；高频动作直接展示、高级能力按需展开参考 [NN/g 渐进展示](https://www.nngroup.com/articles/progressive-disclosure/)。具体整改结论来自上列源码与浏览器观察，仍应让真实教务、教师、学生和扫描员验证优先顺序。
