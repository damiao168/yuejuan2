# SonarQube 高风险类别及 BUG 候选人工核验

核验时间：2026-10-11T13:20:56.788370+08:00。这是 [SonarQube 官方引擎结果](issues.json) 的人工解释附录。原始 2223 条候选、10 条 VULNERABILITY / 1653 条 BUG / 560 条 CODE_SMELL 完整保留；未改平台问题状态或源码。

已逐条检查 10 个 VULNERABILITY、8 个非 CSS BUG，再抽查 4 个 CSS 字体代表，共 22 个告警上下文。其余 2201 条继续为 pending（1641 CSS + 560 CODE_SMELL）。没有把代表抽查或自动枚举说成全仓人工审读。

| 已核部分 | 人工解释 |
| --- | --- |
| 6 个 Python 随机数 VULNERABILITY | 用于种子化匿名仿真图像；不是密钥/认证随机数，安全上下文误报 |
| 3 个 HTTP VULNERABILITY | 2 个服务在真实分析源码中已有生产 TLS / 显式开发回环边界，1 个文档工具固定回环；上下文误报 |
| 1 个外部脚本完整性 VULNERABILITY | 本地 HTML 报告缺 SRI；低优先级文档资源加固 |
| 8 个非 CSS BUG | 5 个上下文误报、2 个外部 runner 输入的有意表达式、1 个文档选择器缺少可访问名称 |
| 4 个 CSS 代表（原始总数 1645） | 内嵌 SVG 的 Microsoft YaHei 显式字体列表缺通用字体；低优先级文档显示回退问题 |

为防止并行任务改动源码造成错误行号/结论，本附录从 `http://127.0.0.1:19000/api/sources/raw` 只读取得本机平台保存的实际分析源，存为 [review-sources](review-sources)。[source manifest](review-source-manifest.json) 记录每份返回源及当前工作区哈希。该 API 统一 LF 并增加末尾空行，原始字节 SHA 不同本身不能证明漂移；另比较仅忽略 CRLF/LF 与末尾空行的文本。预览 HTML 有实质漂移，所以 CSS offsets 的核验使用平台保存版本。

两个 HTTP 业务服务的判断不使用旧快照：扫描保存的 serve_forever 前确有 TLS 包装和启动前监听策略。这个判断限于源码边界，未测试真实部署的证书、代理或运行环境。另一个随机数结论限于仿真图像用途，不构成全仓 RNG 安全证明。

1645 个 CSS 自动 BUG 都来自同一份压缩成少量长行的图纸预览 HTML 中多个内联字体声明，重复呈现放大了数量。外层 :root 已有 sans-serif，但内层显式 font-family 会覆盖继承列表；不能据此认定规则全误报。它们也不能合并算作 1645 个阅卷运行故障。应通过图纸/预览正式生成流程修订样式。

## python:S5332 · 7f939914-9bb6-4005-80a0-4c2efe12eedb

[ai-services/grading_agent/server.py:291](<D:/project/yuejuan/ai-services/grading_agent/server.py:291>)

平台：VULNERABILITY / MINOR；人工：`false_positive_context` / `none`。

实际被 Sonar 保存的源代码先调用 settings._validate_credentials_and_listener，再构造服务器；配置证书时用 TLS>=1.2 包装 socket 后才 serve_forever。被分析的 config.py 限定无证书只能显式开发环境、显式布尔 opt-in、字面 IPv4 回环地址，其余拒绝启动。HTTPServer 基类/serve_forever 名称不能证明生产明文暴露。

建议：保留生产 TLS 和开发回环例外边界；这条应记录上下文误报。TLS 部署证书和实际运行配置未做线上验证。

平台保存源 SHA-256：`234e569c160d44438f58cce4b63e5e19a24983e773d0fce47bf4f5e272f8448f`。读过的具体行/内联 offsets 见 [finding-review.json](finding-review.json)。

## typescript:S6544 · ebbf07bc-b59d-4c1d-9f6f-c323abc27810

