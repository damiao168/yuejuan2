# 辅助小字方案：学生、桌面、组织、模型与账号

2026-10-28。共 27 条；本轮重新核对源码，仅写方案，不改业务。JSON 为可合并结构化版本。

本轮证据是静态源码及调用条件，没有新增浏览器或原生扫描硬件验收。literal 为完整连续静态原文，template 为原样插值模板，excerpt 为明确标记的连续 JSX/调用截取。

关键限制留在动作旁：版本、身份、图片未持久化、恢复码消耗、不可恢复和试点范围。普通学生/教师只先读当前任务；运维可按需查协议与诊断。旧草稿、精确坐标、可选多模型已经折叠，不重复建议折叠。

<a id="mc-p01"></a>

## MC-P01 · 学生端登录（P2）

- 角色：学生。
- 当前条件：未登录时，说明与学校代码、账号、密码同时显示。
- 负担／保留理由：两段重复学校账号来源；常规登录前承担故障分支的阅读量。
- 处置：shorten, merge；依赖：copy。

原文与依据：

- `apps/student-portal/src/features/session/SessionScreens.tsx:32`（literal）：
  > 使用学校发放的学生账号登录。未发布的成绩不会在此显示。
- `apps/student-portal/src/features/session/SessionScreens.tsx:41`（literal）：
  > 学校代码或账号不清楚、忘记密码时，请联系学校管理员获取登录信息或重置密码。

目标文案／呈现：

> 使用学校发放的学生账号，查看本人已发布成绩。

- help：学校代码、账号或密码有问题？请联系学校管理员。

目标条件：主说明常驻，求助说明仍在表单下。
位置：标题下＋表单下

边界：

- 仅本人已发布成绩；不省略学校代码这一身份范围。

验收：

- 初次访问理解账号来源与查分范围；账号有问题仍知道联系学校管理员。

<a id="mc-p02"></a>

## MC-P02 · 逐题复核申请与回执（P2）

- 角色：学生。
- 当前条件：打开申请表显示次数与来源；同 releaseID＋question_id 已有申请时显示只读回执。
- 负担／保留理由：提交前和成功后重复强调次数与收到，下一步靠后。
- 处置：shorten, merge；依赖：copy。

原文与依据：

- `apps/student-portal/src/features/appeals/QuestionAppealForm.tsx:181`（template）：
  > 每道题在同一版成绩下只能提交一次。学校将按第 {releaseVersion} 版成绩核对原卷和评分标准。
- `apps/student-portal/src/features/appeals/QuestionAppealForm.tsx:79`（literal）：
  > 学校已收到本题复核申请。
- `apps/student-portal/src/features/appeals/QuestionAppealForm.tsx:79`（literal）：
  > 本题已提交过复核申请，不能重复提交。处理结果会显示在页面下方。

目标文案／呈现：

> 第 {releaseVersion} 版：本题仅可申请一次，学校将核对这一版的原卷与评分标准。

- receipt：已收到第 {releaseVersion} 版的本题申请，不可重复提交。
- link：查看申请与处理结果
- button：提交复核申请

目标条件：限制提交前保留一次；成功显示状态、版号与记录入口。
位置：表单标题下；成功后原位置

边界：

- 限制为每题每版一次，不能写成永久一次。
- 保留版本核对、读取失败等阻断与已收到确认。

验收：

- 同版重复打开只读且可看记录；换版不能混用旧申请。

<a id="mc-p03"></a>

## MC-P03 · 复核草稿保存状态（P1）

- 角色：学生。
- 当前条件：有 storageKey 且输入说明或选区后显示；存储成功、失败分别呈现。
- 负担／保留理由：成功长句掩盖会话范围；失败是丢失风险，不能与成功一起降为通用小字。
- 处置：shorten, contextual, keep；依赖：copy。

原文与依据：

- `apps/student-portal/src/features/appeals/QuestionAppealForm.tsx:12`（literal）：
  > 草稿已保存在当前标签页会话，切题、返回或重新登录后可恢复；退出登录将清除。
- `apps/student-portal/src/features/appeals/QuestionAppealForm.tsx:12`（literal）：
  > 浏览器未允许保存草稿，当前输入仅在本页保留。刷新或关闭前请先复制说明。

目标文案／呈现：

> 草稿仅存于当前标签页会话，切题、返回或重新登录可恢复；退出登录会清除。

- failure：草稿未能保存，仅在本页保留。刷新或关闭前，请先复制说明。

目标条件：保留成功与失败两分支、错误样式及 aria-live。
位置：申请输入区保存状态

边界：

- 不承诺跨标签页、跨设备或永久保存。
- 保留重新登录可恢复与主动退出清除的区别。

验收：

- 禁用存储显示失败，不写已保存；辅助技术收到状态。

<a id="mc-p04"></a>

## MC-P04 · 成绩换版与核对失败（P1）

- 角色：学生。
- 当前条件：不同 release_id 先保留当前展示，提示主动查看新版；请求失败显示先前版本。
- 负担／保留理由：多个旧新版事实挤在一句；核对失败不能暗示旧数据仍为最新。
- 处置：shorten, contextual, keep；依赖：copy。

原文与依据：

- `apps/student-portal/src/features/results/ResultDetail.tsx:92`（template）：
  > 学校已更新成绩，第 {pendingResult.release_version} 版可查看。旧版草稿仍可复制，新版需重新核对。
- `apps/student-portal/src/features/results/ResultDetail.tsx:91`（template）：
  > 最新成绩核对失败，当前显示此前读取的第 {result.release_version} 版。{error}

目标文案／呈现：

> 第 {pendingResult.release_version} 版成绩已发布。旧草稿可复制；新版需重新核对后申请。

- failure：未能核对最新成绩，现显示此前读取的第 {result.release_version} 版。{error}

目标条件：新版提示与核对失败独立，保留现有查看新版动作。
位置：成绩标题与分数旁

边界：

- 不自动迁移草稿与选区。
- 核对失败仅描述正在展示的版本，不能称当前生效版。
- 保留错误、重试、必要重新登录。

验收：

- 新版由用户主动查看；网络失败明确显示先前读取版号。

<a id="mc-p05"></a>

## MC-P05 · 我的复核结果与当前生效成绩（P1）

- 角色：学生。
- 当前条件：有申请时显示来源、原分、处理状态、学校回复、关联发布和当前生效版；ID 相等/不同/未知分别分支。
- 负担／保留理由：状态与结论重复；已公布记录也可能出现转入重评，用户难判断仍等待还是已生效。
- 处置：merge, contextual, keep；依赖：frontend。

