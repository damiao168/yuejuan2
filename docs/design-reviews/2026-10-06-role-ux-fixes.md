# 四个角色的产品操作修复记录

本次按已确认方案修复平台管理员、学校管理员、阅卷教师和学生在操作、理解状态和错误恢复方面的问题。工作从 2026 年 10 月 5 日开始，按目录并行实施，保留工作区原有未提交修改。本文记录实际实现和验证边界，供验收与后续维护使用。

## 逐项修复

| 身份 | 原问题 | 已实现行为 | 主要实现位置 |
| --- | --- | --- | --- |
| 平台管理员 | 模型管理刷新后丢失当前学校，失效深链可能落到另一学校 | 学校和标签写入恢复地址；刷新保持范围；失效学校明确提示重新选择 | [学校上下文](../../apps/web-admin/src/features/platform-model-config/lib/modelConfigLocation.ts)、[模型页](../../apps/web-admin/src/pages/PlatformModelConfigPage.tsx) |
| 平台管理员 | 默认对话模型与评分角色的健康状态混在一起 | 分别展示对话默认模型及 A/B/C；无默认对话模型时仍显示评分角色；明确健康不等于评分准入 | [学校模型状态](../../apps/web-admin/src/features/platform/schools/SchoolModelsTab.tsx)、[模型选择](../../apps/web-admin/src/features/platform-model-config/components/SchoolModelSelector.tsx) |
| 平台管理员 | 创建学校成功后缺少可交接资料 | 保留学校创建回执，展示预填学校的登录链接、管理员账号和后续步骤；复制资料不含初始密码 | [学校创建回执](../../apps/web-admin/src/features/platform/schools/SchoolCreatedReceipt.tsx)、[首次创建](../../apps/web-admin/src/features/onboarding/FirstSchoolCard.tsx) |
| 平台管理员 | 三智能体要求直接理解题型编码、Prompt 版本与强度编号 | 常用题型和真实部署版本可选；明确套用当前版本仅修改草稿；自定义编码、版本和强度放入高级设置；逐角色保存 | [三智能体配置](../../apps/web-admin/src/pages/PanelModelBindingsSection.tsx) |
| 学校管理员 | 准备确认没有清楚说明不可逆影响，也不能先核对真实名单 | 明确冻结题目、评分配置和名单；同步后展示真实学生及逐题评分方式；配置摘要包含实际策略及学生班级归属，确认在考试锁内绑定复核版本，重建名单后再次校验；后续阶段不显示重新冻结入口；失败保留窗口内容 | [准备确认](../../apps/web-admin/src/pages/ExamPreparationPage.tsx)、[冻结版本比较](../../services/api-gateway/internal/paper/store_postgres_configuration.go) |
| 学校管理员 | AI 阅卷方式描述容易被理解为整场考试自动评分 | 创建考试明确 AI 是评分建议；准备页展示每题实际策略及未配置项，保持各题政策约束 | [创建考试](../../apps/web-admin/src/features/exams/create/CreateExamComposer.tsx)、[准备确认](../../apps/web-admin/src/pages/ExamPreparationPage.tsx) |
| 学校管理员 | 同一人员兼任多个职责难以表达或修改 | 新建及已有成员均能配置多职责和班级；明确学校边界；关键授权再次认证；旧授权冲突拒绝覆盖，失败保留输入；保护最后一位启用管理员并撤销旧凭据 | [成员管理](../../apps/web-admin/src/features/members/teachers/TeacherManagementPage.tsx)、[授权后端](../../services/api-gateway/internal/auth/store_postgres.go) |
| 学校管理员 | 人员超过首批读取数量后名单不完整 | 人员管理和阅卷员选择读取完整分页、去重；异常游标明确失败，不将部分结果当作完整名单 | [人员查询](../../apps/web-admin/src/api/users.ts)、[分页回归](../../apps/web-admin/src/api/users.test.ts) |
| 学校管理员 | 大规模阅卷任务只能从当前页分配，缺少实际影响预览 | 区分已加载与全部匹配；全部匹配完整读取成功后才能选择；按题目为多人均分并预览；部分失败保留已成功批次 | [分配预览](../../apps/web-admin/src/features/grading/workbench/components/AssignmentPreview.tsx)、[分配动作](../../apps/web-admin/src/features/grading/workbench/hooks/useGradingQueueActions.ts) |
| 学校管理员 | 两套成绩公开入口难以辨认，发布前不能核对学生实际公开内容 | 推荐汇总确认、门户草稿、学生预览、发布的工作流程；学校原有查询公开使用独立入口；预览读取指定冻结版本；公开题干、知识点与分布由管理员勾选 | [成绩工作区](../../apps/web-admin/src/features/score-management/components/ScoreActionsPanel.tsx)、[学生预览](../../apps/web-admin/src/features/score-management/components/StudentScorePreviewDialog.tsx) |
| 阅卷教师 | 缺少完整题干、答案和评分依据，可能看到当前版本代替冻结版本 | 展示冻结题干、选项、标准及等价答案、误差、解答全文、步骤、评分点、扣分与样例；冻结细则缺失明确提示，不回退当前题库 | [冻结参考资料](../../apps/web-admin/src/features/grading/workbench/components/FrozenQuestionReference.tsx)、[任务上下文](../../services/api-gateway/internal/review/store_postgres_context.go) |
| 阅卷教师 | 采纳 AI 分数时同时带入未经确认的反馈 | 分数、学生公开反馈和内部备注分别确认；只有版本、评分点身份及合计完整匹配时自动填分，否则保留评分点并提示总分采纳原因 | [AI 采纳](../../apps/web-admin/src/features/grading/workbench/aiAdoption.ts)、[评分动作](../../apps/web-admin/src/features/grading/workbench/hooks/useGradingReviewActions.ts) |
| 阅卷教师 | 重评和回标窗口关闭、切换或请求失败后丢失内容 | 未提交评分离开前确认；提交期间锁住编辑与关闭并阻止重复请求；失败保留评分；换账号丢弃旧内容和迟到响应 | [重评](../../apps/web-admin/src/components/RegradeQueue.tsx)、[回标](../../apps/web-admin/src/components/BackmarkQueue.tsx) |
| 阅卷教师 | 桌面任务资料和保存提示不能反映真正离线状态 | 下载与同步预检读取冻结任务上下文；保存完成前不显示已保存；输入改变显示未保存；图片仅限会话，载入草稿清除旧图；旧来源未知包要求重新下载 | [桌面任务包](../../apps/desktop-client/src/api/review.ts)、[离线阅卷](../../apps/desktop-client/src/components/OfflineWorkbench.tsx) |
| 阅卷教师 | 跨考试任务上下文不清，连续阅卷和筛选进度不一致，数学按钮缺少禁用原因 | 展示考试、中文学科、题目并组织连续任务；列表和匹配统计使用相同范围；完成进度使用全状态数据；数学建议操作显示原因及下一步 | [任务队列](../../apps/web-admin/src/features/grading/workbench/hooks/useGradingQueue.ts)、[任务索引](../../apps/web-admin/src/features/grading/workbench/components/TaskRail.tsx) |
| 学生 | 失分题看不到公开题干，公式难以阅读 | 在学校公开题干后展示原文，安全渲染公式；普通文字经 React 转义，公式禁止受信任 HTML 命令 | [答卷复盘](../../apps/student-portal/src/features/questions/AnswerReview.tsx)、[题目文字](../../apps/student-portal/src/features/questions/StudentText.tsx) |
| 学生 | 切题或重新登录后申诉草稿丢失，可能串到别的账号或成绩版本 | 按学校、账号、考试、发布版本和题目隔离草稿；会话内恢复文字与圈选；显式退出清除草稿 | [申诉草稿](../../apps/student-portal/src/appealDrafts.ts)、[学生会话](../../apps/student-portal/src/studentSession.tsx) |
| 学生 | 网络失败后的恢复动作不清晰，提交中仍能改变圈选 | 失败保留输入并给出对应重试；批注重试不重新加载题目；提交等待期间阻止切换和圈选变化 | [学生流程](../../apps/student-portal/src/App.tsx)、[错误恢复](../../apps/student-portal/src/errors.ts) |
| 学生 | 申诉结论、新成绩版本和当前门户成绩难以对应 | 回执区分申诉所依据版本、关联新版本和门户当前版本；后端返回新发布版本号，界面不把关联版本当作已发布 | [学生 API](../../apps/student-portal/src/api.ts)、[申诉版本回执](../../services/api-gateway/internal/appeal/published_question_types.go) |
| 学生 | 手机上连续复盘失分题容易丢失位置，空状态暗示不存在的后续功能 | 连续失分题导航与返回定位；页面重新获得焦点检查成绩更新；空状态和后续入口依据实际能力展示 | [学生页面](../../apps/student-portal/src/App.tsx)、[交互回归](../../apps/student-portal/src/App.productUx.test.tsx) |

