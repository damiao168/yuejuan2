# 阅卷、试评、质量与成绩发布辅助小字方案

2026-10-28。共 28 项：17 项纯文案，11 项必须配套前端条件或布局调整；无新增后端能力要求。

仅静态复核当前源码、组件调用及已有状态条件；未运行交互或接口测试，未修改业务代码。evidence.line 为当前源文件的一基行号，literal 为完整连续原文，template 为源码动态模板，excerpt 为明确截取。

本报告只给出方案，不修改业务代码。原文已逐条匹配当前源文件；验收场景尚未执行。角色权限、双评独立性、正式提交、方案冻结、发布再次检查、匿名材料遮挡、版本更正和未提交内容丢失等边界必须保留。

前端配套项：MC-G01、MC-G02、MC-G04、MC-G07、MC-G10、MC-G13、MC-G16、MC-G22、MC-G24、MC-G27、MC-G28。不能仅替换字符串后即视为完成。

<a id="mc-g01"></a>

## MC-G01 · 阅卷设置 / 本场大题 AI 方案（P2）

角色：学校管理员及只读教师。处理：合并 / 按条件显示。依赖：前端交互/条件配套。

原文与定位：

- [apps/web-admin/src/features/grading/workbench/components/GradingAIPlanSettings.tsx:122](D:/project/yuejuan/apps/web-admin/src/features/grading/workbench/components/GradingAIPlanSettings.tsx:122)（literal）：“学校管理员在阅卷前确认。仅用于解答、计算、证明等大题；选择题和填空题不使用。只有一个独立模型也可确认单模型方案。”
- [apps/web-admin/src/features/grading/workbench/components/GradingAIPlanSettings.tsx:22](D:/project/yuejuan/apps/web-admin/src/features/grading/workbench/components/GradingAIPlanSettings.tsx:22)（literal）：“仅用于解答、计算、证明等大题；选择题和填空题不使用本方案。”

当前条件：进入 AI 方案设置且已有已保存方案时，页面导语和 GradingAIPlanDescription 同时解释适用题型；后者还复用于启动评分摘要。

判断：设置页同屏重复解释适用范围；但启动摘要的范围提示仍独立必要。

替换文案：

- 主文案：仅用于解答、计算、证明题；选择题和填空题不使用。可确认单模型方案。
- 补充说明：设置页仅在顶部保留此句；已保存方案卡内省略范围句。启动评分弹窗独立展示范围句。

目标条件：设置页显示一次；启动评分弹窗保留自己的适用范围。

位置：方案设置标题下；启动摘要首行。

保留边界：

- 不扩大支持题型；不把单模型误写成必须三模型。
- 学校管理员确认权限和确认动作保持不变。

验收场景（待实施后执行）：

- 已有方案时，设置页适用范围只出现一次。
- 从启动评分弹窗进入时仍能看到不适用于选择题、填空题。

<a id="mc-g02"></a>

## MC-G02 · 阅卷设置 / 分差规则（P2）

角色：学校管理员。处理：按条件显示 / 渐进展开 / 缩短。依赖：前端交互/条件配套。

原文与定位：

- [apps/web-admin/src/features/grading/workbench/components/GradingAIPlanSettings.tsx:142](D:/project/yuejuan/apps/web-admin/src/features/grading/workbench/components/GradingAIPlanSettings.tsx:142)（literal）：“两个允许分差均须手动填写，可填 0 分；三评后允许分差不得小于前两模型允许分差。”
- [apps/web-admin/src/features/grading/workbench/components/GradingAIPlanSettings.tsx:142](D:/project/yuejuan/apps/web-admin/src/features/grading/workbench/components/GradingAIPlanSettings.tsx:142)（literal）：“当前不足两个独立评分模型，可直接确认单模型方案，不需要填写分差。”
- [apps/web-admin/src/features/grading/workbench/components/GradingAIPlanSettings.tsx:143](D:/project/yuejuan/apps/web-admin/src/features/grading/workbench/components/GradingAIPlanSettings.tsx:143)（literal）：“A、B 差值不超过第一项时取均分，超过才启用第三评分；没有第三模型则转教师处理。按未取整分比较，三评后 C 与 A、B 均分的差值超过第二项，或三者极差超过第二项的两倍，才作为分差异常转教师处理。作答不清或模型调用失败，也不直接计零分。”
- [apps/web-admin/src/features/grading/workbench/components/GradingAIPlanSettings.tsx:144](D:/project/yuejuan/apps/web-admin/src/features/grading/workbench/components/GradingAIPlanSettings.tsx:144)（literal）：“若设置 2 分，差 2 分不调用第三模型，超过 2 分才调用。”

当前条件：规则 details 默认 open；无论当前选一个、两个或三个独立模型，后两段都出现。

判断：单模型用户仍读三评算法；双模型用户读不存在的第三评分分支。长算法与已保存方案说明重复。

替换文案：

- 主文案：按所选模型显示当前规则。
- 单模型：当前为单模型，无需填写分差。建议分仍需教师确认。
- 双模型：两个分差均必填，可填 0；第二项不得小于第一项。A、B 分差在第一项以内时取均分，超过则转教师处理。
- 三模型：两个分差均必填，可填 0；第二项不得小于第一项。A、B 分差超过第一项时才调用第三模型。
- 补充说明：“计算规则”展开内容：按未取整分比较；相差恰好等于第一项时不触发第三评分。三评按 A 25%、B 25%、C 50% 汇总，最后向上取整且不超过满分。C 与 A、B 均分的差值超过第二项，或三者极差超过第二项的两倍时，转教师处理。作答不清或模型调用失败，不直接计零分。仅展示当前所选模式适用的计算段落；已保存方案与编辑中方案分别标明，不能混用。

目标条件：跟随编辑中的独立模型数量、第三模型是否存在切换；计算规则保留可展开入口。

位置：字段旁保留必填与大小约束；算法放“计算规则”。

保留边界：

- 当前代码在存在两个独立模型时仍要求两个分差，不能仅靠隐藏第二字段取消后端/保存约束。
- 保留严格超过、未取整比较、权重、最终取整和不计零分的业务边界。
- 不改变已启动任务冻结方案。

验收场景（待实施后执行）：

- 单模型页面不出现当前不适用的三评解释。
- 双模型仍能填写并校验现有两项必填值。
- 阈值为 0 或 2 时，展开规则明确等于阈值不会触发。

<a id="mc-g03"></a>

## MC-G03 · 阅卷设置 / 图片能力检测（P2）

角色：学校管理员。处理：缩短 / 保留。依赖：纯文案。

原文与定位：

- [apps/web-admin/src/features/grading/workbench/components/GradingAIPlanSettings.tsx:135](D:/project/yuejuan/apps/web-admin/src/features/grading/workbench/components/GradingAIPlanSettings.tsx:135)（literal）：“点击检测可能产生一次模型调用；已有同配置版本的检测可复用。保存方案不会自动调用模型。”

当前条件：选定模型的检测列表下方固定出现。

判断：费用/调用提醒必要，原句可缩短但不能完全删掉或移到远离检测按钮的位置。

替换文案：

- 主文案：检测可能调用模型；同配置的检测结果可复用。保存方案不会调用模型。

目标条件：有至少一个已选模型和检测按钮时显示。

位置：检测按钮组下方一行。

保留边界：

- 不承诺检测免费、不承诺每次都缓存命中。
- 失败原因仍在对应模型旁。

验收场景（待实施后执行）：

- 点击检测前可读到可能调用模型。
- 保存方案按钮旁不出现“保存会自动检测”的误导。

<a id="mc-g04"></a>

## MC-G04 · 阅卷设置 / 只读方案（P2）

角色：无配置权限的教师、管理员。处理：按条件显示 / 合并。依赖：前端交互/条件配套。

原文与定位：

- [apps/web-admin/src/features/grading/workbench/components/GradingAIPlanSettings.tsx:128](D:/project/yuejuan/apps/web-admin/src/features/grading/workbench/components/GradingAIPlanSettings.tsx:128)（literal）：“请学校管理员确认本场大题 AI 评分方案。”
- [apps/web-admin/src/features/grading/workbench/components/GradingAIPlanSettings.tsx:28](D:/project/yuejuan/apps/web-admin/src/features/grading/workbench/components/GradingAIPlanSettings.tsx:28)（literal）：“需要学校管理员先确认本场大题方案；只有一个模型也可确认单模型。”