原文与依据：

- `apps/student-portal/src/features/appeals/AppealStatus.tsx:86`（template）：
  > 最近核对的当前生效成绩：第 {currentResult.release_version} 版。
- `apps/student-portal/src/features/appeals/AppealStatus.tsx:36`（literal）：
  > 处理结论：已支持复核，转入重评。
- `apps/student-portal/src/features/appeals/AppealStatus.tsx:106`（template）：
  > 此复核结果已公布（${version}），当前生效成绩为第 ${current.release_version} 版，不是本次处理关联的发布。请以当前成绩为准。

目标文案／呈现：

> 状态仅显示一次；学校处理决定与当前进度分开表达。当前生效版在列表头显示一次，各申请保留来源与关联关系。

- waiting：学校复核中
- rejected：未支持申请
- regrading：已支持申请，等待重评结果发布。
- sameRelease：本次复核关联的{version}已是当前生效成绩{scoreSuffix}。
- differentRelease：复核结果已关联{version}；当前生效为第 {current.release_version} 版，请查看当前成绩。
- unverified：已关联{version}；当前生效成绩尚未核实。
- noLinkedRelease：本次申请尚未关联后续成绩版本。
- rejectDecision：学校决定：未支持本次申请。
- referRegradeDecision：学校决定：支持复核，交由重新评分。
- unknownStatus：处理中

目标条件：仅删除相同含义的重复状态；有明确决定时保留决定。decision=reject 或 status=rejected 仍优先拒绝结论；关联发布与当前发布不同的警示常显，未知不推断。
位置：申请卡标题＋来源＋一条关联提示

边界：

- 按 release ID 相等判断，不能凭版本号大小或 resolved 判断生效。
- 保留原版原分、复核分、当前分与学校回复。
- 未核实当前结果不能写已生效。
- 保留现有拒绝结论优先：decision=reject 或 status=rejected 时不能因唯一状态标题而吞掉拒绝事实。
- 当前本题分数未公开时保留缺失说明，不能用历史复核分补成当前分。

验收：

- 学校复核中、未支持、重评中、已公布分支文案各自正确。
- 关联另一版仍提示当前版；核对失败仍提示未核实。
- 决定与状态不一致时仍呈现拒绝优先，未知状态保留“处理中”。
- 已公布记录中的支持决定可保留，但不得把它写成当前仍在等待进入重评。

<a id="mc-p06"></a>

## MC-P06 · 旧版未提交草稿（P2）

- 角色：学生。
- 当前条件：当前账号同考试其他发布有草稿时才出现；details 已默认折叠。
- 负担／保留理由：现有折叠合适，仅调整内文次序，不重复建议新增折叠。
- 处置：shorten, keep；依赖：copy。

原文与依据：

- `apps/student-portal/src/features/results/OldAppealDrafts.tsx:20`（literal）：
  > 这些草稿只保存在当前标签页会话，退出登录会清除。说明可复制；旧版圈选不会迁移，新版必须重新核对证据。

目标文案／呈现：

> 可复制旧说明；旧版圈选不会迁移，新版需重新核对。草稿仅存于当前标签页会话，退出登录会清除。

目标条件：保持有旧草稿才出现、默认折叠。
位置：现有旧草稿 details 内

边界：

- 保留来源版号题号，不承诺自动迁移。
- 保留复制失败的手动选择回退。

验收：

- 展开可见复制、选区不迁移、退出清除；无草稿不显示。

<a id="mc-p07"></a>

## MC-P07 · 逐题参比范围小字（P2）

- 角色：学生。
- 当前条件：cohort 存在才显示；同一行平均分和群体对比各渲染一次范围及人数。
- 负担／保留理由：同题同范围重复两次推高长表，人数却仍须逐题保留。
- 处置：merge, shorten；依赖：frontend。

原文与依据：

- `apps/student-portal/src/features/questions/QuestionCohortScope.tsx:8`（literal）：
  > 参比范围为当前成绩版本中，本次考试本题已发布成绩的参试学生；不是班级人数。
- `apps/student-portal/src/features/questions/QuestionCohortScope.tsx:8`（template）：
  > 本版考试本题 · {population}
- `apps/student-portal/src/features/questions/QuestionTables.tsx:55`（excerpt）：
  > <QuestionCohortScope question={question} />
- `apps/student-portal/src/features/questions/QuestionTables.tsx:56`（excerpt）：
  > <QuestionCohortScope question={question} />

目标文案／呈现：

> 统计范围：本版成绩中，本次考试本题已发布成绩的参试学生，非班级人数。

- count：参比 {count} 人
- unknown：参比人数暂未提供

目标条件：表前说明一次；每题仅一格保留人数；独立详情仍保留范围。
位置：表格说明＋参试平均分列

边界：

- 逐题 sample_size 可能不同，不能合并成班级人数。
- 未知人数仍为暂未提供，不写 0。
- 群体位置不等于知识掌握。

验收：

- 同题不重复小字；不同题人数不丢；独立题目仍知道范围。

<a id="mc-p08"></a>

## MC-P08 · 桌面连接与登录帮助（P2）

- 角色：扫描员／阅卷教师。
- 当前条件：连接和登录面板同时常显；handleLogin 直接使用输入 serverUrl，检查和保存不是必经状态。
- 负担／保留理由：说明制造先检查再保存再登录的额外顺序；凭据边界与选项重复。
- 处置：shorten, merge, contextual；依赖：copy。

原文与依据：

- `apps/desktop-client/src/App.tsx:326`（literal）：
  > 请向学校教务处或技术老师索取服务器地址和学校代码。地址先检查并保存，再填写账号登录。
- `apps/desktop-client/src/App.tsx:345`（literal）：
  > 使用学校分配的账号。勾选自动登录后，密码仅保存到当前 Windows 用户的系统凭据库。
- `apps/desktop-client/src/features/session/useDesktopSession.ts:140`（excerpt）：
  > { server_url: serverUrl, tenant_code: tenantCode, username, password }

目标文案／呈现：

> 服务器地址和学校代码由学校提供。填写后即可登录；连接失败时可先检查连接。

- account：使用学校分配的账号登录。
- remember：密码仅保存到当前 Windows 用户的系统凭据库。

目标条件：账号面板仅留来源，凭据说明放自动登录选项旁。
位置：服务器标题下＋自动登录项下

边界：

- 不省略服务器、学校代码和账号范围。
- 浏览器开发模式不承诺保存密码。
- 连接成功不等于认证成功。

验收：

