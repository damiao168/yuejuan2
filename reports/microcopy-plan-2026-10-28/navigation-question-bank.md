# 登录、题库与助手的小字处置方案

日期：2026-10-28。以下均为待实施方案，不表示功能或文案已经上线。原文为源码可定位文本；带花括号的目标文案为动态展示模板。

<a id="mc-r01"></a>

## MC-R01 · P2 · 教师首页：任务摘要

- 角色与当前条件：阅卷教师、仲裁教师；有阅卷或仲裁权限时显示摘要；有当前任务时另显示段落。
- 原文：“已加载待阅卷” [apps/web-admin/src/pages/TeacherDashboardPage.tsx:81](../../apps/web-admin/src/pages/TeacherDashboardPage.tsx#L81)；“已加载待仲裁” [apps/web-admin/src/pages/TeacherDashboardPage.tsx:82](../../apps/web-admin/src/pages/TeacherDashboardPage.tsx#L82)；“已加载已提交” [apps/web-admin/src/pages/TeacherDashboardPage.tsx:83](../../apps/web-admin/src/pages/TeacherDashboardPage.tsx#L83)；“分别查看已分配的阅卷与仲裁；数量以当前已加载任务为准。” [apps/web-admin/src/pages/TeacherDashboardPage.tsx:183](../../apps/web-admin/src/pages/TeacherDashboardPage.tsx#L183)
- 判断：同一取数范围重复四次，标签难读；当前响应数量又不能冒充全量。
- 处理：shorten / merge；依赖：frontend。
- 目标文案：{"summary":"待阅卷 / 待仲裁 / 已提交","details":"摘要仅统计当前已读取任务。完整任务请进入阅卷或仲裁列表。"}
- 展示：标签常驻；范围说明在摘要下只显示一次。 位置：摘要标签及统一范围说明。
- 保留边界：当前数据未包含全量计数，不能改成‘全部待办’。；某类别读取失败仍显示‘—’，不可变成0。
- 验收：同时有两种任务时范围说明只出现一次。；任一接口失败时保留对应失败提示与重试。

<a id="mc-r02"></a>

## MC-R02 · P2 · 教师首页：空状态

- 角色与当前条件：阅卷教师、仲裁教师；有权限且tasks为空时；当前也可能伴随接口降级警告。
- 原文：“当前没有已加载的待办” [apps/web-admin/src/pages/TeacherDashboardPage.tsx:166](../../apps/web-admin/src/pages/TeacherDashboardPage.tsx#L166)；“任务分配后会显示在这里；如果任务区加载失败，请重试。” [apps/web-admin/src/pages/TeacherDashboardPage.tsx:166](../../apps/web-admin/src/pages/TeacherDashboardPage.tsx#L166)
- 判断：正常空状态提前解释失败，与真实失败提示重叠，且‘没有’可能掩盖尚未读取。
- 处理：contextual / shorten；依赖：frontend。
- 目标文案：{"summary":"当前暂无待办","failure":"任务暂时无法读取，请重试。","details":"任务分配后会显示在这里。"}
- 展示：仅相应数据成功读取且为空时显示正常空状态；读取失败显示失败提示。 位置：任务列表占位区。
- 保留边界：先区分空结果与读取失败；不得只替换标题而保留错误条件。
- 验收：成功空数组显示暂无待办。；请求失败不出现‘暂无待办’结论。

<a id="mc-r03"></a>

## MC-R03 · P3 · 密码登录：找回入口

- 角色与当前条件：登录用户；密码登录表单底部常驻。
- 原文：“忘记密码？请联系学校管理员核对身份并生成一次性恢复链接。” [apps/web-admin/src/pages/LoginPage.tsx:169](../../apps/web-admin/src/pages/LoginPage.tsx#L169)
- 判断：用户只需知道联系谁；管理员生成凭据的实现步骤过早出现。
- 处理：shorten；依赖：copy。
- 目标文案：{"summary":"忘记密码？请联系学校管理员。"}
- 展示：密码登录方式下保留；管理员操作页继续说明身份核验和一次性链接。 位置：登录按钮下。
- 保留边界：不能增加尚不存在的自助找回链接；管理员侧身份核验不删。
- 验收：用户能识别联系对象，不误认为已发送恢复链接。

<a id="mc-r04"></a>

## MC-R04 · P1 · 登录：公共电脑

- 角色与当前条件：使用共享设备的用户；public_device被勾选时显示。
- 原文：“公共电脑会话最长 4 小时；页面会持续提示，离开前请点退出。” [apps/web-admin/src/pages/LoginPage.tsx:212](../../apps/web-admin/src/pages/LoginPage.tsx#L212)
- 判断：时限与离开前退出是必要提醒；‘页面会持续提示’只是介绍界面。
- 处理：shorten / keep；依赖：copy。
- 目标文案：{"summary":"公共电脑登录最长 4 小时，离开前请退出。"}
- 展示：仍仅公共电脑策略被选中时出现。 位置：设备策略旁的可见提示。
- 保留边界：保留4小时时限和主动退出要求；不得改成‘4小时后自动清除全部数据’。
- 验收：公共电脑策略开启能直接看到限制；关闭后不残留。

<a id="mc-r05"></a>

## MC-R05 · P2 · 微信登录：有效期与隐私

- 角色与当前条件：微信登录用户；微信登录页常驻，二维码尚未生成时也显示。
- 原文：“二维码 5 分钟内有效，仅用于本次登录，不会读取微信好友或聊天信息。” [apps/web-admin/src/pages/LoginPage.tsx:188](../../apps/web-admin/src/pages/LoginPage.tsx#L188)
- 判断：有效期应与已生成二维码关联，隐私说明适合独立帮助，当前把两件事挤成小字。
- 处理：contextual / progressive；依赖：frontend。
- 目标文案：{"summary":"二维码 5 分钟内有效，仅供本次登录。","detailsLabel":"微信登录会读取什么？","details":"不会读取微信好友或聊天信息。"}
- 展示：生成二维码后显示有效期；隐私帮助在生成前后都可展开。 位置：二维码下方和独立帮助入口。
- 保留边界：5分钟沿用当前实现文案；若后端返回有效期有差异，须以实际挑战期限为准。；不把二维码过期误写成账号登录会话到期。
- 验收：生成前没有悬空的有效期句子；生成后时限可见。；二维码失败、过期消息仍直接显示。

<a id="mc-r06"></a>

## MC-R06 · P2 · 主站学生身份过渡页

- 角色与当前条件：从主站进入的学生；学生身份进入主站；按是否配置VITE_STUDENT_PORTAL_URL分支。
- 原文：“这里提供学校学生端入口；成绩与学习反馈是否可见，以学生端实际发布状态为准。” [apps/web-admin/src/pages/IdentityLandingPage.tsx:11](../../apps/web-admin/src/pages/IdentityLandingPage.tsx#L11)；“使用学校提供的学生端查看本人已发布的成绩和学习反馈。” [apps/web-admin/src/pages/IdentityLandingPage.tsx:30](../../apps/web-admin/src/pages/IdentityLandingPage.tsx#L30)；“此站点尚未配置学生端网址” [apps/web-admin/src/pages/IdentityLandingPage.tsx:31](../../apps/web-admin/src/pages/IdentityLandingPage.tsx#L31)；“请使用学校发放的学生端网址；如不清楚入口，请联系学校管理员。” [apps/web-admin/src/pages/IdentityLandingPage.tsx:31](../../apps/web-admin/src/pages/IdentityLandingPage.tsx#L31)
- 判断：两段都在解释另一个入口；缺失配置时向学生暴露部署状态。
- 处理：merge / contextual；依赖：frontend。
- 目标文案：{"summary":"进入学生端，查看已发布成绩和学习反馈。","missing":"请使用学校发放的学生端网址，或联系学校管理员获取。"}
- 展示：有网址只保留一句和进入按钮；无网址只显示获取入口的说明。 位置：学生入口按钮附近。
- 保留边界：不承诺跨站自动登录；不改审计员工作台说明。
- 验收：有网址和无网址各显示一段；不显示技术环境变量或配置诊断。

<a id="mc-r07"></a>

## MC-R07 · P2 · 题库：首页说明

- 角色与当前条件：题库教师；题库页页头常驻。
- 原文：“维护题目、答案和评分标准。发布后供授权教师选入考试；已开展考试使用的版本不随题库更新改变。” [apps/web-admin/src/features/question-bank/QuestionBankHeader.tsx:17](../../apps/web-admin/src/features/question-bank/QuestionBankHeader.tsx#L17)
- 判断：任务介绍、权限范围、版本规则混在页头；版本影响应在发布或选题时强调。
- 处理：shorten / contextual；依赖：frontend。
- 目标文案：{"summary":"维护题目、答案和评分标准。","details":"发布供授权教师选用；已有考试继续使用原版本。"}
- 展示：页头只保留任务说明；发布/新版本/选题位置保留版本影响。 位置：页头与发布结果区。
- 保留边界：必须与MC-R10配套，不能把已有考试不自动更新的事实从所有入口删掉。
- 验收：列表首屏不铺开版本规则；发布后仍清楚提示已有考试不变。

<a id="mc-r08"></a>

## MC-R08 · P1 · 题库：审核流程说明

- 角色与当前条件：作者、审核教师、发布教师；题目详情审核区，所有工作流状态均显示。
- 原文：“草稿 → 审核 → 发布到学校题库 → 在考试中选入题目。提交后锁定当前内容，作者不能审核自己的版本。” [apps/web-admin/src/features/question-bank/QuestionBankPanels.tsx:109](../../apps/web-admin/src/features/question-bank/QuestionBankPanels.tsx#L109)
- 判断：整套流程每次重复，真正决定当前操作的是锁定、独立审核与下一步。
- 处理：contextual / progressive；依赖：frontend。
- 目标文案：{"summary":"提交后锁定当前内容，需由其他教师审核。","reviewing":"审核中，当前内容已锁定。","approved":"审核通过，等待有发布权限的教师发布。","detailsLabel":"查看审核与发布流程","details":"草稿 → 审核 → 发布到题库 → 选入考试。"}
- 展示：按draft/reviewing/approved/published状态显示当前一步；完整流程可展开。 位置：审核动作附近。
- 保留边界：提交前锁定影响必须可见，不只放悬浮提示。；作者不能审核自己的版本，权限校验保持。
- 验收：四种状态各只显示相关说明；提交前能看到锁定后果。

<a id="mc-r09"></a>

## MC-R09 · P1 · 题库：交接与未通知事实

- 角色与当前条件：作者、审核教师；人员信息段落、复制审核链接成功提示、提交成功提示分别出现。
- 原文：“此处列出实际有权限的人员，提交不会自动发送通知。” [apps/web-admin/src/features/question-bank/QuestionBankPanels.tsx:112](../../apps/web-admin/src/features/question-bank/QuestionBankPanels.tsx#L112)；“审核链接已复制，请发送给上方有权限的审核教师；系统不会自动通知。” [apps/web-admin/src/features/question-bank/QuestionBankPanels.tsx:104](../../apps/web-admin/src/features/question-bank/QuestionBankPanels.tsx#L104)；“已提交审核，请通知本题库的审核教师在题库中处理。” [apps/web-admin/src/features/question-bank/questionBankPageState.ts:6](../../apps/web-admin/src/features/question-bank/questionBankPageState.ts#L6)
- 判断：需要人工交接是真实限制，不能只删；三处重复的权限解释可以收敛到一次阶段提示。
- 处理：merge / shorten / keep；依赖：frontend。
- 目标文案：{"summary":"审核教师：{有权限人员姓名}。请自行联系，系统暂不发送通知。","submitted":"已提交审核，请联系上方审核教师。","approved":"发布教师：{有权限人员姓名}。","copiedReviewing":"链接已复制，请发给审核教师。","copiedApproved":"链接已复制，请发给发布教师。"}
- 展示：草稿/审核中显示审核联系人和一次未通知提示；已批准切换为发布联系人。 位置：当前阶段联系人旁；成功提示只确认动作。
- 保留边界：保留服务端授权人员名单和姓名读取失败分支。；未增加通知功能前不得写‘已通知’或‘对方将收到待办’。
- 验收：提交与复制都不会制造已通知的错觉；名单失败仍可重试且不伪造姓名。；已批准阶段复制链接时，提示联系发布教师，与当前联系人一致。

<a id="mc-r10"></a>

## MC-R10 · P1 · 题库：发布结果

- 角色与当前条件：发布教师；发布版本后，详情成功块和成功消息。
- 原文：“下一步在考试的“小题与分值”页点击“从题库选题”。已有考试保留原版本；新版本不会自动替换考试中的题目。” [apps/web-admin/src/features/question-bank/QuestionBankPanels.tsx:115](../../apps/web-admin/src/features/question-bank/QuestionBankPanels.tsx#L115)；“已发布到学校题库。请在考试的题目页选择此版本；已有考试不受影响。” [apps/web-admin/src/features/question-bank/questionBankPageState.ts:5](../../apps/web-admin/src/features/question-bank/questionBankPageState.ts#L5)
- 判断：两个位置重复下一步与版本影响；‘已有考试不受影响’不如明确仍用原版。
- 处理：merge / shorten / keep；依赖：frontend。
- 目标文案：{"summary":"已发布到学校题库。","details":"在考试的“小题与分值”中选入此版本。已有考试仍使用原版本。","action":"去考试中选题"}
- 展示：成功消息只报发布成功，版本影响集中在详情成功块。 位置：发布结果块及既有选题按钮。
- 保留边界：已有考试不会自动替换必须保留。；按钮仍按访问权限显示。
- 验收：发布后两处不重复整段说明；无考试权限时不出现不可用的入口。

<a id="mc-r11"></a>

## MC-R11 · P1 · 题库：并发冲突警告

- 角色与当前条件：题目编辑教师；检测到草稿冲突时，以及查看最新版本弹窗内。
- 原文：“本地修改已保留。先查看并比较最新版本，可下载本地备份，再决定是否放弃修改。” [apps/web-admin/src/features/question-bank/QuestionBankPanels.tsx:56](../../apps/web-admin/src/features/question-bank/QuestionBankPanels.tsx#L56)；“查看最新版本不会覆盖本地修改。采用前请核对差异，下载的备份包含当前题目内容、答案、评分编辑和附件信息。” [apps/web-admin/src/features/question-bank/QuestionBankConflictDialog.tsx:26](../../apps/web-admin/src/features/question-bank/QuestionBankConflictDialog.tsx#L26)
- 判断：‘已保留’易被理解为持久保存；两段重复教程挤占差异本身。
- 处理：shorten / merge / keep；依赖：frontend。
- 目标文案：{"summary":"草稿已有更新。你在本页的修改尚未被覆盖，请先比较版本。","details":"采用最新版本会替换本页未保存内容。可先下载本地修改备份。","backupHelp":"备份包含题目、答案、评分编辑和附件信息；附件信息不等同于附件文件。"}
- 展示：警告提示只说明当前事实；替换影响在比较弹窗和最终危险确认中可见。 位置：冲突提示、比较弹窗与采用确认。
- 保留边界：不承诺刷新、关闭浏览器后仍可恢复；不暗示备份附带图片二进制。；保留有未保存修改时的最终危险确认。
- 验收：只读比较不改输入；点击采用前看到替换范围；取消不丢修改。

<a id="mc-r12"></a>

## MC-R12 · P1 · 题库：两个编辑区互锁

- 角色与当前条件：题目编辑教师；contentDirty或scoringDirty时显示；另一编辑区禁用。
- 原文：“请先保存当前编辑区域，再编辑另一部分或提交审核。” [apps/web-admin/src/features/question-bank/QuestionBankPanels.tsx:62](../../apps/web-admin/src/features/question-bank/QuestionBankPanels.tsx#L62)
- 判断：‘当前区域/另一部分’含糊，用户还需推断应按哪个保存按钮。
- 处理：contextual / shorten；依赖：frontend。
- 目标文案：{"contentDirty":"先保存题目内容，再编辑评分标准或提交审核。","scoringDirty":"先保存评分标准，再编辑题目内容或提交审核。"}
- 展示：按实际脏区选择文案；仅在互锁存在时显示。 位置：禁用区域前并关联相应保存动作。
- 保留边界：现有版本互锁仍保留；统一保存属于另一项功能方案，不靠改小字假装解决。
- 验收：分别只改题干和只改评分时，提示准确指向可用保存动作。

<a id="mc-r13"></a>

## MC-R13 · P3 · 题库：审核记录空意见

- 角色与当前条件：题库教师；每条审核记录没有comment时，仍渲染独立段落。
- 原文：“无补充意见” [apps/web-admin/src/features/question-bank/QuestionBankPanels.tsx:118](../../apps/web-admin/src/features/question-bank/QuestionBankPanels.tsx#L118)
- 判断：多次操作都重复一个空值，记录时间和决定才是有效信息。
- 处理：delete / contextual；依赖：frontend。
- 目标文案：{"summary":"无意见时不渲染意见段落；有意见时原样展示。"}
- 展示：只省略空comment的正文，不省略操作记录。 位置：审核时间线。
- 保留边界：不删除决定、操作者已有信息、修订号和时间等审计事实。
- 验收：连续5条空意见不出现5段占位；有退回原因仍完整显示。

<a id="mc-r14"></a>

## MC-R14 · P2 · 题库：授权职责解释

- 角色与当前条件：题库管理员；访问权限弹窗顶部常驻所有职责解释。
- 原文：“浏览可看题；出题可编辑草稿；审核可批准其他教师的版本；发布可将已批准版本用于题库；管理仅维护结构与授权，不自动获得看题权限。” [apps/web-admin/src/features/question-bank/QuestionBankDialogs.tsx:125](../../apps/web-admin/src/features/question-bank/QuestionBankDialogs.tsx#L125)
- 判断：五种角色同段解释，新增某一授权时仍需扫描全部；管理不含看题是关键反直觉边界。
- 处理：progressive / contextual / keep；依赖：frontend。
- 目标文案：{"summary":"按实际职责授权。","roleHelp":{"Viewer":"可浏览题目和查看统计。","Author":"可浏览、新建题目并编辑草稿。","Reviewer":"可浏览并审核其他教师的版本。","Publisher":"可浏览、发布已批准版本并停止选用题目。","Manager":"仅管理结构与授权，不自动获得看题权限。"},"detailsLabel":"查看各职责权限"}
- 展示：每条授权选择某职责后显示该项说明；完整职责对照可展开。 位置：职责选择器邻近；完整权限对照。
- 保留边界：roleHelp展示键须对照实际preset枚举映射，示例键不是新增契约。；管理职责不自动获得阅读权限必须选中时可见。；职责解释须按presetActions完整说明；不能遗漏发布职责同时包含的停止选用权限。
- 验收：每种预设职责帮助与服务端权限一致；不因删说明扩大权限。

<a id="mc-r15"></a>

## MC-R15 · P1 · 题库：历史授权保护

- 角色与当前条件：题库管理员；存在自定义组合、受保护用户或授权读取失败时，分别显示相关提示。
- 原文：“以下历史组合授权由服务端原样保留。可继续调整其他人员与普通题库组的预设职责；本次更新不会删改这些组合权限。” [apps/web-admin/src/features/question-bank/QuestionBankDialogs.tsx:128](../../apps/web-admin/src/features/question-bank/QuestionBankDialogs.tsx#L128)；“创建者和当前操作者的直接授权也由服务端保留。” [apps/web-admin/src/features/question-bank/QuestionBankDialogs.tsx:129](../../apps/web-admin/src/features/question-bank/QuestionBankDialogs.tsx#L129)；“当前授权读取失败，不能据空列表覆盖原授权” [apps/web-admin/src/features/question-bank/QuestionBankDialogs.tsx:131](../../apps/web-admin/src/features/question-bank/QuestionBankDialogs.tsx#L131)
- 判断：历史权限块解释过长；读取失败分支是强保护必须直接可见。
- 处理：merge / shorten / keep；依赖：frontend。
- 目标文案：{"summary":"这些历史或受保护授权只读保留，本次保存不会修改。","failure":"授权读取失败，暂不能保存。请重新读取。"}
- 展示：只读保留区头部解释一次；读取失败单独显示错误及重试。 位置：只读授权区和保存阻断提示。
- 保留边界：只读用户与自定义组合仍完整列出具体权限。；失败时继续禁用保存，不能只去掉警告。
- 验收：旧授权不被空列表覆盖；保护对象和具体授权可核对。

<a id="mc-r16"></a>

## MC-R16 · P2 · 题库：分类规则维护

- 角色与当前条件：题库管理员；分类弹窗概览、字段区、目录区均有说明。
- 原文：“填写显示名称和内容类型，系统生成内部编号。已有编号保持不变，以便历史题目继续使用。” [apps/web-admin/src/features/question-bank/QuestionBankClassificationFields.tsx:14](../../apps/web-admin/src/features/question-bank/QuestionBankClassificationFields.tsx#L14)；“每行填写一个知识点或分类名称。不再使用的条目请关闭“允许新题目选择”，历史题目中的记录会保留。” [apps/web-admin/src/features/question-bank/QuestionBankClassificationFields.tsx:31](../../apps/web-admin/src/features/question-bank/QuestionBankClassificationFields.tsx#L31)；“先建立知识点或分类目录，再按需要添加补充字段。内部编号自动生成，无需填写。” [apps/web-admin/src/features/question-bank/QuestionBankDialogs.tsx:161](../../apps/web-admin/src/features/question-bank/QuestionBankDialogs.tsx#L161)
- 判断：自动内部编号被两次介绍；‘每行’像批量文本输入，当前实际是逐条表单。
- 处理：delete / shorten / merge；依赖：frontend。
- 目标文案：{"summary":"添加知识点、分类目录或补充字段。","fields":"设置字段名称和内容类型。","terms":"停用条目后，新题不能再选，历史题目记录保留。"}
- 展示：概览一句；字段区不讲内部编号；停用影响靠近允许新题目选择开关。 位置：分类维护分区说明。
- 保留边界：已有题目继续用原规则的版本说明保留。；不改历史稳定ID生成与保留逻辑。
- 验收：三个区域不重复内部编号；停用时可理解影响仅限新选用。

<a id="mc-r17"></a>

## MC-R17 · P3 · 题库：特殊评分方式已有折叠

- 角色与当前条件：题库教师；特殊设置已放在原生details中，默认收起。
- 原文：“特殊作答与评分方式（通常无需修改）” [apps/web-admin/src/features/question-bank/ContentFields.tsx:70](../../apps/web-admin/src/features/question-bank/ContentFields.tsx#L70)；“选择题型时自动带出常用方式；仅在实际作答形式不同时调整。” [apps/web-admin/src/features/question-bank/ContentFields.tsx:74](../../apps/web-admin/src/features/question-bank/ContentFields.tsx#L74)
- 判断：现有做法有效；辅助说明解释何时修改，有实际决策价值。
- 处理：keep；依赖：copy。
- 目标文案：{"summary":"保留现有折叠和说明。"}
- 展示：保持默认收起；用户展开后再看到说明。 位置：特殊作答与评分方式折叠内。
- 保留边界：此项是保留决策，不计作需要删除的缺陷。
- 验收：键盘可展开；普通题目无需阅读此段便可继续。

<a id="mc-r18"></a>

## MC-R18 · P1 · 题库：引用图片授权

- 角色与当前条件：导题教师、题库教师；存在reference materials时，按已有版本与导入预览分支。
- 原文：“图片随本题版本保留，原材料授权撤回或图片不可用时将停止使用。” [apps/web-admin/src/features/question-bank/QuestionBankReferenceImages.tsx:13](../../apps/web-admin/src/features/question-bank/QuestionBankReferenceImages.tsx#L13)；“导入将把这份图片清单的使用权绑定到目标草稿版本；原材料撤回后仍会停止使用。” [apps/web-admin/src/features/question-bank/QuestionBankReferenceImages.tsx:13](../../apps/web-admin/src/features/question-bank/QuestionBankReferenceImages.tsx#L13)
- 判断：绑定清单的实现表述较绕，授权撤回的后果必须保留。
- 处理：shorten / keep；依赖：copy。
- 目标文案：{"summary":"本版本引用这些图片；原材料授权撤回或图片不可用时，将停止使用。","importing":"导入后本草稿将引用这些图片；原材料授权撤回后，将停止使用。"}
- 展示：仍仅有引用图片时显示，保留导入前后语态。 位置：图片组上方的一次说明。
- 保留边界：不得改成‘永久保存’或‘离线可用’；不承诺授权撤回后图片仍可访问。
- 验收：撤回和不可用状态不被弱化成普通加载延迟。

<a id="mc-r19"></a>

## MC-R19 · P3 · 题库：结构化答案帮助

- 角色与当前条件：出题教师；标准答案字段下始终显示。
- 原文：“按题型选择或逐项填写；系统会保留结构化答案。” [apps/web-admin/src/features/question-bank/ScoringEditor.tsx:254](../../apps/web-admin/src/features/question-bank/ScoringEditor.tsx#L254)
- 判断：后半句只描述数据表示方式，对如何填写没有帮助；控件本身已按题型变化。
- 处理：delete / contextual；依赖：frontend。
- 目标文案：{"summary":"删除‘系统会保留结构化答案’。","details":"仅当某题型有额外填写规则时，在该控件旁说明；规则须来自实际控件约束。"}
- 展示：普通输入不显示泛化帮助；特殊多空或判断控件按需显示真实规则。 位置：具体答案控件邻近。
- 保留边界：不删除结构化数据；未知历史答案内容继续可见，不能因文案清理丢字段。
- 验收：选择/判断/填空/简答四种题型可完成填写；输入规则不靠猜。

<a id="mc-r20"></a>

## MC-R20 · P1 · 题库：误差单位

- 角色与当前条件：数值题出题教师；绝对误差和相对误差字段当前无题型条件常驻。
- 原文：“例如答案为10，填写0.1可接受9.9至10.1。” [apps/web-admin/src/features/question-bank/ScoringEditor.tsx:262](../../apps/web-admin/src/features/question-bank/ScoringEditor.tsx#L262)；“0.01表示允许1%的误差。” [apps/web-admin/src/features/question-bank/ScoringEditor.tsx:265](../../apps/web-admin/src/features/question-bank/ScoringEditor.tsx#L265)
- 判断：非数值题无关；相对误差小数与百分比换算是必要单位解释，不能直接删。
- 处理：progressive / keep；依赖：frontend。
- 目标文案：{"summary":"误差设置（数值答案）","absolute":"如标准答案为10，误差0.1可接受9.9～10.1。","relative":"填写小数，例如0.01表示1%。"}
- 展示：按assessment_archetype === numeric_expression判断数值判分适用性，不按一般题型或答案外观判断。误差设置可展开；任何已有误差值均自动展示，并可清除。 位置：误差字段及其单位帮助。
- 保留边界：保持比例入参语义，不能只把label改百分比。；若要统一百分比需另做输入/输出换算与接口验证，本项不含。；非numeric_expression的历史误差须能查看和清除，否则发布校验仍会拒绝。不得只隐藏字段而保留用户无法发现的值。
- 验收：0.01提交仍为0.01；已有误差不被折叠隐藏到无法发现。；相同question_type下，数值判分方式可编辑误差，其他判分方式若有旧值会展示清除入口；未清除时不谎报可发布。

<a id="mc-r21"></a>

## MC-R21 · P1 · 题库：选用模板

- 角色与当前条件：题库教师；模板字段extra常驻，选中模板后再出现第二个info。
- 原文：“保存时复制评分点。模板后续更新不会更改本题。” [apps/web-admin/src/features/question-bank/ScoringEditor.tsx:279](../../apps/web-admin/src/features/question-bank/ScoringEditor.tsx#L279)；“选用模板将以该版本的评分点替换当前评分点；分值需一致。” [apps/web-admin/src/features/question-bank/ScoringEditor.tsx:295](../../apps/web-admin/src/features/question-bank/ScoringEditor.tsx#L295)
- 判断：同一动作的替换、时机、冻结后果分散两处，必须一起读才理解。
- 处理：merge / contextual / keep；依赖：frontend。
- 目标文案：{"summary":"保存时用此模板替换本题评分细则（评分点、扣分规则和样例答案）；满分须一致。模板后续更新不影响本题。"}
- 展示：未选模板不显示替换警告；选中后在保存动作前只显示一次完整后果。 位置：已选模板与保存评分标准之间。
- 保留边界：不得写成选择即已替换；实际复制在保存时发生。；模板版本和满分不一致的拒绝规则保留。；服务端复制整个Rubric，包含评分点、扣分规则、样例答案；不替换当前题目的标准答案和解析。
- 验收：选中、清除、保存、满分不一致四种场景文案与效果一致。；本题已有扣分规则和样例答案时，保存前能理解它们也将由模板替换；标准答案与解析不因此被替换。

<a id="mc-r22"></a>

## MC-R22 · P2 · 题库：版本比较重复标签

- 角色与当前条件：处理冲突的教师；18个固定比较项及评分点逐项渲染，行内反复标两列名称和相同/差异。
- 原文：“有差异，请核对” [apps/web-admin/src/features/question-bank/QuestionBankConflictComparison.tsx:120](../../apps/web-admin/src/features/question-bank/QuestionBankConflictComparison.tsx#L120)；“未配置” [apps/web-admin/src/features/question-bank/QuestionBankConflictComparison.tsx:51](../../apps/web-admin/src/features/question-bank/QuestionBankConflictComparison.tsx#L51)；“本地修改” [apps/web-admin/src/features/question-bank/QuestionBankConflictComparison.tsx:116](../../apps/web-admin/src/features/question-bank/QuestionBankConflictComparison.tsx#L116)；“最新版本” [apps/web-admin/src/features/question-bank/QuestionBankConflictComparison.tsx:116](../../apps/web-admin/src/features/question-bank/QuestionBankConflictComparison.tsx#L116)
- 判断：同列标题与空值重复；真正差异被大量相同项淹没。
- 处理：merge / progressive；依赖：frontend。
- 目标文案：{"summary":"{差异项数}项不同","same":"查看{相同项数}项相同内容","columns":"本地修改 / 最新版本"}
- 展示：列标题集中在表头；默认展示实际有差异项，相同内容可展开。 位置：版本比较弹窗。
- 保留边界：须先修复F01语义空值假差异，不能直接依赖当前fingerprint计数。；明确删除非空内容仍是差异；未知历史字段和全部原内容可展开核对。
- 验收：同一版本未修改时不虚报差异；真删除仍提示；展开可见完整内容。

<a id="mc-r23"></a>

## MC-R23 · P3 · 全局：空状态默认复述

- 角色与当前条件：各角色；EmptyState未传自定义description时，标题和默认说明同时出现。
- 原文：“暂无数据” [apps/web-admin/src/components/PageState.tsx:30](../../apps/web-admin/src/components/PageState.tsx#L30)；“暂时还没有相关记录。” [apps/web-admin/src/components/PageState.tsx:30](../../apps/web-admin/src/components/PageState.tsx#L30)
- 判断：默认说明重复标题，没有说明下一步；所有使用点都多出一行。
- 处理：delete / contextual；依赖：frontend。
- 目标文案：{"summary":"默认只展示空状态标题。","details":"调用方仅在有真实下一步或查询范围需要解释时传description。"}
- 展示：默认description改为可选，仅显式非空值时渲染。 位置：共享空状态组件。
- 保留边界：逐个核对调用方：筛选无结果、首次无数据、权限不足、加载失败不能混用。
- 验收：默认空状态不出现两句同义句；已有业务说明仍显示。

<a id="mc-r24"></a>

## MC-R24 · P2 · 助手：底部业务提醒

- 角色与当前条件：学校AI助手用户；对话输入区下方常驻。
- 原文：“业务回答以本轮来源为依据；正式操作请进入业务页面核实。” [apps/web-admin/src/features/school-assistant/AccountConversationView.tsx:53](../../apps/web-admin/src/features/school-assistant/AccountConversationView.tsx#L53)
- 判断：有必要提示正式操作边界，但‘以本轮来源为依据’像实现说明且非业务对话也出现。
- 处理：shorten / keep；依赖：copy。
- 目标文案：{"summary":"业务信息请核对来源，正式操作请进入对应页面。"}
- 展示：保留一处常驻短提醒；来源缺失、失败或范围不足须另在该条回复旁显示具体问题。 位置：输入区下方；具体异常靠近回复。
- 保留边界：不写成所有答案均已核验；不承诺助手已替用户发布或改分。
- 验收：撰写普通文本仍不过度警告；业务回答保留来源入口和操作边界。

<a id="mc-r25"></a>

## MC-R25 · P2 · 助手：文件上传规则

- 角色与当前条件：学校AI助手用户；目前规则仅在上传按钮Tooltip中。
- 原文：“可直接粘贴 PNG、JPEG 图片，每张不超过 3 MB；支持 PDF 和文本，共最多 3 个附件” [apps/web-admin/src/features/school-assistant/AccountConversationView.tsx:49](../../apps/web-admin/src/features/school-assistant/AccountConversationView.tsx#L49)
- 判断：长规则隐藏在悬浮层，触屏用户和键盘用户可能到失败才知道限制。
- 处理：shorten / contextual / keep；依赖：frontend。
- 目标文案：{"summary":"支持图片、PDF和文本，最多3个附件。","images":"PNG/JPEG图片每张不超过3MB，可直接粘贴。"}
- 展示：选择附件入口或有附件时显示短规则；具体类型/大小失败在附件旁说明，不只靠Tooltip。 位置：附件选择区和逐附件错误。
- 保留边界：3MB仅原文明确约束图片，不可擅自扩大为所有文件统一上限。；Tooltip若保留需键盘可触发、可关闭并在指针移入时保持。
- 验收：触屏不悬停也能知道数量限制；超图片大小有准确提示。

<a id="mc-r26"></a>

## MC-R26 · P2 · 助手：数据来源和查询进度

- 角色与当前条件：学校AI助手用户；每条有业务证据的回复；来源详情当前已默认折叠，查询工具状态逐项可见。
- 原文：“本轮数据来源（” [apps/web-admin/src/features/school-assistant/ConversationEvidenceView.tsx:31](../../apps/web-admin/src/features/school-assistant/ConversationEvidenceView.tsx#L31)；“工作区版本 ” [apps/web-admin/src/features/school-assistant/ConversationEvidenceView.tsx:36](../../apps/web-admin/src/features/school-assistant/ConversationEvidenceView.tsx#L36)；“生效发布版本 v” [apps/web-admin/src/features/school-assistant/ConversationEvidenceView.tsx:37](../../apps/web-admin/src/features/school-assistant/ConversationEvidenceView.tsx#L37)
- 判断：来源折叠已经合理；可减少成功查询过程残留，但失败/部分可用不能被收进折叠后失去风险提示。
- 处理：keep / contextual / merge；依赖：frontend。
- 目标文案：{"summary":"数据来源（{数量}）","success":"已查询{数量}项资料","failure":"部分资料未能读取，请核对回答范围。"}
- 展示：运行中保留真实进度；全部完成后汇总成功项，异常摘要常显；版本与时间仍留来源详情。 位置：回复旁状态及现有来源折叠。
- 保留边界：不把unavailable/forbidden/partial改成无记录。；工作区修订和已发布版本含义不同，不合并数值。
- 验收：成功查询不铺满过程标签；任一失败或部分可用时摘要提醒可见；详情可追溯。

<a id="mc-r27"></a>

## MC-R27 · P2 · 助手：每条网页引用

- 角色与当前条件：阅读助手回复的用户；模型正文有有效HTTP(S)地址时，引用文字、按钮、完整URL一起渲染。
- 原文：“复制参考地址” [apps/web-admin/src/components/mathMarkdownRenderers.tsx:46](../../apps/web-admin/src/components/mathMarkdownRenderers.tsx#L46)；“复制失败，请手动复制下方地址” [apps/web-admin/src/components/mathMarkdownRenderers.tsx:45](../../apps/web-admin/src/components/mathMarkdownRenderers.tsx#L45)
- 判断：多来源回复出现重复按钮与长地址，打断阅读。
- 处理：progressive / merge；依赖：frontend。
- 目标文案：{"summary":"参考资料（{数量}）","entry":"{引用文字} · {域名}","action":"复制地址","failure":"复制失败，请手动复制此地址。"}
- 展示：正文保留短来源标记；资料区展示引用文字和域名，完整地址可展开及复制。 位置：每条回复的参考资料区。
- 保留边界：保持模型正文不能直接导航或发起外部访问的限制。；复制失败时地址必须仍可选择；不要把未经核验的引用文字承诺为真实网页标题。
- 验收：多个链接可区分；键盘可查看和复制；恶意协议不变成可执行链接。

<a id="mc-r28"></a>

## MC-R28 · P1 · 全局：待确认操作与原内容

- 角色与当前条件：发生网络中断的阅卷/教务用户；存在当前账号的待确认命令；点击查询回执后按状态显示。
- 原文：“原请求可继续提交” [apps/web-admin/src/components/PendingBusinessCommands.tsx:169](../../apps/web-admin/src/components/PendingBusinessCommands.tsx#L169)；“按以上原内容继续提交” [apps/web-admin/src/components/PendingBusinessCommands.tsx:176](../../apps/web-admin/src/components/PendingBusinessCommands.tsx#L176)；“原提交内容：” [apps/web-admin/src/components/PendingBusinessCommands.tsx:175](../../apps/web-admin/src/components/PendingBusinessCommands.tsx#L175)；“服务器未接收，请返回业务页面重新提交” [apps/web-admin/src/components/PendingBusinessCommands.tsx:169](../../apps/web-admin/src/components/PendingBusinessCommands.tsx#L169)
- 判断：‘原请求’偏技术，但原分数/原因/版本是继续提交的决策依据，不能折叠成一个盲点按钮。
- 处理：shorten / keep / contextual；依赖：copy。
- 目标文案：{"summary":"提交结果待确认","processing":"正在处理，请稍后查看结果。","takeover":"可以继续提交此前内容。","action":"按原内容继续提交","notAccepted":"此次提交未被接收，请返回原页面重新提交。"}
- 展示：沿用真实回执状态；只有takeover_ready且含payload时显示续交按钮，原内容始终紧邻。 位置：待确认操作摘要、原内容与对应按钮。
- 保留边界：本条不增加自动回执查询；原有手动查询行为保持。；不得把unknown写成失败，也不得自动重新提交或隐藏冻结分数/原因/版本。
- 验收：处理中、未知、未接收、拒绝、可续交、已完成六类结果可区分；继续提交前可核对原内容。
