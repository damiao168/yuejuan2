# 个人稍后复查与简洁教师阅卷界面设计

- 日期：2026-10-18。
- 状态：实施与本地验收完成；覆盖实际工作台、合成浏览器场景和真实隔离 PostgreSQL。原生浏览器缩放、真机输入法与外部服务未实测，实际结果及边界见[实施记录](../../reports/personal-review-bookmarks-implementation-2026-10-18/implementation-plan.md)。
- 目标：老师可以先记下一份需要再看的答卷，继续当前阅卷；之后从个人清单找回材料，不改变评分流程。
- 基线：本地 HEAD `242ea44b7ec42355697e41136309e724d970f7dd` 及当前未提交工作树。已有修改保留，不以远端分支或历史文档代替当前实现。
- 依据：[比较报告中的 A4](../../reports/ai-marker-suite-comparison-2026-10-18.md)、用户提供的简洁阅卷界面参考图，以及本次关联代码和测试源码审读。图片只参考布局，不复制其内容、素材或代码。

## 1. 先明确产品边界

“稍后复查”是当前用户对某次阅卷任务的个人标记。它不是任务的新状态，不代表已经判错，也不等于争议、重评、暂放、金标提名或最终确认。

| 操作 | 具体含义 | 会不会改变评分业务 |
| --- | --- | --- |
| 稍后复查 | 将这次任务加入个人清单，可写简短说明 | 不改任务状态、分配人、优先级、租约、草稿和分数 |
| 编辑复查说明 | 补充自己之后要核对的内容 | 只改个人说明，不进入学生反馈或教师评分备注 |
| 移出我的复查 | 取消个人标记 | 不表示成绩正确，不关闭质检或重评任务 |
| 暂放 | 按现有流程释放当前处理中的任务 | 继续沿用现有租约及草稿保存规则 |
| 标记争议 | 按现有流程退回重评 | 必须走原有权限、状态和版本检查 |
| 提交并下一份 | 提交本次人工意见，再推进可处理任务 | 继续进入质检；不等于发布成绩 |

首版包含：当前答卷一键标记、可选说明、服务端持久化、个人清单、已提交材料只读回看、撤权后的内容保护、个人记录清理，以及教师工作区布局简化。

首版不做提醒时间、团队共享、批量改分、自动终审、书签导出、长期图像历史快照、全局待办系统。教师布局与书签能力分切片交付，两者不互为数据库前置条件。所有功能正确性可通过本地数据库、HTTP 集成和浏览器验证，不依赖真实模型、扫描仪或第三方阅卷平台。

## 2. 已核对的本地事实及其设计影响

| 当前代码事实 | 设计必须遵守的结果 |
| --- | --- |
| [任务与人工成绩](../../services/api-gateway/internal/review/types.go)分别记录 task、reviewer、grade_round；同一题块可以参与双评 | 书签绑定某次 `task_id`，不能只绑定 `answer_segment_id` |
| [授权派生](../../services/api-gateway/internal/auth/store_postgres.go)只把普通阅卷员的 assigned/in_progress/returned 任务放入活动任务作用域 | 已提交回看需要专用窄接口；不能把所有历史任务加入全局 AccessScope |
| [普通对象边界](../../services/api-gateway/internal/auth/resource_scope.go)还检查具体任务及当前分配人 | 书签不是访问凭证，每次打开材料与读取图片都重新鉴权 |
| [暗样本钩子](../../services/api-gateway/internal/seedquality/context.go)使用公开任务 ID、合成答卷/题块 ID，隐藏 Gold 来源 | 普通任务与暗样本必须同样支持书签，不公开内部来源类型 |
| [领取路由](../../services/api-gateway/internal/review/handlers.go)在领取时调用 Seed；普通 ListTasks 不混入这些任务 | 我的复查需要独立授权查询，不能过滤当前加载的任务数组凑清单 |
| [当前异常材料读取](../../services/api-gateway/internal/review/store_postgres_context.go)的 loadAutomationResult 会读取当前 question_grade | 已提交回看不能复用整个 TaskContext；否则可能带入后续终分 |
| [Seed 提交](../../services/api-gateway/internal/seedquality/service.go)把本人分数和评分点存入 seed_observation，而非 human_grade | 需要单独的本人意见白名单查询；不能序列化完整质量观察 |
| [题块图服务](../../services/api-gateway/internal/segment/images.go)读当前裁剪资产及哈希 | 回看不是提交时原图快照；需要图像版本绑定和材料变化提示 |
| [归档边界](../../services/api-gateway/internal/auth/archive_boundary.go)允许读取、拒绝业务写入，数据库也有归档约束 | 归档后新增和编辑说明禁止；个人取消采用精确定义的清理例外 |
| [草稿保存与续租](../../apps/web-admin/src/features/grading/workbench/GradingWorkbench.tsx)由完整工作台容器自动触发 | 只读回看使用独立容器，不能仅把输入框 disabled |
| [评分控件](../../apps/web-admin/src/features/grading/SharedScoreControl.tsx)以一位小数合计评分点，总分调整另有原因规则 | 简化布局不引入截图中的 0.25 分精度，也不重写计分逻辑 |
| [快捷键](../../apps/web-admin/src/features/grading/workbench/hooks/useGradingKeyboard.ts)已保护输入法、弹层和单键评分 | 书签说明弹层必须进入现有阻断条件，不新增冲突单字符快捷键 |

## 3. 界面方案：答题图为主，评分操作集中

### 3.1 教师桌面视图

视觉方向：平静、紧凑、少装饰；保留项目现有蓝色操作色、系统字体、字号与焦点样式。答题材料占主空间，初始界面只呈现老师当前需要的任务、分数和操作。

```text
┌──────────────────────────────────────────────────────────────────────────┐
│ 任务 / 我的复查  考试 · 第12题 · 匿名卷A017   本题8/10   稍后复查   提交并下一份 │
├──────────────────┬───────────────────────────────────────────────────────┤
│ 本题评分          │ 答题区域                 缩小 100% 放大 旋转 适配窗口 │
│ 本题分 [8.0] /10 │                                                       │
│                  │                                                       │
│ 1. 评分点  [3/3] │                  学生答题图主画布                     │
│ 2. 评分点  [3/4] │                                                       │
│ 3. 评分点  [2/3] │                                                       │
│                  │                                                       │
│ ▸ 题目与参考解答 │                                                       │
│ ▸ 完整评分依据   │                                                       │
│ ▸ AI辅助材料     │                                                       │
│ ▸ 评语与备注     │                                                       │
│ 草稿已保存       │ 识别文本 / 数学证据 / 批注，按需展开                   │
└──────────────────┴───────────────────────────────────────────────────────┘
```