[apps/web-admin/src/components/ResponsiveTable.tsx:93](<D:/project/yuejuan/apps/web-admin/src/components/ResponsiveTable.tsx:93>)

平台：BUG / MAJOR；人工：`false_positive_context` / `none`。

.some 回调只检测 column.sorter / filters / filterDropdown 属性是否存在，返回值按 JavaScript truthiness 判断；没有调用异步函数或等待 Promise。即使类型联合中含 Promise/函数，也只是能力存在标记，外层 Boolean 再得到布尔值。未发现被遗忘的异步执行。

建议：如需提高可读性，后续显式 Boolean 属性组合；无需按异步结果丢失修复。

平台保存源 SHA-256：`27117f003d2535f251179c1645acf33a704800736286fd3b7c7d2d9482b7b96a`。读过的具体行/内联 offsets 见 [finding-review.json](finding-review.json)。

## typescript:S7727 · b045cc65-0807-4e5a-9b7a-ba492c934ffa

[apps/web-admin/src/features/grading/workbench/reviewContext.ts:37](<D:/project/yuejuan/apps/web-admin/src/features/grading/workbench/reviewContext.ts:37>)

平台：BUG / MAJOR；人工：`false_positive_context` / `none`。

normalizeRubricPoint(value,index) 明确使用 .map 提供的第二参数 index 生成评分点 fallback id/说明，.map 的实参次序正好匹配；第三参数未使用。不是 parseInt 一类把索引错当参数的情形。

建议：保留索引语义，可在后续维护注释解释 fallback id 的来源；本次不改功能。

平台保存源 SHA-256：`18e85018bf9f8808f006f39f9e592522079320b75c855ff83beda4f5cccac25b`。读过的具体行/内联 offsets 见 [finding-review.json](finding-review.json)。

## css:S4649 · 0000be2f-c050-482e-9410-5ecdf93db1f9

[docs/architecture/previews/index.html:3](<D:/project/yuejuan/docs/architecture/previews/index.html:3>)

平台：BUG / MAJOR；人工：`low_document_font_fallback` / `low`。

被分析 HTML 内嵌 SVG foreignObject 的 style 显式设置 font-family:'Microsoft YaHei'，这一字体列表未含 generic fallback。外层 :root 的 sans-serif 不会自动拼接进子元素已显式覆盖的列表；缺少该字体环境仍有浏览器缺字/默认回退策略，但展示可控性较弱。代表告警有规则依据，不是 1645 个独立业务运行缺陷。

建议：后续在图纸/预览生成流程为内联字体补通用 sans-serif 后重新生成；按文档维护优先级处理。这里只人工抽查四条，其他 1641 条不自动记为人工已审读。

平台保存源 SHA-256：`f57dae6036fd7f15239a635e39bbf8aced11ed2dd76270133875de3587577eea`。读过的具体行/内联 offsets 见 [finding-review.json](finding-review.json)。

## css:S4649 · 00b40bda-21bd-4dda-a90d-0ef77b7c0ef2

[docs/architecture/previews/index.html:3](<D:/project/yuejuan/docs/architecture/previews/index.html:3>)

平台：BUG / MAJOR；人工：`low_document_font_fallback` / `low`。

被分析 HTML 内嵌 SVG foreignObject 的 style 显式设置 font-family:'Microsoft YaHei'，这一字体列表未含 generic fallback。外层 :root 的 sans-serif 不会自动拼接进子元素已显式覆盖的列表；缺少该字体环境仍有浏览器缺字/默认回退策略，但展示可控性较弱。代表告警有规则依据，不是 1645 个独立业务运行缺陷。

建议：后续在图纸/预览生成流程为内联字体补通用 sans-serif 后重新生成；按文档维护优先级处理。这里只人工抽查四条，其他 1641 条不自动记为人工已审读。

平台保存源 SHA-256：`f57dae6036fd7f15239a635e39bbf8aced11ed2dd76270133875de3587577eea`。读过的具体行/内联 offsets 见 [finding-review.json](finding-review.json)。