当前条件：can_configure=false 时显示第一句；无论 confirmed_at 是否存在都要求确认。未确认摘要另有相同要求。

判断：已确认的只读方案也催促联系管理员确认，混淆权限限制与尚未完成设置。

替换文案：

- 主文案：按已确认状态显示不同提示。
- 已确认：方案已确认。如需修改，请联系学校管理员。
- 未确认：方案尚未确认，请联系学校管理员。

目标条件：仅 can_configure=false；按 confirmed_at 分支，且与摘要中的未确认提示合并为一次。

位置：只读方案底部。

保留边界：

- 不把只读角色变成配置角色。
- 未确认时仍清楚阻止把方案视为可用。

验收场景（待实施后执行）：

- 已确认+只读时不再出现“请确认”。
- 未确认+只读仍提示具体联系角色。

<a id="mc-g05"></a>

## MC-G05 · 阅卷设置 / 保存方案按钮（P1）

角色：学校管理员。处理：缩短 / 保留。依赖：纯文案。

原文与定位：

- [apps/web-admin/src/features/grading/workbench/components/GradingAIPlanSettings.tsx:148](D:/project/yuejuan/apps/web-admin/src/features/grading/workbench/components/GradingAIPlanSettings.tsx:148)（literal）：“保存用于之后启动的评分，已启动任务使用其冻结方案。”
- [apps/web-admin/src/features/grading/workbench/components/GradingAIPlanSettings.tsx:145](D:/project/yuejuan/apps/web-admin/src/features/grading/workbench/components/GradingAIPlanSettings.tsx:145)（literal）：“建议分仍需按本场既有规则由教师确认。”

当前条件：可配置分支中，确认按钮上下各有一行。

判断：两项均是必要作用范围提示；“冻结方案”偏实现术语，能直接说明对已有任务的影响。

替换文案：

- 主文案：新方案仅用于之后启动的评分；已启动任务仍用原方案。
- 补充说明：AI 建议分仍需教师确认。

目标条件：可保存方案时保留第一句，存在 AI 建议分时保留第二句。

位置：确认按钮下方保留作用范围；建议分说明贴近方案摘要。

保留边界：

- 不暗示保存会重跑已有任务。
- 不暗示确认 AI 方案等同确认最终分。

验收场景（待实施后执行）：

- 修改并保存方案前，能判断已有任务不会切换。
- 单模型、多模型均保留人工确认最终分的边界。

<a id="mc-g06"></a>

## MC-G06 · 启动评分 / 评分路线确认（P1）

角色：成绩管理员。处理：缩短 / 按条件显示。依赖：纯文案。

原文与定位：

- [apps/web-admin/src/features/grading/workbench/components/ExamScoringDialogs.tsx:23](D:/project/yuejuan/apps/web-admin/src/features/grading/workbench/components/ExamScoringDialogs.tsx:23)（literal）：“系统将自动完成作答识别、规则判分和 AI 建议生成。AI 建议由教师确认最终分数，处理过程中可关闭页面。”

当前条件：scoring.plan 存在的评分路线确认弹窗；下方同时列规则、AI、人工数量和单题路线。

判断：导语概括成全自动完成，和实际人工路线或冻结策略不可读后降人工路线的摘要不一致。

替换文案：

- 主文案：将按以下路线处理作答。AI 建议分仍需教师确认；启动后可关闭页面。

目标条件：路线确认弹窗中固定显示；人工路线数量、题目注记和降级警告继续按原条件展示。

位置：弹窗导语，紧邻路线汇总。

保留边界：

- 保留启动确认，不承诺所有步骤成功。
- 不删除冻结策略不可读时按人工路线处理的警告。
- “启动后”指启动请求成功后，不表示仅打开弹窗就已经启动。

验收场景（待实施后执行）：

- 包含人工题的计划不会被描述为全自动完成。
- 启动失败时仍留在弹窗并看到错误，不显示已启动成功文案。

<a id="mc-g07"></a>

## MC-G07 · 阅卷台 / 最终分录入（P1）

角色：阅卷教师。处理：渐进展开 / 按条件显示 / 保留。依赖：前端交互/条件配套。

原文与定位：

- [apps/web-admin/src/features/grading/workbench/components/ScoreEditor.tsx:73](D:/project/yuejuan/apps/web-admin/src/features/grading/workbench/components/ScoreEditor.tsx:73)（literal）：“填分后自动提交；标记的试卷只保存草稿，取消标记后恢复自动提交。关闭快捷键后仍可使用页面按钮。”
- [apps/web-admin/src/features/grading/workbench/components/ScoreEditor.tsx:73](D:/project/yuejuan/apps/web-admin/src/features/grading/workbench/components/ScoreEditor.tsx:73)（literal）：“录分：G 聚焦并选中最终分，可输入 0、多位数或小数；1–9 直接打分（不超过满分）。A 采纳建议分。”
- [apps/web-admin/src/features/grading/workbench/hooks/useAutomaticGradeSubmission.ts:33](D:/project/yuejuan/apps/web-admin/src/features/grading/workbench/hooks/useAutomaticGradeSubmission.ts:33)（excerpt）：“if (!ready || marked || !edit || edit.taskId !== taskId) return;”

当前条件：自动交分后果目前位于默认折叠的“键盘帮助”内；该行为也作用于鼠标输入。

判断：正式交分的后果不应依赖用户打开快捷键说明；同时不能把正在保存或离线写成已保存。

替换文案：

- 主文案：将交分后果移到最终分输入框旁。
- 未标记：本次修改有效得分后将自动提交；如需稍后核对，请先标记答卷。
- 已标记：已标记：当前仅编辑草稿；取消标记后恢复自动提交。
- 补充说明：键盘帮助仅保留：G 定位最终分；1–9 录分；A 采纳建议分。F 标记；R 退回重评；Z 撤销未提交修改；+/- 缩放。输入框内不会触发单字符操作。

目标条件：仅当前本人可编辑任务显示；根据已确认的标记状态切换。只读记录不出现自动交分提示。

位置：最终分输入框下方常驻一行；操作键说明仍折叠。

保留边界：

- 需要向 ScoreEditor 传入真实标记状态，不根据点击意图提前切换提示。
- 现有网络、合法分值、任务可编辑、保存状态等提交条件保持不变。
- 仅本次打开后主动修改得分或采分点才产生交分意图；恢复历史草稿、仅查看或只改说明不触发交分。
- 不增加自动保存能力；不把当前输入描述成已持久化。
- 不能把盲评的 AI 或第二意见内容加入帮助。

验收场景（待实施后执行）：

- 未展开键盘帮助也能理解录分会交分。
- 恢复历史草稿但未主动编辑时不自动交分；本次修改有效得分后，仅满足原提交条件时自动交分。
- 标记失败时仍显示原状态，不宣称已暂停交分。
- 离线、保存失败和只读任务继续显示各自状态。

<a id="mc-g08"></a>

## MC-G08 · 阅卷台 / 草稿版本冲突（P1）

角色：阅卷教师。处理：缩短 / 保留。依赖：纯文案。

原文与定位：

- [apps/web-admin/src/features/grading/workbench/components/WorkbenchHeader.tsx:53](D:/project/yuejuan/apps/web-admin/src/features/grading/workbench/components/WorkbenchHeader.tsx:53)（literal）：“草稿已被其他会话更新”
- [apps/web-admin/src/features/grading/workbench/components/WorkbenchHeader.tsx:53](D:/project/yuejuan/apps/web-admin/src/features/grading/workbench/components/WorkbenchHeader.tsx:53)（literal）：“为防止覆盖其他会话的修改，自动保存已暂停。可逐项选择本页或服务端内容，再按服务端新版本重新保存。”

当前条件：draftSaveStatus=conflict，显示冲突 Alert 及“逐项合并 / 放弃本页修改”。

