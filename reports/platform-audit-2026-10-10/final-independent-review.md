# 本地平台扫描交付证据复核

核验截点：2026-10-11T13:40:37.164508+08:00（Asia/Shanghai）。读取本地报告及保存的执行/结果附件；本次只写这一复核文件，没有改平台报告、原始结果或业务源码，没有查询项目远端。

截至此截点，未发现下面四份平台报告将未执行的云平台、无效覆盖率、自动告警或未审文件冒充完成的实质矛盾。CodeQL Go 合并附录的结构与位置也已对齐原始 SARIF。总报告的 CodeQL 行当时仍有 Go/Rust 待完成文字，由主智能体在最终汇总时更新；这份复核不据此前置文字认定 Rust 已完成。

## 对齐结果

| 报告 | 保存的实际证据 | 核验结论 |
| --- | --- | --- |
| [SonarQube](sonar/REPORT.md) | CE SUCCESS；2223 唯一问题、51 命中规则、1339 个平台指标文件；BUG 1653 / VULNERABILITY 10 / CODE_SMELL 560 | 与原始 issues/metrics/export 一致；报告明确原 Scanner 压缩中断，真实恢复提交通过本机官方接口完成 |
| [Semgrep](semgrep/REPORT.md) | exit 0；105 命中，ERROR 7 / WARNING 87 / MEDIUM 11；16 PartialParsing；3197 CLI 目标 | 105 人工条目和 105 Markdown 表行均齐全，路径去除容器快照前缀后，逐项 rule/path/line 对齐原始结果 |
| [GitHub Actions 本地工作流检查](actions/report.md) | actionlint 1.7.7；exit 0；原 log 0 字节；3 个工作流 SHA 与本次输入一致 | 仅声明本地静态检查通过，明确未触发 GitHub Actions run，未把 CI 声明任务算作实际通过 |
| [Codecov / 覆盖率旧附件](coverage/report.md) | 旧 go-core.cover 13 字节，仅 mode: atomic；旧 go-test.log 空；无旧执行退出状态 | 正确记为无效未完成附件，没有计算实测百分比，也没有声称 Codecov 已运行或已上传 |

## SonarQube 关键边界

原始扫描没有标准发布成功证据。报告恢复 ZIP 24401500 字节、SHA-256 与 recovery-integrity.json 一致；完整报告目录的 12029 条记录、ZIP CRC/逐条 SHA 校验标志齐全。Compute Engine 的任务 ID、独立项目和 SUCCESS 状态可在 compute-engine-task.json 对照。报告清楚说明只提交至 127.0.0.1:19000，旧失败与恢复路径没有混算。

指标 227136 ncloc、重复率 1.5%、修复估计 6898 分钟、维护性 A / 可靠性 D / 安全 C 均与 metrics.json 对齐。coverage=0.0 没有被当作实测零覆盖；quality-gate.json 的 OK 同时附 conditions=[]，没有被当作全仓质量条件有效达标。门禁定义与本次评估分开保存。

原始 BUG 里 css:S4649 为 1645 条；文档问题占比及 8 个其他 BUG 的描述没有将所有 BUG 等同运行故障。finding-review.json 的 22 条已审和 2201 条 pending 恰好覆盖全部 2223 个 issue key，无重号或缺漏。Python 单文件分析失败、Rust Clippy 跳过、高亮/符号警告、live 工作区变化在报告明确保留。

## Semgrep 关键边界

scan-summary.json 和 triage 的人工分布一致：false_positive 68、needs_review 13、hardening 11、context_limited 4、needs_deployment_review 2、test_only 7；后四类合计低影响 24。human_pending_findings=0 指每个告警已分配人工结论，13 条条件性风险和 2 条部署前提仍需确认；并未被说成所有风险已消除。

stderr 明确加载735规则、实际执行434规则和约99.9%解析行。报告没有将735全部视为运行，也没有把 skipped_rules=[] 当作完整覆盖证明。源码只读、容器断网、metrics off、OSS而非Pro跨文件分析范围均在执行附件/报告保留。SG-090 后续由其他工作修复，报告保留扫描快照告警和后续状态，不冒充按最新代码重新扫描。

自动源码清单的状态仍保留：恢复维护清单2502个文件全为pending；Semgrep源码清单3196个文件的 human_review 分布为 {'pending': 3196}。105 个告警位置已复核不能转换成3196文件全篇人工审读完成。