## css:S4649 · 7be57a1f-7c6f-4e3a-8946-e0f022b4cf97

[docs/architecture/previews/index.html:3](<D:/project/yuejuan/docs/architecture/previews/index.html:3>)

平台：BUG / MAJOR；人工：`low_document_font_fallback` / `low`。

被分析 HTML 内嵌 SVG foreignObject 的 style 显式设置 font-family:'Microsoft YaHei'，这一字体列表未含 generic fallback。外层 :root 的 sans-serif 不会自动拼接进子元素已显式覆盖的列表；缺少该字体环境仍有浏览器缺字/默认回退策略，但展示可控性较弱。代表告警有规则依据，不是 1645 个独立业务运行缺陷。

建议：后续在图纸/预览生成流程为内联字体补通用 sans-serif 后重新生成；按文档维护优先级处理。这里只人工抽查四条，其他 1641 条不自动记为人工已审读。

平台保存源 SHA-256：`f57dae6036fd7f15239a635e39bbf8aced11ed2dd76270133875de3587577eea`。读过的具体行/内联 offsets 见 [finding-review.json](finding-review.json)。

## Web:InputWithoutLabelCheck · c3342152-57b5-49bc-924e-b9c8c718f1d5

[docs/architecture/previews/index.html:3](<D:/project/yuejuan/docs/architecture/previews/index.html:3>)

平台：BUG / MAJOR；人工：`low_document_accessibility` / `low`。

架构图纸预览的 select#picker 没有关联 label、aria-label 或 aria-labelledby；标题 h1 不构成该控件的可访问名称。是独立本地文档工具的无障碍缺口，不是成绩写入或评分运行错误。

建议：在图纸预览正式生成源中增加选择控件名称/label，再按生成流程更新预览；不要只批改大段生成 SVG。

平台保存源 SHA-256：`f57dae6036fd7f15239a635e39bbf8aced11ed2dd76270133875de3587577eea`。读过的具体行/内联 offsets 见 [finding-review.json](finding-review.json)。

## css:S4649 · fff688a0-8c8d-4640-bad6-97f9a6464272

[docs/architecture/previews/index.html:3](<D:/project/yuejuan/docs/architecture/previews/index.html:3>)

平台：BUG / MAJOR；人工：`low_document_font_fallback` / `low`。

被分析 HTML 内嵌 SVG foreignObject 的 style 显式设置 font-family:'Microsoft YaHei'，这一字体列表未含 generic fallback。外层 :root 的 sans-serif 不会自动拼接进子元素已显式覆盖的列表；缺少该字体环境仍有浏览器缺字/默认回退策略，但展示可控性较弱。代表告警有规则依据，不是 1645 个独立业务运行缺陷。

建议：后续在图纸/预览生成流程为内联字体补通用 sans-serif 后重新生成；按文档维护优先级处理。这里只人工抽查四条，其他 1641 条不自动记为人工已审读。

平台保存源 SHA-256：`f57dae6036fd7f15239a635e39bbf8aced11ed2dd76270133875de3587577eea`。读过的具体行/内联 offsets 见 [finding-review.json](finding-review.json)。

## javascript:S905 · 75ac3d77-53f0-47a5-9335-dc49df53ed13

[docs/architecture/tools/capture_preview_qa.js:2](<D:/project/yuejuan/docs/architecture/tools/capture_preview_qa.js:2>)

平台：BUG / MAJOR；人工：`intentional_behavior` / `none`。

文档浏览器工具将 async(page)=>{...} 文件作为 Playwright run-code 输入，由 runner 注入 page 并调用；作为单独 Node 程序确实不执行函数体，但这不是记录的使用方式。

建议：精确标记 runner 输入文件用途或配置扫描范围；不要添加缺少 page 的自调用。

平台保存源 SHA-256：`f33744110499eea1ab04ef5dcecd9592e362fac471621bc17655edbdb63810e1`。读过的具体行/内联 offsets 见 [finding-review.json](finding-review.json)。

## python:S5332 · 60b4486b-ab39-42bd-914d-ebe6e345921f