判断：暂停保存和选择后果必要；并发版本实现的长描述可让位于当前选择。

替换文案：

- 主文案：草稿已被其他会话更新
- 补充说明：自动保存已暂停。请逐项选择要保留的内容，或放弃本页修改。

目标条件：保持现有冲突条件，解决前持续显示。

位置：原冲突 Alert；保持两个解决入口。

保留边界：

- 不能删除“自动保存已暂停”。
- 不暗示选择合并前已有新内容保存成功。
- 不删弃本页修改的后果确认。

验收场景（待实施后执行）：

- 两处同时编辑造成冲突时，用户能知道当前未继续保存。
- 完成合并或放弃后，提示随实际保存状态解除。

<a id="mc-g09"></a>

## MC-G09 · 阅卷任务抽屉 / 筛选范围（P2）

角色：阅卷教师。处理：缩短 / 保留。依赖：纯文案。

原文与定位：

- [apps/web-admin/src/features/grading/workbench/components/TaskRail.tsx:174](D:/project/yuejuan/apps/web-admin/src/features/grading/workbench/components/TaskRail.tsx:174)（literal）：“考试与题目选项来自已加载的授权任务；也可搜索考试名称。”

当前条件：任务抽屉底部，紧随已加载/当前显示/全部匹配统计。

判断：已加载范围提醒真实必要，但授权细节与搜索功能可缩短；不能删成全量下拉的暗示。

替换文案：

- 主文案：筛选选项仅含已加载任务；可搜索考试名称。

目标条件：保留当前显示条件；不根据未核实的全量完成状态自动隐藏。

位置：任务计数下方一行。

保留边界：

- 不宣称考试/题目下拉覆盖全部任务。
- 不暗示搜索会获得未授权内容。

验收场景（待实施后执行）：

- 仍有后续分页时，用户知道下拉范围有限。
- 零匹配时仍可识别为当前筛选结果，而非无任何授权任务。

<a id="mc-g10"></a>

## MC-G10 · 阅卷前试评 / 顶部引导（P2）

角色：试评教师、质量管理员。处理：合并 / 按条件显示。依赖：前端交互/条件配套。

原文与定位：

- [apps/web-admin/src/features/calibration/CalibrationDrawer.tsx:250](D:/project/yuejuan/apps/web-admin/src/features/calibration/CalibrationDrawer.tsx:250)（literal）：“用已批准的标准卷练习评分，检查你的给分与参考标准是否一致。”
- [apps/web-admin/src/features/calibration/CalibrationDrawer.tsx:250](D:/project/yuejuan/apps/web-admin/src/features/calibration/CalibrationDrawer.tsx:250)（literal）：“管理员准备标准卷和通过标准”
- [apps/web-admin/src/features/calibration/CalibrationDrawer.tsx:250](D:/project/yuejuan/apps/web-admin/src/features/calibration/CalibrationDrawer.tsx:250)（literal）：“阅卷老师独立评分”
- [apps/web-admin/src/features/calibration/CalibrationDrawer.tsx:250](D:/project/yuejuan/apps/web-admin/src/features/calibration/CalibrationDrawer.tsx:250)（literal）：“提交后查看分差，全部完成后判定是否达标”
- [apps/web-admin/src/features/calibration/CalibrationDrawer.tsx:44](D:/project/yuejuan/apps/web-admin/src/features/calibration/CalibrationDrawer.tsx:44)（literal）：“独立给标准卷打分，系统会与参考分比较。每份提交后才显示参考分和相差分数。”

当前条件：抽屉顶部同时展示目的段落、三步列表、资格 Alert 的独立评分说明；教师也看到管理员准备步骤。

判断：同一任务连续解释三遍；教师需要知道自己如何开始，以及提交前不能看参考分。

替换文案：

- 主文案：独立完成全部样本；每份提交后才显示参考分，完成后判定是否通过。
- 管理端：请先配置通过标准，并准备足量的已批准标准卷。

目标条件：教师显示 summary；canManagePolicy 分支在配置区域显示 admin；已通过状态由资格卡接管，不继续重复入门三步。

位置：抽屉标题下只保留一段；管理员准备要求放配置区域。

保留边界：

- 保留独立评分与提交后才见参考分。
- 不能把标准卷准备步骤从管理员配置场景删除。
- 不把试评练习分直接当正式任务分。

验收场景（待实施后执行）：

- 教师打开抽屉只读到自己的下一步。
- 未提交当前样本时仍不能看到参考分。
- 管理员仍能明确样本必须已批准。

<a id="mc-g11"></a>

## MC-G11 · 阅卷前试评 / 已获资格提示（P1）

角色：试评教师。处理：缩短 / 保留。依赖：纯文案。

原文与定位：

- [apps/web-admin/src/features/calibration/CalibrationDrawer.tsx:43](D:/project/yuejuan/apps/web-admin/src/features/calibration/CalibrationDrawer.tsx:43)（literal）：“本题试评已通过，已具备阅卷资格”
- [apps/web-admin/src/features/calibration/CalibrationDrawer.tsx:44](D:/project/yuejuan/apps/web-admin/src/features/calibration/CalibrationDrawer.tsx:44)（template）：“本题资格有效至 ${new Date(qualification.valid_until).toLocaleDateString()}；标准卷变化后需重新确认资格。”

当前条件：qualification.status=qualified；资格状态 Alert。

判断：有效期与标准卷变化后的资格重核属于必要边界，应保留，但标题与描述可少重复本题/资格。

替换文案：

- 主文案：试评已通过
- 补充说明：有效至 {日期}；标准卷变化后需重新确认资格。

目标条件：只在资格接口实际返回 qualified 时显示，日期采用原 valid_until。

位置：资格状态卡；紧邻开始/领取相关操作。

保留边界：

- 不能把考试范围、题目范围或资格有效期省略成永久通过。
- 不承诺标准卷变化后自动恢复资格。

验收场景（待实施后执行）：

- 过期、未通过与通过三种状态不共用成功文案。
- 标准卷更新后按真实接口状态显示需重新确认。

<a id="mc-g12"></a>

## MC-G12 · 阅卷前试评 / 通过标准字段说明（P2）

角色：质量管理员。处理：缩短 / 保留。依赖：纯文案。

原文与定位：

- [apps/web-admin/src/features/calibration/CalibrationDrawer.tsx:256](D:/project/yuejuan/apps/web-admin/src/features/calibration/CalibrationDrawer.tsx:256)（literal）：“各项要求需同时达标。题型和满分须与标准卷一致；请先在“标准卷”中准备足量且已批准的样本。”
- [apps/web-admin/src/features/calibration/CalibrationDrawer.tsx:262](D:/project/yuejuan/apps/web-admin/src/features/calibration/CalibrationDrawer.tsx:262)（literal）：“每份与参考分相差的绝对值，再取平均”
- [apps/web-admin/src/features/calibration/CalibrationDrawer.tsx:263](D:/project/yuejuan/apps/web-admin/src/features/calibration/CalibrationDrawer.tsx:263)（literal）：“例如 60%：至少六成样本与参考分相同”
- [apps/web-admin/src/features/calibration/CalibrationDrawer.tsx:265](D:/project/yuejuan/apps/web-admin/src/features/calibration/CalibrationDrawer.tsx:265)（literal）：“设置后须有可比较的采分点数据”
- [apps/web-admin/src/features/calibration/CalibrationDrawer.tsx:266](D:/project/yuejuan/apps/web-admin/src/features/calibration/CalibrationDrawer.tsx:266)（literal）：“0% 表示不允许出现严重分差”
- [apps/web-admin/src/features/calibration/CalibrationDrawer.tsx:267](D:/project/yuejuan/apps/web-admin/src/features/calibration/CalibrationDrawer.tsx:267)（literal）：“达到此分差即计为严重”

当前条件：canManagePolicy 的通过标准设置 details 展开后，配置导语与字段 extra 同时显示；它已是高级折叠区域。

判断：平均绝对差、零比例和阈值含等号是有效解释，不能因“小字多”全删。仅收短重复示例和准备说明。

替换文案：