示意数值只是合成例子。桌面左评分栏建议 280–320px；其余宽度给画布，在常见宽屏约占工作区 60%–75%。不用三块常驻侧栏同时挤压答题图。任务和“我的复查”在同一抽屉中切换；管理视图继续保留常驻队列、分派、阅卷员进度与评分总览。

顶部固定显示匿名卷号，不加入姓名/学号输入框；分数明确为“本题分”，没有填分时显示“未评分”，不能把缺失显示为 0。主按钮使用现有“提交并下一份”。归档或已提交只读时改为只读信息与复查导航，不出现审批/提交按钮。

顶部提交与现有 ScoreEditor 的提交入口共用同一提交回调、canSubmit、actioning 和防重复锁；教师简洁布局只保留一处主要按钮，不能复制出两套提交状态。管理员布局可以保留原入口。按钮旁紧凑说明“提交后进入质检”，详细规则放在帮助中。

### 3.2 参考图哪些吸收、哪些不用

| 参考图元素 | 本地采用方式 |
| --- | --- |
| 大面积作答画布 | 采用，复用缩放、旋转、适配和键盘平移 |
| 左侧逐点评分 | 使用冻结 Rubric 的真实评分点，复用现有合计与人工调整规则 |
| Review later | 改为固定名称“稍后复查”，与评分动作分离 |
| Approve | 改为“提交并下一份”，不冒充发布或最终批准 |
| 姓名、学号 | 不采用，继续匿名阅卷 |
| 整卷总分 | 不放到当前题评分栏，展示本题得分/本题满分 |
| 页码切换 | 当前普通阅卷主要处理题块，使用真实任务/题块导航；没有多页数据时不制造页码控件 |
| 每个采分点的彩色框 | 只使用已有人工批注或已验证数学步骤 bbox；不能宣称所有采分点已自动定位 |
| 增加/删除给分框 | 不允许据此临时改变冻结评分标准；新增批注不等于新增评分点 |

AI 材料可以折叠，但高风险独立评分的“显式查看第二意见”规则必须原样保留。折叠不意味着前端可以自动采纳建议，也不能把未经验证的数学定位显示成确定判分。必要的草稿冲突、图片失败、证据校正未保存提示不能因简化界面而藏入折叠区。

### 3.3 稍后复查交互

1. 未标记时点“稍后复查”，立即发起保存；说明可为空，不弹强制表单。
2. 服务器成功后显示选中书签图标和已加入状态，再提供“说明”小入口。保存中只锁这个书签按钮，不锁整个评分面板。
3. 说明使用纯文本弹层，最多 200 个 Unicode 码点，显示计数；有“保存/取消”。前后端统一去首尾空白、换行规范和计数，不能直接拿 JS 字符串 length 与数据库 char_length 混用。
4. 再点已选中的“稍后复查”可移除；无破坏性确认弹窗，成功后给简短状态提示。
5. 提交、暂放、退出或重评不自动清掉书签。老师看完后主动移除。
6. “稍后复查”的可访问名称保持不变，以 `aria-pressed` 表达是否加入；“编辑复查说明”是另一个普通按钮。失败不能提前展示成功。

说明与评分草稿的 comments/private_note/student_feedback 完全分离。它在普通页面仅本人可见，不出现在学生端、其他教师清单、管理统计、模型请求或普通报告中；不承诺数据库运维人员不可接触。

## 4. 我的复查：独立清单，不改任务状态枚举

新增 `listView = queue | bookmarks`，与现有 `queueScope = mine | all`、任务业务状态分开。书签清单只查询本人，不提供 owner 筛选或“全体教师的复查”。管理权限不自动获得别人清单和说明。

| 项目 | 规则 |
| --- | --- |
| 默认状态 | 我的复查首次进入用“全部”，避免已提交项被活动队列默认筛选藏掉 |
| 清单筛选 | 考试、题目、学科、可处理/已提交，以及考试名/题号/匿名码搜索 |
| 清单信息 | 考试、题号、匿名码、统一公开状态、加入时间、本人说明摘要 |
| 排序 | 本次加入时间倒序；修改说明不换位置，移除后重新加入排到前面 |
| 考试工作区 | 固定当前考试，不允许通过 query 切到其他考试 |
| 个人阅卷入口 | 可从本人可见书签中跨考试筛选 |
| 清单计数 | 只计算当前有权看到的项；不能把撤权对象计入题目、考试筛选项或匹配数 |
| 打开可处理任务 | 可查看，再显式“继续阅卷”；进入原有工作台前重新验证当前任务和范围 |
| 打开已提交任务 | 进入专用只读详情，不调用普通评分 hooks |

同一公开清单把普通任务的 submitted/completed 和暗样本的 completed 都投影为“已提交”；待处理状态统一为“可处理”。不返回暗样本专用状态解释、来源类型、参考分或质量指标。内部轮次不从 Seed 暗样本推导额外徽标。公共 DTO 不直接序列化 ReviewTask/PublicTask 的 source、grade_round 原值：两种来源的这些枚举并不一致，需采用共同的白名单字段和归一化文案。

各视图分别保留筛选、已选任务、滚动锚点。URL 仅保存视图和不透明对象 ID，例如 `?view=bookmarks&bookmark=...`，不保存说明、图片 URL 或学生信息。刷新深链重新请求授权，不能依靠旧缓存重建正文。

返回原队列恢复会话内的来源筛选与滚动锚点；原任务失效时返回清单并说明。移除当前书签后，不自动跳到另一份答卷。原设计允许暂留本页已加载的正文和 Blob，实际实现采用立即清除详情材料的更严格行为；两者都不能重新取得已取消书签的材料。此时停止该书签的重新加载及预取，取消前尚未返回的材料请求按书签版本/页面代次丢弃；刷新或重新打开已取消记录返回统一 404。“下一条复查”只翻清单，不领取、不提交；“提交并下一份”仍推进原可处理队列。