[docs/architecture/tools/preview_server.py:36](<D:/project/yuejuan/docs/architecture/tools/preview_server.py:36>)

平台：VULNERABILITY / MINOR；人工：`false_positive_context` / `none`。

文档预览工具在被分析源码中固定监听 127.0.0.1:8766，提供本地架构图纸预览，不是业务服务/学生阅卷接口。规则的 HTTP 类告警存在，但没有对外监听或跨主机凭据传输证据。

建议：保持回环监听及文档工具范围；不可因为此告警推断业务 API 无 TLS。

平台保存源 SHA-256：`503afccb9034e237d23e1dcd260ab1c7bda3e72632b87440ccd5825a994b106d`。读过的具体行/内联 offsets 见 [finding-review.json](finding-review.json)。

## javascript:S905 · e50a781b-1426-49d6-aa49-aa91cee217ae

[docs/architecture/tools/render_native.js:2](<D:/project/yuejuan/docs/architecture/tools/render_native.js:2>)

平台：BUG / MAJOR；人工：`intentional_behavior` / `none`。

文档浏览器工具将 async(page)=>{...} 文件作为 Playwright run-code 输入，由 runner 注入 page 并调用；作为单独 Node 程序确实不执行函数体，但这不是记录的使用方式。

建议：精确标记 runner 输入文件用途或配置扫描范围；不要添加缺少 page 的自调用。

平台保存源 SHA-256：`becc6726c0993db632713b6e5f296b00b749490d5a623e495da963697f501ad9`。读过的具体行/内联 offsets 见 [finding-review.json](finding-review.json)。

## Web:S5725 · d705893a-0b2e-42ae-9c0b-f522537545ef

[frontend-review-report/前端代码审查报告.html:558](<D:/project/yuejuan/frontend-review-report/前端代码审查报告.html:558>)

平台：VULNERABILITY / MINOR；人工：`low_document_integrity` / `low`。

独立前端审查 HTML 报告引用固定版本 CDN ECharts 脚本，缺少 integrity/crossorigin，规则确有代码依据。该文件为本地文档，未发现应用路由加载它，未确认 CDN 被篡改或业务会话利用路径。风险是打开报告时执行的外部资源完整性。

建议：后续为报告构建使用本地固定资产，或根据实际字节计算 SRI 并配 crossorigin；不要捏造 integrity 哈希，也不要把此项报成生产业务已被入侵。

平台保存源 SHA-256：`13115be3087e34e51b5d8e9798c1236689d259529139aa53acd39e9fc23d8f3b`。读过的具体行/内联 offsets 见 [finding-review.json](finding-review.json)。

## javascript:S7727 · 04ab9958-fbf5-47ce-8d12-2eba020e31fb

[lab/scripts/govern-dataset.js:76](<D:/project/yuejuan/lab/scripts/govern-dataset.js:76>)

平台：BUG / MAJOR；人工：`false_positive_context` / `none`。

questionGroupKey 只声明/读取 sample；map 额外 index/array 被 JavaScript 忽略。该函数按题目与量规版本求隔离组，没有接收 radix 或 options 参数，未发现数据拆分语义错误。

建议：保留按题目及量规版本隔离；函数实际实现另读当前本地 governance.js 与固定 CodeQL 快照，Sonar 项目未保存此支持文件的 raw source，因此不宣称其为 Sonar 分析源码的逐字副本。

平台保存源 SHA-256：`bc790f6ef15e0fa7a8506956ae6b20f0d13b75b7f3693c77ae128a4c2d3f40f9`。读过的具体行/内联 offsets 见 [finding-review.json](finding-review.json)。

## javascript:S2871 · 505aaa43-82e1-4327-912b-62665baba8e1

[scripts/check-story052-production-deployment.mjs:70](<D:/project/yuejuan/scripts/check-story052-production-deployment.mjs:70>)

平台：BUG / CRITICAL；人工：`false_positive_context` / `none`。