- 不必先点检查和保存才被文字允许登录；选项仍明确凭据存于当前 Windows 用户。

<a id="mc-p09"></a>

## MC-P09 · 桌面本机离线授权（P1）

- 角色：阅卷教师／扫描员。
- 当前条件：原生持久化可用时显示复选项；勾选后需在线登录建立授权。
- 负担／保留理由：复选项过长且下一段重复主语；期限和撤销不能藏进帮助。
- 处置：shorten, keep, contextual；依赖：frontend。

原文与依据：

- `apps/desktop-client/src/App.tsx:361`（literal）：
  > 允许当前 Windows 用户在有效期内离线解锁本账号草稿
- `apps/desktop-client/src/App.tsx:361`（literal）：
  > 联网登录后建立授权，有效期不超过这次登录且最多七天。仅解锁本机加密数据；退出登录或清除保存信息会撤销授权。

目标文案／呈现：

> 允许本机离线解锁本账号草稿

- before：在线登录后生效，仅当前 Windows 用户可用。有效期不超过本次登录，最长七天；退出登录或清除保存信息会撤销。
- after：可离线解锁至 {expiresAt}；仅限本机加密数据，获取任务和上传仍需在线登录。

目标条件：建立前显示条件，成功后显示真实 expiresAt，不将勾选本身当生效。
位置：登录选项及授权状态旁

边界：

- 限当前 Windows 用户、本服务器、本学校和本账号本机数据。
- 有效期不超过本次登录且最多七天，不固定承诺七天。
- 退出或清除保存信息撤销；获取任务和上传仍需在线登录。

验收：

- 未登录勾选不显示已授权；成功后真实到期；退出不遗留可用暗示。

<a id="mc-p10"></a>

## MC-P10 · 离线材料与图片持久化限制（P1）

- 角色：阅卷教师。
- 当前条件：原生工作台说明、材料状态卡、离线恢复提示分别出现。
- 负担／保留理由：关键限制分散，材料已保存易被当作图片也离线保存。
- 处置：merge, shorten, keep；依赖：frontend。

原文与依据：

- `apps/desktop-client/src/components/offlineWorkbench/OfflineWorkbenchView.tsx:238`（literal）：
  > 点击保存后，任务数据、冻结细则与评分草稿加密写入本机，密钥由 Windows 凭据库保护。答卷图片仅缓存于本次会话，重启或加载草稿后可联网恢复图片，人工评分保持原文。
- `apps/desktop-client/src/components/offlineWorkbench/OfflineWorkbenchView.tsx:42`（template）：
  > ${materialDescription}${answerDescription}${draftDescription}答卷图片${imageDescription}，不随加密草稿恢复。
- `apps/desktop-client/src/App.tsx:447`（literal）：
  > 请先加密保存草稿；重启后可在连接登录页解锁本账号草稿。答卷图片仍需联网恢复，上传和下载任务需在线登录。

目标文案／呈现：

> 加密保存：任务、冻结细则、评分草稿。答卷图片仅本次会话可用，不随草稿保存或恢复；重启或加载草稿后需联网恢复。

- saved：任务与评分草稿已加密保存；答卷图片未保存。
- notSaved：任务与评分草稿尚未保存。
- key：密钥由当前 Windows 用户的系统凭据库保护。
- browser：浏览器开发模式仅用于显式加密草稿回退，不能验证生产扫描或上传恢复。

目标条件：图片限制常显；材料卡只更新已保存与否，授权区以到期和短提醒为主。
位置：保存草稿附近，不能仅移入折叠区

边界：

- 显式点击保存，不承诺自动保存。
- 本机授权不提供离线图片恢复。
- 原生与浏览器回退分开，浏览器不能冒充生产扫描站验收。
- 恢复图片不改人工评分；无可核对材料仍按现有门禁。

验收：

- 首次即见图片不保存；保存成功不暗示图片已存；加载后知道联网恢复。

<a id="mc-p11"></a>

## MC-P11 · 离线密钥状态标签（P1）

- 角色：阅卷教师／开发调试人员。
- 当前条件：统一标签用 hasKey；原生可用直接为 true，输入框仅浏览器回退显示。
- 负担／保留理由：原生用户没有输入密钥却被告知已输入，造成找设置的困惑。
- 处置：contextual, shorten；依赖：frontend。

原文与依据：

- `apps/desktop-client/src/components/offlineWorkbench/OfflineWorkbenchView.tsx:22`（literal）：
  > 离线密钥已输入
- `apps/desktop-client/src/components/offlineWorkbench/OfflineWorkbenchView.tsx:22`（literal）：
  > 未配置离线密钥
- `apps/desktop-client/src/components/offlineWorkbench/useOfflineWorkbench.tsx:54`（excerpt）：
  > const hasKey = hasDurableDesktopStore() || offlineKey.trim().length >= 8;

目标文案／呈现：

> 原生：使用本机加密存储

- browserReady：开发密钥已设置
- browserMissing：请设置开发密钥（至少 8 位）

目标条件：按原生能力和浏览器回退分别命名，不用 hasKey 推断写入成功。
位置：工作台状态标签

边界：

- 原生能力可用不等于当前账号绑定、授权或数据保存成功。
- 浏览器达到长度不等于密钥已验证或安全强度保证。
- 仍保留至少八位和显式保存。

验收：

- 原生不再显示已输入；浏览器按八位阈值显示设置状态；保存失败独立显示。

<a id="mc-p12"></a>

## MC-P12 · 评分同步结果待确认（P1）

- 角色：阅卷教师。
- 当前条件：pendingCommand 存在且未已确认同步时显示，评分冻结但可继续同步。
- 负担／保留理由：原操作回执偏内部术语；冻结原因是必须保留的动作解释。
- 处置：shorten, keep；依赖：copy。

原文与依据：

- `apps/desktop-client/src/components/offlineWorkbench/OfflineWorkbenchView.tsx:320`（literal）：
  > 原评分结果尚待确认，分数和评语已冻结。继续同步会先核对原操作回执。

目标文案／呈现：

> 上次提交结果尚未确认，分数和评语暂不可修改。再次同步会先查询上次提交结果。

目标条件：保持仅未知时出现，直至回执核实，冲突与成功各用原状态。
位置：冻结输入区与同步按钮上方

边界：

- 未知不等于失败，不鼓励新开提交。
- 不取消同一操作核对及冻结，不承诺重试必成功或成绩已生效。

验收：

- 超时后知道不能改分原因；再次同步说明核对上次结果；确认后不继续显示未知。

<a id="mc-p13"></a>

