# 考试创建与准备：辅助小字执行方案

本报告重新核对当前源码，只交付方案，未修改业务代码。核对日期：2026-10-28；源码提交：d375c2501c57dd7501977602be09a6f051c7733e。

共 26 项：21 项纯文案，5 项需前端调整，无需新增后端能力。证据共 69 处。逐文件记录 52 份：全文 15、片段 7、未读 30；没有把枚举或上一轮阅读当作本轮全文审读。未运行浏览器，验收场景待实现时验证。

方案保留冻结、完整性核对、版本绑定、数据发送范围、学生身份条码、不可逆操作和失败恢复等必要约束。已有折叠保持现状。依赖 `copy` 仅改已有文案及重复小字容器；`frontend` 需新增披露、移动位置或按真实状态呈现，均不是已实现承诺。

<a id="mc-e01"></a>

## MC-E01 · P3 · 删除复述字段名称的创建页导语

- 页面 / 角色：新建考试 / 学校考试管理员；有对应权限的教师
- 当前出现条件：创建表单始终显示基本信息和添加学科两段说明。
- 问题：字段标签与占位符已经表达任务；第一段只说明系统内部用途，第二段重复下方操作。
- 处置：delete；依赖：copy

源证据（原样连续文本；`excerpt` 为明确截取，`template` 含动态变量）：