输入在排序前被正则限制为六位 ASCII 十进制迁移号，或以同样六位号开头的文件名；固定位数字符串字典序与数字版本顺序一致。当前默认 .sort() 满足取最新迁移的语义，CRITICAL 自动标签不等于真实版本排序缺陷。

建议：保留六位编号约束；未来若接受不等长/非 ASCII 版本号，再改显式数字或版本比较并增加对应契约验证。

平台保存源 SHA-256：`f2bb3e19c221308399a30667e999d64ec3bccc769e7593365d1dfaa9aa7b1a12`。读过的具体行/内联 offsets 见 [finding-review.json](finding-review.json)。

## javascript:S2871 · e1279acc-b8c8-4f39-8969-0068ea39f933

[scripts/ci/schema-version.mjs:18](<D:/project/yuejuan/scripts/ci/schema-version.mjs:18>)

平台：BUG / CRITICAL；人工：`false_positive_context` / `none`。

输入在排序前被正则限制为六位 ASCII 十进制迁移号，或以同样六位号开头的文件名；固定位数字符串字典序与数字版本顺序一致。当前默认 .sort() 满足取最新迁移的语义，CRITICAL 自动标签不等于真实版本排序缺陷。

建议：保留六位编号约束；未来若接受不等长/非 ASCII 版本号，再改显式数字或版本比较并增加对应契约验证。

平台保存源 SHA-256：`cc80a3dab729c3c2084dd26d64a085c40bb3f53ffdadf88438743020277bc5e2`。读过的具体行/内联 offsets 见 [finding-review.json](finding-review.json)。

## python:S2245 · 47e8a60e-b39d-4658-8498-58cee45877da

[scripts/generate-fujian-2024-math-simulation.py:270](<D:/project/yuejuan/scripts/generate-fujian-2024-math-simulation.py:270>)

平台：VULNERABILITY / MAJOR；人工：`false_positive_context` / `none`。

种子化随机数用于匿名 synthetic 仿真答题卡的笔迹颜色、填涂偏移、图像噪声和像素坐标；不生成密钥、认证令牌、身份或安全挑战。可预测性用于复现模拟图像，在该用途不是安全缺陷。

建议：保留可复现种子；对这六处记录仿真用途，不应统一替换成密码学随机数破坏图像复现。

平台保存源 SHA-256：`c47468509bd3663b936c74ea994f871cb292df6344f95990e87262809212fb48`。读过的具体行/内联 offsets 见 [finding-review.json](finding-review.json)。

## python:S2245 · 714b91a4-a04d-48cf-b395-8ea5b285612e

[scripts/generate-fujian-2024-math-simulation.py:270](<D:/project/yuejuan/scripts/generate-fujian-2024-math-simulation.py:270>)

平台：VULNERABILITY / MAJOR；人工：`false_positive_context` / `none`。

种子化随机数用于匿名 synthetic 仿真答题卡的笔迹颜色、填涂偏移、图像噪声和像素坐标；不生成密钥、认证令牌、身份或安全挑战。可预测性用于复现模拟图像，在该用途不是安全缺陷。

建议：保留可复现种子；对这六处记录仿真用途，不应统一替换成密码学随机数破坏图像复现。

平台保存源 SHA-256：`c47468509bd3663b936c74ea994f871cb292df6344f95990e87262809212fb48`。读过的具体行/内联 offsets 见 [finding-review.json](finding-review.json)。

## python:S2245 · 93ebff75-53e8-459e-bc8b-e76b2a9dcda5

[scripts/generate-fujian-2024-math-simulation.py:270](<D:/project/yuejuan/scripts/generate-fujian-2024-math-simulation.py:270>)

平台：VULNERABILITY / MAJOR；人工：`false_positive_context` / `none`。

种子化随机数用于匿名 synthetic 仿真答题卡的笔迹颜色、填涂偏移、图像噪声和像素坐标；不生成密钥、认证令牌、身份或安全挑战。可预测性用于复现模拟图像，在该用途不是安全缺陷。