## 验证记录

- 学生端全量 Vitest：5 个文件，66 项通过。
- 桌面端全量 Vitest：22 个文件，140 项通过；桌面安全配置检查通过。
- 教师 Chromium：20 份键盘连续任务、120 份三页全部提交、双窗口草稿冲突、转派后筛选进度、完整冻结资料及 AI 分数与反馈独立采纳，共 5 个场景通过。
- 平台 Chromium：学校创建与初始化、模型管理、学校详情既有流程通过；三智能体到评测、批准、策略的流程通过。首次加载超时用单 worker 重跑通过，未调整生产行为或测试超时。
- 管理端全量 Vitest：164 个文件、1,262 项，1,258 项通过，评分规则页 4 项超时；该文件随后单 worker 重跑 7/7 通过，覆盖原先超时的 4 项。超时原因未最终确认，没有修改生产逻辑或提高超时门限。之后新增的 5 项冻结阶段边界回归也通过；冻结确认、正式请求体、学生预览和公开摘要专项共 17 项通过。
- 三端及 SDK 的最终 TypeScript 检查通过；三端生产构建通过，新增冻结 API 后的管理端最终构建和体积检查也通过。用户文案、生产路由和 Biome 错误级别检查通过；不代表现有全部警告已清零。
- Go 最终回归：auth、review、paper、scorerelease、appeal、server 六个包通过。冻结策略与班级归属新增 9 个顶层回归通过，覆盖策略变更、归属变化、默认配置规范化、历史版本兼容及冲突回滚。
- OpenAPI、SDK 和路由覆盖使用正式生成流程；新接口具备契约，未直接编辑生成 SDK。遗留缺口上限随补齐接口下降，没有提高债务预算。最终 524 条注册路由中 275 条具备 OpenAPI 契约，249 条处于已审核遗留缺口；契约生成一致性、破坏性变更和授权门禁检查通过。

## 已明确的能力边界

学生草稿仅保存在当前浏览器会话，不是永久存储；显式退出会清除。桌面图片仅在当前会话缓存，本次没有实现持久化图片。多阅卷员分配按批提交，可能部分成功；界面会保留结果，不能承诺整体原子事务。

管理员学生预览展示指定版本的冻结公开成绩，既可预览草稿也可预览历史版本。它不会正式发布或切换门户当前版本。答卷图片、跨科当前汇总、班级排名和实际申诉须在正式学生端验证，预览已明确说明这些边界。原有学校成绩查询和学生门户仍使用各自发布入口，没有把其中一个误设为另一个的必需前提。

浏览器回归使用 API 夹具；单元测试、内存仓储和 SQL 脚本驱动回归不代表生产 PostgreSQL 并发集成验证。本机未配置 EDUGRADE_E2E_DATABASE_URL，数据库验收用例未运行，不能算作通过。此次没有提交、推送或部署，也没有新增错题本、学习计划等方案之外的能力。