- 主文案：所有已设置条件均须达标。标准卷须已批准，且题型、满分一致。
- 平均分差上限：各样本与参考分的绝对差，再取平均。
- 分数完全相同的最低比例：例如 60% 表示至少六成样本同分。
- 采分点判断相同的最低比例：须有可比较的采分点数据。
- 严重分差的最高比例：0% 表示不允许严重分差。
- 相差几分算严重分差：相差达到此值即计入（含等于）。

目标条件：保持现有管理员及 details 展开条件；高风险阈值解释仍贴在字段旁。

位置：配置区域导语、对应 Form.Item extra。

保留边界：

- 不能将平均绝对差误写成带正负的平均差。
- 不能将“达到”改成“超过”。
- 不改阈值范围、同时达标规则和样本要求。

验收场景（待实施后执行）：

- 相差恰好等于严重阈值时，文案预期与现有判断一致。
- 0% 条件能被理解为一个严重分差都不允许。

<a id="mc-g13"></a>

## MC-G13 · 阅卷前试评 / 未读到通过标准（P1）

角色：试评教师、质量管理员。处理：按条件显示 / 缩短。依赖：前端交互/条件配套。

原文与定位：

- [apps/web-admin/src/features/calibration/CalibrationDrawer.tsx:279](D:/project/yuejuan/apps/web-admin/src/features/calibration/CalibrationDrawer.tsx:279)（literal）：“尚未设置本题通过标准。请管理员展开上方设置并保存，同时在“标准卷”中准备已批准的样本。”

当前条件：!session 且 !policy 时显示；教师可能没有上方管理设置；加载失败也可能得到空 policy。

判断：“尚未设置”把未读到数据当成确定未配置，并引导教师找一个无权限看到的上方区域。

替换文案：

- 主文案：暂未读到本题通过标准，当前不能开始试评。请联系管理员核对。
- 管理端：暂未读到本题通过标准。请核对并保存标准，同时准备已批准的标准卷。

目标条件：教师使用 summary；有配置权限时使用 admin；现有错误 Alert 保留且优先显示真实失败。

位置：原开始试评区域，替换指向不存在上方设置的段落。

保留边界：

- 不宣称服务端必然没有设置。
- 不放宽无通过标准时不能开始的门禁。
- 不新增“自动重试/已自动读取成功”承诺。

验收场景（待实施后执行）：

- 无管理权限的教师不会被要求展开上方设置。
- 读取失败时错误 Alert 和空状态不会互相声称失败/未配置已确定。

<a id="mc-g14"></a>

## MC-G14 · 成绩检查 / 检查通过状态（P1）

角色：成绩管理员、质量管理员。处理：缩短 / 按条件显示 / 保留。依赖：纯文案。

原文与定位：

- [apps/web-admin/src/features/score-management/components/QualityPanel.tsx:28](D:/project/yuejuan/apps/web-admin/src/features/score-management/components/QualityPanel.tsx:28)（literal）：“成绩已发布，质量校验通过”
- [apps/web-admin/src/features/score-management/components/QualityPanel.tsx:29](D:/project/yuejuan/apps/web-admin/src/features/score-management/components/QualityPanel.tsx:29)（literal）：“成绩已发布并锁定，所有检查项均已处理。”
- [apps/web-admin/src/features/score-management/components/QualityPanel.tsx:29](D:/project/yuejuan/apps/web-admin/src/features/score-management/components/QualityPanel.tsx:29)（literal）：“所有检查项均已通过，可以发布成绩。”
- [apps/web-admin/src/pages/QualityDashboardViews.tsx:122](D:/project/yuejuan/apps/web-admin/src/pages/QualityDashboardViews.tsx:122)（template）：“发布可行性：${gateLabel(data.gate)}”
- [apps/web-admin/src/pages/QualityDashboardViews.tsx:26](D:/project/yuejuan/apps/web-admin/src/pages/QualityDashboardViews.tsx:26)（literal）：“当前没有阻断性质量事件。”
- [apps/web-admin/src/pages/ScoreManagementPage.tsx:222](D:/project/yuejuan/apps/web-admin/src/pages/ScoreManagementPage.tsx:222)（excerpt）：“const publishedOrLocked = selectedExam?.status === "published" || allGradesLocked;”

当前条件：QualityPanel 使用 publishedOrLocked 分支；它代表基础成绩发布或锁定，不等价于学生门户版本已发布。质量总览根据质量事件给出 gateLabel。

判断：局部检查成功被写成总发布结论，可能掩盖门户版本门禁、待发布状态或其他检查。

替换文案：

- 主文案：基础成绩检查通过
- 基础成绩已锁定：基础成绩已锁定；本项检查通过。门户发布状态见版本区域。
- 基础成绩未锁定：本项检查通过；请继续核对门户发布检查。
- 质量总览标题：评分质量检查
- 质量总览通过描述：未发现阻断性质量事件；其他发布条件以成绩发布检查为准。

目标条件：仍依据各自已有数据渲染；不通过局部质量状态推导门户 published。

位置：QualityPanel 成功状态；质量总览 Alert。

保留边界：

- 不删除实际阻断项或放宽发布门禁。
- 基础成绩锁定与门户版本状态保持独立。
- 不把没有质量事件描述成所有发布条件已满足。

验收场景（待实施后执行）：

- 基础成绩已锁定但无门户版本时，不出现“成绩已发布”。
- 质量事件通过但匿名材料缺失时，用户仍看到发布未就绪。

<a id="mc-g15"></a>

## MC-G15 · 成绩工作台 / 发布流程导语（P2）

角色：成绩管理员。处理：合并 / 缩短。依赖：纯文案。

原文与定位：

- [apps/web-admin/src/features/score-management/components/ScoreReleaseWorkspace.tsx:92](D:/project/yuejuan/apps/web-admin/src/features/score-management/components/ScoreReleaseWorkspace.tsx:92)（literal）：“推荐流程：汇总并确认成绩 → 创建门户草稿 → 核对学生视角 → 发布到学生门户。创建草稿不会改变学生所见。”
- [apps/web-admin/src/features/score-management/components/ScoreActionsPanel.tsx:36](D:/project/yuejuan/apps/web-admin/src/features/score-management/components/ScoreActionsPanel.tsx:36)（literal）：“先汇总、确认，再到版本区域预览并发布到学生门户”

当前条件：页面发布工作区和基础操作栏同时重复推荐流程。

判断：横跨基础成绩与门户版本的流程说明必要，但可只保留一个常驻入口，不应在相邻卡片重复。

替换文案：

- 主文案：汇总并确认 → 创建门户草稿 → 核对学生预览 → 发布。创建草稿不会公开给学生。
- 基础操作栏：汇总并确认基础成绩。

目标条件：工作区保留总流程；基础操作栏只解释本栏职责，不自动代办任一步。

位置：发布工作区标题下、基础操作栏标题下。

保留边界：

- 保留草稿不公开、预览和正式发布的区分。
- 不把学校原查询入口和学生门户合成一个状态。

验收场景（待实施后执行）：

- 正常页面只有一段完整四步流程。
- 首次管理员仍能知道创建草稿后还有正式发布。

<a id="mc-g16"></a>

## MC-G16 · 成绩工作台 / 发布门禁结果（P2）

角色：成绩管理员。处理：缩短 / 渐进展开 / 保留。依赖：前端交互/条件配套。

原文与定位：

- [apps/web-admin/src/features/score-management/components/ScoreReleaseWorkspace.tsx:101](D:/project/yuejuan/apps/web-admin/src/features/score-management/components/ScoreReleaseWorkspace.tsx:101)（literal）：“等待发布门禁数据”
- [apps/web-admin/src/features/score-management/components/ScoreReleaseWorkspace.tsx:101](D:/project/yuejuan/apps/web-admin/src/features/score-management/components/ScoreReleaseWorkspace.tsx:101)（literal）：“门禁结果读取完成后才能发布版本。”
- [apps/web-admin/src/features/score-management/components/ScoreReleaseWorkspace.tsx:116](D:/project/yuejuan/apps/web-admin/src/features/score-management/components/ScoreReleaseWorkspace.tsx:116)（literal）：“当前发布门禁通过”
- [apps/web-admin/src/features/score-management/components/ScoreReleaseWorkspace.tsx:116](D:/project/yuejuan/apps/web-admin/src/features/score-management/components/ScoreReleaseWorkspace.tsx:116)（literal）：“创建草稿后，仍会在实际发布时再次核验，避免状态变化后误发布。”
- [apps/web-admin/src/features/score-management/components/ScoreReleaseWorkspace.tsx:109](D:/project/yuejuan/apps/web-admin/src/features/score-management/components/ScoreReleaseWorkspace.tsx:109)（excerpt）：“{issue.count} 项 · {issue.code}”