私有记录因转派/撤权等不可用后，普通清单完全不返回题目、匿名码、说明或对应数量。提供次级“清理不可用记录”入口，使用账号级接口只列出本人书签 ID 与版本，显示统一占位和移除按钮，不提供源对象筛选和正文。即使阅卷角色已取消，活跃账号仍可以清理这类个人元数据；账号停用或会话失效后不能使用。

## 5. 目标身份与数据模型

### 5.1 为什么按任务保存

一个 answer_segment 可以有一评、二评和后续重评；把它们合成一个书签会混合不同人的意见、轮次和权限。暗样本还有合成题块身份。因此公开目标是某次不透明 `task_id`，内部解析为真实 review_task 或 seed_task。

建议独立 `review_bookmark` 表，不复用 review_draft，不在 review_task 上增加全局 marked/status 字段。

| 字段 | 约束及含义 |
| --- | --- |
| id | UUID，书签稳定主键，取消再加仍沿用 |
| tenant_id / owner_id | 来自服务端认证身份；请求不能指定 |
| review_task_id / seed_task_id | 可空复合外键，恰有一个非空；内部使用，不进入公共 DTO |
| task_id | 可用 COALESCE 两源列的存储生成列，作为唯一键辅助，不代替两条外键 |
| reason | 默认空，纯文本，最多 200 码点；取消时清空 |
| revision | 独立 BIGINT，持久行从 1 开始，只递增 |
| marked_at | 当前这次加入清单的时间；改说明不更新 |
| created_at / updated_at | 首次创建与最近实际修改 |
| deleted_at | NULL 是当前标记，否则为取消后的墓碑 |
| observed_crop_sha256 | 服务端记录加入时可取得的裁剪哈希，内部使用，可空 |
| observed_snapshot_id | 加入时的题目冻结版本身份，内部使用；无法确认时明确 unknown |

约束：`UNIQUE(tenant_id, owner_id, task_id)` 不带活跃行条件；两条 `(tenant_id, source_id)` 复合 FK 分别连接 review_task、seed_task，owner FK 连接 app_user；恰有一个 source 非空，revision 正值，reason 长度同时由 Go 与数据库校验。task 类型由服务端解析，客户端不能提交 source_kind 或真实来源 ID。

主查询索引建议 `(tenant_id, owner_id, marked_at DESC, id DESC) WHERE deleted_at IS NULL`；按执行计划决定是否增加考试维度索引，首版不复制学生、分数、OCR 或图像到书签表。两类 task ID 若在可信源中同时存在，解析失败关闭并记录内部错误，不能猜来源。