## MC-P13 · 扫描上传与质量检查说明（P2）

- 角色：扫描员。
- 当前条件：未选择上下文、队列标题与服务端状态区分别出现；高级答卷标识已折叠。
- 负担／保留理由：真实或假上下文是实现解释；上传、页数、图像区别则是必要工作信息。
- 处置：shorten, merge, keep；依赖：copy。

原文与依据：

- `apps/desktop-client/src/features/scan/ScanWorkspace.tsx:80`（literal）：
  > 请先选择真实考试和可继续上传的采集批次；客户端不会创建假考试上下文。
- `apps/desktop-client/src/features/scan/ScanWorkspace.tsx:84`（literal）：
  > 默认只显示当前考试/批次。文件上传后仍需等待服务端匹配与质量检查。
- `apps/desktop-client/src/features/scan/ScanWorkspace.tsx:92`（literal）：
  > 已确认上传表示服务端收到文件；页数完整性与图像质量分别显示，图像通过后才能继续识别与阅卷。

目标文案／呈现：

> 请选择考试和可上传的采集批次。

- queue：当前考试与批次的待上传文件。上传后仍需服务端匹配与质量检查。
- server：上传确认仅表示已收到文件；请分别查看页数和图像质量，图像通过后才能继续识别与阅卷。
- pagePassed：页数检查通过；图像质量以下方结果为准。

目标条件：未选择只给下一步；已上传后保留接收、匹配、页数、图像独立结果。
位置：选择区空态＋服务端状态区

边界：

- 不取消真实考试批次与允许上传校验。
- 保留当前批次或全部批次范围。
- 页数通过不等于图像通过；未启用设备直接采集仍明确。

验收：

- 不再让用户读假上下文；上传后仍可知等待匹配/质量；页数通过不冒充图像通过。

<a id="mc-p14"></a>

## MC-P14 · 桌面诊断与本地记录来源（P2）

- 角色：学校技术人员。
- 当前条件：诊断、本地日志与本地操作页面分别常显区块说明。
- 负担／保留理由：多次真实或不是 audit_log，可以正向说明来源；技术端点有排障价值。
- 处置：shorten, merge, keep；依赖：copy。

原文与依据：

- `apps/desktop-client/src/App.tsx:504`（literal）：
  > 读取 Tauri 运行时信息；浏览器开发模式会明确标注。
- `apps/desktop-client/src/App.tsx:532`（literal）：
  > 调用真实 /api/v1/system/status；依赖未配置会显示未配置/待接入。
- `apps/desktop-client/src/App.tsx:620`（literal）：
  > 只展示本客户端本地 error 级别日志；不是后端 audit_log。
- `apps/desktop-client/src/App.tsx:646`（literal）：
  > 记录客户端侧关键操作；这不是后端 audit_log。

目标文案／呈现：

> 客户端错误日志：仅本机记录，级别为 error。

- runtime：当前客户端运行环境（原生／浏览器开发模式）。
- server：学校服务器状态；未配置的依赖会单独标明。
- events：本机关键操作记录。学校审计请由有权限的管理员在管理端查看。

目标条件：每区说明来源；接口路径、版本与运行时仍保留诊断明细。
位置：各区标题下，端点留明细

边界：

- 不能称本机记录为学校完整审计。
- 未配置或未接入仍真实显示。
- 不整体删掉专业排障信息。

验收：

- 区分服务器状态和本机日志；浏览器仍标开发模式；完整审计不误导至本机。

<a id="mc-p15"></a>

## MC-P15 · 归档与永久删除本机原件（P1）

- 角色：扫描员／学校技术人员。
- 当前条件：原生面板显示留存说明；选候选后弹窗显示学校保留天数和永久删除边界。
- 负担／保留理由：这类小字必要；仅压缩句式，不能删除归档区别和破坏后果。
- 处置：keep, shorten；依赖：copy。

原文与依据：

- `apps/desktop-client/src/components/NativeStoragePanel.tsx:80`（literal）：
  > 原件持续留存在本机；归档只隐藏队列，不释放磁盘。当前没有自动清理期限。
- `apps/desktop-client/src/components/NativeStoragePanel.tsx:55`（template）：
  > 学校要求保留 {days} 天。仅删除本机加密原件，上传回执仍保留。删除后不能从本机恢复；请先确认学校的服务端备份和保留要求。

目标文案／呈现：

> 归档只隐藏队列，不释放磁盘。原件保留在本机，目前不会自动清理。

- confirmation：学校要求保留 {days} 天。将永久删除所选本机加密原件，上传回执保留；本机无法恢复。请先确认服务端备份和学校保留要求。

目标条件：面板短规则；每次删除确认保留动态天数、文件明细和不可恢复。
位置：存储面板＋破坏性操作确认正文

边界：

- 保留天数不写死；仅删本机原件，回执保留。
- 本机无法恢复，必须先核对备份与学校保留要求。
- 不缩减候选资格、留存期、数量上限与确认步骤。

验收：

- 归档不被理解为释放磁盘；删除前能核对数量文件日期大小及动态保留要求。

<a id="mc-p16"></a>

## MC-P16 · 验证器试点与更换/丢失帮助（P1）

- 角色：教师／学校及平台管理员。
- 当前条件：试点说明、更换/丢失帮助常显；非个人会话加只读限制。
- 负担／保留理由：保护范围必须醒目，异常处理长段可按需读。
- 处置：shorten, progressive, keep；依赖：frontend。

原文与依据：

- `apps/web-admin/src/components/TOTPManagement.tsx:169`（literal）：
  > 验证器生成 6 位动态码，无需短信。目前仅用于本页的验证器管理，尚未用于登录或阅卷操作的强制验证。
- `apps/web-admin/src/components/TOTPManagement.tsx:176`（literal）：
  > 更换验证器：验证并关闭原验证器，重新登录后再设置。若手机和恢复码都丢失，请联系学校管理员记录问题；目前没有自动重置入口，密码恢复链接也不会清除验证器。当前试点不会因此阻断普通登录。
- `apps/web-admin/src/components/TOTPManagement.tsx:173`（literal）：
  > 公共电脑和服务会话只能查看状态。请在个人设备上登录后设置、关闭或重新生成恢复码。

目标文案／呈现：

> 可选试点：目前仅验证本页的验证器管理操作，登录和阅卷尚不强制使用。

- readOnly：当前为公共电脑或服务会话，仅可查看。请在个人设备登录后管理验证器。
- change：更换验证器：先验证并关闭，重新登录后再设置。
- lost：手机和恢复码都丢失时，请联系学校管理员记录问题。目前无自动重置入口，重置密码也不会清除验证器；普通登录仍可使用。