- [apps/web-admin/src/features/exams/create/CreateExamComposer.tsx:183](../../apps/web-admin/src/features/exams/create/CreateExamComposer.tsx#L183) · literal
  > 名称用于考试列表；类型和年级决定后续可选范围。
- [apps/web-admin/src/features/exams/create/CreateExamComposer.tsx:205](../../apps/web-admin/src/features/exams/create/CreateExamComposer.tsx#L205) · literal
  > 选择本场考试包含的学科，并设置每科满分和考试时间。

建议准确文案：

```json
{
  "summary": "",
  "details": "删除这两段导语；保留字段标签、示例名称、必填及错误说明。"
}
```

目标出现条件：沿用当前表单；两处不再显示导语。

目标位置：基本信息、添加学科标题下方

边界：

- 不能删除学校/年级、班级范围和必填校验。

风险：

- 删除导语后若连字段标签、示例或校验一起去掉，首次创建会失去输入依据。

验收场景：

- 空表单仍能知道哪些字段必填。
- 名称示例、年级选项和科目分值/分钟单位仍可见。

<a id="mc-e02"></a>

## MC-E02 · P2 · 创建方式小字说明实际先后顺序

- 页面 / 角色：新建考试 / 学校考试管理员；有对应权限的教师
- 当前出现条件：三种创建方式卡片同时显示。
- 问题：第一项容易被理解为当前表单可立即上传；第二项偏泛；三项应只解释选择差异。
- 处置：shorten；依赖：copy

源证据（原样连续文本；`excerpt` 为明确截取，`template` 含动态变量）：

- [apps/web-admin/src/features/exams/create/CreateExamComposer.tsx:22](../../apps/web-admin/src/features/exams/create/CreateExamComposer.tsx#L22) · literal
  > 上传完整试卷识别题目与分值，无需预填题目数量
- [apps/web-admin/src/features/exams/create/CreateExamComposer.tsx:23](../../apps/web-admin/src/features/exams/create/CreateExamComposer.tsx#L23) · literal
  > 先建立考试与科目，稍后继续完善资料和设置
- [apps/web-admin/src/features/exams/create/CreateExamComposer.tsx:24](../../apps/web-admin/src/features/exams/create/CreateExamComposer.tsx#L24) · literal
  > 套用学校或系统方案中的科目和试卷结构

建议准确文案：

```json
{
  "summary": "按创建方式显示一句区别。",
  "variants": {
    "materials": "创建后上传试卷，识别题目与分值。",
    "quick": "先创建考试，稍后补充资料。",
    "template": "带入方案中的科目和试卷结构。"
  }
}
```

目标出现条件：继续使用现有创建模式和后续跳转，不改变提交行为。

目标位置：各创建方式标题下

边界：

- 不能承诺当前页已支持上传、已自动确认识别结果或已带入方案不存在的设置。

风险：

- 小字若仍暗示当前页可以上传，会与创建后才进入资料页的真实顺序冲突。

验收场景：

- 选择资料创建后仍先创建再到资料页。
- 三种模式小字不使用“已识别”“一键完成”。

<a id="mc-e03"></a>

## MC-E03 · P3 · 合并反复出现的创建后上传预告

- 页面 / 角色：新建考试 / 学校考试管理员；有对应权限的教师
- 当前出现条件：第4节、每科只读行及提交区同时预告后续上传。
- 问题：同一个下一步被重复说明；没有学科时“第2题”还把步骤叫题。
- 处置：merge / shorten；依赖：copy

源证据（原样连续文本；`excerpt` 为明确截取，`template` 含动态变量）：

- [apps/web-admin/src/features/exams/create/CreateExamComposer.tsx:228](../../apps/web-admin/src/features/exams/create/CreateExamComposer.tsx#L228) · literal
  > 考试创建后，按学科上传试卷、答案、解析和评分标准；系统会逐科识别，不混合资料。
- [apps/web-admin/src/features/exams/create/CreateExamComposer.tsx:229](../../apps/web-admin/src/features/exams/create/CreateExamComposer.tsx#L229) · literal
  > 创建后上传试卷与答案
- [apps/web-admin/src/features/exams/create/CreateExamComposer.tsx:229](../../apps/web-admin/src/features/exams/create/CreateExamComposer.tsx#L229) · literal
  > 先在第 2 题添加学科，资料入口会按学科生成。
- [apps/web-admin/src/features/exams/create/CreateExamComposer.tsx:251](../../apps/web-admin/src/features/exams/create/CreateExamComposer.tsx#L251) · excerpt
  > 创建后进入工作区按学科继续上传资料

建议准确文案：

```json
{
  "summary": "创建后按学科上传试卷、答案或评分资料。",
  "details": "只在第4节说明一次；删除逐科相同小字和页脚重复句。无学科时显示“请先添加学科。”"
}
```

目标出现条件：资料预告区域保留，学科与分值数据不改。

目标位置：第4节标题下一行；不再逐科重复

边界：

- 不承诺多科资料自动跨科分配；实际仍需选对学科。
- 本项不把只读预告改成上传控件。

风险：

- 合并时若删除按学科处理的范围，教师可能把不同学科资料混传；不能顺带重做创建流程。

验收场景：

- 多科考试只出现一次上传预告。
- 未选科目时不出现“第2题”。

<a id="mc-e04"></a>

## MC-E04 · P2 · 保留阅卷组织与逐题策略的边界

- 页面 / 角色：新建考试→阅卷与发布设置 / 学校考试管理员；有对应权限的教师
- 当前出现条件：仅展开第5节显示；当前已有渐进披露。
- 问题：需要解释考试级组织方式与单题策略不同，但可缩成两句；不应删除模型建议与最终分数的区别。
- 处置：shorten / keep；依赖：copy

源证据（原样连续文本；`excerpt` 为明确截取，`template` 含动态变量）：

- [apps/web-admin/src/features/exams/create/CreateExamComposer.tsx:245](../../apps/web-admin/src/features/exams/create/CreateExamComposer.tsx#L245) · literal
  > 此处选择阅卷组织方式。每道题实际采用的评分策略会在考试准备时核对并冻结，AI 建议不能代替教师确认。

建议准确文案：

```json
{
  "summary": "这里设置阅卷组织方式；各题判分策略在准备确认时冻结。AI 建议须由教师确认。"
}
```

目标出现条件：保持只在展开第5节后出现。

目标位置：阅卷方式选择下方

边界：

- 不得把AI建议写成自动最终成绩。
- 不将此句扩到全页常驻；不改现有双评/盲评选项。

风险：

- 过度压缩会把组织方式和实际逐题策略混为一谈，或暗示AI直接确认最终成绩。

验收场景：

- 收起高级设置时不额外出现解释。
- 选AI建议时仍明确教师确认。

<a id="mc-e05"></a>

## MC-E05 · P3 · 考试方案抽屉保留带入范围

- 页面 / 角色：新建考试→选择考试方案 / 学校考试管理员；有对应权限的教师
- 当前出现条件：方案抽屉打开时显示。
- 问题：范围具体、有助于选择；只需压缩措辞，不删除可调整说明。
- 处置：shorten / keep；依赖：copy

源证据（原样连续文本；`excerpt` 为明确截取，`template` 含动态变量）：

- [apps/web-admin/src/features/exams/create/CreateExamComposer.tsx:261](../../apps/web-admin/src/features/exams/create/CreateExamComposer.tsx#L261) · literal
  > 方案会带入科目、满分、时长和已有试卷结构，选中后仍可调整。

建议准确文案：

```json
{
  "summary": "带入科目、满分、时长和已有题目结构，选择后可调整。"
}
```

目标出现条件：与现有抽屉一致。

目标位置：抽屉标题下

边界：

- 只列实际带入字段，不能写成会带入学生名单、答案或正式评分配置。

风险：

- 若写成带入全部设置，会让教师误以为学生、答案和细则已完整继承。

验收场景：

- 选择方案后科目/满分/时长/结构确实对应；其他资料不作承诺。

<a id="mc-e06"></a>

## MC-E06 · P2 · 学生范围说明按冻结前后分别表达

- 页面 / 角色：考试准备→学生范围 / 学校考试管理员；有对应权限的教师
- 当前出现条件：顶部导语常驻；非draft/configured时额外显示冻结Alert。
- 问题：普通导语与冻结告警说明同一名单边界；应按状态给任务和影响。
- 处置：shorten / contextual；依赖：frontend

源证据（原样连续文本；`excerpt` 为明确截取，`template` 含动态变量）：

- [apps/web-admin/src/pages/ExamPreparationPage.tsx:136](../../apps/web-admin/src/pages/ExamPreparationPage.tsx#L136) · literal
  > 选择参加本场考试的班级；确认准备完成时，系统将按当前在读学籍生成最终冻结名单。
- [apps/web-admin/src/pages/ExamPreparationPage.tsx:137](../../apps/web-admin/src/pages/ExamPreparationPage.tsx#L137) · literal
  > 学生范围和名单同步已关闭。若发现漏选或名单错误，请联系考试负责人核对处理方案，不能通过修改学校学籍直接改变本场冻结名单。

建议准确文案：

```json
{
  "summary": "选择参考班级；确认准备时，按当前在读学籍冻结本场名单。",
  "variants": {
    "frozen": "名单已冻结，不能再改范围或同步。名单有误请联系考试负责人；修改学籍不会改变本场名单。"
  }
}
```

目标出现条件：草稿/配置中显示普通导语；后续阶段只显示冻结说明。

目标位置：范围页标题下保留一条状态说明

边界：

- 需要基于现有exam.status分支呈现；不能仅凭canManage判断冻结。
- 保留最终名册来源及学籍变化不影响冻结事实。

风险：

- 若把只读身份和冻结状态混成一条，教师可能误以为管理员可通过修改学籍改动正式名单。

验收场景：

- 草稿态可看到冻结时机。
- 已ready/collecting时不显示“选择班级”式可编辑暗示。
- 只读权限与已冻结两个原因不混写。

<a id="mc-e07"></a>

## MC-E07 · P1 · 准备页成功与只读提示去重

- 页面 / 角色：考试准备→确认准备完成 / 学校考试管理员；有对应权限的教师
- 当前出现条件：页头冻结导语始终显示；confirmed/frozen/collecting说明按状态可能并列。
- 问题：成功后仍读“确认后”导语，加上成功和已导入阶段说明，使当前状态与下一步分散。
- 处置：merge / contextual；依赖：frontend

源证据（原样连续文本；`excerpt` 为明确截取，`template` 含动态变量）：

- [apps/web-admin/src/pages/ExamPreparationPage.tsx:195](../../apps/web-admin/src/pages/ExamPreparationPage.tsx#L195) · literal
  > 准备已确认，当前配置有匹配的确认记录；准备配置只读。
- [apps/web-admin/src/pages/ExamPreparationPage.tsx:196](../../apps/web-admin/src/pages/ExamPreparationPage.tsx#L196) · literal
  > 当前没有匹配的准备确认记录，请联系考试负责人核对。
- [apps/web-admin/src/pages/ExamPreparationPage.tsx:197](../../apps/web-admin/src/pages/ExamPreparationPage.tsx#L197) · literal
  > 考试已进入答卷导入阶段，后续处理请前往答卷导入工作区。
- [apps/web-admin/src/pages/ExamPreparationPage.tsx:332](../../apps/web-admin/src/pages/ExamPreparationPage.tsx#L332) · literal
  > 确认后冻结学生名单、题目、答案、评分配置和答题区域，之后不能直接修改。

建议准确文案：

```json
{
  "summary": "用一条当前状态说明替代多段。",
  "variants": {
    "editable": "确认后将冻结学生名单、题目、答案、评分配置和答题区域，不能直接修改。",
    "confirmed": "准备已确认，名单与配置已冻结。",
    "collectingConfirmed": "正在导入答卷；准备配置已冻结。",
    "frozenWithoutCurrentConfirmation": "当前阶段只读，准备确认记录与当前配置不匹配；请联系考试负责人核对。"
  }
}
```

目标出现条件：沿用confirmed与editable实际判断组合；collecting且confirmed只显示collectingConfirmed；没有匹配记录优先保留异常说明。

目标位置：标题下单一状态行；下一步继续用现有“前往答卷导入”按钮

边界：

- 不能用历史confirmed时间掩盖当前配置不匹配。
- 状态说明不替代最终冻结弹窗的知情确认。

风险：

- 成功与当前配置不匹配是不同状态，错误合并会掩盖需要负责人核对的准备记录问题。

验收场景：

- ready、collecting、后续阶段记录不匹配三种状态各有准确一句。
- 无匹配确认不能显示“已确认”。

<a id="mc-e08"></a>

## MC-E08 · P1 · 最终冻结确认保持完整后果

- 页面 / 角色：考试准备→最终确认弹窗 / 学校考试管理员；有对应权限的教师
- 当前出现条件：用户点击确认准备完成、最新readiness预读通过后打开。
- 问题：不可逆的业务约束必须当面保留；后一句导航教程与决策无关。
- 处置：keep / shorten / delete；依赖：copy

源证据（原样连续文本；`excerpt` 为明确截取，`template` 含动态变量）：

- [apps/web-admin/src/pages/ExamPreparationPage.tsx:384](../../apps/web-admin/src/pages/ExamPreparationPage.tsx#L384) · literal
  > 确认后将冻结本场考试准备
- [apps/web-admin/src/pages/ExamPreparationPage.tsx:384](../../apps/web-admin/src/pages/ExamPreparationPage.tsx#L384) · literal
  > 系统将按当前在读学籍生成最终学生名单，并冻结题目、答案、评分方案、评分细则和答题区域，之后不能直接修改。
- [apps/web-admin/src/pages/ExamPreparationPage.tsx:385](../../apps/web-admin/src/pages/ExamPreparationPage.tsx#L385) · literal
  > 确认准备完成后，可前往答卷导入；阅卷与成绩请从各自入口进入。

建议准确文案：

```json
{
  "summary": "确认后将冻结本场准备",
  "details": "按当前在读学籍生成最终学生名单，并冻结题目、答案、评分方案、评分细则和答题区域；之后不能直接修改。",
  "remove": "删除弹窗中的阅卷/成绩入口导航说明。"
}
```

目标出现条件：仅原最终确认弹窗；异常提示仍独立可见。

目标位置：警告标题与正文，紧邻确认按钮

边界：

- 保留冻结对象、名单生成来源、不能直接修改及明确确认按钮。
- 不把准备检查通过写成已确认；不删最新版本核对。

风险：

- 删除冻结对象或不可直接修改的后果，会削弱最终确认的知情基础。

验收场景：

- 弹窗仍清楚说明名单及所有配置冻结。
- 回退修改和网络失败提示不变。

<a id="mc-e09"></a>

## MC-E09 · P3 · 非阻断提醒保留“可继续”语义

- 页面 / 角色：考试准备→提醒项 / 学校考试管理员；有对应权限的教师
- 当前出现条件：readiness.advisories存在时，每条Alert标题附小字。
- 问题：该标注能帮助区分必须处理与可稍后核对，应该保留；“开考”与准备任务不一致。
- 处置：shorten / keep；依赖：copy

源证据（原样连续文本；`excerpt` 为明确截取，`template` 含动态变量）：

- [apps/web-admin/src/pages/ExamPreparationPage.tsx:352](../../apps/web-admin/src/pages/ExamPreparationPage.tsx#L352) · literal
  > 提醒项，不阻断开考

建议准确文案：

```json
{
  "summary": "仅提醒，不影响准备确认"
}
```

目标出现条件：仍仅显示于advisories；不应用于checks和评分blocking_reason。

目标位置：提醒标题旁状态标签

边界：

- 必须以服务端非阻断advisory分类为依据，不能把阻断项换成非阻断。

风险：

- “仅提醒”若误用到阻断项，会让教师低估准备检查失败。

验收场景：

- 同时含失败检查和advisory时，只有advisory出现此标签。

<a id="mc-e10"></a>

## MC-E10 · P1 · 粘贴资料帮助分层，保留真实发送范围

- 页面 / 角色：考试资料→粘贴文本 / 文件上传 / 学校考试管理员；有对应权限的教师
- 当前出现条件：粘贴弹窗输入区上方始终显示格式说明与长Alert。
- 问题：格式帮助可以按需查看；模型发送与历史资料范围须在提交前直接可见。图片发送说明不能只从文本弹窗删除，实际上传入口目前没有对应告知，需同步新增明确落点。
- 处置：progressive / shorten；依赖：frontend

源证据（原样连续文本；`excerpt` 为明确截取，`template` 含动态变量）：

- [apps/web-admin/src/features/paper-import/PaperImportTextModal.tsx:19](../../apps/web-admin/src/features/paper-import/PaperImportTextModal.tsx#L19) · excerpt
  > 行内公式使用
- [apps/web-admin/src/features/paper-import/PaperImportTextModal.tsx:21](../../apps/web-admin/src/features/paper-import/PaperImportTextModal.tsx#L21) · literal
  > 结构清晰的 Markdown 在本地解析
- [apps/web-admin/src/features/paper-import/PaperImportTextModal.tsx:22](../../apps/web-admin/src/features/paper-import/PaperImportTextModal.tsx#L22) · literal
  > 题号、【答案】和【解析/详解】明确时不会调用大模型。只有结构无法可靠确定时，才发送本次新增资料的相关原文片段进入模型解析；若本次上传的是图片，则发送本次新增图片页面。不会重发已识别的历史资料。

建议准确文案：

```json
{
  "summary": "粘贴题目、答案或解析，至少20个字符；建议保留题号。",
  "modelNotice": "结构清晰的文本在本地解析；无法可靠确定结构时，相关新增原文会送入模型解析。不会重发已识别的历史资料。",
  "uploadNotice": "需要模型解析时，发送本次新增资料的相关原文片段；图片资料发送本次新增图片页面。不会重发已识别的历史资料。",
  "helpTitle": "公式格式帮助",
  "formatHelp": "行内公式用 $x^2$，独立公式用 $$...$$；也支持 \\(...\\) 与 \\[...\\]。",
  "scopeNote": "uploadNotice 须新增在 PaperImportWorkspace 文件上传入口；当前该入口没有单独的模型发送说明。这是前端配套要求，不能只删除文本弹窗里的图片说明。"
}
```

目标出现条件：粘贴弹窗在输入前常显summary与modelNotice；仅公式格式帮助可展开。文件上传入口在选择或拖入文件前常显uploadNotice，不要求用户先打开粘贴弹窗。两处改动一并交付，不新增模型解析或重发能力。

目标位置：PaperImportTextModal.tsx：现有Tabs之前，输入任务行及模型发送说明常显，公式帮助可展开；PaperImportWorkspace.tsx：现有上传标题与Upload.Dragger之间新增uploadNotice。

边界：

- 文案依据当前文本解析分支；不可概括为所有文本都本地处理或全程数据不出校。
- 新增披露控件须键盘可达，必要的数据发送范围仍可在提交前查看。
- 模型发送告知本身不折叠；只有公式格式帮助按需展开。
- 删除文本弹窗中的图片分支前，须先在实际文件上传入口落实图片发送范围。

风险：

- 披露太深会掩盖模型发送；笼统说本地解析会误导用户的数据范围判断。

验收场景：

- 未展开公式帮助时，点击“添加并识别”前仍能看到文本进入模型的条件、发送范围与不重发历史资料。
- 只使用文件选择或拖入图片、不打开粘贴弹窗时，上传前仍能看到新增图片页面可能送入模型的告知。
- 模型发送说明不使用“所有文本均本地处理”“全程不出校”或“历史资料已全部重发”。
- 公式格式帮助可用键盘展开；原字符上限、20字符门槛及提交禁用规则保持不变。

<a id="mc-e11"></a>

## MC-E11 · P2 · 上传提示只说明资料与格式

- 页面 / 角色：考试资料→文件上传 / 学校考试管理员；有对应权限的教师
- 当前出现条件：资料页上传区常驻。
- 问题：自动匹配被写成确定成功；文件类型未区分Word仅docx。
- 处置：shorten / merge；依赖：copy

源证据（原样连续文本；`excerpt` 为明确截取，`template` 含动态变量）：

- [apps/web-admin/src/features/paper-import/PaperImportWorkspace.tsx:56](../../apps/web-admin/src/features/paper-import/PaperImportWorkspace.tsx#L56) · literal
  > 上传试题、答案、解析或评分标准，系统会自动识别并匹配到题目。
- [apps/web-admin/src/features/paper-import/PaperImportWorkspace.tsx:63](../../apps/web-admin/src/features/paper-import/PaperImportWorkspace.tsx#L63) · literal
  > PDF、Word、图片、Markdown、TXT；可一次添加多份资料

建议准确文案：

```json
{
  "summary": "添加试题、答案、解析或评分标准，识别后请核对匹配结果。",
  "details": "PDF、Word（.docx）、图片、Markdown、TXT；支持多文件。"
}
```

目标出现条件：沿用上传区，不改变文件accept列表。

目标位置：标题下说明任务，拖拽框内只放格式

边界：

- 不承诺每份资料都能可靠匹配，也不扩大支持到.doc。
- 图片的具体限制仍以accept和后端校验为准。

风险：

- 扩大Word格式或承诺可靠自动匹配，会造成上传失败或未核对结果被误用。

验收场景：

- 错误资料仍提示需核对。
- 选择器仍只允许原有类型。

<a id="mc-e12"></a>

## MC-E12 · P1 · 连接失败时突出旧状态与原任务恢复

- 页面 / 角色：考试资料→结果读取异常 / 学校考试管理员；有对应权限的教师
- 当前出现条件：config.importConnectionError传入时显示；结果区仍保留上次读取内容。
- 问题：这不是普通网络小字；需要保留“旧状态”以免教师按过期进度决策，但可缩短并让原任务动作更明确。
- 处置：shorten / keep；依赖：copy

源证据（原样连续文本；`excerpt` 为明确截取，`template` 含动态变量）：

- [apps/web-admin/src/features/paper-import/PaperImportWorkspace.tsx:71](../../apps/web-admin/src/features/paper-import/PaperImportWorkspace.tsx#L71) · literal
  > 当前任务结果尚未确认
- [apps/web-admin/src/features/paper-import/PaperImportWorkspace.tsx:71](../../apps/web-admin/src/features/paper-import/PaperImportWorkspace.tsx#L71) · template
  > ${connectionError}。下方显示最后一次读取的状态，请重新检查原任务获取最新结果。

建议准确文案：

```json
{
  "summary": "结果状态未更新",
  "details": "下方是上次读取的结果。请重新检查原任务；${connectionError}",
  "button": "重新检查原任务"
}
```

目标出现条件：仅读取异常时显示；动态错误继续经现有安全用户消息处理。

目标位置：结果区上方原Alert

边界：

- 不能写成识别失败或要求重新上传。
- 回读失败不抹掉已成功识别/登记事实。

风险：

- 把读取失败说成处理失败，会诱导重新上传、重做资料结构，增加重复操作。

验收场景：

- 写入成功但回读失败时，提示只引导原任务重读。
- 刷新成功后Alert消失。

<a id="mc-e13"></a>

## MC-E13 · P2 · 资料登记说明保留与正式配置的区别

- 页面 / 角色：考试资料→登记状态 / 学校考试管理员；有对应权限的教师
- 当前出现条件：描述已在“查看确认详情”details内；未完成且blockedReason时原因直显。
- 问题：已合理折叠，不能再次建议隐藏；优化状态措辞并保留资料登记不等于正式评分配置。
- 处置：shorten / keep；依赖：copy

源证据（原样连续文本；`excerpt` 为明确截取，`template` 含动态变量）：

- [apps/web-admin/src/features/paper-import/PaperMaterialsConfirmation.tsx:43](../../apps/web-admin/src/features/paper-import/PaperMaterialsConfirmation.tsx#L43) · literal
  > 请先预览并采用本轮完整试卷的题目结构，再确认考试资料。
- [apps/web-admin/src/features/paper-import/PaperMaterialsConfirmation.tsx:44](../../apps/web-admin/src/features/paper-import/PaperMaterialsConfirmation.tsx#L44) · literal
  > 没有完整识别的试题资料来源，请补充或重新识别试卷；答案、解析资料不能代替试卷。
- [apps/web-admin/src/features/paper-import/PaperMaterialsConfirmation.tsx:45](../../apps/web-admin/src/features/paper-import/PaperMaterialsConfirmation.tsx#L45) · literal
  > 试卷题目资料已登记；题目分值、答案和评分配置仍以小题与分值中的正式配置为准。
- [apps/web-admin/src/features/paper-import/PaperMaterialsConfirmation.tsx:46](../../apps/web-admin/src/features/paper-import/PaperMaterialsConfirmation.tsx#L46) · literal
  > 请核对试卷资料完整后确认；答案、分值和评分配置在小题与分值中继续核对。

建议准确文案：

```json
{
  "summary": "按当前状态显示。",
  "variants": {
    "unconfirmed": "先核对完整试卷并采用本轮题目结构。",
    "invalid": "请补充或重新识别完整试卷；答案和解析不能代替试卷。",
    "completed": "资料已登记；答案、分值和评分配置以“小题与分值”页为准。",
    "pending": "确认试卷资料完整后，继续核对小题与分值。"
  }
}
```

目标出现条件：保留当前details与阻断原因直显规则。

目标位置：原状态详情；无额外常驻说明

边界：

- 不能把登记写成全部题目/答案/细则已确认。
- 完整试卷要求不可删除。

风险：

- 把登记成功写成全部准备完成，会让教师误以为答案和正式评分已冻结。

验收场景：

- completed不显示“全部准备完成”。
- 只有答案资料时仍明确缺试卷。

<a id="mc-e14"></a>

## MC-E14 · P1 · 结构预览摘要与最终警告各承担一个任务

- 页面 / 角色：考试资料→确认题目结构 / 学校考试管理员；有对应权限的教师
- 当前出现条件：未采用本轮结构时展示摘要Alert；打开确认弹窗后展示详细警告。
- 问题：摘要负责告诉下一步，弹窗负责说明删改范围与结果边界；两处重复长过程说明可精简。
- 处置：shorten / keep；依赖：copy

源证据（原样连续文本；`excerpt` 为明确截取，`template` 含动态变量）：

- [apps/web-admin/src/features/paper-import/PaperStructurePanel.tsx:137](../../apps/web-admin/src/features/paper-import/PaperStructurePanel.tsx#L137) · literal
  > 核对完整试卷及题目范围后确认，系统会登记试卷来源并采用题目结构。小题分值和评分配置在下一步核对。
- [apps/web-admin/src/features/paper-import/PaperStructurePanel.tsx:167](../../apps/web-admin/src/features/paper-import/PaperStructurePanel.tsx#L167) · literal
  > 请先确认上传了完整试卷
- [apps/web-admin/src/features/paper-import/PaperStructurePanel.tsx:168](../../apps/web-admin/src/features/paper-import/PaperStructurePanel.tsx#L168) · literal
  > 仅上传部分页面、答案或解析时，不能据此减少题目。未识别到可靠分值的小题将显示待确认，不能直接开始阅卷。已有答案和解析会保留。

建议准确文案：

```json
{
  "summary": "确认完整试卷与题目范围；分值和评分配置在下一步核对。",
  "modalTitle": "请核对完整试卷",
  "modalDetails": "部分页面、答案或解析不能作为减少题目的依据。缺少可靠分值的小题仍需核对，不能直接阅卷；已有答案和解析保留。"
}
```

目标出现条件：保留两层当前条件：外部摘要与最终结构确认弹窗。

目标位置：当前Alert位置

边界：

- 结构减少题目风险、缺分值不能阅卷、保留已有内容三个边界均必须出现。
- 保留变更表、原总分/识别总分与用户勾选；不自动采用。

风险：

- 删除部分试卷和缺分值说明，会让用户按不完整材料误确认范围或准备程度。

验收场景：

- 仅传答案时不会诱导删原题。
- 缺分值时仍知道需下一步补齐。

<a id="mc-e15"></a>

## MC-E15 · P1 · 保留结构与完整资料的明确勾选

- 页面 / 角色：考试资料→结构/资料最终确认 / 学校考试管理员；有对应权限的教师
- 当前出现条件：结构采用弹窗、采用后补登记弹窗各有自身checkbox；不会同时挂载为同一步双确认。
- 问题：勾选代表具体知情事实，不是泛泛免责声明，必须保留；补登记顶部再次要求核对可去掉重复句。
- 处置：keep / shorten；依赖：copy

源证据（原样连续文本；`excerpt` 为明确截取，`template` 含动态变量）：

- [apps/web-admin/src/features/paper-import/PaperStructurePanel.tsx:188](../../apps/web-admin/src/features/paper-import/PaperStructurePanel.tsx#L188) · literal
  > 我已核对资料完整，以上题目结构与实际试卷一致
- [apps/web-admin/src/features/paper-import/PaperMaterialsConfirmation.tsx:196](../../apps/web-admin/src/features/paper-import/PaperMaterialsConfirmation.tsx#L196) · literal
  > 我已核对试卷资料完整，题目范围与实际试卷一致
- [apps/web-admin/src/features/paper-import/PaperMaterialsConfirmation.tsx:193](../../apps/web-admin/src/features/paper-import/PaperMaterialsConfirmation.tsx#L193) · literal
  > 请核对这些试卷资料完整，题目分值在下一步设置。

建议准确文案：

```json
{
  "summary": "保留两个业务节点各自的明确勾选。",
  "variants": {
    "structureAdoption": "我已核对完整试卷，以上题目结构与实际试卷一致。",
    "materialsRegistration": "我已核对完整试卷，题目范围与实际试卷一致。"
  },
  "details": "小题分值在下一步核对。"
}
```

目标出现条件：沿用原弹窗各自流程：PaperStructurePanel 的结构采用checkbox使用structureAdoption；PaperMaterialsConfirmation 的资料补登记checkbox使用materialsRegistration。保持原勾选校验与代次重置，不统一成只核对题目范围。

目标位置：原两个弹窗各自的checkbox；小题分值提示保留在资料列表前，不新增第二次勾选。

边界：

- 不合并或删除结构采用与登记两个不同事实。
- 勾选未完成时按钮继续禁用；切代次仍重置勾选。
- 结构采用必须明确核对题目结构，不能只核对题目数量或范围。

风险：

- 把两个业务节点的勾选一并删除，可能让旧预览或未核对结构被采用。

验收场景：

- 结构变更或换资料后不得保留旧checkbox。
- 登记恢复不能重复采用结构。
- 结构采用预览含题型或题目增减时，勾选明确指向“以上题目结构一致”；补登记仍只确认完整资料与题目范围。

<a id="mc-e16"></a>

## MC-E16 · P2 · 答题卡空页只说上传与核对任务

- 页面 / 角色：答题卡设置→无来源 / 学校考试管理员；有对应权限的教师
- 当前出现条件：无答题卡来源且可编辑时显示标题说明+上传框说明。
- 问题：两段都说自动识别，学校库规则过早；正常流程已自动匹配、建稿、识别，不能再增加手工建稿指引。
- 处置：merge / shorten / progressive；依赖：frontend

源证据（原样连续文本；`excerpt` 为明确截取，`template` 含动态变量）：

- [apps/web-admin/src/features/answer-sheet-template/view/TemplateSourceToolbarView.tsx:48](../../apps/web-admin/src/features/answer-sheet-template/view/TemplateSourceToolbarView.tsx#L48) · literal
  > 上传后自动匹配模板、识别并对应题目，发现错误可直接修改。空白卡和已填写答题卡均可辅助识别，收录学校库需核对空白来源。
- [apps/web-admin/src/features/answer-sheet-template/view/TemplateSourceToolbarView.tsx:55](../../apps/web-admin/src/features/answer-sheet-template/view/TemplateSourceToolbarView.tsx#L55) · literal
  > 支持 PDF，或一次选择多张 PNG、JPG 图片；系统自动识别页序与答题区域。

建议准确文案：

```json
{
  "summary": "上传答题卡，核对识别出的题目与答题区域。",
  "details": "支持PDF或多张PNG、JPG图片。",
  "reuseHelp": "空白卡和已填写答题卡均可辅助识别；收录学校模板库前须使用不含个人信息的空白来源。"
}
```

目标出现条件：普通上传只显示summary+格式；复用帮助并入现有“学校模板库”details或上传帮助。

目标位置：空页标题下与拖拽框；不新增常驻Alert

边界：

- 不承诺每次识别成功或无需核对。
- 将收录隐私规则移到实际学校库入口仍须在提交前明确可见。

风险：

- 把学校库隐私要求移走后若不在入库前恢复可见，可能收录含学生身份的底图。

验收场景：

- 未知模板来源继续沿用自动建稿/识别链。
- 学校库收录仍能看到空白来源约束。

<a id="mc-e17"></a>

## MC-E17 · P3 · 等待页序时去掉模型术语

- 页面 / 角色：答题卡设置→多页上传中 / 学校考试管理员；有对应权限的教师
- 当前出现条件：sourceUpload.uploading且phase为ordering时显示。
- 问题：用户只需知道正在排列页面，无需理解模型类别。
- 处置：shorten；依赖：copy

源证据（原样连续文本；`excerpt` 为明确截取，`template` 含动态变量）：

- [apps/web-admin/src/features/answer-sheet-template/view/TemplateSourceToolbarView.tsx:35](../../apps/web-admin/src/features/answer-sheet-template/view/TemplateSourceToolbarView.tsx#L35) · literal
  > 多模态识别页序…

建议准确文案：

```json
{
  "summary": "正在识别页序…"
}
```

目标出现条件：完全沿用ordering条件和进度条。

目标位置：原上传状态行

边界：

- 不能写“已排序完成”或给出没有根据的剩余时间。

风险：

- 用“已完成”替代进行中状态会让用户提前操作后续步骤。

验收场景：

- ordering时仍读到进行中状态；其他上传阶段不误显示页序识别。

<a id="mc-e18"></a>

## MC-E18 · P3 · 学校库收录成功后只保留下一步边界

- 页面 / 角色：答题卡设置→学校模板库 / 学校考试管理员；有对应权限的教师
- 当前出现条件：第一句在已折叠模板库展开后显示；第二句只在published成功时显示。
- 问题：库复用说明已在合适层级，不扩大折叠；成功文案必须区分入库与本场启用。
- 处置：shorten / keep；依赖：copy

源证据（原样连续文本；`excerpt` 为明确截取，`template` 含动态变量）：

- [apps/web-admin/src/features/answer-sheet-template/library/TemplateLibraryPanel.tsx:34](../../apps/web-admin/src/features/answer-sheet-template/library/TemplateLibraryPanel.tsx#L34) · literal
  > 选择学校已收录的答题卡，或将当前空白答题卡收录供下次复用。
- [apps/web-admin/src/features/answer-sheet-template/library/TemplateLibraryPanel.tsx:45](../../apps/web-admin/src/features/answer-sheet-template/library/TemplateLibraryPanel.tsx#L45) · literal
  > 学校模板库已保存。本场考试是否启用仍需在“确认答题卡设置”中完成。

建议准确文案：

```json
{
  "summary": "复用学校模板，或收录当前空白答题卡供以后使用。",
  "success": "已收录到学校库；本场仍需“确认答题卡设置”。"
}
```

目标出现条件：沿用原展开/成功条件；错误和冲突仍直显。

目标位置：现有模板库说明与成功Alert

边界：

- 入库不能被写为本场已启用。
- 不把已折叠区域误认为默认常驻。

风险：

- 把已入库等同于已用于本场，可能导致准备检查仍缺绑定且用户找不到原因。

验收场景：

- 收录成功但未绑定本场时仍明确下一步。

<a id="mc-e19"></a>

## MC-E19 · P1 · 模板收录保留空白来源和逐页范围核对

- 页面 / 角色：答题卡设置→收录学校模板库 / 学校考试管理员；有对应权限的教师
- 当前出现条件：用户打开入库核对弹窗；两项勾选同时满足才能收录。
- 问题：有明确隐私和跨考试复用风险，不能删除两项勾选；末尾Alert重复空白来源要求，可合并成必要动作说明。
- 处置：keep / shorten / merge；依赖：copy

源证据（原样连续文本；`excerpt` 为明确截取，`template` 含动态变量）：

- [apps/web-admin/src/features/answer-sheet-template/library/TemplateLibraryPanel.tsx:50](../../apps/web-admin/src/features/answer-sheet-template/library/TemplateLibraryPanel.tsx#L50) · literal
  > 收录会保存并锁定当前版本；已收录布局供学校后续考试复用。本场启用另行确认。
- [apps/web-admin/src/features/answer-sheet-template/library/TemplateLibraryPanel.tsx:51](../../apps/web-admin/src/features/answer-sheet-template/library/TemplateLibraryPanel.tsx#L51) · literal
  > 确认底图为空白答题卡，且无学生姓名、学号、条码或手写答案等个人信息
- [apps/web-admin/src/features/answer-sheet-template/library/TemplateLibraryPanel.tsx:52](../../apps/web-admin/src/features/answer-sheet-template/library/TemplateLibraryPanel.tsx#L52) · literal
  > 已逐页核对题号和完整作答区域，区域适用于同版式答题卡
- [apps/web-admin/src/features/answer-sheet-template/library/TemplateLibraryPanel.tsx:53](../../apps/web-admin/src/features/answer-sheet-template/library/TemplateLibraryPanel.tsx#L53) · literal
  > 已填写的学生答题卡可辅助识别；请换成经过核对的空白来源后再收录学校模板库。

建议准确文案：

```json
{
  "summary": "收录会保存并锁定当前版本，供学校同版式考试复用；本场启用另行确认。",
  "blankCheckbox": "底图为空白答题卡，不含学生姓名、学号、条码、手写答案等个人信息。",
  "layoutCheckbox": "已逐页核对题号与完整作答区域，适用于同版式答题卡。",
  "details": "如使用已填写的答题卡，请先更换为核对过的空白来源。"
}
```

目标出现条件：原弹窗，勾选与禁用规则不动；末尾动作说明用普通辅助文本替换重复info Alert。

目标位置：原checkbox与其后的动作说明

边界：

- 不能从“无个人信息”删掉条码/手写答案，不能把所有空白印刷码等同于学生码。
- 不要新增系统已自动清除个人信息的承诺。

风险：

- 过分压缩空白要求会漏掉条码与手写答案，或暗示系统已自动去除个人信息。

验收场景：

- 已填写卡仍不能仅凭勾选推断合格，需按原流程更换来源。
- 两项checkbox和版本切换后的重置保持。

<a id="mc-e20"></a>

## MC-E20 · P1 · 模板版本、锁定和本场使用说明分支准确

- 页面 / 角色：答题卡设置→本场考试模板 / 学校考试管理员；有对应权限的教师
- 当前出现条件：草稿时显示锁定后果；未绑定时显示未用于本场；bound_auto时说明仍需人工确认。
- 问题：边界有必要，当前用“明确”“使用设置”表达偏抽象；改成用户可判定状态，仍保留版本。
- 处置：shorten / keep；依赖：copy

源证据（原样连续文本；`excerpt` 为明确截取，`template` 含动态变量）：

- [apps/web-admin/src/features/answer-sheet-template/view/TemplateBindingView.tsx:31](../../apps/web-admin/src/features/answer-sheet-template/view/TemplateBindingView.tsx#L31) · literal
  > 确认将保存并锁定当前版本；修改已锁定区域须克隆新版本。
- [apps/web-admin/src/features/answer-sheet-template/view/TemplateBindingView.tsx:53](../../apps/web-admin/src/features/answer-sheet-template/view/TemplateBindingView.tsx#L53) · literal
  > 当前版本尚未明确用于本场考试。区域检查与本场模板使用设置分别确认。
- [apps/web-admin/src/features/answer-sheet-template/view/templateViewRules.ts:71](../../apps/web-admin/src/features/answer-sheet-template/view/templateViewRules.ts#L71) · template
  > 首张答卷自动识别为 v${selectedTemplate.version_no}，仍需人工核对并确认。

建议准确文案：

```json
{
  "summary": "确认后保存并锁定当前版本；修改区域需另建版本。",
  "variants": {
    "unbound": "此版本尚未用于本场考试，请确认答题卡设置。",
    "boundAuto": "已自动识别为第${selectedTemplate.version_no}版，请核对后确认用于本场。"
  }
}
```

目标出现条件：沿用draft、无examBinding、bound_auto分支；不改变usageConfirmed判定。

目标位置：版本行和确认按钮附近

边界：

- 不能把自动匹配、锁定和本场已用视作同一事实。
- 保留hash/版本不匹配及冻结禁用原因。

风险：

- 把锁定、自动匹配和本场使用合并为一个成功状态，会误导模板版本选择。

验收场景：

- 自动识别版本仍显示未最终确认。
- 锁定但未绑定不显示本场已用。

<a id="mc-e21"></a>

## MC-E21 · P3 · 精确坐标说明保留参照系，已折叠不再隐藏

- 页面 / 角色：答题卡设置→高级精确坐标 / 学校考试管理员；有对应权限的教师
- 当前出现条件：选中区域后，用户展开“精确坐标”或“单个选项”details才显示。
- 问题：参照系不同会导致错框，是必须留的小字；只做短句，不以删除技术信息换取少字。
- 处置：keep / shorten；依赖：copy

源证据（原样连续文本；`excerpt` 为明确截取，`template` 含动态变量）：

- [apps/web-admin/src/features/answer-sheet-template/view/TemplateRegionSettingsView.tsx:13](../../apps/web-admin/src/features/answer-sheet-template/view/TemplateRegionSettingsView.tsx#L13) · literal
  > 优先在画布上框选。精确输入以整页为参照：左、上表示距页边的位置；宽、高表示占整页的百分比。
- [apps/web-admin/src/features/answer-sheet-template/view/TemplateRegionSettingsView.tsx:76](../../apps/web-admin/src/features/answer-sheet-template/view/TemplateRegionSettingsView.tsx#L76) · literal
  > 选项框以本题区域为参照，数值为占本题区域的百分比；每个框只覆盖一个填涂点。

建议准确文案：

```json
{
  "summary": "以整页为参照，单位为百分比；左、上是距页边的位置，宽、高是区域尺寸。",
  "optionHelp": "选项坐标以本题区域为参照，单位为百分比；每框只覆盖一个填涂点。"
}
```

目标出现条件：完全保留原高级details展开条件。

目标位置：对应坐标输入前

边界：

- 整页比例与题内比例不可合并成同一个“坐标”说法。
- 不隐藏键盘和数字调整替代操作。

风险：

- 删除参照系说明可能把题内比例当成整页比例，导致选项框错位。

验收场景：

- 教师能区分两个参照系；输入单位仍为%。

<a id="mc-e22"></a>

## MC-E22 · P1 · OMR校准只压缩措辞，不省资格条件

- 页面 / 角色：答题卡设置→选择题识别与校准 / 学校考试管理员；有对应权限的教师
- 当前出现条件：识别方式帮助仅差分模式显示；校准状态说明按session.status显示。
- 问题：长句可压缩，但98%不是准确率保证，自动确认还有其他资格；盲标不可改是用户必须提前知道的规则。
- 处置：shorten / keep；依赖：copy

源证据（原样连续文本；`excerpt` 为明确截取，`template` 含动态变量）：

- [apps/web-admin/src/features/answer-sheet-template/view/OMRCalibrationView.tsx:85](../../apps/web-admin/src/features/answer-sheet-template/view/OMRCalibrationView.tsx#L85) · literal
  > 校准已生效：整套模板中识别把握不低于 98% 且符合自动确认条件的结果可自动确认，其余仍转人工复核。
- [apps/web-admin/src/features/answer-sheet-template/view/OMRCalibrationView.tsx:94](../../apps/web-admin/src/features/answer-sheet-template/view/OMRCalibrationView.tsx#L94) · literal
  > 请只依据答题图片盲标；提交后才显示机器结果，且标注不可修改。
- [apps/web-admin/src/features/answer-sheet-template/view/OMRCalibrationView.tsx:173](../../apps/web-admin/src/features/answer-sheet-template/view/OMRCalibrationView.tsx#L173) · literal
  > 该方式用同一版空白答题卡作对照，排除印刷文字干扰。校准通过前，识别结果仅供参考，不会计入成绩。

建议准确文案：

```json
{
  "summary": "用同版空白卡对照识别；校准通过前，结果仅供参考，不计入成绩。",
  "variants": {
    "approved": "校准已生效：识别把握≥98%且满足自动确认条件的结果可自动确认，其余转人工复核。",
    "draft": "只按答题图片标注；提交后才显示机器结果，标注不能修改。"
  }
}
```

目标出现条件：保留原差分模式与session状态条件，不将校准说明移到普通上传页。

目标位置：原模式帮助、校准抽屉状态Alert

边界：

- 保留“且满足条件”，不能写成98%准确率或全部自动评分。
- 不删样本覆盖、独立审批等后台门禁；若阈值配置变化须用实际配置值同步。

风险：

- 省掉98%之外的自动资格或把识别把握写成准确率，会夸大自动确认能力。

验收场景：

- 未approved不能显示支持自动确认。
- 已批准文案不保证所有样本自动通过。

<a id="mc-e23"></a>

## MC-E23 · P1 · 打印说明保留唯一条码和补打后果

- 页面 / 角色：答题卡设置→打印包 / 学校考试管理员；有对应权限的教师
- 当前出现条件：打印面板、签发弹窗和补打弹窗在各自原条件显示。
- 问题：这些说明解释受控签发的后果，有必要；“签发/受控”可用更直接的语言，不删关键限制。
- 处置：shorten / keep；依赖：copy

源证据（原样连续文本；`excerpt` 为明确截取，`template` 含动态变量）：

- [apps/web-admin/src/features/answer-sheet-template/view/StudentPrintingView.tsx:107](../../apps/web-admin/src/features/answer-sheet-template/view/StudentPrintingView.tsx#L107) · literal
  > 按班级生成带学生身份条码的受控 PDF。系统不会把姓名或学号印在答题卡上。
- [apps/web-admin/src/features/answer-sheet-template/view/StudentPrintingView.tsx:221](../../apps/web-admin/src/features/answer-sheet-template/view/StudentPrintingView.tsx#L221) · literal
  > 每名学生、每一页都有唯一条码。重复复印或打乱页面会进入人工核验；补打必须使用作废与重印流程。
- [apps/web-admin/src/features/answer-sheet-template/view/StudentPrintingView.tsx:260](../../apps/web-admin/src/features/answer-sheet-template/view/StudentPrintingView.tsx#L260) · literal
  > 已排除缺考学生和已有有效答题卡的学生，避免重复签发。
- [apps/web-admin/src/features/answer-sheet-template/view/StudentPrintingView.tsx:265](../../apps/web-admin/src/features/answer-sheet-template/view/StudentPrintingView.tsx#L265) · literal
  > 系统会同时作废旧条码并生成该学生的新卡。已扫描的旧卡会进入核验，请收回纸质旧卡。

建议准确文案：

```json
{
  "summary": "按班级生成带学生身份条码的PDF，不打印姓名或学号。",
  "issueHelp": "每人每页的条码唯一，请勿重复复印或打乱页面；补打请使用“作废旧卡并补打”。",
  "scopeHelp": "仅为应考且没有有效答题卡的学生生成。",
  "reprintHelp": "确认后旧条码作废并生成新卡；请收回旧卡。已扫描的旧卡将进入核验。"
}
```

目标出现条件：保持原打印入口、签发确认和补打确认各自可见；旧卡影响留在确认前。

目标位置：原说明段与警告正文

边界：

- 不承诺条码不包含身份关联，不把“不打印姓名学号”写成匿名不可识别。
- 不删旧卡作废、唯一性和已有有效卡排除范围。

风险：

- 省略唯一条码和作废规则会导致重复复印、旧卡混扫或错误再次签发。

验收场景：

- 普通签发与补打两种任务各看到相关限制。
- 下载失败不提示重新签发，继续沿用原恢复入口。

<a id="mc-e24"></a>

## MC-E24 · P1 · 导入页序规则用短提示加完整规则

- 页面 / 角色：答卷导入→批次文件 / 学校考试管理员；有对应权限的教师
- 当前出现条件：每个批次工作区常驻完整大段Alert。
- 问题：页序和一次选完整答卷是任务前提，不能全折叠；格式计算细节适合按需查看。
- 处置：shorten / progressive；依赖：frontend

源证据（原样连续文本；`excerpt` 为明确截取，`template` 含动态变量）：

- [apps/web-admin/src/features/capture/CaptureBatchPanels.tsx:249](../../apps/web-admin/src/features/capture/CaptureBatchPanels.tsx#L249) · literal
  > 请将同一学生的全部页面连续排列，再排列下一名学生
- [apps/web-admin/src/features/capture/CaptureBatchPanels.tsx:249](../../apps/web-admin/src/features/capture/CaptureBatchPanels.tsx#L249) · literal
  > 按选择的文件顺序、文件内页序，每 N 页组成一份学生答卷。1 页用于单面，2 页用于正反面，支持 1–20 页。PDF/TIFF 按解码页数计算；每次选择独立分组，缺页不会进入后续处理，也不会跨两次选择凑页。

建议准确文案：

```json
{
  "summary": "每人${pagesPerStudent ?? '待选择'}页，按学生连续排列；每次须选择完整答卷。",
  "details": "按文件选择顺序及文件内页序，每N页组成一份答卷。支持每人1–20页；PDF/TIFF按解码页数计算。每次选择独立分组，不能跨两次选择补页；缺页须处理后才能继续。",
  "helpTitle": "查看页序与分组规则"
}
```

目标出现条件：summary始终在选择文件前可见；详情经可访问的披露控件查看。

目标位置：每人页数与导入按钮下方

边界：

- 一次完整选择和不能跨次补页至少在可见短提示/详情分别明确；不得改分组隔离。
- pagesPerStudent未知时不能默认显示1页已确认。

风险：

- 把关键页序规则全部折叠，会让用户在选择文件前看不到一次完整分组要求。

验收场景：

- 双面、三页、多PDF/TIFF和缺页各场景能找到对应规则。
- 未选页数仍保持原禁用与提示。

<a id="mc-e25"></a>

## MC-E25 · P1 · 材料替代/排除/取消用各自准确后果

- 页面 / 角色：答卷导入→材料处理确认 / 学校考试管理员；有对应权限的教师
- 当前出现条件：editor状态决定取消批次、替代材料、不计入或学生考勤处理，当前在一条嵌套模板中。
- 问题：说明长且分支集中，不能用同一“移除文件”概括批次/整组范围；应分支保持完整事实并缩短。
- 处置：contextual / shorten / keep；依赖：copy

源证据（原样连续文本；`excerpt` 为明确截取，`template` 含动态变量）：

- [apps/web-admin/src/features/capture/CaptureCollectionPanel.tsx:198](../../apps/web-admin/src/features/capture/CaptureCollectionPanel.tsx#L198) · literal
  > 取消后，该批次的全部材料和未完成上传不再计入本场核对。请确认没有需保留的材料；对应学生仍需补传或核实缺考。
- [apps/web-admin/src/features/capture/CaptureCollectionPanel.tsx:198](../../apps/web-admin/src/features/capture/CaptureCollectionPanel.tsx#L198) · excerpt
  > 本次选择的全部文件及其答卷将退出有效输入，历史记录保留。
- [apps/web-admin/src/features/capture/CaptureCollectionPanel.tsx:198](../../apps/web-admin/src/features/capture/CaptureCollectionPanel.tsx#L198) · excerpt
  > 已产生评分的材料无法在这里移除。
- [apps/web-admin/src/features/capture/CaptureCollectionPanel.tsx:198](../../apps/web-admin/src/features/capture/CaptureCollectionPanel.tsx#L198) · literal
  > 请先核实学生情况并填写原因，系统会保留本次名册调整记录。

建议准确文案：

```json
{
  "summary": "按原操作类型呈现。",
  "variants": {
    "cancelBatch": "取消后，本批全部材料和未完成上传不再计入核对。请确认没有需保留的材料；对应学生仍需补传或核实缺考。",
    "replace": "请选择每人页数，并一次上传原选择对应的全部完整答卷。新文件登记成功后，原选择的全部文件及答卷退出有效输入，历史保留；新答卷处理成功才算导入完成。已有评分的材料不能在此移除。",
    "exclude": "原选择的全部文件及答卷将不再计入，历史保留；对应学生仍需补传或核实缺考。已有评分的材料不能在此移除。",
    "attendance": "请核实学生情况并填写原因，本次名册调整会留有记录。"
  }
}
```

目标出现条件：完全沿用editor对应分支及已有校验；replace分支在继续选择文件之前明确每人页数和原整组答卷范围，不改变登记或处理时点，无需新后端状态。

目标位置：原确认弹窗、原因输入上方；replace说明置于“继续选择完整答卷”按钮之前。

边界：

- 替代成功前不能说原资料已退出；必须保留登记成功和处理成功两个时点。
- 本次选择全部材料不简写成当前单个文件；取消批次影响全批。
- 同一次选择包含多名学生时，替代须覆盖该原选择对应的全部完整答卷；不能只补其中一个失败文件或一名学生。
- 保留每人页数选择，不把替代上传默认为单页或沿用未确认页数。

风险：

- 把整组/全批影响简写成单个文件、或提前说替代完成，会导致保留材料范围误判。

验收场景：

- 替代登记成功而后续处理失败时文案仍准确。
- 有评分材料仍明确无法在此移除。
- 取消批次与标记缺考不能混用。
- 原一次选择包含多名学生或多文件时，替代前明确要求重新选择对应的全部完整答卷，并设置每人页数。
- 取消替代选择或新文件尚未登记成功时，不显示原材料已经退出或新答卷已经导入完成。

<a id="mc-e26"></a>

## MC-E26 · P1 · 结束导入保留人数与阶段变化，去掉同页重复解释

- 页面 / 角色：答卷导入→学生核对与结束确认 / 学校考试管理员；有对应权限的教师
- 当前出现条件：可结束导入且无finishReasons时页面说明；点击结束时弹窗有实时人数和阶段后果。
- 问题：人数、封存与阶段后果是必要确认；页面常驻句可只解释无需逐页确认，避免与弹窗重复流程。
- 处置：shorten / keep；依赖：copy

源证据（原样连续文本；`excerpt` 为明确截取，`template` 含动态变量）：

- [apps/web-admin/src/features/capture/CaptureCollectionPanel.tsx:79](../../apps/web-admin/src/features/capture/CaptureCollectionPanel.tsx#L79) · template
  > 应收 ${summary.required_count} 人，已成功导入 ${summary.ready_count} 人，缺考 ${summary.absent_count} 人。结束时系统会再次核对全部学生与材料，并封存已处理批次。
- [apps/web-admin/src/features/capture/CaptureCollectionPanel.tsx:79](../../apps/web-admin/src/features/capture/CaptureCollectionPanel.tsx#L79) · literal
  > 考试将进入阅卷阶段。
- [apps/web-admin/src/features/capture/CaptureCollectionPanel.tsx:79](../../apps/web-admin/src/features/capture/CaptureCollectionPanel.tsx#L79) · literal
  > 本次补传不会改变当前阅卷阶段。
- [apps/web-admin/src/features/capture/CaptureCollectionPanel.tsx:185](../../apps/web-admin/src/features/capture/CaptureCollectionPanel.tsx#L185) · literal
  > 应收学生答卷已齐全，正常处理通过的页面无需逐页人工确认。结束时由系统再次核对。

建议准确文案：

```json
{
  "summary": "正常通过的页面无需逐页确认；仍请核对材料问题。",
  "dialog": "应收${summary.required_count}人，成功导入${summary.ready_count}人，缺考${summary.absent_count}人。结束时再次核对学生与材料，并封存已处理批次。",
  "variants": {
    "collecting": "确认后进入阅卷。",
    "supplement": "本次补传结束，当前阅卷阶段保持不变。"
  }
}
```

目标出现条件：保持summary.can_complete、权限、本地busy及revision校验；只有能结束时显示该正常短提示。

目标位置：学生核对区短说明；最终人数与后果仍在原弹窗

边界：

- 不能将ready_count与文件/页面数混写。
- 补传不得声称回退或新进入阅卷；不删除最终核对和封存。

风险：

- 把页面人数与材料数混算，或把补传写成进入阅卷，会误导最终结束确认。

验收场景：

- 普通collecting与grading/reviewing补传分别看到正确后果。
- 缺卷/未决材料仍由原blockers阻断。
- 响应丢失继续原命令恢复，不重复结束。

## 逐文件实际阅读状态

`reviewed` 为本轮全文；`partial` 为本轮片段；`pending` 为仅枚举、未审读。片段证据不代表整文件已覆盖。

| 文件 | 状态 | 说明 |
| --- | --- | --- |
| AGENTS.md | reviewed | 2026-10-28重新全文审读；已核对本次辅助文案的出现条件与相邻行为。 |
| apps/web-admin/src/features/answer-sheet-template/library/TemplateLibraryPanel.tsx | reviewed | 2026-10-28重新全文审读；已核对本次辅助文案的出现条件与相邻行为。 |
| apps/web-admin/src/features/answer-sheet-template/view/ClearTemplateQuestionRegionsButton.tsx | pending | 本轮范围盘点，尚未完成当前源码辅助文案审读。 |
| apps/web-admin/src/features/answer-sheet-template/view/FillGradingControl.tsx | pending | 本轮范围盘点，尚未完成当前源码辅助文案审读。 |
| apps/web-admin/src/features/answer-sheet-template/view/IdentityRegionPanel.tsx | pending | 本轮范围盘点，尚未完成当前源码辅助文案审读。 |
| apps/web-admin/src/features/answer-sheet-template/view/OMRCalibrationView.tsx | partial | 2026-10-28仅核对所列相关片段与搜索命中；前轮阅读不冒充本轮全文完成。 |
| apps/web-admin/src/features/answer-sheet-template/view/ObjectiveGridCanvas.tsx | pending | 本轮范围盘点，尚未完成当前源码辅助文案审读。 |
| apps/web-admin/src/features/answer-sheet-template/view/ObjectiveGridDrawer.tsx | pending | 本轮范围盘点，尚未完成当前源码辅助文案审读。 |
| apps/web-admin/src/features/answer-sheet-template/view/StudentPrintingView.tsx | partial | 2026-10-28仅核对所列相关片段与搜索命中；前轮阅读不冒充本轮全文完成。 |
| apps/web-admin/src/features/answer-sheet-template/view/TemplateBindingView.tsx | reviewed | 2026-10-28重新全文审读；已核对本次辅助文案的出现条件与相邻行为。 |
| apps/web-admin/src/features/answer-sheet-template/view/TemplateCanvasView.tsx | pending | 本轮范围盘点，尚未完成当前源码辅助文案审读。 |
| apps/web-admin/src/features/answer-sheet-template/view/TemplateQuestionPane.tsx | pending | 本轮范围盘点，尚未完成当前源码辅助文案审读。 |
| apps/web-admin/src/features/answer-sheet-template/view/TemplateRegionSettingsView.tsx | reviewed | 2026-10-28重新全文审读；已核对本次辅助文案的出现条件与相邻行为。 |
| apps/web-admin/src/features/answer-sheet-template/view/TemplateSettingsView.tsx | reviewed | 2026-10-28重新全文审读；已核对本次辅助文案的出现条件与相邻行为。 |
| apps/web-admin/src/features/answer-sheet-template/view/TemplateSourceToolbarView.tsx | reviewed | 2026-10-28重新全文审读；已核对本次辅助文案的出现条件与相邻行为。 |
| apps/web-admin/src/features/answer-sheet-template/view/TemplateWorkspaceView.tsx | pending | 本轮范围盘点，尚未完成当前源码辅助文案审读。 |
| apps/web-admin/src/features/answer-sheet-template/view/templateViewRules.ts | reviewed | 2026-10-28重新全文审读；已核对本次辅助文案的出现条件与相邻行为。 |
| apps/web-admin/src/features/capture/CaptureBatchPanels.tsx | reviewed | 2026-10-28重新全文审读；已核对本次辅助文案的出现条件与相邻行为。 |
| apps/web-admin/src/features/capture/CaptureCollectionPanel.tsx | partial | 2026-10-28仅核对所列相关片段与搜索命中；前轮阅读不冒充本轮全文完成。 |
| apps/web-admin/src/features/capture/ExamCapturePage.tsx | pending | 本轮范围盘点，尚未完成当前源码辅助文案审读。 |
| apps/web-admin/src/features/capture/ImageQualityWorkspace.tsx | pending | 本轮范围盘点，尚未完成当前源码辅助文案审读。 |
| apps/web-admin/src/features/capture/MatchingWorkspace.tsx | pending | 本轮范围盘点，尚未完成当前源码辅助文案审读。 |
| apps/web-admin/src/features/capture/SubmissionCaptureSummary.tsx | pending | 本轮范围盘点，尚未完成当前源码辅助文案审读。 |
| apps/web-admin/src/features/capture/captureColumns.tsx | pending | 本轮范围盘点，尚未完成当前源码辅助文案审读。 |
| apps/web-admin/src/features/capture/submissionColumns.tsx | pending | 本轮范围盘点，尚未完成当前源码辅助文案审读。 |
| apps/web-admin/src/features/exams/create/CreateExamComposer.tsx | reviewed | 2026-10-28重新全文审读；已核对本次辅助文案的出现条件与相邻行为。 |
| apps/web-admin/src/features/exams/create/CreateExamPage.tsx | pending | 本轮范围盘点，尚未完成当前源码辅助文案审读。 |
| apps/web-admin/src/features/exams/create/presentation.ts | reviewed | 2026-10-28重新全文审读；已核对本次辅助文案的出现条件与相邻行为。 |
| apps/web-admin/src/features/exams/management/ManagementExamArchiveDialog.tsx | pending | 本轮范围盘点，尚未完成当前源码辅助文案审读。 |
| apps/web-admin/src/features/exams/management/ManagementExamDetails.tsx | pending | 本轮范围盘点，尚未完成当前源码辅助文案审读。 |
| apps/web-admin/src/features/exams/management/ManagementExamEditDrawer.tsx | pending | 本轮范围盘点，尚未完成当前源码辅助文案审读。 |
| apps/web-admin/src/features/exams/management/ManagementExamOverviewPage.tsx | pending | 本轮范围盘点，尚未完成当前源码辅助文案审读。 |
| apps/web-admin/src/features/exams/workspace/ExamWorkspaceLayout.tsx | partial | 2026-10-28仅核对所列相关片段与搜索命中；前轮阅读不冒充本轮全文完成。 |
| apps/web-admin/src/features/paper-import/PaperImportRecognitionPreview.tsx | pending | 本轮范围盘点，尚未完成当前源码辅助文案审读。 |
| apps/web-admin/src/features/paper-import/PaperImportResults.tsx | pending | 本轮范围盘点，尚未完成当前源码辅助文案审读。 |
| apps/web-admin/src/features/paper-import/PaperImportReviewComparison.tsx | pending | 本轮范围盘点，尚未完成当前源码辅助文案审读。 |
| apps/web-admin/src/features/paper-import/PaperImportReviewPanel.tsx | pending | 本轮范围盘点，尚未完成当前源码辅助文案审读。 |
| apps/web-admin/src/features/paper-import/PaperImportSourceList.tsx | pending | 本轮范围盘点，尚未完成当前源码辅助文案审读。 |
| apps/web-admin/src/features/paper-import/PaperImportTextModal.tsx | reviewed | 2026-10-28重新全文审读；已核对本次辅助文案的出现条件与相邻行为。 |
| apps/web-admin/src/features/paper-import/PaperImportWorkspace.tsx | reviewed | 2026-10-28重新全文审读；已核对本次辅助文案的出现条件与相邻行为。 |
| apps/web-admin/src/features/paper-import/PaperMaterialsConfirmation.tsx | reviewed | 2026-10-28重新全文审读；已核对本次辅助文案的出现条件与相邻行为。 |
| apps/web-admin/src/features/paper-import/PaperStructurePanel.tsx | reviewed | 2026-10-28重新全文审读；已核对本次辅助文案的出现条件与相邻行为。 |
| apps/web-admin/src/features/paper-import/RubricReviewEditor.tsx | pending | 本轮范围盘点，尚未完成当前源码辅助文案审读。 |
| apps/web-admin/src/features/paper-import/usePaperImportMaterials.ts | partial | 2026-10-28仅核对所列相关片段与搜索命中；前轮阅读不冒充本轮全文完成。 |
| apps/web-admin/src/pages/AnswerSheetTemplatePage.tsx | partial | 2026-10-28仅核对所列相关片段与搜索命中；前轮阅读不冒充本轮全文完成。 |
| apps/web-admin/src/pages/CaptureBatchPage.tsx | pending | 本轮范围盘点，尚未完成当前源码辅助文案审读。 |
| apps/web-admin/src/pages/ExamPreparationPage.test.ts | pending | 本轮范围盘点，尚未完成当前源码辅助文案审读。 |
| apps/web-admin/src/pages/ExamPreparationPage.tsx | partial | 2026-10-28仅核对所列相关片段与搜索命中；前轮阅读不冒充本轮全文完成。 |
| apps/web-admin/src/pages/ExamWorkspacePage.tsx | pending | 本轮范围盘点，尚未完成当前源码辅助文案审读。 |
| apps/web-admin/src/pages/SubmissionCapturePage.tsx | pending | 本轮范围盘点，尚未完成当前源码辅助文案审读。 |
| apps/web-admin/src/pages/SubmissionCaptureViews.tsx | pending | 本轮范围盘点，尚未完成当前源码辅助文案审读。 |
| docs/engineering/commenting-standard.md | reviewed | 2026-10-28重新全文审读；已核对本次辅助文案的出现条件与相邻行为。 |