采用租户 RLS 的 ENABLE/FORCE 和项目现有函数；**现有 RLS 不是用户级隔离**，每个存储入口仍必须加入 owner_id=当前用户，不能因为管理员同租户就省略。参考 [PostgreSQL 官方行安全文档](https://www.postgresql.org/docs/current/ddl-rowsecurity.html)。

### 5.2 独立版本与取消墓碑

```text
从未保存（虚拟revision=0）
  → 加入 revision=1
  → 改说明 revision=2
  → 移除 revision=3，清说明，保留墓碑
  → 重新加入 revision=4，重新记录marked_at及材料观察信息
```

旧的 revision=0 创建、revision=1 移除不能影响 revision=4 的标记。不要用物理删除后重新从 1 开始，也不要仅对活跃行建立唯一约束。无变化的重复意图仍先检查 expected_revision；版本相同且内容相同可不增加版本，不先跳过版本检查。

墓碑不单独按短期时间删除，随目标对象正式保留策略和受控物理清理处理；目标 UUID 不复用。取消不改 review_task/seed_task 的 revision、updated_at、status、assignee、租约，也不改 human_grade、seed_observation、question_grade、发布记录或草稿。

## 6. 权限设计：书签归属和材料访问分开

### 6.1 当前可处理任务

要求有效登录、active 账号、当前实际 `review:work`、同租户、任务仍分配本人、源对象未删，并通过当前任务相关权限检查；同时实时核对 `app_user.school_id = exam.school_id`、双方非空且学校/考试未删除。现有活动任务作用域不等于已保证这条同校关系，不能只复用 assignee 检查。这个学校前置条件适用于普通/暗样本、可处理/已提交的全部书签内容读取、列表、新增与编辑；个人元数据清理除外。图片和冻结材料沿用相应领域约束。书签操作本身不要求领取租约、不检查校准资格，也不赋予领取或提交资格。

首版不为管理员检查别人任务提供私有标记入口。管理员实际被分配阅卷任务且拥有 review:work 时，按相同本人规则使用。review:manage 不隐含 review:work。

### 6.2 本人已提交任务的只读授权

新增局部 `CanReadOwnCompletedReview`，每次读取都核对：

```text
有效会话 + 当前active账号 + 当前review:work
AND bookmark.tenant_id/owner_id 为当前租户/用户
AND 源任务仍存在、assigned_to仍为本人
AND 普通任务已提交/完成，或暗样本已完成
AND 存在该task、该用户本人提交的成绩事实
AND app_user.school_id = exam.school_id，双方非空且学校/考试未删除
```

普通本人意见取 `human_grade WHERE tenant/task/reviewer`，不能按 segment 查最近一次分数。暗样本本人意见新增按 task+grader 的窄查询，只投影 submitted_score、rubric_selection_json、observed_at，满分取相应任务。不能使用 ListObservations 宽对象后靠前端隐藏。

学校关系需要数据库 JOIN：auth.User 本身没有 SchoolID，活动任务派生的 ExamIDs 也不是独立的组织授权。可以参考 [workflowactors.List](../../services/api-gateway/internal/workflowactors/directory.go) 的同校、启用账号及实际权限查询模式。不得假设调校一定同步撤销旧任务。

以上授权只服务个人只读接口，不加入全局 `ReviewTaskIDs/SubmissionIDs/FileIDs`，不新增永久历史访问 grant。转派即失去该源材料访问，不自动把书签迁到新任务。R3 校准资格仍在领取和正式提交处执行，不把只读回看当成一次新评分。

### 6.3 权限矩阵

| 场景 | 新增/编辑说明 | 本人清单与材料 | 取消自己的标记 |
| --- | --- | --- | --- |
| 本人可处理任务 | 允许 | 可看；继续评分走原工作台 | 允许 |
| 本人已提交且满足窄授权 | 允许 | 仅本次本人意见和有限材料 | 允许 |
| 转派他人、源取消或删除 | 禁止 | 普通清单隐藏，正文拒绝 | 按已知书签ID或清理视图允许 |
| 跨校、撤销review:work | 禁止 | 内容拒绝，不保留旧授权 | 活跃账号可清理本人元数据 |
| 管理员读取其他人书签 | 禁止 | 不允许 | 不允许 |
| 其他租户或其他owner | 不允许 | 不存在口径 | 不允许 |
| 考试/整场归档 | 不新增，不改原因，不恢复 | 已授权内容只读 | 允许个人清理 |
| 会话失效、账号停用或锁定未解锁 | 不允许 | 不允许 | 不允许 |

不能承诺撤权后离线浏览器立即得知。可保证下一次请求重新检查，收到拒绝后清理显示和缓存；重新打开详情和图片前必须请求新授权。已经送达客户端的内容无法通过服务器撤权追回。

## 7. 专用只读详情与图像版本

### 7.1 白名单 DTO

已提交详情使用独立 `PersonalReviewBookmarkContext`，而不是完整 TaskContext 加 readonly=true。首版只包含：

- 公开 task_id、考试/题号/匿名码、统一状态及只读原因。
- 任务绑定的冻结题目和 Rubric。
- 本人当次提交的分数、评分点和时间，明确标签“本人本次提交分”，不称为当前终分。
- 当前仍获授权的题块、材料版本及与标记时是否变化。
- 本人的书签说明和版本。

首版只读 DTO 不返回 AI 建议、另一阅卷人的成绩/草稿/批注、双评分差、仲裁意见、当前终分、学生身份、整页原图、Gold/Seed 身份、参考分、真实来源 ID、领取/续租/写草稿字段。

context 与 image 只接受尚未取消的书签，并在每次请求重新核对源权限；取消后的墓碑只用于状态版本、清理和防重放，不继续授予材料访问。按 task 查询元数据仍可返回墓碑版本，以支持明确的重新加入。

普通与暗样本使用同一结构。暗样本现有提交不保存 comments/private_note/student_feedback，不能承诺完整历史评语；首版统一只展示两种来源都能证明的本人分数、评分点和时间。需要扩充历史文本保存时另立需求。

实施审读进一步确认：普通任务与暗样本都无法提供可公开且绑定裁剪版本的识别文字，首版统一返回空 `answer.text` 与 `text_binding=unavailable`，不按来源显示不同的文本能力。题干/选项沿用考试 ready 后禁止修改的数据库规则；Rubric 取绑定的考试题目快照。草稿/配置阶段的题干仍能修改，不能仅因已有快照就当作冻结材料回看。内存实现同样要求非空且与租户、考试、题目匹配的快照；Seed 固定任务原快照。缺失或错配仅拒绝材料读取，仍可维护个人标记与说明，不能让证据能力变成书签元数据的额外前置条件。

只读容器不挂 claim/renew/draft、数学校正、请求 AI、批注写入或评分快捷键 hooks。可复用纯展示组件；当前 AnswerEvidencePane 会挂 ReviewAnnotationWorkspace，即使 disabled 仍须核查其读取副作用，因此应抽出纯题块浏览器，而非直接复用整个容器。

这条约束针对只读容器和正在回看的对象。实际清单抽屉覆盖另一可处理任务时，底层评分工作台的原有自动保存可能继续写该原任务；不能将覆盖式打开宣称为整页零请求。浏览器验收分别记录回看任务是否发生评分写入，以及没有底层活动任务时是否全局零写，不用不同对象的后台请求互相替代结论。

### 7.2 这里不是提交时原图

当前题块图可因配准/裁剪纠正而变化；Seed 只冻结来源 URL 和题目快照，没有冻结图片字节。本功能展示当前可读题块，和本人历史提交意见是两类事实。

加入书签时尽力记录 observed_crop_sha256；回看给出 `material_change = unchanged | changed | unknown`。没有原哈希就显示“无法确认与标记时材料一致”，不能默认为未变化。发生变化要在画布旁提示“当前材料较加入复查时有变化；本人提交分为历史记录”。编辑说明不会覆盖观察基线。

题目和评分依据仍须从该任务冻结版本读取；不能把当前可编辑题库内容拿来补历史材料。所需历史事实不存在时显示 unavailable，不凭新版本猜补。

### 7.3 context 与图片绑定同一版本

现有 `?v=` 图地址不能直接当一致性保证：当前图片 handler 不校验该参数，仍读取当前 crop。新增流程应：

1. 同一只读数据库快照读取授权、冻结材料、本人意见与当前证据身份。
2. 产生不可逆的 `evidence_version`，以服务端 HMAC 绑定租户、owner、公开 task、裁剪资产/哈希、配准/纠正版本，以及返回的识别文本记录ID/版本或规范化摘要、冻结题目身份等材料版本。不能把内部来源 ID/裸哈希 base64 后称为不透明。现有 answer_segment_answer 没有显式crop外键，answer_candidate.input_hash也不能一概当作crop哈希；无法证明文字与当前裁剪同源时，DTO必须标记 `text_binding=unverified/unavailable`，不可称为该图的已核对识别。
3. 返回书签图片代理地址，携带这个不透明版本；版本本身不授予权限。
4. 图片请求重新鉴权，取得**实际将被打开的固定资产信息**后重算版本并比较。不匹配返回 `409 evidence_version_changed`，要求整体重读 context，不能默默把新图配给旧文字。
5. 底层 reader 返回的资源必须绑定同一份已核对资产；不得先校验旧 evidence 再调用另一个重新解析当前 crop 的函数，否则仍有检查与使用间隙。

图片通过现有对象存储与受控 reader 获取，但书签路由重写公共 filename 为 `answer.png`，采用 `Cache-Control: private, no-store`，不透传真实 segment 文件名、源 URL、裸 crop ETag、存储 key 或 upstream headers。普通与暗样本执行相同公共处理；HEAD 与条件请求也必须先鉴权，首版不提供通过源 ETag 直接 304 的捷径。

此处保证 context 与所打开的裁剪资产版本相符，不声称为提交时历史快照，也不声称书签功能新增了底层对象字节的全量校验。

## 8. API

路径均由独立 `internal/reviewbookmark` 领域包实现，复用 review/seedquality 的窄能力，通过 provider 隐藏内部来源，不让前端识别类型。请求与响应字段以 OpenAPI 权威文件为准。

| 方法与路径 | 用途 | 权限 |
| --- | --- | --- |
| GET `/api/v1/me/review-bookmarks` | 本人可见清单、计数和筛选项 | 当前review:work + owner + 每个源的局部策略 |
| GET `/api/v1/me/review-bookmarks/by-task/{taskId}` | 当前任务的标记状态，含未标记/墓碑版本 | 同上，先验源权限 |
| PUT `/api/v1/me/review-bookmarks/by-task/{taskId}` | 设为已标记，或修改原因 | 同上，源/归档检查与CAS |
| GET `/api/v1/me/review-bookmarks/{bookmarkId}/context` | 专用只读材料 | owner + 当前源材料权限 |
| GET/HEAD `/api/v1/me/review-bookmarks/{bookmarkId}/image?version=...` | 专用题块代理 | 同上并核对实际图像版本 |
| DELETE `/api/v1/me/review-bookmarks/{bookmarkId}` | 取消个人标记 | 活跃且已解锁账号 + 同租户owner；不要求源权限 |
| GET `/api/v1/me/review-bookmarks/cleanup` | 仅列出本人不可用记录的ID/版本以便清理 | 活跃且已解锁账号；不返回任何源信息/说明 |

`me` 不接受 owner/tenant 请求字段。前端 SessionUser.tenant 当前来自 tenant_code，不能当数据库 UUID；租户和操作者只取服务端 auth.User。

写接口沿用 Cookie/CSRF 保护和请求大小限制。为新小型 JSON 体设合理明确上限，例如 8 KiB；输入只接受 `reason` 与 `expected_revision`，限制未知字段，UUID/游标合法性按现有约定处理。DELETE 请求体的 expected_revision 与当前批注删除模式保持一致。

PUT 示例：

```json
{
  "expected_revision": 0,
  "reason": "核对第二步的给分依据"
}
```

按 task 读取状态示例：

```json
{
  "bookmark": {
    "id": "BOOKMARK_UUID",
    "task_id": "PUBLIC_TASK_UUID",
    "marked": true,
    "reason": "核对第二步的给分依据",
    "revision": 4,
    "marked_at": "2026-10-18T04:30:00Z",
    "can_edit_reason": true
  }
}
```

从未保存时 id=null、marked=false、revision=0；取消后的同 owner 记录只返回当前墓碑版本与空原因。无源访问权则不能借该接口探测状态，需使用清理专口。

成功写入只返回 `{bookmark_id, revision, marked}` 的确认，不含原因、题目和图片。前端再 GET 当前状态；确认回执不是最新状态。清理列表同样只含 bookmark_id/revision，不返回 task_id、考试/题号、原因或材料。

错误：400 输入/游标非法；401 会话失效；403 当前角色不足；不可见源或非 owner 对象统一 404；409 `bookmark_revision_conflict`、`idempotency_conflict`、`exam_archived` 或 `evidence_version_changed`。冲突时可只向合法 owner 返回 `conflict_revision`，不能为了清理而回传失权源正文。领域状态不可用错误不暴露暗样本内部类型。

## 9. 并发、幂等与查询

### 9.1 写入顺序

采用现有 [CommandIdentity](../../services/api-gateway/internal/idempotency/command_identity.go)和[业务事务回执](../../services/api-gateway/internal/commandreceipt/receipt.go)，不使用通用 HTTP 响应缓存重放私有内容。

路由必须新增专用 guard：正常写操作采用 authenticate → 当前 review:work → CommandIdentity → handler/service 内的 owner 与源授权；取消清理写操作采用 authenticate → CommandIdentity → owner 校验。GET/HEAD 读取不用 CommandIdentity；特别是当前 CommandIdentity 只豁免 GET，若直接包装图片 HEAD，会错误要求命令键。读取采用 authenticate → 所需权限 → 专用 owner/源策略，cleanup GET 只验账号与 owner。不能直接套 requireReviewWork/requireAccount 的 idempotent 包装。客户端自动添加键后，通用幂等中间件即使遇到非 recoverableRoutes 路由，也可能缓存响应；仅不登记恢复路由不足以避开它。书签源对象全部由专用 provider 校验，不走会提前阻止历史任务的普通 review_task 对象边界。

1. 认证并捕获当前身份，校验命令键及原始请求。
2. 事务调用 commandreceipt.Load，取得租户+actor+command 的事务级 advisory lock；这不是目标行锁。
3. 每次重新检查 owner 和所需源权限，命中旧成功回执也不能跳过。取消清理只检查 owner，不回查源正文。
4. 加入/编辑时取得归档可变屏障，并与转派所用源行锁协调；确定锁顺序后固化并发测试，不能只在 handler 查一次负责人。
5. 首次 expected_revision=0 通过唯一约束竞争；更新/取消用独立书签 revision 的 CAS。版本不符返回冲突，不能覆盖另一个标签页。
6. 状态和最小成功回执在同一事务提交；失败回滚。

书签命令使用独立 operation 前缀 `personal_review_bookmark.*`，不加入 review-commands、recoverableRoutes 或 PendingBusinessCommands。只用 apiClient 和该功能内存中的 pending intent，保留同键、同请求用于用户显式重试；原因不写入 localStorage、通用 transport request cache 或命令 result。

同键不同载荷返回冲突。确认服务器已拒绝的 400/409 请求后丢弃旧 intent，重新读当前 revision；用户再次操作使用新键。丢包且结果不确定时才原样重试，不能自动生成新键反复写入。旧 add 回执在后续 remove 后仍可能返回当年的成功，UI 不能因此重新亮标记；重新读取当前状态，并拒绝低于已知版本或旧会话的回填。

上述原命令重试只保证同一页面仍存活且 pending intent 尚在内存的场景。刷新或浏览器重启后不恢复原命令 ID/请求，重新 GET 当前书签与版本确认服务器事实，不自动补写；编辑中的未保存说明可能需要重新填写。服务器重启后，只要页面仍持有同一意图，持久化业务回执仍可确认原命令；这与浏览器重启是不同验收条件。

通用身份/组织撤权与书签事务之间不存在天然的跨模块原子性；本设计承诺每次请求按当前会话和事务事实检查，转派与书签通过源行锁协调，不宣称离线或在途内容可被实时追回。

### 9.2 归档数据库规则

新增迁移复用已有 `exam_archive_require_mutable` 的考试/整场归档及并发屏障，不修改历史 000183。不能直接同时挂通用 `reject_archived_exam_business_mutation`，因为它会阻止归档后取消书签。

专用触发器只允许一种免归档检查的变化：active → deleted，reason 清空，revision 恰加一，仅 updated_at/deleted_at/revision/reason 改变，其余身份、源引用、marked_at、观察版本均不变。新增、恢复标记、改原因全部检查归档。运行时硬 DELETE 禁止，正式物理清理走受控维护。墓碑原样重试不发 UPDATE。

登记归档关联表时注明书签采用专用触发器，不能被后续脚本再挂一层通用全禁规则。直接数据库写入测试验证新增、编辑、恢复被挡且严格清理可执行，不只测 HTTP。

### 9.3 清单查询与分页

SQL 先构造“tenant + owner + 活跃书签 + 当前可读源”的统一基础集合，普通任务与暗样本用安全投影 UNION ALL 后应用筛选、排序、计数。不能先分页再在 Go 里过滤，也不能在浏览器与当前队列取交集。

默认 50、最大 200，按 `(marked_at DESC, id DESC)` keyset 游标；游标绑定筛选摘要，筛选变更重置。列表、total_count、考试/题目选项从同一授权集合取值，可在同一只读 REPEATABLE READ 事务保证单次响应一致；不同页不是永久快照，前端按 bookmark_id 去重，增删后明确刷新。

书签状态不进入原业务任务聚合、阅卷员完成率或业务搜索。任务行上的书签标识可以通过批量 owner 状态查询补齐，但要与当前任务集合一次获取，避免每行一次请求；该批量查询仍须支持领取时插入的暗样本，不改变原任务列表含义。

## 10. 前端状态、草稿与失败恢复

| 场景 | 明确行为 |
| --- | --- |
| 加入/取消中 | 仅锁该书签写操作，保留当前画布；收到确认后再更新状态 |
| 加载清单失败 | 显示错误与重试，不用“暂无复查”掩盖失败 |
| 无标记/筛选无匹配 | 区分空态，后者提供清除筛选 |
| 离线 | 不显示已保存，不创建跨账号自动补写队列；允许明确提示和用户重试 |
| 两窗口改原因 | 409 后保留本页文字，展示新版本提示；用户确认后用新 revision 保存 |
| A任务请求迟到时已打开B | 只更新A的同域缓存，不改B图标、原因或提示 |
| 切账号/租户/解锁重登 | 取消在途请求，清列表、说明、pending意图、只读内容和Blob URL；丢弃旧代次结果 |
| 源访问被拒绝 | 清对应正文、预取、图片及可见清单项，退出到统一不可用态 |
| 图像版本变化 | 停止展示旧文字配新图，重新载入整个只读context |

查询键至少包含现有 session.scope/generation、考试范围、筛选和 bookmark/task；详情与图像版本单独入键。沿用 [reviewTaskContext](../../apps/web-admin/src/query/reviewTaskContext.ts) 的会话守卫思路；普通工作台缓存与只读书签缓存分开，避免把只读 DTO 写入可评分 TaskContext。

加入书签不保存评分草稿；切视图或任务仍走统一离开出口。现有页内 changeQueueScope 重点保护 math.dirty，路由离开还有 useUnsavedChangesGuard。实施时必须同时核对评分草稿、数学校正和书签说明三个独立状态：未同步评分保留既有保存/兜底；证据校正先确认保存或放弃；说明弹层先保存、放弃或继续编辑。不能因打开清单就绕过原保护，也不能把复查说明写成评分草稿。

快捷确认抽屉里的书签不等于“已核对”。首版不自动排除书签任务、不自动勾选、不改变资格函数；可显示标识并提供进入完整工作台的入口。原有提交前复读任务与建议版本继续保留。

## 11. 响应式与无障碍

桌面保持一条任务顶部、左评分栏、右画布；小于可读双栏宽度时纵向排列，答题图在上、评分在下，清单与资料使用可关闭抽屉。主要触摸目标采用项目现有 44px 规范；视口 390px、320px 和 200% 缩放下检查溢出、遮挡和输入法。

操作必须键盘可达；按钮名称明确；状态提示通过 role=status/适当alert宣读；说明弹层关闭后焦点回到入口；移除清单行后焦点移到邻项或清单标题；固定操作栏不能遮挡焦点或软键盘下的保存按钮。首版不新增单字符快捷键，输入法与弹层打开时原评分快捷键暂停。

这些设计分别参考 [W3C 按钮模式](https://www.w3.org/WAI/ARIA/apg/patterns/button/)、[状态消息要求](https://www.w3.org/WAI/WCAG22/Understanding/status-messages.html)及[目标尺寸说明](https://www.w3.org/WAI/WCAG22/Understanding/target-size-minimum.html)。44px 是本项目的产品选择，不把它写成该条 AA 的唯一法定阈值。

## 12. 实施切片与修改地图

设计审读时最高迁移号为 000193；正式实施采用新增迁移 `000194_personal_review_bookmarks.sql`，未修改历史迁移。SDK、路由覆盖和 schema 元数据经正常流程生成，不手改生成内容或提示词。

| 切片 | 可评审出口 | 主要位置 |
| --- | --- | --- |
| S1 数据与本人权限 | 普通/暗样本同API加入、改说明、取消，CAS/RLS/归档可验证 | 新 reviewbookmark 包、迁移、server guards/routes、review/seed窄provider |
| S2 安全只读详情 | 本人已提交意见可看，图像版本绑定，其他成绩/来源不可见 | provider、只读DTO、versioned crop reader、专用图代理 |
| S3 工作台书签与个人清单 | 一键标记、说明、跨设备清单、只读、位置恢复闭环 | query/hooks、小组件、TaskRail/GradingWorkbench与只读容器 |
| S4 简洁教师布局 | 大画布、左评分、任务抽屉、按需资料，管理能力保持原入口 | WorkbenchHeader/TaskContent/ScoreEditor/AnswerEvidencePane及CSS |

每个切片按仓库 Story 的 Plan → Plan Review → Implementation → Review → Fixes → Approval 留证；S1/S2 没通过权限和状态验收，不把前端按钮当完成。相关实现可在无文件冲突的范围内并行，但最终一次只验收一个实施切片。

后端必须接齐内存/PG两套工厂与依赖图：`server/module_grading.go`、`server/application_stores.go`、`server/routes_review.go`、`server/router_guards.go`；新provider分别处理普通任务与seed，不只改SQL。建议新增包文件包括 types/service/store_memory/store_postgres/handlers/provider_review/provider_seed 及 doc.go，具体拆分随职责决定。

设计审读发现暗样本内存存储缺少具体 task 与 observation 的内部关联，而 PostgreSQL 的 seed_observation 已有 seed_task_id。实施已在内存内部保留精确任务关系，未改变公开 Observation DTO；本人意见不能按 Gold、题目或时间猜测。相关 Go 及真实 PostgreSQL 用例验证同人同题多次暗样本回看不会串用评分，实际输出见[实施记录](../../reports/personal-review-bookmarks-implementation-2026-10-18/implementation-plan.md)。

API 权威文件是 [services/api-gateway/openapi/edugrade-api.openapi.json](../../services/api-gateway/openapi/edugrade-api.openapi.json)，生成流程走 `npm run generate:sdk` 和既有契约门禁。更新 [review API 说明](../api/review.md)的相关权限章节；其旧文案不能当作当前 guard 的事实源。

新增路由还需 `npm run generate:route-coverage`；新增迁移后执行 `npm run sync:schema-version`，同步部署 schema 元数据，再执行 `npm run ci:contracts` 等既有门禁。这些脚本的生成/检查结果不能代替实际数据库迁移和权限测试。内存工厂、PostgreSQL 工厂及模块依赖完整性测试必须同时接线；保留 pageRendering.tsx 中教师个人入口与考试工作区已有的权限差异，不用一个宽泛 canWork 替代。

前端新增独立书签 API、query、写入 hook、小按钮/说明编辑器、清单及只读详情容器。调整现有工作台只做连接与布局；不把书签逻辑塞入评分提交 hook。readonly 文案按具体原因显示“已提交·只读”“考试归档·只读”，不沿用当前固定的“管理员只读”。

按[注释标准](../engineering/commenting-standard.md)解释身份来源、暗样本隐藏、只读授权、CAS/墓碑、归档清理例外、材料版本和迟到请求边界；不逐行注释，也不写实现未保证的承诺。

## 13. 不依赖外部环境的验收清单

| 编号 | 本地场景 | 必须证明 |
| --- | --- | --- |
| T01 | 加入、改说明、取消、再加入 | 版本连续；说明边界正确；再加入更新marked_at |
| T02 | 非干扰 | task/seed版本、状态、updated_at、租约、草稿、评分和发布事实都不被书签操作改变 |
| T03 | 两个标签页并发创建/修改 | 唯一行，旧revision失败，不静默覆盖 |
| T04 | 取消再加后重放旧新增/删除 | 墓碑防旧命令影响新标记；旧回执不回填当前状态 |
| T05 | 响应丢失、服务重启、页面刷新、同键异载荷 | 页面存活时同键原意图可确认；浏览器刷新/重启仅GET当前态、不自动补写；变更请求冲突；原因不进通用缓存 |
| T06 | 两用户、同校管理员、跨租户 | 个人说明与清单隔离；实际PG RLS也拒绝跨租户 |
| T07 | 转派/跨校/撤权/停用/旧会话 | 正文与图片都拒绝；活跃账号可仅清理本人记录 |
| T08 | 普通双评与后继重评 | 只读只显示原task本人的分，不带另一评/仲裁/当前终分 |
| T09 | Seed标记与已完成回看 | 相同公开流程；无Gold/观察/参考分/真实来源，缺文本不伪造；内存与PG均按具体task定位，同人同题多次不串分 |
| T10 | Seed与普通图片GET/HEAD/条件请求 | 不泄漏源文件名/ID/裸ETag/存储地址；均先鉴权 |
| T11 | context后裁剪或识别文字变化 | image旧版本409，重新读取；不把task revision当crop版本；文字同源未知时不伪称已核对 |
| T12 | 原图/历史快照边界 | 仅题块；changed/unknown准确；无历史材料时不猜补 |
| T13 | 归档/并发归档 | 新增/编辑/恢复在HTTP与数据库都拒绝，严格取消允许 |
| T14 | 跨页、筛选、同一响应计数 | 普通/Seed均不漏；权限先过滤；筛选项无失权内容侧漏 |
| T15 | 草稿、数学校正、说明编辑切换 | 不丢编辑、不意外提交，三种未保存状态分别处理 |
| T16 | A→B、换账号、解锁重登、失权、取消时的在途材料 | 旧异步结果不污染当前内容；取消后不重拉，刷新/重开404；Blob在关闭/切换或失权时释放 |
| T17 | 只读容器副作用 | 只读容器不发起领取/续租/草稿保存/校正/AI请求/批注写入；覆盖其他活动任务时单独归因既有后台保存，另验无活动任务的全局零写 |
| T18 | 快捷确认和简洁布局回归 | 书签不等于核对；顶部只有同一提交链；第二意见限制和键盘保护保持 |
| T19 | 320/390/1024/1440与200%缩放 | 无横向溢出、焦点遮挡、移动键盘下按钮不可达 |
| T20 | 日志、审计、命令回执 | 不含原因、图像、学生身份、Seed来源；必要事件可追踪 |

测试层次：内存Store和组件单测；真实隔离本地 PostgreSQL 的迁移/FK/RLS/并发/归档；生产router的HTTP权限；浏览器合成UI与可访问性验证。`store_postgres_batch34_test.go` 使用SQL驱动夹具，不当作真实PG证据；数据库未配置导致skip不算通过。可复用 e2eOpenPostgresTestDB/e2eApplyPostgresMigrations/e2ePostgresRouter 的隔离入口；浏览器mock不能证明后端权限。

实际浏览器检查覆盖 1440/1024/390/320px，200% 场景使用 720×500 CSS 视口作等效重排；最后的只读弹层修复另在 320/390/720/1024/1440px 确认页面、弹层和 wrap 均无横向溢出。未执行原生浏览器缩放或真机软键盘/输入法操作，因此不能表述为 T19 全部条件已经实测通过；实际覆盖及剩余限制见实施记录。

首版审计只保留动作、owner、书签ID、请求ID和必要版本，不保存说明。若沿用现有尽力而为审计，只能按此强度声明；需要审计原子性则在实现中采用事务记录并验证，不能靠文档声称恰好一次。

## 14. 本次审读覆盖与剩余边界

下表记录设计阶段围绕目标链路的定向审读，不是全仓逐文件审计；未列模块和文件内无关逻辑不宣称审读完成。设计阶段未运行项目测试；实施阶段已完成的本地 Go、真实 PostgreSQL、前端、浏览器和契约检查及未实测边界，统一以[实施记录](../../reports/personal-review-bookmarks-implementation-2026-10-18/implementation-plan.md)为准。第 13 节是验收要求，不表示每项已经通过。

| 链路 | 已核对文件/符号或测试 | 得到的约束 |
| --- | --- | --- |
| 任务与成绩 | review/types.go；store_postgres_tasks.go；store_postgres_resolution.go | task/轮次身份、状态、本人意见查询 |
| 工作台聚合 | task_context.go、task_context_handler.go、task_context_postgres.go；store_postgres_context.go | 冻结材料、AI限制、当前终分不得当历史 |
| 分派与权限 | auth/store_postgres.go、resource_scope.go、access_scope.go；workflowactors/directory.go | 活动scope、本人分派、学校真实关系 |
| 会话与归档 | auth/managed_access_store.go、archive_boundary.go；000183迁移相关函数 | epoch/撤权、归档只读与数据库屏障 |
| 暗样本 | seedquality/context.go、service.go、types及store相关提交字段 | PublicTask、本人submitted字段、无历史图字节冻结 |
| 图像 | review/handlers.go的GetWorkspaceSegmentImage；segment/images.go、store_postgres.go | 当前crop、资源filename/ETag、版本检查位置 |
| 命令 | commandreceipt/receipt.go；idempotency/command_identity.go；server/router_guards.go | 身份锁、业务回执、不能通用缓存私有正文 |
| 前端数据 | api/client.ts、api/businessCommand.ts；query/reviewTaskContext.ts；gradingTaskContext.ts | cookie/CSRF、单次自动键、会话代次与第二意见 |
| 前端编辑 | GradingWorkbench.tsx、workbenchReviewState.ts、useGradingReviewActions、草稿生命周期与离开守卫 | 自动保存/续租、统一提交、切任务保护 |
| 展示/快捷键 | WorkbenchHeader、TaskRail、TaskContent、AnswerEvidencePane、FrozenQuestionReference、ScoreEditor、SharedScoreControl、useGradingKeyboard | 布局位置、精度、批注副作用、输入法保护 |
| 批量确认 | FastConfirmDrawer、ExamScoringPanel/Overview/Dialogs | 逐份核对、管理与教师入口分离 |
| 依赖接线 | server/module_grading.go、application_stores.go、routes_review.go；pageRendering.tsx | 双存储/Seed钩子、角色实际页面props |
| 后端测试源码 | auth的resource_scope/seed_boundary/managed_access/archive_boundary测试；seed的service/command_recovery测试；segment/images_test；server/review_route_test等 | 现有隔离规则及需要补的精确场景 |
| 前端测试源码 | GradingWorkbench.batch8；gradingHooks.batch30；reviewContext/workbenchReviewState；review/queue/math action的scope/session测试 | 迟到请求、深链、只读和版本回归入口 |

代码审读还发现一个既有相邻风险：Seed图片路由直接复用资源响应时，文件名/ETag可能暴露真实题块标识或形成来源关联；现有图片路由的v参数也不构成context一致性校验。本方案要求新增书签图路由避免这些问题；既有路由如需修复，单独形成缺陷任务，不在设计阶段悄悄改业务。

### 设计复核记录

2026-10-18 设计阶段的并行审读覆盖权限/暗样本、存储/API、工作台交互、依赖接线和边界复核，修订了跨校检查、Seed 内存本人提交定位、专用命令 guard 与 HEAD、旧回执、图像版本和刷新恢复边界。用户随后批准方案并授权并行实施，现已完成代码接入与本地验收；取消后实际立即清除详情，普通与暗样本统一不展示无法证明同源的 OCR。最终交付状态和验收证据以[实施记录](../../reports/personal-review-bookmarks-implementation-2026-10-18/implementation-plan.md)为准，不沿用设计阶段的“可进入实施”结论。

### 设计阶段交互示意验证（历史记录）

合成数据示意已用浏览器检查：1024/736/390/320px 无横向溢出；1024px 评分栏宽 280px，390/320px 答题图在评分栏上方。验证了 0–满分且最多一位小数的示意输入、200/201 个 emoji 码点边界、书签不丢评分草稿、筛选、已提交只读且无提交按钮、模拟提交进入质检并推进下一份、书签与说明保留；未捕获页面脚本错误。已提交示意隐藏历史评分说明，并标明本人提交分。

示意不接真实 API，刷新会重置。上述结果只证明设计示意的交互和布局，不能代替第 13 节验收清单中的后端权限、真实数据库、并发、图字版本、会话清理、200% 缩放与实际评分组件回归。实际应用浏览器验证另行留证，不复用此处结果计作通过。

## 15. 联网参考及采用边界

| 一手资料 | 采用的设计依据 | 本项目自行决定的部分 |
| --- | --- | --- |
| [Moodle Using Quiz](https://docs.moodle.org/500/en/Using_quiz) | 标记作为回看定位，与作答/提交分开 | 该例主要是学生标记，不把它当教师阅卷权限模型 |
| [Slack 保存以供稍后查看](https://slack.com/help/articles/360042650274-Save-messages-and-files-for-later) | 个人保存与回到原上下文 | 不照搬提醒/待办体系，也不从“保存”推导永久材料权限 |
| [W3C APG Button](https://www.w3.org/WAI/ARIA/apg/patterns/button/) | 固定名称toggle及aria-pressed、键盘与焦点 | 具体按钮文案及位置结合当前工作台 |
| [WCAG 状态消息](https://www.w3.org/WAI/WCAG22/Understanding/status-messages.html) | 加入/失败状态可被辅助技术读取 | 不把每次轮询都强制宣读 |
| [RFC 9110](https://www.rfc-editor.org/rfc/rfc9110.html#section-9.2.2) | 设定状态式操作与重试语义，避免toggle重试反转 | expected_revision及命令回执沿用项目设计；HTTP幂等不自动保证并发无覆盖 |
| [PostgreSQL 行安全](https://www.postgresql.org/docs/current/ddl-rowsecurity.html) | RLS与表owner例外需要明确 | owner私有性继续由业务显式过滤与实际数据库测试保证 |

以上只吸收功能原则与协议要求；具体界面、数据模型、API和实现独立设计。未复制 AI-Marker-Suite、Moodle 或截图的软件代码、文案与素材。