## Actions 与覆盖率关键边界

actionlint-exit.txt 和 execution.json 都为0，actionlint-version.txt 为1.7.7。空log与无诊断一致。核验时三个工作流字节哈希均匹配11:26本地检查记录；已声明的测试/构建/竞态/架构任务与实际运行结果明确区分，当前文件未有Sonar/Semgrep/CodeQL/Codecov集成步骤。

contracts/testing/coverage-floor.json 六个门槛为auth 57.1、subjective 63.4、modelgovernance 45.6、scorerelease 29.2、releasegate 41.4、capture 11.4，和coverage/report.md表格一致。表格是门槛，未冒充测量或系统总覆盖率。

主智能体已另行启动固定快照六Go包覆盖率复测；本截点尚无新结果。新版若生成须以其实际退出状态、非空profile条目和解析结果再核验，旧header结论只对应旧附件。无论复测成功与否，Codecov云平台仍未运行。

## CodeQL Go 合并附录核对

go-finding-review.json 的23条零基索引完整覆盖0..22，逐项rule_id/path/line全部匹配go.sarif，保存的SARIF SHA-256也匹配原文件。人工分类为14上下文误报、1有意测试行为、6低影响维护、1资源预算加固、1条件性部署检查，计数合计23；不存在告警遗漏或因条件性部署风险已分配结论而宣称风险彻底排除。该检查限于合并一致性，不重新认证根智能体其他9项的全部业务调用链。

## 文档与证据指纹

平台报告中的相对本地附件Markdown链接未发现缺失目标；原始文件、失败记录与人工附录都留在本机。下面哈希标识此截点实际审阅的报告文本，后续新增覆盖率附件/汇总会另有时间与状态。

| 文件 | SHA-256 |
| --- | --- |
| sonar/REPORT.md | `4a14133a67449fdd94adc23f6270d0fb3c35e74145d7c393b37152926f9bd9e4` |
| semgrep/REPORT.md | `147b6f2a5d0eeb6fddfc0cdec0ca83ca73d8ff5d5dee895b6fbaf04e315869ad` |
| actions/report.md | `383c1ce97530ab0ae233224843c697d81c41c1ec8188435f13d3bad761e78802` |
| coverage/report.md | `8242dddcc6a14171c2d815cf2546657c5b706fb45a2e11e5f097d562d7725df1` |


## 覆盖率最终补充核验（2026-10-11）

核验截点：2026-10-11T13:59:06.781700+08:00（Asia/Shanghai）。这一节针对新增第二轮有效产物；前文“旧附件无效”的判断仅对应 2026-10-10 的 13 字节旧文件头，不再作为当前本地测量状态。

重新解析 `coverage/go-core-resume.cover` 的 `mode: atomic` 和全部 11,105 条记录，无格式错误；大小 1,068,265 字节，SHA-256 为 `507f3df437985de21658f7000018092cc3e33e3d609f42a3f90f2a1e0e4c90ee`。逐包覆盖/总语句/记录数、精确有理数门槛比较及合计均独立对齐 `coverage-summary.json`；没有用舍入值决定达标。已核对官方 `go tool cover -func` 的合计 49.5% 与精确加权值一致，工具退出码为 0。

| 包 | 覆盖/总语句 | 精确值（显示6位小数） | 配置最低门槛 | 核验 |
| --- | ---: | ---: | ---: | --- |
| auth | 2576/4253 | 60.569010% | 57.1% | 达到 |
| subjective | 2133/3248 | 65.671182% | 63.4% | 达到 |
| modelgovernance | 2252/3761 | 59.877692% | 45.6% | 达到 |
| scorerelease | 735/1694 | 43.388430% | 29.2% | 达到 |
| releasegate | 176/426 | 41.314554% | 41.4% | 未达到 |
| capture | 511/3563 | 14.341847% | 11.4% | 达到 |

第二轮 `go test` 于 13:46:37–13:49:57 真实完成，六包均 ok、退出码 0。六包合计为 8,383/16,945 条语句，即 49.47182059604603%；这不是全仓或整个阅卷系统覆盖率。报告没有把测试通过说成门槛通过。

既有仓库门禁导出的 `main` 通过已有 `spawn` 注入点读取第二轮保存的原始 stdout 和退出码，实际执行原解析、缺失检查和门槛分支；它不是再跑一轮 Go 测试，也不是 GitHub Actions 平台 run。13:55:45–13:55:48 的门槛执行退出码 1，原 log 明确 `coverage violation: releasegate: 41.3% is below the 41.4% floor`。脚本从一位小数测试日志判断；独立 profile 精确比较同样仅 releasegate 未达到。没有改门槛来制造通过。