目标条件：试点范围和只读原因常显；更换或丢失帮助可展开。
位置：卡标题下＋操作区下帮助

边界：

- 不声称已保护登录阅卷。
- 无自动重置，重置密码不清除验证器。
- 双重丢失仍不阻断当前试点普通登录。
- 只读会话不能获得管理能力。

验收：

- 首次即知试点范围；公共电脑知只读原因；展开帮助仍见无法自动重置与可普通登录。

<a id="mc-p17"></a>

## MC-P17 · 验证器手动设置与技术参数（P2）

- 角色：个人会话账号所有者。
- 当前条件：enroll 阶段二维码与手动添加区同时显示，类型算法位数周期密钥未折叠。
- 负担／保留理由：扫码用户也要读整套手动参数；它们对手动路径有用，应渐进展示。
- 处置：progressive, shorten, keep；依赖：frontend。

原文与依据：

- `apps/web-admin/src/components/totpManagementPresentation.tsx:112`（literal）：
  > 在验证器中选择“添加账号”，扫描二维码；无法扫描时选择手动输入密钥。请勿截图、分享或将二维码上传到第三方网站。
- `apps/web-admin/src/components/totpManagementPresentation.tsx:114`（literal）：
  > 类型：基于时间 · SHA-1 · 6 位 · 30 秒
- `apps/web-admin/src/components/totpManagementPresentation.tsx:117`（template）：
  > 如果动态码即将刷新，请等待新的码。同一码成功验证后不能再用；如持续失败，请核对手机时间。设置有效期剩余 {deadline}。

目标文案／呈现：

> 在验证器中添加账号并扫码。无法扫码？手动输入密钥。

- secretWarning：请勿截图、分享二维码或密钥，也不要上传到第三方网站。
- codeHelp：动态码即将刷新时请等新码；成功验证的码不能再用。持续失败请核对手机时间。设置剩余 {deadline}。

目标条件：二维码默认，手动展开完整参数和密钥；到期与秘密提醒仍可见。
位置：二维码下现有手动添加区

边界：

- 勿截图分享上传第三方的提醒不能随手动参数一起隐藏。
- 保留时间类型、SHA-1、六位、三十秒及账号。
- 保留成功码不能复用、刷新等待和手机时间提示。

验收：

- 扫码不必读参数；展开后所有原参数和值完整；展开控件可被辅助技术操作。

<a id="mc-p18"></a>

## MC-P18 · 恢复码一次性显示与已生效状态（P1）

- 角色：个人会话账号所有者。
- 当前条件：首次验证启用或重新生成后展示；保存勾选只控制完成关闭，不决定启用。
- 负担／保留理由：已启用藏在长段末，容易以为未勾选即未生效；复制下载也不等于安全保存。
- 处置：merge, shorten, keep；依赖：frontend。

原文与依据：

- `apps/web-admin/src/components/totpManagementPresentation.tsx:144`（literal）：
  > 这批恢复码只显示一次，每条仅能使用一次。关闭、离开页面或超时后不会再次显示。
- `apps/web-admin/src/components/totpManagementPresentation.tsx:148`（template）：
  > 恢复码不能用于登录、修改密码或验证其他业务。此页将在 {deadline} 后隐藏；验证码验证成功时验证器已经启用，无需等待勾选才生效。
- `apps/web-admin/src/components/TOTPManagement.tsx:146`（literal）：
  > 已复制，请粘贴到安全位置；复制不等于已保存

目标文案／呈现：

> 验证器已启用。请安全保存恢复码；这批码关闭、离页或 {deadline} 后不再显示。

- usage：每条仅可使用一次，仅用于本试点关闭验证器；不能登录、改密码或验证其他业务。
- rotate：恢复码已更新，旧码已全部失效。请安全保存这批新码。
- copyToast：已复制，请粘贴到安全位置；复制不等于已保存。
- download：下载的是明文文件，请转存安全位置并清理下载目录；下载不等于已安全保存。

目标条件：首次显示已启用；重新生成显示已更新和旧码失效，再显示用途和安全保存要求。
位置：码列表顶部＋保存确认旁

边界：

- 验证成功已启用；重新生成不得冒充首次启用事件。
- 每码一次，仅关闭验证器用途，不能登录改密码或验证其他业务。
- 旧码重新生成后全失效；复制下载不自动确认安全保存。
- 仍提醒独立安全位置、不留公共电脑共享文件夹聊天，不与手机同失。

验收：

- 未勾选仍知已启用；重新生成知旧码失效；复制下载不自动勾选；关闭超时不承诺再次读取。

<a id="mc-p19"></a>

## MC-P19 · 恢复码验证即消耗（P1）

- 角色：个人会话账号所有者。
- 当前条件：关闭流程选恢复码时输入步骤说明；验证成功确认步骤再次提示。
- 负担／保留理由：看似重复但分别说明将发生与已发生后果，不能删掉。
- 处置：keep, shorten；依赖：copy。

原文与依据：

- `apps/web-admin/src/components/totpManagementPresentation.tsx:129`（literal）：
  > 恢复码在验证成功时即被消耗，即使随后取消关闭操作，也不能再次使用。它仅能验证关闭，不能重新生成恢复码。
- `apps/web-admin/src/components/totpManagementPresentation.tsx:137`（literal）：
  > 本次使用的恢复码已消耗。关闭后请重新登录，再设置新的验证器。

目标文案／呈现：

> 恢复码验证成功即作废，随后取消关闭也不能再用。恢复码仅可验证关闭，不能用来重新生成恢复码。

- after：本次恢复码已消耗。确认关闭后需重新登录，再设置验证器。

目标条件：验证前将消耗，成功后已消耗，均不折叠。
位置：输入框下＋最终确认正文

边界：

- 消耗在验证成功，不在最终确认关闭。
- 取消不关闭验证器，也不退还已消耗码。
- 关闭导致所有设备退出及原验证器恢复码失效的警告保留。

验收：

- 输入前理解消耗；成功取消仍知已用；关闭后重登录重设路径保留。

<a id="mc-p20"></a>

## MC-P20 · 人员字段留空的不同含义（P1）

- 角色：学校人员管理员。
- 当前条件：人员资料弹窗两个可选字段：教职工号空白删除，新手机号空白不改；保存走敏感操作。
- 负担／保留理由：不能合并成统一留空不修改，必须贴字段保留。
- 处置：keep, shorten, contextual；依赖：copy。