当前条件：门禁尚未读取时显示等待 Alert；通过时显示成功 Alert；问题行还直接显示 issue.code。

判断：“门禁”偏内部术语，技术错误码和整段再次检查说明增加扫读负担；但异步未就绪与发布时复核不可隐去。

替换文案：

- 主文案：发布检查
- 读取中：正在读取发布检查，暂不能发布。
- 已通过：发布检查通过；正式发布时将再次检查。
- 补充说明：问题标题、阻断/提醒和影响数量常显；内部代码移至“技术详情”，便于排障时复制。

目标条件：保持现有等待/失败/通过分支；详情折叠不改变阻断判定。

位置：门禁区域标题和原状态 Alert；问题行技术详情。

保留边界：

- 未读取完成时按钮继续不可用。
- 再次检查仍在发布动作执行，不能以缓存通过结果跳过。
- 错误码可查，不能销毁排障证据。

验收场景（待实施后执行）：

- 等待时不显示“可以发布”。
- 阻断问题一眼可见，展开后仍可复制 issue.code。
- 通过后、正式发布前状态改变仍会被服务端拒绝。

<a id="mc-g17"></a>

## MC-G17 · 成绩工作台 / 基础成绩锁定与原查询入口（P1）

角色：成绩管理员。处理：缩短 / 保留。依赖：纯文案。

原文与定位：

- [apps/web-admin/src/features/score-management/components/ScoreActionsPanel.tsx:39](D:/project/yuejuan/apps/web-admin/src/features/score-management/components/ScoreActionsPanel.tsx:39)（literal）：“基础成绩不能直接修改；学生门户版本的状态请查看本页版本区域。更正请通过申诉或复评流程处理。”
- [apps/web-admin/src/features/score-management/components/ScoreActionsPanel.tsx:52](D:/project/yuejuan/apps/web-admin/src/features/score-management/components/ScoreActionsPanel.tsx:52)（literal）：“仅在学校使用原有成绩查询入口时操作。此操作锁定基础成绩，不会发布学生门户版本。”

当前条件：基础成绩已锁定时显示锁定 Alert；原入口操作已位于高级折叠区域，只有展开才出现第二句。

判断：两条为不同入口/后果，不能当作每次必读重复流程删除；能收短术语并保留状态分离。

替换文案：

- 主文案：更正须通过申诉或复评；学生门户状态见版本区域。
- 原查询入口：仅用于学校原查询入口。公开后锁定基础成绩；学生门户需另行发布。

目标条件：保持基础成绩锁定条件和原入口高级折叠条件，不前移原入口说明。

位置：原锁定 Alert；原查询入口按钮旁。

保留边界：

- 保留直接改基础成绩受限的事实。
- 学校原入口公开不等于门户发布。
- 不为了少一步取消锁定、权限或审计。

验收场景（待实施后执行）：

- 基础成绩锁定后能找到更正路径。
- 展开原入口时，点击前能看到不会发布门户。

<a id="mc-g18"></a>

## MC-G18 · 创建门户草稿 / 冻结设置提示（P1）

角色：成绩管理员。处理：缩短 / 保留。依赖：纯文案。

原文与定位：

- [apps/web-admin/src/features/score-management/components/ScoreWorkflowModals.tsx:136](D:/project/yuejuan/apps/web-admin/src/features/score-management/components/ScoreWorkflowModals.tsx:136)（literal）：“草稿不会立即对学生生效”
- [apps/web-admin/src/features/score-management/components/ScoreWorkflowModals.tsx:136](D:/project/yuejuan/apps/web-admin/src/features/score-management/components/ScoreWorkflowModals.tsx:136)（literal）：“先创建草稿，核对冻结版本的学生视角，再通过发布门禁正式发布。草稿创建后，以下公开策略随成绩一起冻结。”

当前条件：创建草稿弹窗顶部固定 Alert。

判断：不公开与设置冻结均必须保留；“生效”“策略”“门禁”可换成用户能直接判断的后果。

替换文案：

- 主文案：创建草稿，不会公开给学生
- 补充说明：成绩和以下公开设置将随草稿冻结。核对学生预览后，再正式发布。

目标条件：在创建草稿提交前持续显示。

位置：弹窗顶部，公开设置之前。

保留边界：

- 不能暗示草稿能持续同步基础成绩或可随意改公开设置。
- 不把创建草稿变为立即对外发布。

验收场景（待实施后执行）：

- 创建前能理解本次勾选将随草稿固定。
- 草稿创建成功但未发布时，不出现已公开给学生的成功文案。

<a id="mc-g19"></a>

## MC-G19 · 创建门户草稿 / 逐题公开与复核选项（P1）

角色：成绩管理员。处理：缩短 / 保留。依赖：纯文案。

原文与定位：

- [apps/web-admin/src/features/score-management/components/ScoreWorkflowModals.tsx:138](D:/project/yuejuan/apps/web-admin/src/features/score-management/components/ScoreWorkflowModals.tsx:138)（literal）：“当前仅支持针对题目申请复核，必须公开逐题得分。学生打开题目详情后可选择原因并提交；整卷或总分复核暂未提供在线申请。”
- [apps/web-admin/src/features/score-management/components/ScoreWorkflowModals.tsx:140](D:/project/yuejuan/apps/web-admin/src/features/score-management/components/ScoreWorkflowModals.tsx:140)（literal）：“关闭逐题得分后，题目原文、答案、反馈、评分要点、知识点和题目统计也不会出现在逐题详情中。”

当前条件：考试允许学生申请复核时显示第一条；关闭逐题得分时显示第二条。

判断：提交路径操作说明与多种详情逐项枚举可收短，必要联动和申请范围必须留在选项旁。

替换文案：

- 主文案：开放复核须公开逐题得分；当前仅支持逐题申请。
- 关闭逐题得分：关闭后不显示任何逐题详情，包括题目、答案和反馈。

目标条件：沿用允许复核、关闭逐题得分的现有条件；不为其他分支常驻显示。

位置：逐题得分开关及其受影响设置旁。

保留边界：

- 不承诺支持整卷/总分在线复核。
- 不移除复核开启时公开逐题得分的强制联动。
- “任何逐题详情”仅指逐题详情区域，不指考试级总分/班级统计。

验收场景（待实施后执行）：

- 开启复核后，管理员知道逐题得分必须公开。
- 关闭逐题得分后，相关详情选项仍按现有逻辑禁用/不输出。

<a id="mc-g20"></a>

## MC-G20 · 创建门户草稿 / 匿名范例卷条件（P1）

角色：成绩管理员。处理：缩短 / 保留。依赖：纯文案。

原文与定位：

- [apps/web-admin/src/features/score-management/components/ScoreWorkflowModals.tsx:143](D:/project/yuejuan/apps/web-admin/src/features/score-management/components/ScoreWorkflowModals.tsx:143)（literal）：“发布前自动生成匿名范例卷”
- [apps/web-admin/src/features/score-management/components/ScoreWorkflowModals.tsx:143](D:/project/yuejuan/apps/web-admin/src/features/score-management/components/ScoreWorkflowModals.tsx:143)（literal）：“请确认最高分答卷的每页均有已锁定模板、完成页面配准，并配置覆盖姓名、学号和二维码的身份区域；缺少任一条件将阻断发布。”

当前条件：选中公开匿名范例卷时显示警告。

判断：这是避免身份暴露和发布失败的必要提醒，不能转为默认隐藏帮助；需区分系统校验的版面前提与人工核对的身份覆盖完整性，不能暗示通过门禁即证明完全匿名。

