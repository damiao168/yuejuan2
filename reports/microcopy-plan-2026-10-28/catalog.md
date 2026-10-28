# 小字逐条执行清单

共109项处置决策；59项纯文案，50项前端配套，0项后端配套。含1项仅保留决策；不能把总数当作待删除段落数。

每条的完整原文、条件、边界和验收见对应专项说明或 [JSON](catalog.json)。目标文案中‘删除/保留/移动’等句子是执行说明，不能直接渲染给用户；动态占位须绑定真实数据。当前均待实施。

## navigation-question-bank

| 编号 | 页面/标题 | 优先级 | 处置 | 目标文案或执行说明 | 依赖 |
| --- | --- | --- | --- | --- | --- |
| [MC-R01](navigation-question-bank.md#mc-r01) | 教师首页：任务摘要 | P2 | 缩短、合并 | summary：待阅卷 / 待仲裁 / 已提交；details：摘要仅统计当前已读取任务。完整任务请进入阅卷或仲裁列表。 | 前端配套 |
| [MC-R02](navigation-question-bank.md#mc-r02) | 教师首页：空状态 | P2 | 条件显示、缩短 | summary：当前暂无待办；failure：任务暂时无法读取，请重试。；details：任务分配后会显示在这里。 | 前端配套 |
| [MC-R03](navigation-question-bank.md#mc-r03) | 密码登录：找回入口 | P3 | 缩短 | summary：忘记密码？请联系学校管理员。 | 纯文案 |
| [MC-R04](navigation-question-bank.md#mc-r04) | 登录：公共电脑 | P1 | 缩短、保留 | summary：公共电脑登录最长 4 小时，离开前请退出。 | 纯文案 |
| [MC-R05](navigation-question-bank.md#mc-r05) | 微信登录：有效期与隐私 | P2 | 条件显示、展开 | summary：二维码 5 分钟内有效，仅供本次登录。；detailsLabel：微信登录会读取什么？；details：不会读取微信好友或聊天信息。 | 前端配套 |
| [MC-R06](navigation-question-bank.md#mc-r06) | 主站学生身份过渡页 | P2 | 合并、条件显示 | summary：进入学生端，查看已发布成绩和学习反馈。；missing：请使用学校发放的学生端网址，或联系学校管理员获取。 | 前端配套 |
| [MC-R07](navigation-question-bank.md#mc-r07) | 题库：首页说明 | P2 | 缩短、条件显示 | summary：维护题目、答案和评分标准。；details：发布供授权教师选用；已有考试继续使用原版本。 | 前端配套 |
| [MC-R08](navigation-question-bank.md#mc-r08) | 题库：审核流程说明 | P1 | 条件显示、展开 | summary：提交后锁定当前内容，需由其他教师审核。；reviewing：审核中，当前内容已锁定。；approved：审核通过，等待有发布权限的教师发布。；detailsLabel：查看审核与发布流程；details：草稿 → 审核 → 发布到题库 → 选入考试。 | 前端配套 |
| [MC-R09](navigation-question-bank.md#mc-r09) | 题库：交接与未通知事实 | P1 | 合并、缩短、保留 | summary：审核教师：{有权限人员姓名}。请自行联系，系统暂不发送通知。；submitted：已提交审核，请联系上方审核教师。；approved：发布教师：{有权限人员姓名}。；copiedReviewing：链接已复制，请发给审核教师。；copiedApproved：链接已复制，请发给发布教师。 | 前端配套 |
| [MC-R10](navigation-question-bank.md#mc-r10) | 题库：发布结果 | P1 | 合并、缩短、保留 | summary：已发布到学校题库。；details：在考试的“小题与分值”中选入此版本。已有考试仍使用原版本。；action：去考试中选题 | 前端配套 |
| [MC-R11](navigation-question-bank.md#mc-r11) | 题库：并发冲突警告 | P1 | 缩短、合并、保留 | summary：草稿已有更新。你在本页的修改尚未被覆盖，请先比较版本。；details：采用最新版本会替换本页未保存内容。可先下载本地修改备份。；backupHelp：备份包含题目、答案、评分编辑和附件信息；附件信息不等同于附件文件。 | 前端配套 |
| [MC-R12](navigation-question-bank.md#mc-r12) | 题库：两个编辑区互锁 | P1 | 条件显示、缩短 | contentDirty：先保存题目内容，再编辑评分标准或提交审核。；scoringDirty：先保存评分标准，再编辑题目内容或提交审核。 | 前端配套 |
| [MC-R13](navigation-question-bank.md#mc-r13) | 题库：审核记录空意见 | P3 | 删除、条件显示 | summary：无意见时不渲染意见段落；有意见时原样展示。 | 前端配套 |
| [MC-R14](navigation-question-bank.md#mc-r14) | 题库：授权职责解释 | P2 | 展开、条件显示、保留 | summary：按实际职责授权。；roleHelp：{"Viewer":"可浏览题目和查看统计。","Author":"可浏览、新建题目并编辑草稿。","Reviewer":"可浏览并审核其他教师的版本。","Publisher":"可浏览、发布已批准版本并停止选用题目。","Manager":"仅管理结构与授权，不自动获得看题权限。"}；detailsLabel：查看各职责权限 | 前端配套 |
| [MC-R15](navigation-question-bank.md#mc-r15) | 题库：历史授权保护 | P1 | 合并、缩短、保留 | summary：这些历史或受保护授权只读保留，本次保存不会修改。；failure：授权读取失败，暂不能保存。请重新读取。 | 前端配套 |
| [MC-R16](navigation-question-bank.md#mc-r16) | 题库：分类规则维护 | P2 | 删除、缩短、合并 | summary：添加知识点、分类目录或补充字段。；fields：设置字段名称和内容类型。；terms：停用条目后，新题不能再选，历史题目记录保留。 | 前端配套 |
| [MC-R17](navigation-question-bank.md#mc-r17) | 题库：特殊评分方式已有折叠 | P3 | 保留 | summary：保留现有折叠和说明。 | 纯文案 |
| [MC-R18](navigation-question-bank.md#mc-r18) | 题库：引用图片授权 | P1 | 缩短、保留 | summary：本版本引用这些图片；原材料授权撤回或图片不可用时，将停止使用。；importing：导入后本草稿将引用这些图片；原材料授权撤回后，将停止使用。 | 纯文案 |
| [MC-R19](navigation-question-bank.md#mc-r19) | 题库：结构化答案帮助 | P3 | 删除、条件显示 | summary：删除‘系统会保留结构化答案’。；details：仅当某题型有额外填写规则时，在该控件旁说明；规则须来自实际控件约束。 | 前端配套 |
| [MC-R20](navigation-question-bank.md#mc-r20) | 题库：误差单位 | P1 | 展开、保留 | summary：误差设置（数值答案）；absolute：如标准答案为10，误差0.1可接受9.9～10.1。；relative：填写小数，例如0.01表示1%。 | 前端配套 |
| [MC-R21](navigation-question-bank.md#mc-r21) | 题库：选用模板 | P1 | 合并、条件显示、保留 | summary：保存时用此模板替换本题评分细则（评分点、扣分规则和样例答案）；满分须一致。模板后续更新不影响本题。 | 前端配套 |
| [MC-R22](navigation-question-bank.md#mc-r22) | 题库：版本比较重复标签 | P2 | 合并、展开 | summary：{差异项数}项不同；same：查看{相同项数}项相同内容；columns：本地修改 / 最新版本 | 前端配套 |
| [MC-R23](navigation-question-bank.md#mc-r23) | 全局：空状态默认复述 | P3 | 删除、条件显示 | summary：默认只展示空状态标题。；details：调用方仅在有真实下一步或查询范围需要解释时传description。 | 前端配套 |
| [MC-R24](navigation-question-bank.md#mc-r24) | 助手：底部业务提醒 | P2 | 缩短、保留 | summary：业务信息请核对来源，正式操作请进入对应页面。 | 纯文案 |
| [MC-R25](navigation-question-bank.md#mc-r25) | 助手：文件上传规则 | P2 | 缩短、条件显示、保留 | summary：支持图片、PDF和文本，最多3个附件。；images：PNG/JPEG图片每张不超过3MB，可直接粘贴。 | 前端配套 |
| [MC-R26](navigation-question-bank.md#mc-r26) | 助手：数据来源和查询进度 | P2 | 保留、条件显示、合并 | summary：数据来源（{数量}）；success：已查询{数量}项资料；failure：部分资料未能读取，请核对回答范围。 | 前端配套 |
| [MC-R27](navigation-question-bank.md#mc-r27) | 助手：每条网页引用 | P2 | 展开、合并 | summary：参考资料（{数量}）；entry：{引用文字} · {域名}；action：复制地址；failure：复制失败，请手动复制此地址。 | 前端配套 |
| [MC-R28](navigation-question-bank.md#mc-r28) | 全局：待确认操作与原内容 | P1 | 缩短、保留、条件显示 | summary：提交结果待确认；processing：正在处理，请稍后查看结果。；takeover：可以继续提交此前内容。；action：按原内容继续提交；notAccepted：此次提交未被接收，请返回原页面重新提交。 | 纯文案 |

## exam-preparation

| 编号 | 页面/标题 | 优先级 | 处置 | 目标文案或执行说明 | 依赖 |
| --- | --- | --- | --- | --- | --- |
| [MC-E01](exam-preparation.md#mc-e01) | 删除复述字段名称的创建页导语 | P3 | 删除 | summary：；details：删除这两段导语；保留字段标签、示例名称、必填及错误说明。 | 纯文案 |
| [MC-E02](exam-preparation.md#mc-e02) | 创建方式小字说明实际先后顺序 | P2 | 缩短 | summary：按创建方式显示一句区别。；variants：{"materials":"创建后上传试卷，识别题目与分值。","quick":"先创建考试，稍后补充资料。","template":"带入方案中的科目和试卷结构。"} | 纯文案 |
| [MC-E03](exam-preparation.md#mc-e03) | 合并反复出现的创建后上传预告 | P3 | 合并、缩短 | summary：创建后按学科上传试卷、答案或评分资料。；details：只在第4节说明一次；删除逐科相同小字和页脚重复句。无学科时显示“请先添加学科。” | 纯文案 |
| [MC-E04](exam-preparation.md#mc-e04) | 保留阅卷组织与逐题策略的边界 | P2 | 缩短、保留 | summary：这里设置阅卷组织方式；各题判分策略在准备确认时冻结。AI 建议须由教师确认。 | 纯文案 |
| [MC-E05](exam-preparation.md#mc-e05) | 考试方案抽屉保留带入范围 | P3 | 缩短、保留 | summary：带入科目、满分、时长和已有题目结构，选择后可调整。 | 纯文案 |
| [MC-E06](exam-preparation.md#mc-e06) | 学生范围说明按冻结前后分别表达 | P2 | 缩短、条件显示 | summary：选择参考班级；确认准备时，按当前在读学籍冻结本场名单。；variants：{"frozen":"名单已冻结，不能再改范围或同步。名单有误请联系考试负责人；修改学籍不会改变本场名单。"} | 前端配套 |
| [MC-E07](exam-preparation.md#mc-e07) | 准备页成功与只读提示去重 | P1 | 合并、条件显示 | summary：用一条当前状态说明替代多段。；variants：{"editable":"确认后将冻结学生名单、题目、答案、评分配置和答题区域，不能直接修改。","confirmed":"准备已确认，名单与配置已冻结。","collectingConfirmed":"正在导入答卷；准备配置已冻结。","frozenWithoutCurrentConfirmation":"当前阶段只读，准备确认记录与当前配置不匹配；请联系考试负责人核对。"} | 前端配套 |
| [MC-E08](exam-preparation.md#mc-e08) | 最终冻结确认保持完整后果 | P1 | 保留、缩短、删除 | summary：确认后将冻结本场准备；details：按当前在读学籍生成最终学生名单，并冻结题目、答案、评分方案、评分细则和答题区域；之后不能直接修改。；remove：删除弹窗中的阅卷/成绩入口导航说明。 | 纯文案 |
| [MC-E09](exam-preparation.md#mc-e09) | 非阻断提醒保留“可继续”语义 | P3 | 缩短、保留 | summary：仅提醒，不影响准备确认 | 纯文案 |
| [MC-E10](exam-preparation.md#mc-e10) | 粘贴资料帮助分层，保留真实发送范围 | P1 | 展开、缩短 | summary：粘贴题目、答案或解析，至少20个字符；建议保留题号。；modelNotice：结构清晰的文本在本地解析；无法可靠确定结构时，相关新增原文会送入模型解析。不会重发已识别的历史资料。；uploadNotice：需要模型解析时，发送本次新增资料的相关原文片段；图片资料发送本次新增图片页面。不会重发已识别的历史资料。；helpTitle：公式格式帮助；formatHelp：行内公式用 $x^2$，独立公式用 $$...$$；也支持 \(...\) 与 \[...\]。；scopeNote：uploadNotice 须新增在 PaperImportWorkspace 文件上传入口；当前该入口没有单独的模型发送说明。这是前端配套要求，不能只删除文本弹窗里的图片说明。 | 前端配套 |
| [MC-E11](exam-preparation.md#mc-e11) | 上传提示只说明资料与格式 | P2 | 缩短、合并 | summary：添加试题、答案、解析或评分标准，识别后请核对匹配结果。；details：PDF、Word（.docx）、图片、Markdown、TXT；支持多文件。 | 纯文案 |
| [MC-E12](exam-preparation.md#mc-e12) | 连接失败时突出旧状态与原任务恢复 | P1 | 缩短、保留 | summary：结果状态未更新；details：下方是上次读取的结果。请重新检查原任务；${connectionError}；button：重新检查原任务 | 纯文案 |
| [MC-E13](exam-preparation.md#mc-e13) | 资料登记说明保留与正式配置的区别 | P2 | 缩短、保留 | summary：按当前状态显示。；variants：{"unconfirmed":"先核对完整试卷并采用本轮题目结构。","invalid":"请补充或重新识别完整试卷；答案和解析不能代替试卷。","completed":"资料已登记；答案、分值和评分配置以“小题与分值”页为准。","pending":"确认试卷资料完整后，继续核对小题与分值。"} | 纯文案 |
| [MC-E14](exam-preparation.md#mc-e14) | 结构预览摘要与最终警告各承担一个任务 | P1 | 缩短、保留 | summary：确认完整试卷与题目范围；分值和评分配置在下一步核对。；modalTitle：请核对完整试卷；modalDetails：部分页面、答案或解析不能作为减少题目的依据。缺少可靠分值的小题仍需核对，不能直接阅卷；已有答案和解析保留。 | 纯文案 |
| [MC-E15](exam-preparation.md#mc-e15) | 保留结构与完整资料的明确勾选 | P1 | 保留、缩短 | summary：保留两个业务节点各自的明确勾选。；variants：{"structureAdoption":"我已核对完整试卷，以上题目结构与实际试卷一致。","materialsRegistration":"我已核对完整试卷，题目范围与实际试卷一致。"}；details：小题分值在下一步核对。 | 纯文案 |
| [MC-E16](exam-preparation.md#mc-e16) | 答题卡空页只说上传与核对任务 | P2 | 合并、缩短、展开 | summary：上传答题卡，核对识别出的题目与答题区域。；details：支持PDF或多张PNG、JPG图片。；reuseHelp：空白卡和已填写答题卡均可辅助识别；收录学校模板库前须使用不含个人信息的空白来源。 | 前端配套 |
| [MC-E17](exam-preparation.md#mc-e17) | 等待页序时去掉模型术语 | P3 | 缩短 | summary：正在识别页序… | 纯文案 |
| [MC-E18](exam-preparation.md#mc-e18) | 学校库收录成功后只保留下一步边界 | P3 | 缩短、保留 | summary：复用学校模板，或收录当前空白答题卡供以后使用。；success：已收录到学校库；本场仍需“确认答题卡设置”。 | 纯文案 |
| [MC-E19](exam-preparation.md#mc-e19) | 模板收录保留空白来源和逐页范围核对 | P1 | 保留、缩短、合并 | summary：收录会保存并锁定当前版本，供学校同版式考试复用；本场启用另行确认。；blankCheckbox：底图为空白答题卡，不含学生姓名、学号、条码、手写答案等个人信息。；layoutCheckbox：已逐页核对题号与完整作答区域，适用于同版式答题卡。；details：如使用已填写的答题卡，请先更换为核对过的空白来源。 | 纯文案 |
| [MC-E20](exam-preparation.md#mc-e20) | 模板版本、锁定和本场使用说明分支准确 | P1 | 缩短、保留 | summary：确认后保存并锁定当前版本；修改区域需另建版本。；variants：{"unbound":"此版本尚未用于本场考试，请确认答题卡设置。","boundAuto":"已自动识别为第${selectedTemplate.version_no}版，请核对后确认用于本场。"} | 纯文案 |
| [MC-E21](exam-preparation.md#mc-e21) | 精确坐标说明保留参照系，已折叠不再隐藏 | P3 | 保留、缩短 | summary：以整页为参照，单位为百分比；左、上是距页边的位置，宽、高是区域尺寸。；optionHelp：选项坐标以本题区域为参照，单位为百分比；每框只覆盖一个填涂点。 | 纯文案 |
| [MC-E22](exam-preparation.md#mc-e22) | OMR校准只压缩措辞，不省资格条件 | P1 | 缩短、保留 | summary：用同版空白卡对照识别；校准通过前，结果仅供参考，不计入成绩。；variants：{"approved":"校准已生效：识别把握≥98%且满足自动确认条件的结果可自动确认，其余转人工复核。","draft":"只按答题图片标注；提交后才显示机器结果，标注不能修改。"} | 纯文案 |
| [MC-E23](exam-preparation.md#mc-e23) | 打印说明保留唯一条码和补打后果 | P1 | 缩短、保留 | summary：按班级生成带学生身份条码的PDF，不打印姓名或学号。；issueHelp：每人每页的条码唯一，请勿重复复印或打乱页面；补打请使用“作废旧卡并补打”。；scopeHelp：仅为应考且没有有效答题卡的学生生成。；reprintHelp：确认后旧条码作废并生成新卡；请收回旧卡。已扫描的旧卡将进入核验。 | 纯文案 |
| [MC-E24](exam-preparation.md#mc-e24) | 导入页序规则用短提示加完整规则 | P1 | 缩短、展开 | summary：每人${pagesPerStudent ?? '待选择'}页，按学生连续排列；每次须选择完整答卷。；details：按文件选择顺序及文件内页序，每N页组成一份答卷。支持每人1–20页；PDF/TIFF按解码页数计算。每次选择独立分组，不能跨两次选择补页；缺页须处理后才能继续。；helpTitle：查看页序与分组规则 | 前端配套 |
| [MC-E25](exam-preparation.md#mc-e25) | 材料替代/排除/取消用各自准确后果 | P1 | 条件显示、缩短、保留 | summary：按原操作类型呈现。；variants：{"cancelBatch":"取消后，本批全部材料和未完成上传不再计入核对。请确认没有需保留的材料；对应学生仍需补传或核实缺考。","replace":"请选择每人页数，并一次上传原选择对应的全部完整答卷。新文件登记成功后，原选择的全部文件及答卷退出有效输入，历史保留；新答卷处理成功才算导入完成。已有评分的材料不能在此移除。","exclude":"原选择的全部文件及答卷将不再计入，历史保留；对应学生仍需补传或核实缺考。已有评分的材料不能在此移除。","attendance":"请核实学生情况并填写原因，本次名册调整会留有记录。"} | 纯文案 |
| [MC-E26](exam-preparation.md#mc-e26) | 结束导入保留人数与阶段变化，去掉同页重复解释 | P1 | 缩短、保留 | summary：正常通过的页面无需逐页确认；仍请核对材料问题。；dialog：应收${summary.required_count}人，成功导入${summary.ready_count}人，缺考${summary.absent_count}人。结束时再次核对学生与材料，并封存已处理批次。；variants：{"collecting":"确认后进入阅卷。","supplement":"本次补传结束，当前阅卷阶段保持不变。"} | 纯文案 |

## grading-release

| 编号 | 页面/标题 | 优先级 | 处置 | 目标文案或执行说明 | 依赖 |
| --- | --- | --- | --- | --- | --- |
| [MC-G01](grading-release.md#mc-g01) | 阅卷设置 / 本场大题 AI 方案 | P2 | 合并、条件显示 | summary：仅用于解答、计算、证明题；选择题和填空题不使用。可确认单模型方案。；details：设置页仅在顶部保留此句；已保存方案卡内省略范围句。启动评分弹窗独立展示范围句。 | 前端配套 |
| [MC-G02](grading-release.md#mc-g02) | 阅卷设置 / 分差规则 | P2 | 条件显示、展开、缩短 | summary：按所选模型显示当前规则。；single：当前为单模型，无需填写分差。建议分仍需教师确认。；dual：两个分差均必填，可填 0；第二项不得小于第一项。A、B 分差在第一项以内时取均分，超过则转教师处理。；triple：两个分差均必填，可填 0；第二项不得小于第一项。A、B 分差超过第一项时才调用第三模型。；details：“计算规则”展开内容：按未取整分比较；相差恰好等于第一项时不触发第三评分。三评按 A 25%、B 25%、C 50% 汇总，最后向上取整且不超过满分。C 与 A、B 均分的差值超过第二项，或三者极差超过第二项的两倍时，转教师处理。作答不清或模型调用失败，不直接计零分。仅展示当前所选模式适用的计算段落；已保存方案与编辑中方案分别标明，不能混用。 | 前端配套 |
| [MC-G03](grading-release.md#mc-g03) | 阅卷设置 / 图片能力检测 | P2 | 缩短、保留 | summary：检测可能调用模型；同配置的检测结果可复用。保存方案不会调用模型。 | 纯文案 |
| [MC-G04](grading-release.md#mc-g04) | 阅卷设置 / 只读方案 | P2 | 条件显示、合并 | summary：按已确认状态显示不同提示。；confirmed：方案已确认。如需修改，请联系学校管理员。；unconfirmed：方案尚未确认，请联系学校管理员。 | 前端配套 |
| [MC-G05](grading-release.md#mc-g05) | 阅卷设置 / 保存方案按钮 | P1 | 缩短、保留 | summary：新方案仅用于之后启动的评分；已启动任务仍用原方案。；details：AI 建议分仍需教师确认。 | 纯文案 |
| [MC-G06](grading-release.md#mc-g06) | 启动评分 / 评分路线确认 | P1 | 缩短、条件显示 | summary：将按以下路线处理作答。AI 建议分仍需教师确认；启动后可关闭页面。 | 纯文案 |
| [MC-G07](grading-release.md#mc-g07) | 阅卷台 / 最终分录入 | P1 | 展开、条件显示、保留 | summary：将交分后果移到最终分输入框旁。；normal：本次修改有效得分后将自动提交；如需稍后核对，请先标记答卷。；bookmarked：已标记：当前仅编辑草稿；取消标记后恢复自动提交。；details：键盘帮助仅保留：G 定位最终分；1–9 录分；A 采纳建议分。F 标记；R 退回重评；Z 撤销未提交修改；+/- 缩放。输入框内不会触发单字符操作。 | 前端配套 |
| [MC-G08](grading-release.md#mc-g08) | 阅卷台 / 草稿版本冲突 | P1 | 缩短、保留 | summary：草稿已被其他会话更新；details：自动保存已暂停。请逐项选择要保留的内容，或放弃本页修改。 | 纯文案 |
| [MC-G09](grading-release.md#mc-g09) | 阅卷任务抽屉 / 筛选范围 | P2 | 缩短、保留 | summary：筛选选项仅含已加载任务；可搜索考试名称。 | 纯文案 |
| [MC-G10](grading-release.md#mc-g10) | 阅卷前试评 / 顶部引导 | P2 | 合并、条件显示 | summary：独立完成全部样本；每份提交后才显示参考分，完成后判定是否通过。；admin：请先配置通过标准，并准备足量的已批准标准卷。 | 前端配套 |
| [MC-G11](grading-release.md#mc-g11) | 阅卷前试评 / 已获资格提示 | P1 | 缩短、保留 | summary：试评已通过；details：有效至 {日期}；标准卷变化后需重新确认资格。 | 纯文案 |
| [MC-G12](grading-release.md#mc-g12) | 阅卷前试评 / 通过标准字段说明 | P2 | 缩短、保留 | summary：所有已设置条件均须达标。标准卷须已批准，且题型、满分一致。；fields：{"平均分差上限":"各样本与参考分的绝对差，再取平均。","分数完全相同的最低比例":"例如 60% 表示至少六成样本同分。","采分点判断相同的最低比例":"须有可比较的采分点数据。","严重分差的最高比例":"0% 表示不允许严重分差。","相差几分算严重分差":"相差达到此值即计入（含等于）。"} | 纯文案 |
| [MC-G13](grading-release.md#mc-g13) | 阅卷前试评 / 未读到通过标准 | P1 | 条件显示、缩短 | summary：暂未读到本题通过标准，当前不能开始试评。请联系管理员核对。；admin：暂未读到本题通过标准。请核对并保存标准，同时准备已批准的标准卷。 | 前端配套 |
| [MC-G14](grading-release.md#mc-g14) | 成绩检查 / 检查通过状态 | P1 | 缩短、条件显示、保留 | summary：基础成绩检查通过；locked：基础成绩已锁定；本项检查通过。门户发布状态见版本区域。；unlocked：本项检查通过；请继续核对门户发布检查。；dashboardTitle：评分质量检查；dashboardReady：未发现阻断性质量事件；其他发布条件以成绩发布检查为准。 | 纯文案 |
| [MC-G15](grading-release.md#mc-g15) | 成绩工作台 / 发布流程导语 | P2 | 合并、缩短 | summary：汇总并确认 → 创建门户草稿 → 核对学生预览 → 发布。创建草稿不会公开给学生。；actionPanel：汇总并确认基础成绩。 | 纯文案 |
| [MC-G16](grading-release.md#mc-g16) | 成绩工作台 / 发布门禁结果 | P2 | 缩短、展开、保留 | summary：发布检查；loading：正在读取发布检查，暂不能发布。；passed：发布检查通过；正式发布时将再次检查。；details：问题标题、阻断/提醒和影响数量常显；内部代码移至“技术详情”，便于排障时复制。 | 前端配套 |
| [MC-G17](grading-release.md#mc-g17) | 成绩工作台 / 基础成绩锁定与原查询入口 | P1 | 缩短、保留 | summary：更正须通过申诉或复评；学生门户状态见版本区域。；legacy：仅用于学校原查询入口。公开后锁定基础成绩；学生门户需另行发布。 | 纯文案 |
| [MC-G18](grading-release.md#mc-g18) | 创建门户草稿 / 冻结设置提示 | P1 | 缩短、保留 | summary：创建草稿，不会公开给学生；details：成绩和以下公开设置将随草稿冻结。核对学生预览后，再正式发布。 | 纯文案 |
| [MC-G19](grading-release.md#mc-g19) | 创建门户草稿 / 逐题公开与复核选项 | P1 | 缩短、保留 | summary：开放复核须公开逐题得分；当前仅支持逐题申请。；hidden：关闭后不显示任何逐题详情，包括题目、答案和反馈。 | 纯文案 |
| [MC-G20](grading-release.md#mc-g20) | 创建门户草稿 / 匿名范例卷条件 | P1 | 缩短、保留 | summary：发布前自动生成匿名范例卷；details：最高分答卷每页须有已锁定模板、成功配准和有效身份区，否则无法发布。请逐页核对身份区确实覆盖姓名、学号、二维码等身份信息；系统仅按配置区域遮挡，不能判断是否遗漏。 | 纯文案 |
| [MC-G21](grading-release.md#mc-g21) | 复评候选复核 / 接受与驳回 | P1 | 缩短、保留 | summary：候选意见尚未改写成绩；details：请逐项完成复核。需要更正时，仍须生成并发布新成绩版本；原版本不变。；exception：请核对证据，填写处理说明后接受或驳回。 | 纯文案 |
| [MC-G22](grading-release.md#mc-g22) | 题目复评 / 创建与预览说明 | P2 | 合并、展开、缩短 | summary：复评只处理所选题目，当前公布成绩不变。；preview：来源：正式成绩 V{版本} · 影响 {份数} 份答卷。创建时将冻结该版本对应的题目范围。；afterCreate：任务完成后，需生成并发布新成绩版本。 | 前端配套 |
| [MC-G23](grading-release.md#mc-g23) | 正式发布 / 最终确认 | P1 | 缩短、保留 | summary：请先核对学生预览。发布后，学生门户将展示此版本；学校原查询入口状态不变。更正须创建新版本。；appealsEnabled：此版本启用逐题复核；学生可在开放时段内从逐题详情申请。；appealsDisabled：此版本未开放学生复核申请。 | 纯文案 |
| [MC-G24](grading-release.md#mc-g24) | 学生题目复核 / 教师独立意见 | P2 | 合并、条件显示、缩短 | summary：核对原公布材料后提交独立意见。意见和建议分仅供教职工查看，不直接改分。；pending：意见已移交学校管理员，当前成绩未改变。；handled：此处保留教师建议；处理结果以管理员答复和正式成绩为准。 | 前端配套 |
| [MC-G25](grading-release.md#mc-g25) | 复核中心 / 教师导语与历史入口 | P2 | 缩短、保留 | summary：核对分派给我的匿名材料，提交独立意见，由学校管理员继续处理。；history：学生门户新申请见“学生门户申请”；本页处理历史申诉记录。 | 纯文案 |
| [MC-G26](grading-release.md#mc-g26) | 历史申诉 / 独立教师复核 | P1 | 缩短、保留 | summary：请独立核对答卷和评分标准。学生身份及原阅卷人已隐藏；意见由管理员终审，不直接改分或结案。 | 纯文案 |
| [MC-G27](grading-release.md#mc-g27) | 复核表单 / 未提交内容与离开提醒 | P1 | 缩短、条件显示、保留 | summary：提交后才会保存。；dirty：有未提交内容；切换、关闭或刷新后无法恢复。；confirmTitle：放弃未提交内容？；confirmBody：当前意见、回复和复评选择尚未提交，离开后无法恢复。；confirmButtons：["放弃并继续","继续编辑"] | 前端配套 |
| [MC-G28](grading-release.md#mc-g28) | 质量总览 / 回标转复评提示 | P2 | 合并、条件显示、展开 | summary：回标仅记录差异，当前成绩不变。；handoff：仅已完成且标记“需重评”的项目可转入题目复评；范围以已发布成绩版本为准，不采用回标候选分。；preview：来源成绩 V{版本} · 接手教师：{教师} · 将冻结 {份数} 份答卷。创建后仍需审批、独立复评、差异复核并发布新版本。 | 前端配套 |

## portals-ops

| 编号 | 页面/标题 | 优先级 | 处置 | 目标文案或执行说明 | 依赖 |
| --- | --- | --- | --- | --- | --- |
| [MC-P01](portals-ops.md#mc-p01) | 学生端登录 | P2 | 缩短、合并 | summary：使用学校发放的学生账号，查看本人已发布成绩。；details：{"help":"学校代码、账号或密码有问题？请联系学校管理员。"} | 纯文案 |
| [MC-P02](portals-ops.md#mc-p02) | 逐题复核申请与回执 | P2 | 缩短、合并 | summary：第 {releaseVersion} 版：本题仅可申请一次，学校将核对这一版的原卷与评分标准。；details：{"receipt":"已收到第 {releaseVersion} 版的本题申请，不可重复提交。","link":"查看申请与处理结果","button":"提交复核申请"} | 纯文案 |
| [MC-P03](portals-ops.md#mc-p03) | 复核草稿保存状态 | P1 | 缩短、条件显示、保留 | summary：草稿仅存于当前标签页会话，切题、返回或重新登录可恢复；退出登录会清除。；details：{"failure":"草稿未能保存，仅在本页保留。刷新或关闭前，请先复制说明。"} | 纯文案 |
| [MC-P04](portals-ops.md#mc-p04) | 成绩换版与核对失败 | P1 | 缩短、条件显示、保留 | summary：第 {pendingResult.release_version} 版成绩已发布。旧草稿可复制；新版需重新核对后申请。；details：{"failure":"未能核对最新成绩，现显示此前读取的第 {result.release_version} 版。{error}"} | 纯文案 |
| [MC-P05](portals-ops.md#mc-p05) | 我的复核结果与当前生效成绩 | P1 | 合并、条件显示、保留 | summary：状态仅显示一次；学校处理决定与当前进度分开表达。当前生效版在列表头显示一次，各申请保留来源与关联关系。；details：{"waiting":"学校复核中","rejected":"未支持申请","regrading":"已支持申请，等待重评结果发布。","sameRelease":"本次复核关联的{version}已是当前生效成绩{scoreSuffix}。","differentRelease":"复核结果已关联{version}；当前生效为第 {current.release_version} 版，请查看当前成绩。","unverified":"已关联{version}；当前生效成绩尚未核实。","noLinkedRelease":"本次申请尚未关联后续成绩版本。","rejectDecision":"学校决定：未支持本次申请。","referRegradeDecision":"学校决定：支持复核，交由重新评分。","unknownStatus":"处理中"} | 前端配套 |
| [MC-P06](portals-ops.md#mc-p06) | 旧版未提交草稿 | P2 | 缩短、保留 | summary：可复制旧说明；旧版圈选不会迁移，新版需重新核对。草稿仅存于当前标签页会话，退出登录会清除。 | 纯文案 |
| [MC-P07](portals-ops.md#mc-p07) | 逐题参比范围小字 | P2 | 合并、缩短 | summary：统计范围：本版成绩中，本次考试本题已发布成绩的参试学生，非班级人数。；details：{"count":"参比 {count} 人","unknown":"参比人数暂未提供"} | 前端配套 |
| [MC-P08](portals-ops.md#mc-p08) | 桌面连接与登录帮助 | P2 | 缩短、合并、条件显示 | summary：服务器地址和学校代码由学校提供。填写后即可登录；连接失败时可先检查连接。；details：{"account":"使用学校分配的账号登录。","remember":"密码仅保存到当前 Windows 用户的系统凭据库。"} | 纯文案 |
| [MC-P09](portals-ops.md#mc-p09) | 桌面本机离线授权 | P1 | 缩短、保留、条件显示 | summary：允许本机离线解锁本账号草稿；details：{"before":"在线登录后生效，仅当前 Windows 用户可用。有效期不超过本次登录，最长七天；退出登录或清除保存信息会撤销。","after":"可离线解锁至 {expiresAt}；仅限本机加密数据，获取任务和上传仍需在线登录。"} | 前端配套 |
| [MC-P10](portals-ops.md#mc-p10) | 离线材料与图片持久化限制 | P1 | 合并、缩短、保留 | summary：加密保存：任务、冻结细则、评分草稿。答卷图片仅本次会话可用，不随草稿保存或恢复；重启或加载草稿后需联网恢复。；details：{"saved":"任务与评分草稿已加密保存；答卷图片未保存。","notSaved":"任务与评分草稿尚未保存。","key":"密钥由当前 Windows 用户的系统凭据库保护。","browser":"浏览器开发模式仅用于显式加密草稿回退，不能验证生产扫描或上传恢复。"} | 前端配套 |
| [MC-P11](portals-ops.md#mc-p11) | 离线密钥状态标签 | P1 | 条件显示、缩短 | summary：原生：使用本机加密存储；details：{"browserReady":"开发密钥已设置","browserMissing":"请设置开发密钥（至少 8 位）"} | 前端配套 |
| [MC-P12](portals-ops.md#mc-p12) | 评分同步结果待确认 | P1 | 缩短、保留 | summary：上次提交结果尚未确认，分数和评语暂不可修改。再次同步会先查询上次提交结果。 | 纯文案 |
| [MC-P13](portals-ops.md#mc-p13) | 扫描上传与质量检查说明 | P2 | 缩短、合并、保留 | summary：请选择考试和可上传的采集批次。；details：{"queue":"当前考试与批次的待上传文件。上传后仍需服务端匹配与质量检查。","server":"上传确认仅表示已收到文件；请分别查看页数和图像质量，图像通过后才能继续识别与阅卷。","pagePassed":"页数检查通过；图像质量以下方结果为准。"} | 纯文案 |
| [MC-P14](portals-ops.md#mc-p14) | 桌面诊断与本地记录来源 | P2 | 缩短、合并、保留 | summary：客户端错误日志：仅本机记录，级别为 error。；details：{"runtime":"当前客户端运行环境（原生／浏览器开发模式）。","server":"学校服务器状态；未配置的依赖会单独标明。","events":"本机关键操作记录。学校审计请由有权限的管理员在管理端查看。"} | 纯文案 |
| [MC-P15](portals-ops.md#mc-p15) | 归档与永久删除本机原件 | P1 | 保留、缩短 | summary：归档只隐藏队列，不释放磁盘。原件保留在本机，目前不会自动清理。；details：{"confirmation":"学校要求保留 {days} 天。将永久删除所选本机加密原件，上传回执保留；本机无法恢复。请先确认服务端备份和学校保留要求。"} | 纯文案 |
| [MC-P16](portals-ops.md#mc-p16) | 验证器试点与更换/丢失帮助 | P1 | 缩短、展开、保留 | summary：可选试点：目前仅验证本页的验证器管理操作，登录和阅卷尚不强制使用。；details：{"readOnly":"当前为公共电脑或服务会话，仅可查看。请在个人设备登录后管理验证器。","change":"更换验证器：先验证并关闭，重新登录后再设置。","lost":"手机和恢复码都丢失时，请联系学校管理员记录问题。目前无自动重置入口，重置密码也不会清除验证器；普通登录仍可使用。"} | 前端配套 |
| [MC-P17](portals-ops.md#mc-p17) | 验证器手动设置与技术参数 | P2 | 展开、缩短、保留 | summary：在验证器中添加账号并扫码。无法扫码？手动输入密钥。；details：{"secretWarning":"请勿截图、分享二维码或密钥，也不要上传到第三方网站。","codeHelp":"动态码即将刷新时请等新码；成功验证的码不能再用。持续失败请核对手机时间。设置剩余 {deadline}。"} | 前端配套 |
| [MC-P18](portals-ops.md#mc-p18) | 恢复码一次性显示与已生效状态 | P1 | 合并、缩短、保留 | summary：验证器已启用。请安全保存恢复码；这批码关闭、离页或 {deadline} 后不再显示。；details：{"usage":"每条仅可使用一次，仅用于本试点关闭验证器；不能登录、改密码或验证其他业务。","rotate":"恢复码已更新，旧码已全部失效。请安全保存这批新码。","copyToast":"已复制，请粘贴到安全位置；复制不等于已保存。","download":"下载的是明文文件，请转存安全位置并清理下载目录；下载不等于已安全保存。"} | 前端配套 |
| [MC-P19](portals-ops.md#mc-p19) | 恢复码验证即消耗 | P1 | 保留、缩短 | summary：恢复码验证成功即作废，随后取消关闭也不能再用。恢复码仅可验证关闭，不能用来重新生成恢复码。；details：{"after":"本次恢复码已消耗。确认关闭后需重新登录，再设置验证器。"} | 纯文案 |
| [MC-P20](portals-ops.md#mc-p20) | 人员字段留空的不同含义 | P1 | 保留、缩短、条件显示 | summary：教职工号：清空后会移除原教职工号。；details：{"phone":"新手机号：留空不修改，填写后替换当前号码。","stepUp":"确认保存人员资料；登录身份变化后，该人员需重新登录。"} | 纯文案 |
| [MC-P21](portals-ops.md#mc-p21) | 班级代码与导入关联 | P2 | 条件显示、缩短、保留 | summary：用于匹配学生导入文件中的班级代码。；details：{"changed":"班级代码已改，后续学生导入文件请使用新代码。"} | 前端配套 |
| [MC-P22](portals-ops.md#mc-p22) | 模型 API Key 分支帮助 | P1 | 条件显示、缩短、保留 | summary：已保存的密钥不回显；留空继续使用。；details：{"changed":"API 域名或端口已变，请重新输入密钥；可以使用原密钥。","new":"密钥将加密保存。保存时检查连接，不生成回复。","reuse":"此配置将单独验证并加密保存；后续更换密钥需逐个更新配置。"} | 前端配套 |
| [MC-P23](portals-ops.md#mc-p23) | 获取模型列表的零 Token 术语 | P2 | 缩短、合并、保留 | summary：可获取模型列表，也可直接输入模型名称；获取列表不生成回复。 | 纯文案 |
| [MC-P24](portals-ops.md#mc-p24) | 自动阅卷用途与图片检测 | P1 | 合并、缩短、条件显示、保留 | summary：生成评分建议，教师确认最终分数。本校自动阅卷独立配置 API 模型，不回退本地模型。；details：{"notSaved":"先保存本校自动阅卷模型，再检测图片能力。","probe":"检测发送合成图片，会产生少量模型用量。密钥停用或配置变化后需重新检测。","roles":"多模型评审角色（可选；单模型可直接测试）"} | 前端配套 |
| [MC-P25](portals-ops.md#mc-p25) | 模型页全局用途指南 | P2 | 展开、合并、缩短 | summary：按用途独立配置学校模型；连接通过不代表已通过各用途检测。；details：{"helpTitle":"用途与检测说明","dialogue":"日常对话：普通回复检测通过后可开放多个模型，学校管理员在聊天页切换。","document":"试卷解析：通过结构化检测后独立指定或跟随默认配置；聊天切换不会改变此设置。","answerSheet":"答题卡识别：独立指定多模态模型，人工核对后发布。","grading":"自动阅卷：独立指定 API 模型并检测图片能力，不回退本地；正式使用仍需准入与最终确认。"} | 前端配套 |
| [MC-P26](portals-ops.md#mc-p26) | 专业模型诊断与返回摘要 | P2 | 保留、缩短 | summary：本次返回摘要（最多 256 字符，不写入数据库）；details：{"noPreview":"供应商未提供可展示的返回摘要。"} | 纯文案 |
| [MC-P27](portals-ops.md#mc-p27) | 学校策略预算零值与单位 | P1 | 缩短、保留 | summary：0 表示预算未配置，不能通过外部沙箱准入。；details：{"exam":"仅限这一场考试，非学校月总额度；最多六位小数。"} | 纯文案 |