原文与依据：

- `apps/web-admin/src/features/members/teachers/MemberProfileDialog.tsx:71`（literal）：
  > 留空可清除原教职工号。
- `apps/web-admin/src/features/members/teachers/MemberProfileDialog.tsx:73`（literal）：
  > 留空保留当前手机号；填写后替换为新号码。
- `apps/web-admin/src/features/members/teachers/MemberProfileDialog.tsx:40`（literal）：
  > 保存姓名、教职工号或新手机号。只有登录身份发生变化时，该人员才需要重新登录。

目标文案／呈现：

> 教职工号：清空后会移除原教职工号。

- phone：新手机号：留空不修改，填写后替换当前号码。
- stepUp：确认保存人员资料；登录身份变化后，该人员需重新登录。

目标条件：字段各留一条；重新登录或原邀请失效只在实际变化成功后显示。
位置：各字段下方，不移至统一底注

边界：

- 不改 payload 空白语义。
- 仅登录身份变更需重新登录。
- 保留邀请失效重生成、并发冲突与输入保留提醒。

验收：

- 只改姓名手机号空白仍保留；清教职工号先知后果；登录身份改变后仍有对应提醒。

<a id="mc-p21"></a>

## MC-P21 · 班级代码与导入关联（P2）

- 角色：学校组织管理员。
- 当前条件：编辑代码字段总显变更影响，新增时显示 CSV 匹配用途。
- 负担／保留理由：用途必要；未改代码时无需常显修改后的后果。
- 处置：contextual, shorten, keep；依赖：frontend。

原文与依据：

- `apps/web-admin/src/features/members/classes/ClassManagementPage.tsx:131`（literal）：
  > 修改后，学生导入文件需使用新的班级代码。
- `apps/web-admin/src/features/members/classes/ClassManagementPage.tsx:135`（literal）：
  > 学生 CSV 导入会使用班级代码进行匹配。

目标文案／呈现：

> 用于匹配学生导入文件中的班级代码。

- changed：班级代码已改，后续学生导入文件请使用新代码。

目标条件：新增显示用途；编辑仅代码相对原值变化时显示导入影响。
位置：代码字段下，学校年级仍常显

边界：

- 仍必填并保留服务端约束。
- 不承诺更新历史导入文件或自动迁移学生。
- 不省略学校和年级范围。

验收：

- 新增理解用途；只改名称不见假变更；代码改变即提醒后续文件同步。

<a id="mc-p22"></a>

## MC-P22 · 模型 API Key 分支帮助（P1）

- 角色：平台模型管理员。
- 当前条件：新建、编辑、沿用已有密钥各有说明；换 API 域名或端口需重填。
- 负担／保留理由：默认读取全部域名端口路径分支太长，可按实际变化提示。
- 处置：contextual, shorten, keep；依赖：frontend。

原文与依据：

- `apps/web-admin/src/features/platform-model-config/components/ModelConfigForm.tsx:47`（literal）：
  > 原密钥不会回显；留空则继续使用。同域名、同端口修改路径无需重填；更换域名或端口时需重新输入，可以使用原密钥。
- `apps/web-admin/src/features/platform-model-config/components/ModelConfigForm.tsx:47`（literal）：
  > 密钥将加密保存；保存只做零生成 Token 连接检测。
- `apps/web-admin/src/features/platform-model-config/components/ModelConfigForm.tsx:29`（literal）：
  > 新模型会单独验证并加密保存；后续更换密钥需逐个更新模型配置。

目标文案／呈现：

> 已保存的密钥不回显；留空继续使用。

- changed：API 域名或端口已变，请重新输入密钥；可以使用原密钥。
- new：密钥将加密保存。保存时检查连接，不生成回复。
- reuse：此配置将单独验证并加密保存；后续更换密钥需逐个更新配置。

目标条件：编辑未改源站显示基本规则；换域或端口切换为重填；仅改同域同端口路径不警告。
位置：API Key 字段下＋复用提示

边界：

- 不回显密钥、不取消换源站重填。
- 复用单独验证加密，后续轮换不自动同步。
- 保存连接检查不代表通过其他用途检测。
- 零生成 Token 不可改为供应商绝不收费。

验收：

- 未改源站空白沿用；仅改路径不强制；换源站明示可用原密钥重填；复用知逐项轮换。

<a id="mc-p23"></a>

## MC-P23 · 获取模型列表的零 Token 术语（P2）

- 角色：平台模型管理员。
- 当前条件：模型名称字段列获取列表帮助；检测按钮已有连接与生成两类 title。
- 负担／保留理由：零 Token 易被当免费承诺，换为实际动作边界。
- 处置：shorten, merge, keep；依赖：copy。

原文与依据：

- `apps/web-admin/src/features/platform-model-config/components/ModelConfigForm.tsx:98`（literal）：
  > 零 Token 获取模型列表，也可手工输入模型名称。
- `apps/web-admin/src/features/platform-model-config/components/modelConfigColumns.tsx:86`（literal）：
  > 只检查接口、密钥和模型权限，不生成回复
- `apps/web-admin/src/features/platform-model-config/components/modelConfigColumns.tsx:86`（literal）：
  > 发送合成测试内容，会产生少量模型用量

目标文案／呈现：

> 可获取模型列表，也可直接输入模型名称；获取列表不生成回复。

目标条件：列表旁一句；检测按钮仍按类型给说明。
位置：名称字段下＋检测动作提示

边界：

- 不保证免费、不受限流。
- 生成检测发送合成内容且会用量。
- 拉列表失败仍允许手动输入。

验收：

- 可手输；连接与生成提示不混用；列表失败有填写回退。

<a id="mc-p24"></a>

## MC-P24 · 自动阅卷用途与图片检测（P1）

- 角色：平台模型管理员。
- 当前条件：点阅卷配置打开用途抽屉；检测依赖保存的启用配置；多模型角色现已可选折叠。
- 负担／保留理由：三段可按阶段组织，不重复讲独立用途与可先单模型。
- 处置：merge, shorten, contextual, keep；依赖：frontend。

原文与依据：

- `apps/web-admin/src/features/platform-model-config/components/ImageGradingModelForm.tsx:33`（literal）：
  > 识别学生答题图并生成评分建议，教师确认最终分数。此用途单独保存，不使用本地模型。
- `apps/web-admin/src/features/platform-model-config/components/ImageGradingModelForm.tsx:34`（literal）：
  > 当前可先使用一个 API 模型测试；多模型评审角色为可选配置，不影响单模型自动阅卷。