独立核对 profile、测试执行附件与 log、原门禁脚本、可信可执行文件辅助模块、包装入口、门槛配置、输入上下文 SHA-256 均匹配记录。补入的 7 个当前工作区 JSON 夹具及另记门槛配置的副本哈希亦匹配。报告明确混合输入来自固定 Go 源码加当前本地夹具，255 个六包 Go 文件前后未变；真实 PostgreSQL 和真实 AI 服务测试未启用，无 race 结果。Codecov 平台仍未执行，也没有上传。

[覆盖率最终报告](coverage/report.md)当前文本 SHA-256：`4a8df0391ab62afd16d316940f90d703db1f9eda0712f8d741ab8a006239bd47`。上述最终结果与报告和执行附件一致，未发现实质矛盾。Rust 仍须等实际建库与查询结束再核验，不能由这一节推断五语言已全部扫描。


## 最终平台证据复核（2026-10-11）

最终核验截点：2026-10-11T16:33:26.754884+08:00（Asia/Shanghai）。本节以真实本地资源补齐后完成的 Rust 复扫为当前结果；前面各节保留各自历史截点。这里追加独立核验，并更新 `codeql/rust-finding-review.json/.md` 为新 SARIF 绑定；没有修改业务源码或其他智能体的平台报告，也没有访问私有远端或上传源码。

已逐语言读取真实 create/analyze 退出状态、对应数据库、完整分析日志及原始 SARIF：五种语言均建库0、分析0、invocation executionSuccessful=true，每种语言的 create/analyze 使用同一本地数据库。选定套件505条与成功日志中的505次 Evaluation done 对齐；不是以源文件枚举或启动查询推断完成。

| CodeQL 语言 | 完整查询完成数 | 原始候选数 | 当前核验 |
| --- | ---: | ---: | --- |
| Python | 174/174 | 55 | 1条有前提的CPU放大风险；54条质量候选已分配人工结论 |
| JavaScript / TypeScript | 203/203 | 14 | 4条有前提的本地文件竞争加固；其余维护/有意行为/上下文结论保留 |
| Go | 59/59 | 23 | 14上下文误报、1测试、6低影响维护、1预算加固、1条件性部署检查 |
| GitHub Actions 语言 | 28/28 | 0 | 官方CodeQL静态工作流查询完成；不等于GitHub平台run |
| Rust | 41/41 | 14 | 新真实资源上下文完成；3上下文误报、11有意测试行为 |
| 合计 | 505/505 | 106 | 全部候选已分配人工结论，pending=0；条件性风险仍保留 |

独立核对 `alerts.csv` 的106个语言/零基索引均唯一且无遗漏，逐项 rule/path/line 与对应原始SARIF匹配；CSV人工分类合计与最终summary完全一致。告警已分配结论不能当成条件全部消失：Python的ReDoS输入路径、JavaScript本地目录控制前提、Go特殊地址部署条件和计算预算限制仍需按主附录解释。

### Rust 最终原件与上下文

新库 `rust-20261011-context` 于15:14:50–15:29:16建库退出0；首个4096 MiB分析因原生分配失败退出99的附件仍保存，恢复仅重跑本库分析。最终3072 MiB / 单线程分析于15:34:35–16:20:55退出0，41条查询完整完成。新 `rust.sarif` SHA-256 为 `322d47e65455d5a6c04bc29428cfbb7d94409c0761a34cb1943162931d2c8423`，不再使用首轮 `69b53cea…` 作为终版绑定。

全部14个完整raw result对象与归档 `rust-attempt3.sarif` 完全相同，包含主位置、关联位置、消息、指纹和全部330个codeFlow位置步骤。本次不是只比较候选总数或行号；已在最终 `source-rust-context-20261011` 源根重算14个主位置与42段列明证据的SHA和逐行内容，56处均匹配。人工结论再次确认为3条布尔/随机初始化上下文误报和11条实际cfg(test)模块内有意固定/错误key测试；原始security-severity 9.8 / Critical、problem warning保持可见，不将14候选换算成14个生产漏洞。