替换文案：

- 主文案：发布前自动生成匿名范例卷
- 补充说明：最高分答卷每页须有已锁定模板、成功配准和有效身份区，否则无法发布。请逐页核对身份区确实覆盖姓名、学号、二维码等身份信息；系统仅按配置区域遮挡，不能判断是否遗漏。

目标条件：仅选择公开匿名范例卷时常显，保持 warning 层级。

位置：匿名范例卷选项紧下方。

保留边界：

- 不取消逐页检查、不弱化身份区域范围。
- 系统只校验每页身份区域存在且坐标有效，并按该区域遮挡；不自动判断区域是否覆盖全部姓名、学号、二维码或其他身份信息。完整性仍须人工逐页核对。
- 不把生成安排写成当前已生成或匿名检查已通过。
- 实际生成失败仍由发布门禁阻止。

验收场景（待实施后执行）：

- 勾选时立即可读到系统校验前提、人工逐页核对责任和无法发布后果。
- 存在但偏离姓名或条码的合法身份区，不得被提示为已通过完整匿名检查；正常生成也不宣称全部身份信息已被自动识别遮挡。
- 未勾选时不堆积与当前方案无关的警告。

<a id="mc-g21"></a>

## MC-G21 · 复评候选复核 / 接受与驳回（P1）

角色：复评审核人、成绩管理员。处理：缩短 / 保留。依赖：纯文案。

原文与定位：

- [apps/web-admin/src/features/score-management/components/ScoreWorkflowModals.tsx:101](D:/project/yuejuan/apps/web-admin/src/features/score-management/components/ScoreWorkflowModals.tsx:101)（literal）：“候选意见尚未改写成绩”
- [apps/web-admin/src/features/score-management/components/ScoreWorkflowModals.tsx:101](D:/project/yuejuan/apps/web-admin/src/features/score-management/components/ScoreWorkflowModals.tsx:101)（literal）：“逐项接受后才能完成复评。完成后仍需生成并发布新的成绩版本，原发布版本保持不变。”
- [apps/web-admin/src/features/score-management/components/ScoreWorkflowModals.tsx:115](D:/project/yuejuan/apps/web-admin/src/features/score-management/components/ScoreWorkflowModals.tsx:115)（literal）：“请重新核对证据，填写处理说明后接受或驳回候选。”

当前条件：候选复核弹窗顶部固定显示前两条；异常候选还显示核对证据提示。实际提供接受和驳回操作。

判断：“逐项接受”误导为必须全部接受，和驳回路径冲突；需要强调逐项处理完成，而非改变审核独立性。

替换文案：

- 主文案：候选意见尚未改写成绩
- 补充说明：请逐项完成复核。需要更正时，仍须生成并发布新成绩版本；原版本不变。
- 异常候选：请核对证据，填写处理说明后接受或驳回。

目标条件：候选复核弹窗；异常项保留独立提示，不用顶部短句取代处理说明要求。

位置：顶部 Alert、异常候选行。

保留边界：

- 不把候选分直接写为正式成绩。
- 接受、驳回及异常说明要求不变。
- 不暗示全驳回也一定要发布无变化版本。

验收场景（待实施后执行）：

- 全部驳回时，提示不再要求逐项接受。
- 异常项没有说明仍按原逻辑不能完成相应处理。
- 接受候选后仍需正式版本发布才改变学生所见。

<a id="mc-g22"></a>

## MC-G22 · 题目复评 / 创建与预览说明（P2）

角色：质量管理员、成绩管理员。处理：合并 / 渐进展开 / 缩短。依赖：前端交互/条件配套。

原文与定位：

- [apps/web-admin/src/features/score-management/components/ScoreWorkflowModals.tsx:156](D:/project/yuejuan/apps/web-admin/src/features/score-management/components/ScoreWorkflowModals.tsx:156)（literal）：“复评不会直接修改已发布成绩”
- [apps/web-admin/src/features/score-management/components/ScoreWorkflowModals.tsx:156](D:/project/yuejuan/apps/web-admin/src/features/score-management/components/ScoreWorkflowModals.tsx:156)（literal）：“先预览影响范围并创建任务；任务完成后生成新的成绩版本，再通过发布门禁正式发布。”
- [apps/web-admin/src/features/score-management/components/ScoreWorkflowModals.tsx:165](D:/project/yuejuan/apps/web-admin/src/features/score-management/components/ScoreWorkflowModals.tsx:165)（literal）：“预览只用于确认范围，创建时系统会再次冻结该版本对应的题目事实。”
- [apps/web-admin/src/features/score-management/components/ScoreReleaseWorkspace.tsx:143](D:/project/yuejuan/apps/web-admin/src/features/score-management/components/ScoreReleaseWorkspace.tsx:143)（literal）：“复评只处理指定题目，完成后必须生成并发布新的成绩版本，不会直接改分。”

当前条件：复评区域介绍、创建弹窗导语与预览成功描述多次解释不直接改分及新版本流程。

判断：需要保留预览只是估计、创建冻结范围、复评不直接改分；但完整链条不必在同一阶段重复。

替换文案：

- 主文案：复评只处理所选题目，当前公布成绩不变。
- 预览结果：来源：正式成绩 V{版本} · 影响 {份数} 份答卷。创建时将冻结该版本对应的题目范围。
- 创建按钮旁：任务完成后，需生成并发布新成绩版本。

目标条件：进入复评区域显示 summary；预览成功显示 preview；创建按钮旁保留 afterCreate。

位置：复评区域、预览结果、创建按钮下各一条，避免同屏重复整段。

保留边界：

- 保留必须先预览的当前流程，不绕过预览/创建接口。
- 预览成功不代表创建必然成功，创建时仍重新核对版本与事实。
- 任务批准、独立复评和发布门禁保持不变。

验收场景（待实施后执行）：

- 预览后来源版本已变化时，创建仍失败并显示真实原因。
- 用户能看到影响范围与现有成绩不变，而不需读三遍完整链条。

<a id="mc-g23"></a>

## MC-G23 · 正式发布 / 最终确认（P1）

角色：成绩管理员。处理：缩短 / 保留。依赖：纯文案。

原文与定位：

- [apps/web-admin/src/features/score-management/hooks/useScoreReleaseWorkflow.tsx:122](D:/project/yuejuan/apps/web-admin/src/features/score-management/hooks/useScoreReleaseWorkflow.tsx:122)（literal）：“学生可从逐题详情申请复核；未限制原因时提供六种常用原因。”
- [apps/web-admin/src/features/score-management/hooks/useScoreReleaseWorkflow.tsx:122](D:/project/yuejuan/apps/web-admin/src/features/score-management/hooks/useScoreReleaseWorkflow.tsx:122)（literal）：“此版本未开放学生复核申请。”
- [apps/web-admin/src/features/score-management/hooks/useScoreReleaseWorkflow.tsx:127](D:/project/yuejuan/apps/web-admin/src/features/score-management/hooks/useScoreReleaseWorkflow.tsx:127)（literal）：“请先从版本记录打开学生视角预览。发布后学生门户将展示此版本；学校原有成绩查询的状态不会因此改变。后续更正需创建新版本。”

当前条件：发布前确认框按复核开关显示申请说明，同时显示学生预览、门户替换与后续更正规则；旧来源/匿名材料警告按原条件叠加。

判断：“六种常用原因”不是本次发布决定的关键；可删计数。其余为对外发布必要后果，不能为减字删除。

替换文案：

- 主文案：请先核对学生预览。发布后，学生门户将展示此版本；学校原查询入口状态不变。更正须创建新版本。
- 已开放复核：此版本启用逐题复核；学生可在开放时段内从逐题详情申请。
- 未开放复核：此版本未开放学生复核申请。

目标条件：沿用发布确认与开关条件；保留考试、版本、公开内容及已有警告。

位置：发布确认框正文。

保留边界：