- `apps/web-admin/src/features/platform-model-config/components/ImageGradingModelForm.tsx:43`（literal）：
  > 先保存，再检测图片能力。检测发送合成图片，会产生少量模型用量；密钥停用或配置变化后需要重新检测。

目标文案／呈现：

> 生成评分建议，教师确认最终分数。本校自动阅卷独立配置 API 模型，不回退本地模型。

- notSaved：先保存本校自动阅卷模型，再检测图片能力。
- probe：检测发送合成图片，会产生少量模型用量。密钥停用或配置变化后需重新检测。
- roles：多模型评审角色（可选；单模型可直接测试）

目标条件：主边界常显；未保存先提示保存；已保存显示图片检测用量及失效条件。
位置：抽屉顶部＋检测按钮旁

边界：

- 单模型不免除准入与最终确认。
- 图片读取通过不等于正式评分全部门禁通过。
- 密钥停用或配置变化需重检。
- 不把当前已折叠的多模型角色误写成全展开。

验收：

- 未保存知下一步；检测前知合成图用量；无需展开多模型仍知人工确认与无本地回退。

<a id="mc-p25"></a>

## MC-P25 · 模型页全局用途指南（P2）

- 角色：平台模型管理员。
- 当前条件：生产模型页顶部 Alert 常显四用途＋密钥说明；角色限平台管理员，用途抽屉又有细则。
- 负担／保留理由：每次看列表需跨过入门长说明，可保留总原则与按需帮助。
- 处置：progressive, merge, shorten；依赖：frontend。

原文与依据：

- `apps/web-admin/src/pages/PlatformModelConfigPage.tsx:583`（literal）：
  > 普通回复检测通过后，可勾选多个供应商的模型。学校管理员在聊天页顶部切换。
- `apps/web-admin/src/pages/PlatformModelConfigPage.tsx:584`（literal）：
  > 通过结构化输出检测后，可独立指定模型或跟随默认配置；学校管理员切换聊天模型不会改变试卷解析。
- `apps/web-admin/src/pages/PlatformModelConfigPage.tsx:585`（literal）：
  > 在“应用用途”中独立分配多模态模型，直接分析答题卡图像；识别结果需人工核对后发布。
- `apps/web-admin/src/pages/PlatformModelConfigPage.tsx:586`（literal）：
  > 点击“阅卷配置”指定本校自动阅卷 API 模型并检测图片能力，系统不会回退本地模型。多模型评审角色可按需展开；模型准入与最终确认继续按已配置策略执行。
- `apps/web-admin/src/pages/PlatformModelConfigPage.tsx:587`（literal）：
  > 连接检查只验证接口、密钥和模型访问权限。同一学校可为同一供应商和模型配置多个 API Key，密钥分别加密保存。

目标文案／呈现：

> 按用途独立配置学校模型；连接通过不代表已通过各用途检测。

- helpTitle：用途与检测说明
- dialogue：日常对话：普通回复检测通过后可开放多个模型，学校管理员在聊天页切换。
- document：试卷解析：通过结构化检测后独立指定或跟随默认配置；聊天切换不会改变此设置。
- answerSheet：答题卡识别：独立指定多模态模型，人工核对后发布。
- grading：自动阅卷：独立指定 API 模型并检测图片能力，不回退本地；正式使用仍需准入与最终确认。

目标条件：学校和用途状态常显，完整分工放可展开用途与检测说明；关键边界仍留各抽屉。
位置：列表上方短说明＋展开帮助

边界：

- 聊天切换不改独立解析/答题卡用途。
- 答题卡人工核对后发布，正式评分依准入与最终确认。
- 自动阅卷不回退本地；多密钥加密与轮换可查。
- 保留所选学校与未保存切校保护。

验收：

- 首次可查四用途；常用不重复读；各用途动作仍见本身边界。

<a id="mc-p26"></a>

## MC-P26 · 专业模型诊断与返回摘要（P2）

- 角色：平台模型运维人员。
- 当前条件：表格已有摘要状态；有记录且无进行操作可点开详情，显示结束原因、输出约束、长度与用量，并非全页常显。
- 负担／保留理由：已处于按需专业诊断层，不应当普通教师负担整体删除；仅短化摘要说明。
- 处置：keep, shorten；依赖：copy。

原文与依据：

- `apps/web-admin/src/features/platform-model-config/hooks/useSchoolModelActions.tsx:81`（literal）：
  > 本次返回摘要（最多 256 个字符，不会写入数据库）
- `apps/web-admin/src/features/platform-model-config/hooks/useSchoolModelActions.tsx:82`（literal）：
  > 供应商没有返回可展示的内容摘要。
- `apps/web-admin/src/features/platform-model-config/components/modelConfigColumns.tsx:68`（excerpt）：
  > const hasDetails = Boolean(status && status !== "untested" || testedAt || message || interruptedStructured);
- `apps/web-admin/src/features/platform-model-config/components/modelConfigColumns.tsx:80`（excerpt）：
  > aria-label={`${config.model_name}：${detailLabel}`} onClick={showDetails}

目标文案／呈现：

> 本次返回摘要（最多 256 字符，不写入数据库）

- noPreview：供应商未提供可展示的返回摘要。

目标条件：保持点击结果查看；不要向列表添加常显技术字段。
位置：现有诊断弹窗摘要上方

边界：

- 保留 Token、finish_reason、response_format、API 地址。
- 不写入数据库仅指摘要，不扩大为检测无记录。
- 不承诺摘要永久可追溯。
- 缺失字段不等于已通过、零用量或零输出；本条不授权改默认值。

验收：

- 各检测详情仍可查看；无摘要不伪造；明确长度与不入库。

<a id="mc-p27"></a>

## MC-P27 · 学校策略预算零值与单位（P1）

- 角色：平台模型治理管理员。
- 当前条件：所选学校治理的使用策略加载后显示；标签为美元，支持六位小数，保存转微美元。
- 负担／保留理由：零值阻断和考试范围必要，存储用微美元可移出主要帮助。
- 处置：shorten, keep；依赖：copy。

原文与依据：

- `apps/web-admin/src/components/model-governance/ManagedGovernanceWorkspace.tsx:166`（literal）：
  > 0 表示尚未配置预算，不能通过外部沙箱准入；不是不限额。
- `apps/web-admin/src/components/model-governance/ManagedGovernanceWorkspace.tsx:167`（literal）：
  > 按平台微美元口径保存，最多六位小数；不是本月的学校总额度。
- `apps/web-admin/src/components/model-governance/ManagedGovernanceWorkspace.tsx:148`（template）：
  > 当前生效策略 · 版本 {policy.version}