建议：保留可复现种子；对这六处记录仿真用途，不应统一替换成密码学随机数破坏图像复现。

平台保存源 SHA-256：`c47468509bd3663b936c74ea994f871cb292df6344f95990e87262809212fb48`。读过的具体行/内联 offsets 见 [finding-review.json](finding-review.json)。

## python:S2245 · da5b8565-5fa8-4f5c-9615-f19c729b4513

[scripts/generate-fujian-2024-math-simulation.py:328](<D:/project/yuejuan/scripts/generate-fujian-2024-math-simulation.py:328>)

平台：VULNERABILITY / MAJOR；人工：`false_positive_context` / `none`。

种子化随机数用于匿名 synthetic 仿真答题卡的笔迹颜色、填涂偏移、图像噪声和像素坐标；不生成密钥、认证令牌、身份或安全挑战。可预测性用于复现模拟图像，在该用途不是安全缺陷。

建议：保留可复现种子；对这六处记录仿真用途，不应统一替换成密码学随机数破坏图像复现。

平台保存源 SHA-256：`c47468509bd3663b936c74ea994f871cb292df6344f95990e87262809212fb48`。读过的具体行/内联 offsets 见 [finding-review.json](finding-review.json)。

## python:S2245 · 98f898f4-db4f-4c79-80d0-7879c792590d

[scripts/generate-fujian-2024-math-simulation.py:448](<D:/project/yuejuan/scripts/generate-fujian-2024-math-simulation.py:448>)

平台：VULNERABILITY / MAJOR；人工：`false_positive_context` / `none`。

种子化随机数用于匿名 synthetic 仿真答题卡的笔迹颜色、填涂偏移、图像噪声和像素坐标；不生成密钥、认证令牌、身份或安全挑战。可预测性用于复现模拟图像，在该用途不是安全缺陷。

建议：保留可复现种子；对这六处记录仿真用途，不应统一替换成密码学随机数破坏图像复现。

平台保存源 SHA-256：`c47468509bd3663b936c74ea994f871cb292df6344f95990e87262809212fb48`。读过的具体行/内联 offsets 见 [finding-review.json](finding-review.json)。

## python:S2245 · 14fc6509-b17c-4aca-9639-d962135439c3

[scripts/generate-fujian-2024-math-simulation.py:449](<D:/project/yuejuan/scripts/generate-fujian-2024-math-simulation.py:449>)

平台：VULNERABILITY / MAJOR；人工：`false_positive_context` / `none`。

种子化随机数用于匿名 synthetic 仿真答题卡的笔迹颜色、填涂偏移、图像噪声和像素坐标；不生成密钥、认证令牌、身份或安全挑战。可预测性用于复现模拟图像，在该用途不是安全缺陷。

建议：保留可复现种子；对这六处记录仿真用途，不应统一替换成密码学随机数破坏图像复现。

平台保存源 SHA-256：`c47468509bd3663b936c74ea994f871cb292df6344f95990e87262809212fb48`。读过的具体行/内联 offsets 见 [finding-review.json](finding-review.json)。

## python:S5332 · 0cb24e71-bce5-4ed4-b2c5-ca9aac275754

[services/math-verification-worker/math_verification_worker/server.py:186](<D:/project/yuejuan/services/math-verification-worker/math_verification_worker/server.py:186>)

平台：VULNERABILITY / MINOR；人工：`false_positive_context` / `none`。

实际分析源包含生产环境必须证书、开发 HTTP 必须明确 opt-in 和字面 IPv4 回环的监听策略；main 在绑定端口前调用策略，证书存在时 socket 先包 TLS>=1.2 再 serve_forever。自动规则未识别包装/拒绝启动边界，不能当作线上明文传输证据。

建议：保留在端口绑定前的校验及 TLS 包装；记录误报上下文。未验证真实部署环境或证书。

平台保存源 SHA-256：`53e3c2cfaf840dec25dea04807d7303b059041bc01a09a9e525439cbb2c978c1`。读过的具体行/内联 offsets 见 [finding-review.json](finding-review.json)。