- 不声称系统已验证用户完成预览。
- appeal_window.enabled 不等同于当前可申请；设有 opens_at 或 closes_at 的历史/API草稿仍按冻结开放时段执行，未设端点不限制对应方向。
- 旧来源版本警告、匿名材料准备、二次验证和服务端复核不得移入折叠或删掉。
- 不合并原查询入口与学生门户状态。

验收场景（待实施后执行）：

- 点击最终发布前能读到学生将看到哪个版本。
- 未开放复核时不出现可申请的描述。
- enabled=true 但开放时间未到或已经结束的草稿，不暗示当前即可申请；未设置时间端点时沿用原有无限制语义。
- 来源落后或匿名材料待生成时，原警告仍清楚可见。

<a id="mc-g24"></a>

## MC-G24 · 学生题目复核 / 教师独立意见（P2）

角色：受派复核教师。处理：合并 / 按条件显示 / 缩短。依赖：前端交互/条件配套。

原文与定位：

- [apps/web-admin/src/components/QuestionAppealReviewOpinionPanel.tsx:24](D:/project/yuejuan/apps/web-admin/src/components/QuestionAppealReviewOpinionPanel.tsx:24)（literal）：“核对原公布版本的答卷和评分依据后保存意见，交由学校管理员继续处理。意见和建议分值仅供教职工核对，学生暂不可见。”
- [apps/web-admin/src/components/QuestionAppealReviewOpinionPanel.tsx:21](D:/project/yuejuan/apps/web-admin/src/components/QuestionAppealReviewOpinionPanel.tsx:21)（literal）：“这份意见尚未改变成绩。学校管理员将核对意见、安排必要的复评并向学生答复。”
- [apps/web-admin/src/components/QuestionAppealReviewOpinionPanel.tsx:21](D:/project/yuejuan/apps/web-admin/src/components/QuestionAppealReviewOpinionPanel.tsx:21)（literal）：“此处为教师当时提出的建议，处理结果以管理员答复与正式发布成绩为准。”

当前条件：可提交时显示填写导语；提交后有意见卡，pendingHandoff 与已处理状态各显示一段；允许修改时这些说明可能同屏。

判断：提交前必须知道学生不可见、非直接改分；提交后以当前阶段说明替代相同流程长段。

替换文案：

- 主文案：核对原公布材料后提交独立意见。意见和建议分仅供教职工查看，不直接改分。
- 已移交待处理：意见已移交学校管理员，当前成绩未改变。
- 已处理记录：此处保留教师建议；处理结果以管理员答复和正式成绩为准。

目标条件：首次可提交用 summary；已有意见时使用 pending/handled 状态，不再次叠加完整首次导语；如可修改，表单只留“修改后需再次提交”。

位置：教师意见表单上方；已保存意见卡下方。

保留边界：

- 学生不可见仅指教师意见/建议分，不包括管理员已公开答复。
- 教师仍不结案、不直接改正式成绩。
- 不承诺管理员必然接受建议或自动安排复评。

验收场景（待实施后执行）：

- 首次提交前明确私有范围。
- 移交后不出现成绩已更正的成功暗示。
- 已有公开答复仍独立可见，不因合并提示隐藏。

<a id="mc-g25"></a>

## MC-G25 · 复核中心 / 教师导语与历史入口（P2）

角色：受派复核教师、复核管理员。处理：缩短 / 保留。依赖：纯文案。

原文与定位：

- [apps/web-admin/src/pages/QuestionAppealCenterPage.tsx:42](D:/project/yuejuan/apps/web-admin/src/pages/QuestionAppealCenterPage.tsx:42)（literal）：“处理学校分派给我的题目复核申请；考试名称和学科随受派任务提供，学生身份继续隐藏。先核对材料并移交独立意见，再由学校管理员处理。”
- [apps/web-admin/src/pages/QuestionAppealCenterPage.tsx:50](D:/project/yuejuan/apps/web-admin/src/pages/QuestionAppealCenterPage.tsx:50)（literal）：“此处保留原有业务记录和处理方式。学生门户新提交的题目复核申请请返回学生门户申请页签。”

当前条件：教师门户申请页导语，或切到历史申诉记录后的说明。

判断：任务摘要如何提供属于实现解释，可移除；历史与门户申请必须仍能区分，避免去错处理入口。

替换文案：

- 主文案：核对分派给我的匿名材料，提交独立意见，由学校管理员继续处理。
- 历史页签：学生门户新申请见“学生门户申请”；本页处理历史申诉记录。

目标条件：门户教师视角使用 summary；仅历史页签显示 history。

位置：对应页签标题下。

保留边界：

- 继续隐藏教师不应获得的学生身份。
- 历史申诉不自动变成门户申请，权限仍按原条件。
- 任务摘要加载失败的具体重读入口与错误保留。

验收场景（待实施后执行）：

- 教师能识别自己的受派任务范围。
- 切到历史页签能立即知道新申请去哪处理。

<a id="mc-g26"></a>

## MC-G26 · 历史申诉 / 独立教师复核（P1）

角色：复核教师。处理：缩短 / 保留。依赖：纯文案。

原文与定位：

- [apps/web-admin/src/features/appeals/AppealDecisionPanel.tsx:39](D:/project/yuejuan/apps/web-admin/src/features/appeals/AppealDecisionPanel.tsx:39)（literal）：“学生姓名、班级和原阅卷教师已隐藏。复核建议仅供管理员终审，不会直接修改成绩或关闭申诉；请只依据答卷和评分标准判断。”

当前条件：teacherBlindView 分支的复核意见面板。

判断：独立性、隐藏范围和不结案都是必要边界；可调整语序并缩短，不能以“规则已看过”为由完全移除。

替换文案：

- 主文案：请独立核对答卷和评分标准。学生身份及原阅卷人已隐藏；意见由管理员终审，不直接改分或结案。

目标条件：保持 teacherBlindView 条件，管理端不展示此教师专用规则。

位置：独立复核表单顶部。

保留边界：

- 不展示原阅卷教师或学生身份以缩短说明成本。
- 不授予教师终审/结案权限。
- 不删原评教师不得复核的管理端分派提示。

验收场景（待实施后执行）：

- 盲复核表单仍说明为何缺少身份与原评人。
- 提交教师建议不会被文案描述为已关闭申诉。

<a id="mc-g27"></a>

## MC-G27 · 复核表单 / 未提交内容与离开提醒（P1）

角色：复核教师、复核管理员。处理：缩短 / 按条件显示 / 保留。依赖：前端交互/条件配套。

原文与定位：

- [apps/web-admin/src/features/appeals/AppealDecisionPanel.tsx:79](D:/project/yuejuan/apps/web-admin/src/features/appeals/AppealDecisionPanel.tsx:79)（literal）：“未提交内容仅在本页，切换申请或刷新会丢失。”
- [apps/web-admin/src/features/appeals/AppealDecisionPanel.tsx:79](D:/project/yuejuan/apps/web-admin/src/features/appeals/AppealDecisionPanel.tsx:79)（literal）：“复核意见和终审结论只有提交后才写入服务端。”
- [apps/web-admin/src/components/QuestionAppealWorkspace.tsx:97](D:/project/yuejuan/apps/web-admin/src/components/QuestionAppealWorkspace.tsx:97)（literal）：“复核意见、给学生的回复或本题复评选择尚未提交，离开后无法恢复。”
- [apps/web-admin/src/components/QuestionAppealWorkspace.tsx:106](D:/project/yuejuan/apps/web-admin/src/components/QuestionAppealWorkspace.tsx:106)（literal）：“当前意见、回复和复评选择只保存在本页，离开后无法恢复。”

当前条件：历史意见非终态时固定一行，dirty 时换警告；门户工作区在 dirty 离开守卫/选择切换确认中提醒。

判断：这是与阅卷台自动保存不同的真实边界，必须保留。可以统一“未提交”术语并仅在 dirty 时强调丢失，不声称表单自动保存。

替换文案：

- 主文案：提交后才会保存。
- 有未提交内容：有未提交内容；切换、关闭或刷新后无法恢复。
- 离开确认标题：放弃未提交内容？
- 离开确认正文：当前意见、回复和复评选择尚未提交，离开后无法恢复。
- 离开确认按钮：放弃并继续 / 继续编辑