目标文案／呈现：

> 0 表示预算未配置，不能通过外部沙箱准入。

- exam：仅限这一场考试，非学校月总额度；最多六位小数。

目标条件：每题保留零值含义，整场保留单次考试范围与精度；技术存储单位留运维文档。
位置：预算字段下，学校和策略版号仍在标题

边界：

- 0 不等于不限额，不能默认放行。
- 美元输入、六位精度和精确微美元转换保持。
- 整场不是学校月额度。
- 保留学校、策略版本、expected_version 与外发授权范围。

验收：

- 0 明确阻断；单位美元且最多六位小数；不误读为月额度。

## 本轮逐文件状态

reviewed 仅全文重新审读；partial 的未读部分仍 pending。专项清单不代表全仓覆盖。

| 文件 | 状态 | 实际阅读 |
| --- | --- | --- |
| `AGENTS.md` | reviewed | 全文复核；仅写两份方案。 |
| `docs/engineering/commenting-standard.md` | reviewed | 全文复核准确边界与逐文件状态要求。 |
| `apps/student-portal/src/features/appeals/AppealStatus.tsx` | reviewed | 本轮全文重新审读；仅支持小字及出现条件，不代表浏览器或硬件验收。 |
| `apps/student-portal/src/features/results/ResultDetail.tsx` | reviewed | 本轮全文重新审读；仅支持小字及出现条件，不代表浏览器或硬件验收。 |
| `apps/student-portal/src/features/results/OldAppealDrafts.tsx` | reviewed | 本轮全文重新审读；仅支持小字及出现条件，不代表浏览器或硬件验收。 |
| `apps/student-portal/src/features/session/SessionScreens.tsx` | reviewed | 本轮全文重新审读；仅支持小字及出现条件，不代表浏览器或硬件验收。 |
| `apps/student-portal/src/features/questions/QuestionCohortScope.tsx` | reviewed | 本轮全文重新审读；仅支持小字及出现条件，不代表浏览器或硬件验收。 |
| `apps/student-portal/src/features/appeals/QuestionAppealForm.tsx` | reviewed | 本轮全文重新审读；仅支持小字及出现条件，不代表浏览器或硬件验收。 |
| `apps/desktop-client/src/components/offlineWorkbench/OfflineWorkbenchView.tsx` | reviewed | 本轮全文重新审读；仅支持小字及出现条件，不代表浏览器或硬件验收。 |
| `apps/desktop-client/src/components/NativeStoragePanel.tsx` | reviewed | 本轮全文重新审读；仅支持小字及出现条件，不代表浏览器或硬件验收。 |
| `apps/desktop-client/src/features/scan/ScanWorkspace.tsx` | reviewed | 本轮全文重新审读；仅支持小字及出现条件，不代表浏览器或硬件验收。 |
| `apps/web-admin/src/components/TOTPManagement.tsx` | reviewed | 本轮全文重新审读；仅支持小字及出现条件，不代表浏览器或硬件验收。 |
| `apps/web-admin/src/components/totpManagementPresentation.tsx` | reviewed | 本轮全文重新审读；仅支持小字及出现条件，不代表浏览器或硬件验收。 |
| `apps/web-admin/src/features/platform-model-config/components/ModelConfigForm.tsx` | reviewed | 本轮全文重新审读；仅支持小字及出现条件，不代表浏览器或硬件验收。 |
| `apps/web-admin/src/features/platform-model-config/components/ImageGradingModelForm.tsx` | reviewed | 本轮全文重新审读；仅支持小字及出现条件，不代表浏览器或硬件验收。 |
| `apps/web-admin/src/features/platform-model-config/components/DocumentModelForm.tsx` | reviewed | 本轮全文重新审读；仅支持小字及出现条件，不代表浏览器或硬件验收。 |
| `apps/web-admin/src/features/platform-model-config/components/AnswerSheetModelForm.tsx` | reviewed | 本轮全文重新审读；仅支持小字及出现条件，不代表浏览器或硬件验收。 |
| `apps/web-admin/src/features/platform-model-config/components/modelConfigColumns.tsx` | reviewed | 本轮全文重新审读；仅支持小字及出现条件，不代表浏览器或硬件验收。 |
| `apps/web-admin/src/features/members/teachers/MemberProfileDialog.tsx` | reviewed | 本轮全文重新审读；仅支持小字及出现条件，不代表浏览器或硬件验收。 |
| `apps/web-admin/src/features/members/classes/ClassManagementPage.tsx` | reviewed | 本轮全文重新审读；仅支持小字及出现条件，不代表浏览器或硬件验收。 |
| `apps/web-admin/src/components/model-governance/ManagedGovernanceWorkspace.tsx` | reviewed | 本轮全文重新审读；仅支持小字及出现条件，不代表浏览器或硬件验收。 |
| `apps/desktop-client/src/App.tsx` | partial | 重读 322–398、442–468、499–676 及邻近渲染，其余 pending。 |
| `apps/desktop-client/src/components/offlineWorkbench/useOfflineWorkbench.tsx` | partial | 重读 43–59 hasKey 与材料条件，其余 pending。 |
| `apps/desktop-client/src/features/session/useDesktopSession.ts` | partial | 重读 65–153 登录与授权，其余 pending。 |
| `apps/desktop-client/src/features/runtime/useRuntimeDiagnostics.ts` | partial | 重读 45–76 地址保存与健康检测，其余 pending。 |
| `apps/student-portal/src/features/questions/QuestionTables.tsx` | partial | 重读表格/范围组件调用与后半渲染，中段输出截断；未全文，其余 pending。 |
| `apps/web-admin/src/features/platform-model-config/hooks/useSchoolModelActions.tsx` | partial | 重读 41–108 三类诊断，其余 pending。 |
| `apps/web-admin/src/pages/PlatformModelConfigPage.tsx` | partial | 重读 577–589 全局指南及检索的用途/多模型折叠调用，其余 pending。 |
| `apps/web-admin/src/router/routes.tsx` | partial | 检索核对平台模型生产路由和角色，未全文，其余 pending。 |
| `apps/student-portal/src/App.tsx` | pending | 本轮未全文重读，不借用旧审计计完成。 |
| `apps/desktop-client/src/lib/durableStore.ts` | pending | 本轮未重读原生加密实现，不声称能力可用即密钥写入成功。 |
| `apps/web-admin/src/pages/SessionManagementPage.tsx` | pending | 本轮未重读；不扩大 TOTP 组件结论至其他会话小字。 |