新源根全部2514条原始输入记录逐一重算哈希，无改动或缺失；额外真实本地ICO为4734字节、SHA `016265bc71058837ebb82d73e5289a0fef8f81737cf7e7f5b6e75de723f87a17`，4个构建生成schema另列并核验。它们补齐了实际构建上下文，没有修改固定维护源码，也没有冒充与首轮全部输入完全相同。既有离线锁定cargo check的实际exit0与上下文附件一致；它是编译探针，不是Rust测试或应用运行。

7项官方BQRS诊断解码的exit0、原BQRS哈希、输出JSON哈希和计数均独立一致：ExtractedFiles16、ExtractionErrors0、ExtractionWarnings28、UnextractedElements0、UnresolvedMacroCalls0；文件度量14 without-error、2 with-errors，后者包括warnings。新库未展开宏查询为0，旧generate_context缺资源/未展开问题仅作为历史记录保留。queue.rs15条、spool.rs13条警告逐项核对均位于CRLF多行SQL字面量行尾，仍作为语义覆盖局限保留，不能把合法字面量警告当成业务语法缺陷，也不能据0个error声称全部业务语义无漏报。

### 其他平台的当前状态与真实门禁

Sonar当前2223唯一问题与原始平台导出、恢复CE SUCCESS一致：BUG1653、CODE_SMELL560、VULNERABILITY10；22条重点候选已审、2201条仍pending。原Scanner标准发布未成功，完整报告通过本机官方API恢复提交的边界保留。quality gate的OK仍为conditions=[]，coverage=0也仍未导入测量，不能把这两项解释成有效质量门禁或系统实测零覆盖。

Semgrep仍为105条原始候选：68上下文误报、13有前提的中等风险、24低影响项；16 PartialParsing、735规则加载/434实际运行的限制保留。Actions本地actionlint退出0且3个工作流当前SHA与执行记录匹配；没有远端GitHub Actions run。

有效六包覆盖率仍为8383/16945，即49.47182059604603%，profile SHA `507f3df437985de21658f7000018092cc3e33e3d609f42a3f90f2a1e0e4c90ee`。测试退出0、官方cover退出0，现有门禁实际退出1；releasegate的176/426=41.31455399061033%低于41.4%，为唯一未达最低门槛包。输入是固定Go源码加7个当前本地JSON夹具及单列门槛配置；真实PostgreSQL/AI服务测试与race没有运行。Codecov平台没有执行或上传，不能把本地覆盖率冒充Codecov平台报告。

维护源码清单2502个全文件人工状态仍为pending；Semgrep3196个全文件清单的历史pending边界继续有效。22个Sonar告警、105个Semgrep候选或106个CodeQL候选的位置审读均不转换成全仓逐文件审读完成。阅卷准确率、切题/识别一致性、AI仲裁及并发事务可靠性没有由静态扫描认证。

### 此截点交付指纹

| 当前附件 | SHA-256 |
| --- | --- |
| sonar/REPORT.md | `4a14133a67449fdd94adc23f6270d0fb3c35e74145d7c393b37152926f9bd9e4` |
| semgrep/REPORT.md | `147b6f2a5d0eeb6fddfc0cdec0ca83ca73d8ff5d5dee895b6fbaf04e315869ad` |
| actions/report.md | `383c1ce97530ab0ae233224843c697d81c41c1ec8188435f13d3bad761e78802` |
| coverage/report.md | `4a8df0391ab62afd16d316940f90d703db1f9eda0712f8d741ab8a006239bd47` |
| codeql/README.md | `70bbd7636f1ce5a9785b40a73d299738404a494d9e9ec702ab379a24f77c57a1` |
| codeql/summary.json | `933392af9d4a4806014749e90801bec27f67645532df58e3f6ee85edafd189d3` |
| codeql/rust.sarif | `322d47e65455d5a6c04bc29428cfbb7d94409c0761a34cb1943162931d2c8423` |
| codeql/rust-finding-review.json | `891fefd96be477ff0c88ca4db29bb90accbd5a56e86e2a1508adfd2766073a4b` |
| codeql/rust-finding-review.md | `9b76e35e7dd9d8a1efb6262011300871b1eadc57cc5341061b69cf763f5d0ac4` |

本次独立核验未发现以上最终结果与保存原始证据的实质矛盾。主智能体将在所有写入结束后执行机器交付校验并生成最终附件SHA清单；本节不提前宣称那个未来步骤已通过，也不把先前preflight的检查数当成最终检查数。