目标条件：干净状态仅可编辑表单显示 summary；dirty 后显示醒目 dirty；离开守卫保持现有支持范围，不宣称覆盖所有系统关闭方式。

位置：提交按钮旁状态行；已有离开确认。

保留边界：

- 保持现有真实守卫，不承诺拦截一切浏览器/系统强制退出。
- 专门的“重读任务摘要保留本页内容”路径仍按现状保留，不能用一刀切句子制造误解。
- 提交失败时保留本页输入但不说已持久化；不增加后端草稿能力。

验收场景（待实施后执行）：

- 未编辑时不弹放弃确认。
- 编辑后切换申请出现放弃/继续编辑选择。
- 提交失败仍可继续编辑，成功后 dirty 提醒解除。
- 重读当前任务摘要不清空已填写意见。

<a id="mc-g28"></a>

## MC-G28 · 质量总览 / 回标转复评提示（P2）

角色：质量管理员。处理：合并 / 按条件显示 / 渐进展开。依赖：前端交互/条件配套。

原文与定位：

- [apps/web-admin/src/pages/QualityDashboardViews.tsx:195](D:/project/yuejuan/apps/web-admin/src/pages/QualityDashboardViews.tsx:195)（literal）：“系统将从已发布成绩版本重新冻结本批次对应的学生范围；不会直接采用回标候选分。”
- [apps/web-admin/src/pages/QualityDashboardViews.tsx:210](D:/project/yuejuan/apps/web-admin/src/pages/QualityDashboardViews.tsx:210)（literal）：“回标只记录候选差异，不会修改当前成绩”
- [apps/web-admin/src/pages/QualityDashboardViews.tsx:210](D:/project/yuejuan/apps/web-admin/src/pages/QualityDashboardViews.tsx:210)（literal）：“只有标记为“需重评”的已完成项目，才能由质量管理员明确转入题目复评。新任务仍需审批、人工复评并创建新的成绩发布版本。”
- [apps/web-admin/src/pages/QualityDashboardViews.tsx:186](D:/project/yuejuan/apps/web-admin/src/pages/QualityDashboardViews.tsx:186)（excerpt）：“确认后仍需审批、复评、差异复核和新版本发布。”

当前条件：回标差异卡常显不改分及完整链条；转复评区域和预览描述又解释冻结范围及后续审批。

判断：完整治理链多处同屏重复；不同阶段保留与当前选择有关的限制即可，不要误判审批和复评本身多余。

替换文案：

- 主文案：回标仅记录差异，当前成绩不变。
- 转交说明：仅已完成且标记“需重评”的项目可转入题目复评；范围以已发布成绩版本为准，不采用回标候选分。
- 预览结果：来源成绩 V{版本} · 接手教师：{教师} · 将冻结 {份数} 份答卷。创建后仍需审批、独立复评、差异复核并发布新版本。

目标条件：卡片常显 summary；仅有权限且可转复评时显示 handoff；预览成功后显示 preview。批次未完成/无已发布版本时仍显示原阻断原因。

位置：回标卡标题下、转复评按钮旁、预览结果各一条。

保留边界：

- 不把回标候选分直接采纳为正式成绩。
- 不删除显式转交、审批、独立人工复评和新版本发布。
- 来源范围仍由服务端冻结；预览不具创建授权效力。

验收场景（待实施后执行）：

- 只查看差异的人只读简短状态，不被无权限操作说明淹没。
- 批次未完成和无已发布版本时仍明确不能创建。
- 有权限且预览完成时，后续治理链和不直接采用候选分仍清楚可见。

## 本轮逐文件审读状态

`reviewed` 仅代表本轮全文读过；`partial` 为片段核对，`pending` 不计审读覆盖。

| 文件 | 状态 | 说明 |
| --- | --- | --- |
| AGENTS.md | reviewed | 本轮全文审读；静态核对源码及对应状态条件，未修改业务代码。 |
| docs/engineering/commenting-standard.md | reviewed | 本轮全文审读；静态核对源码及对应状态条件，未修改业务代码。 |
| apps/web-admin/src/features/grading/workbench/components/GradingAIPlanSettings.tsx | reviewed | 本轮全文审读；静态核对源码及对应状态条件，未修改业务代码。 |
| apps/web-admin/src/features/grading/workbench/components/ExamScoringDialogs.tsx | reviewed | 本轮全文审读；静态核对源码及对应状态条件，未修改业务代码。 |
| apps/web-admin/src/features/grading/workbench/components/GradingSettingsView.tsx | reviewed | 本轮全文审读；静态核对源码及对应状态条件，未修改业务代码。 |
| apps/web-admin/src/features/grading/workbench/components/ScoreEditor.tsx | reviewed | 本轮全文审读；静态核对源码及对应状态条件，未修改业务代码。 |
| apps/web-admin/src/features/grading/workbench/components/WorkbenchHeader.tsx | reviewed | 本轮全文审读；静态核对源码及对应状态条件，未修改业务代码。 |
| apps/web-admin/src/features/grading/workbench/components/TaskRail.tsx | reviewed | 本轮全文审读；静态核对源码及对应状态条件，未修改业务代码。 |
| apps/web-admin/src/features/score-management/components/ScoreReleaseWorkspace.tsx | reviewed | 本轮全文审读；静态核对源码及对应状态条件，未修改业务代码。 |
| apps/web-admin/src/features/score-management/components/ScoreActionsPanel.tsx | reviewed | 本轮全文审读；静态核对源码及对应状态条件，未修改业务代码。 |
| apps/web-admin/src/features/score-management/components/ScoreWorkflowModals.tsx | reviewed | 本轮全文审读；静态核对源码及对应状态条件，未修改业务代码。 |
| apps/web-admin/src/features/calibration/CalibrationDrawer.tsx | reviewed | 本轮全文审读；静态核对源码及对应状态条件，未修改业务代码。 |
| apps/web-admin/src/features/score-management/hooks/useScoreReleaseWorkflow.tsx | reviewed | 本轮全文审读；静态核对源码及对应状态条件，未修改业务代码。 |
| apps/web-admin/src/components/QuestionAppealReviewOpinionPanel.tsx | reviewed | 本轮全文审读；静态核对源码及对应状态条件，未修改业务代码。 |
| apps/web-admin/src/features/appeals/AppealDecisionPanel.tsx | reviewed | 本轮全文审读；静态核对源码及对应状态条件，未修改业务代码。 |
| apps/web-admin/src/pages/QuestionAppealCenterPage.tsx | reviewed | 本轮全文审读；静态核对源码及对应状态条件，未修改业务代码。 |
| apps/web-admin/src/features/score-management/components/QualityPanel.tsx | reviewed | 本轮全文审读；静态核对源码及对应状态条件，未修改业务代码。 |
| apps/web-admin/src/pages/QualityDashboardViews.tsx | reviewed | 本轮全文审读；静态核对源码及对应状态条件，未修改业务代码。 |
| apps/web-admin/src/features/grading/workbench/hooks/useAutomaticGradeSubmission.ts | reviewed | 本轮全文审读；静态核对源码及对应状态条件，未修改业务代码。 |
| apps/web-admin/src/components/QuestionAppealViews.tsx | partial | 本轮仅审读辅助文案及附近条件的筛选片段（43–47、55–58、66–68、76、108–110、138–152 等），不计全文覆盖。此次条目未直接据此文件新增。 |
| apps/web-admin/src/components/QuestionAppealWorkspace.tsx | partial | 本轮仅审读错误、未提交守卫、选择切换、教师权限和提交后提示附近片段（29–37、95–106、193–218、224–265、283–287）；不计全文覆盖。 |
| apps/web-admin/src/pages/ScoreManagementPage.tsx | partial | 本轮仅核对 publishedOrLocked 的计算及调用（201–224、448、454 等），用于确认基础成绩状态与门户当前版本分离。 |
| apps/web-admin/src/features/score-management/components/StudentScorePreviewDialog.tsx | pending | 本轮尝试批量读取时输出被截断，未得到可审读正文；上一轮覆盖不替代本轮复核。本文件未作为条目依据。 |
