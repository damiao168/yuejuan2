# A01 模型调用与多图契约设计核查

日期：2026-10-19。本文是结合当前工作区代码的设计说明，未修改业务代码、提示词或生成文件，未调用真实评分模型。官方联网资料由主方案统一引用；下文标记“本地政策建议”的数值不代表供应商限制。
最终实施边界以 [详细主方案](../../architecture/a01-reference-materials-design-2026-10-19.md) 为准：发送许可跨 Agent 实例以共享 CAS 消费；已消费但缺可靠传输记录显示“可能已发起，待核实”；成功/错误信封均保留材料记录；本期不把预算配置字段等同金额预留执行链。本文保留推导和后续备选。

## 1. 首期结论

复用现有数学 v2 的冻结题目、数学证据、服务器量规结算和 A02 试改链路，增加“同题参考图清单”。首期在现有兼容 Chat Completions 的数学 v2 路径完成端到端闭环；只有实际部署通过多图能力验证并获图像出站授权时才启用。`dashscope_native` 当前生产评分路径明确不支持视觉消息，首期继续拒绝，不能把未接入生产的原生多模态夹具当作现成功能。

应同时完成图片解析、版本绑定、真实请求观察、迟到结果校验和教师查看记录。只在界面显示附件，或只在 Go 构造请求体，均不能验收为“模型已接收参考图”。

## 2. 已核实的代码事实

| 已审读文件 | 当前实现与 A01 影响 |
| --- | --- |
| `services/api-gateway/internal/subjective/grading_agent_v2_builder.go` | `grading-agent-v2` 只有单个 `media_evidence`；对象类型限定 `answer_segment_crop`、PNG，摘要排除 Base64 正文但绑定图像摘要、尺寸、bbox。请求体上限 8 MiB。`reference_context` 只传标准答案、解题文字等，不能装图片。 |
| `services/api-gateway/internal/subjective/active_crop_resolver.go` | 按租户、当前分段、本题核验裁剪归属；读取对象前后重查元数据、比大小及 SHA-256。PNG 验证主要是签名和 IHDR 头，不能称为“完整解码验证”。学生裁剪限制 5 MiB、1200 万像素、bbox 面积小于 0.9。 |
| `services/api-gateway/internal/subjective/math_grading.go` | `buildAdapterInput` 和 `buildPanelMathInput` 解析学生图，绑定数学产物输入摘要；`settleMathOutput` 服务器重算采分点并复核数学产物版本。含图/图形不确定等仍强制人工复核；参考图接入不能顺带解除该规则。 |
| `services/api-gateway/internal/subjective/types.go` | `ActiveCrop` 为 `json:"-"` 内存数据；`GradingReferenceContext` 为冻结文字事实；`AdapterTelemetry` 有 token 与执行诊断，没有参考图发送回执。 |
| `services/api-gateway/internal/subjective/adapter_http_v2.go` | 向 `/grading/grade-v2` 发请求；返回用 `DisallowUnknownFields` 解码，并核对 schema、请求、模型、提示词、量规和能力配置。增加响应字段必须同步更新 Go 契约，不能只加 Python 字段。 |
| `contracts/grading-agent/v2/request.schema.json`、`response.schema.json`、`contract.json` | schema 使用 `additionalProperties:false`；单图，`reference_context` 已是可选项。`contract.json` 尚有 `unreachable_fixture_only` 等历史描述，而代码已经存在生产 v2 route；发布前需按权威流程消除此描述差异，不能据其宣称生产已启用原生多图。 |
| `services/api-gateway/internal/subjective/grading_agent_v2_contract_test.go` | 请求/响应执行精确字段校验，跨语言夹具验证单图绑定。旧夹具应保留，新协议另增多图正反例。 |
| `ai-services/grading_agent/contract_v2.py` | `_exact_fields` 严格校验；一个 `media_evidence`，校验 Base64、长度、头部尺寸、摘要和跨语言 binding；不会自动接受新增数组。 |
| `ai-services/grading_agent/app_v2.py` | `ProductionMathV2Application` 接生产 v2；`math_candidate_messages` 组一个学生 `image_url`。幂等缓存/inflight 在单进程内存中；不能当作跨实例或重启后的外部调用一次保证。当前清理只清一个媒体字段。 |
| `ai-services/grading_agent/model.py` | `LocalLlamaCppAdapter.request_structured` 把 messages 传到兼容 `/chat/completions`；实际视觉能力依赖部署，类名不能证明有视觉。`DashScopeNativeAdapter.request_structured` 对列表 content 主动返回 `capability_not_supported`，避免把图传 text-generation。 |
| `ai-services/grading_agent/dashscope_native_contract.py` | `ApprovedImageCrop`、`build_dashscope_multimodal_payload` 只允许一份服务器核验的答案裁剪；角色和范围均未支持参考图。 |
| `ai-services/grading_agent/dashscope_native_transport.py` | 是受 `FixtureDashScopeTransport` 类型限制的离线 seam；硬性一个内联 PNG，8 MiB 请求上限。它不是评分生产网络通道。 |
| `ai-services/grading_agent/managed_model.py` | 文件职责是认证后的试卷解析，不是评分策略。该通道多模态 48 MiB 不会自动扩大评分 v2 的 8 MiB；不能借其解析配置绕过评分准入。 |
| `ai-services/grading_agent/server.py` | `/grading/grade-v2` 已注册，HTTP 读取对 v2 使用专用请求上限；`/ready` 只报告运行可用，不能证明多图契约或模型能力。 |
| `services/api-gateway/internal/modelgovernance/governance.go` | `TenantPolicy.ImageExportEnabled`、deployment modalities、`CapabilityPolicy` 可承载图像出站及能力约束；只有字段存在不等于当前评分路径在发图前逐项执行。 |
| `services/api-gateway/internal/subjective/eligibility.go` | 现有 A14 评分资格依据快照、质量、评测和校准。它不能替代新的参考图授权、撤权及每次调用图像预算校验。 |
| `services/api-gateway/internal/subjective/panel_orchestrator.go` | `requireManagedApprovals` 用“无 OCR 文字且有图引用”判 `image`；A01 必须依据实际请求媒体清单判图像调用。非空 OCR 不能把带图调用当作 text 审批。 |
| `services/api-gateway/internal/subjective/worker_execution_service.go`、`worker_completion_atomic.go` | Prepare 校验答案/题目快照/量规和数学绑定；事务提交再次检查当前数据库事实、租约与终态。参考图版本/撤权需进入相同提交边界。 |
| `services/api-gateway/migrations/000197_subjective_run_snapshot_binding.sql` | 新运行已有 `exam_question_snapshot_id`；旧行保持 NULL。A01 在此基础上附加冻结图清单摘要，不能另造脱离题目快照的绑定。 |
| `services/api-gateway/internal/subjective/trial_rules.go`、`trial_postgres.go` | A02 试改 fingerprint 绑定题目/量规/参考快照与答案；必须加图片清单摘要、策略/实际提示版本，变更时旧扩批决定失效。 |
| `services/api-gateway/internal/subjective/store_postgres.go`、`services/api-gateway/internal/modelgovernance/usage_ledger.go` | 已有成绩事务内 token 账本、请求维度幂等和调用事实；参考图不会自动带来图片 token 明细。按每次外部 attempt 记录发送与计量事实，再汇总到既有逻辑请求，避免重试漏记或重复计算。 |
| `services/api-gateway/internal/modelgovernance/runtime_prompt.go`、`ai-services/grading_agent/model.py` | 普通 PromptRegistry 有文件 manifest 和 bundle hash；数学 `math_candidate_messages` 在 `app_v2.py` 直接构造系统指令，当前 registry 的哈希不覆盖它。A01 必须补真正数学模板及 wrapper/schema 版本。 |

以上为定向审读；未声称完成相关目录的全文件审查。

## 3. 内部契约与混合版本部署

### 推荐：保留 v2 路径，显式协商 material-input-v1

保留 `/grading/grade-v2` 与 `schema_version: grading-agent-v2`，旧请求形状不变。新增 `required_features:["material-input-v1"]` 作为材料图协议开关，和 `reference_manifest`、参考图数组一并出现；三者缺一不可。没有该 feature 的请求仅按旧协议校验，不得夹带参考图片字段。即使新请求数组为空，也必须明确清单为空并匹配冻结版本，不通过省略字段猜含义。

新增认证后的 `/grading/capabilities`（或扩展现有认证运行清单），返回支持的 schema、required features、有效图数/字节/像素限制、wrapper/prompt/schema 哈希。它证明应用和已验证部署能力，不把 `/ready: true` 等同多图支持。部署探测用合成图执行实际两图/多图传输与结构输出检查，保存探测版本及配置修订号。

发新请求前核对能力；接收端拒绝未知 `schema_version`、未知 required feature 和不满足的限制。新响应必须回显 `schema_version`、`accepted_features:["material-input-v1"]`、`reference_manifest_sha256`，并携带发送事实。Go 必须逐项校验。某个旧实例意外接到新请求时会因当前严格字段检查而拒绝，应向网关映射为明确的不兼容错误；不得移除图片后重试旧协议，不得用无图成功响应作为成功结果。负载均衡期间请求仍必须自描述并由每个实例执行强校验，启动能力探测不能消除请求级校验。

### 为什么不只加 v2 可选字段

给旧 v2 schema 仅加可选 `reference_media`，第三方宽松实现可能忽略，且单凭“字段可选”无法解释必需图缺失。“可选”只能表示无参考图的旧请求可以沿用原形状；一旦冻结清单非空，`required_features` 与响应回显必须强制，校验失败即阻断。两端未完成 feature negotiation 前，网关不发送该扩展。schema authority 用 `oneOf` 或等价条件明确区分严格旧请求与严格材料图请求，Python/Go 按同一规则执行。若后续出现更大语义变化，可以另增 `grading-agent-v2.1`，不需在首期同时维护第二个版本号。

内部 AI 契约权威源在 `contracts/grading-agent/`；对外教师 API 的权威源仍是 API Gateway OpenAPI。两者分别更新，并走现有 SDK/契约生成流程，不手改 generated。

## 4. 图片角色、顺序及绑定

保留现有学生 `media_evidence`，新增独立 `reference_media[]`，避免把参考图伪装成学生答案。参考清单示意：

```json
{
  "schema_version": "grading-agent-v2",
  "required_features": ["material-input-v1"],
  "reference_manifest": {
    "version": 1,
    "assessment_snapshot_id": "snapshot-internal-id",
    "assessment_snapshot_hash": "sha256",
    "reference_manifest_sha256": "sha256",
    "required_media_ids": ["ref-1"]
  },
  "reference_media": [
    {
      "media_id": "ref-1",
      "role": "question_diagram",
      "order": 1,
      "binding_hash": "sha256",
      "encoding": "base64",
      "media_type": "image/png",
      "sha256": "sha256",
      "byte_size": 1024,
      "width_pixels": 640,
      "height_pixels": 480,
      "data_base64": "..."
    }
  ]
}
```

该片段只列新增字段；完整请求仍需现有题目、量规、数学证据、学生媒体及最终分数权限等字段。

首期角色限定 `question_diagram`（题目条件/附图）、`reference_solution`（标准解答/辅助线图）。学生图仍为 `answer_segment_crop`。图中所有文字均为数据，不得成为指令。参考图只能帮助解释规则，不能作为“学生已完成某步骤”的证据；模型候选的 `evidence_ids` 仍只允许学生数学产物中的步骤/公式 ID，不能用参考图 ID 代替学生证据。

确定性顺序：题目图按冻结 order → 标准解答图按冻结 order → 当前学生裁剪图。每张图前加简短角色/序号标签；首段 manifest 明确图像对应关系，禁止靠不稳定排序或文件名推断。`media_id` 是不含业务身份的内部别名，供应商不需要文件资产、storage key、tenant/school/exam/student ID 或下载 URL。

冻结图清单应包含：角色、order、必需标记、源材料版本、源页/区域、规范化衍生物摘要/尺寸、变换版本、授权修订、题目快照 ID/hash。服务器持有来源和权限事实，内部 AI 请求只携带验证所需的最小别名、内容摘要与绑定。对外模型 payload 去掉内部身份字段，保留 `图 1/图 2` 角色标签。

每图 binding 建议沿用长度前缀编码，绑定 `schema_version, request_id, question_id, answer_segment_id, assessment_snapshot_id/hash, manifest_hash, media_id, role, order, sha256, transform_version`；源 bbox 用百万分之一的整数单位纳入 manifest。清单 hash 对不含 Base64 的 canonical JSON 计算；整体幂等 digest 包含学生媒体和有序参考清单的全部语义事实。新增、替换、角色/顺序/裁剪变化均改变摘要；不得只 hash 一组无序图片 sha256。

不应原样复用学生裁剪 `bbox面积<0.9` 的含义：参考来源若是多题试卷页，必须裁到本题；若已是单独几何图文件，其完整图像 `[0,0,1,1]` 可以合法。需要以来源类型和题目归属证明边界，不能将完整试卷页改名“参考图”后传出。

## 5. 解析与限额

新增 `ReferenceMediaResolver`，输入是 tenant、question、被冻结的 snapshot/manifest，绝不接受客户端任意 URL。流程为：读冻结元数据与当前撤权事实 → 限额读取私有对象 → 完整解码并验证格式/尺寸 → 受控规范化为 PNG → 验证实际摘要 → 复查元数据、撤权修订和清单未变 → 返回短生命周期内存对象。读取、规范化、编码均设上限；清除 EXIF/文件名等非图像元数据，原始资产不必因此修改。

新 resolver 要使用完整 PNG 解码或生成时的完整解码证明，限制解压像素、内存和时间；仅检查 IHDR 不能发现截断/畸形图片。规范化服务应先检查 dimensions 再解码，禁动画、多帧和任意格式自动识别。图片旋转/缩放必须产生新衍生物并记录变换版本；不可在每次模型请求里偷偷重压缩导致教师预览与发送内容不同。

| 项目 | 首期建议 | 依据 |
| --- | --- | --- |
| 学生图数量 | 1 | 当前实现，首期保持 |
| 参考图数量 | 0–4，每种参考角色最多 2 张，总图数最多 5 | 本地政策建议；满足多数一道题的题目附图和标准解答图，需用样本评估再调整 |
| 每张参考图原始 PNG 字节 | ≤1 MiB | 本地政策建议；超过时需生成可读衍生图或人工重裁，不能删除后悄悄调用 |
| 学生图单图字节 | 保留 ≤5 MiB | 现有 active_crop 限制 |
| 本次全部解码后 PNG 字节之和 | ≤5 MiB | 本地政策建议，为 Base64 的约 4/3 膨胀和文字/schema 留余量；并非保证任意满额组合都一定通过 |
| 单张参考图像素 | ≤400 万 | 本地政策建议；小字辨识质量要用样本验证，不一律按供应商最大尺寸上传 |
| 全部图像总像素 | ≤1200 万 | 本地政策建议，复用现有单图上限作为总资源预算，避免多图成倍放大 |
| 网关内部 JSON 实际字节 | ≤8 MiB | 当前 Go builder、Python v2 限制 |
| 供应商 JSON 实际字节 | ≤8 MiB，且不超过已核验的供应商/部署限制 | 本地政策建议；在真实 wire payload 编码后再检查，不能拿 managed paper 的 48 MiB 当评分预算 |
| MIME | PNG | 保持现有评分批准格式 |
| URL/原文件名/存储地址 | 不出站 | 延续现有 v2 与 native export 白名单 |

有效限额取“冻结策略、当前租户更严格策略、部署能力、内外层服务限制”的最小值；不把缩放后的视觉 token 估计当供应商实际计量。任何必需图缺失/超限/损坏时整次图像调用阻断并回人工；非必需图若允许省略，必须在试改前明确人工确认省略原因、形成新清单版本，不能运行中自行省略。

## 6. 首期生产通道与原生通道的边界

首期沿现有 `ProductionMathV2Application → request_structured → /chat/completions`，按新媒体清单组装多图消息。需要补请求总大小、实际图数/顺序摘要和部署视觉能力验证；`local_llama_cpp` 名称本身不构成能力证明。不新引入供应商、不复用文档解析的 managed credential，不改现有模型选择目标。

DashScope native 维持明确 `capability_not_supported`。界面/能力清单如实显示本评分部署尚不能用参考图；有必需图的考试不能退到纯文本评分。这样首期能完整交付现有支持通道，原生改造可作为独立后续任务。

若后续要开放 native，必须一起改 `ApprovedImageCrop` 的角色类型/多图容器、原生 payload builder、生产 `DashScopeNativeAdapter.request_structured`、路径选择与 provider parser、wire 限制和回执。使用 multimodal-generation 路径、`input.messages[].content[].image` 的供应商格式；严禁仅给 text-generation 标上“supports_native_visual_wrapper”。必须以真实生产 transport 的合成图测试验收，不能只通过 fixture transport 测试。

## 7. 治理、版本与迟到结果

新的单次 `ImageEgressDecision`（设计名称）在实际调用前执行，并附审计事实：租户政策版本、图像出站许可、数据区域、目标部署、批准的能力探测版本、媒体角色/数量/总字节/像素、必需图齐备、预算预留和撤权修订。现有 A14 保持评分资格职责；`sandbox_admission.go` 的 shadow/native 规则不能替代真实评分路径的这一步。

同一道题可以有 OCR 文字和图片，modality 必须按实际媒体判断为 `image`；panel 三个角色每次使用同一冻结清单，但仍独立调用、计量和校验。若首期不覆盖 panel 的多图路径，带参考图时该入口也必须明确阻断，不能走旧 path 丢图。

新 run 保存 `reference_manifest_hash`、契约/feature 版本、实际数学提示/wrapper/schema hash 和出站政策修订，关联既有 `exam_question_snapshot_id`。旧 run 不回填猜测，不让现有 request ID 在换图后继续复用。A02 fingerprint 纳入清单/提示/能力策略摘要；新清单使试改与扩批许可失效，重新试改仍遵守已接触 AI 的样本独立性规则，不允许把同版已见 AI 答案重新宣称独立人工基准。

至少在三处校验同一绑定：入队创建 run；worker Prepare/模型调用前；成绩提交事务内。提交时重新查询当前允许使用的 snapshot/manifest、撤权状态及运行/试改终态，与冻结事实比较；不能只相信早先构造的 AdapterInput。沿用现有 run → runtime 的锁顺序，不能在持有数据库事务锁时等待对象存储或模型网络。

撤权/试改停止不保证已经离开服务器的图像被供应商删除；它阻止后续发送、使未提交结果不可接受，并记录在途事实。若模型已返回而素材刚被撤权，保留调用/用量事实但丢弃评分结果。已完成历史结果保留其历史清单证明；撤权影响新的推理/回放权限，不伪造“从未发送”。

## 8. 回执：可证明什么，不能证明什么

应把“媒体清单核验”和“供应商请求尝试”拆开记录，不用单一 `images_sent:true`。建议持久化每个 attempt 的状态与时间点：

| 状态 | 允许展示的事实 | 不可推导的事实 |
| --- | --- | --- |
| `prepared` | 本地已校验冻结图清单，构造候选 payload | 已发网络、供应商已收到、产生费用 |
| `sent` | 真实生产 transport 已进入发送路径，保存 wire payload 摘要和本 attempt 身份 | HTTP 返回一定收到、供应商完整读取了每张图 |
| `accepted` | 收到可关联的供应商成功响应；记录其 request ID（存在时）、完成诊断和 usage 状态 | 模型逐像素消费/理解了每张图、使用了图中某条条件 |
| `unknown` | 发送可能发生后超时、断流或进程崩溃；不能判定供应商受理和费用 | 未调用、零费用、可以无代价自动重发 |
| `rejected` | 有明确未受理/拒绝依据，如本地校验失败或已解析的供应商拒绝响应 | 所有错误都无费用；错误码应保留具体证据 |

`sent` 是本端发送观测，若传输库不能证明完整写出，应同时记录 `write_started/write_completed` 证据等级，界面用“已发起发送”。没有可关联请求号的成功响应可记录 `accepted` 但 `provider_request_id` 为空，不能编造。若出现回执与结果写入之间崩溃，重启应恢复为 `unknown`，不是直接标 `accepted`。

每条记录至少包含逻辑 request/run ID、attempt ID/no、lease generation、冻结清单摘要、实际 wire 图像数/有序内容 sha256、实际 request body sha256/bytes、目标部署和传输实现版本、时间、HTTP 状态、供应商 request ID、usage 状态及值、错误分类。内部受限记录可以含业务关联，出站 payload 和常规日志不能带这些身份。只存摘要不保存 Base64 或完整 payload；教师看见其有权限的缩略图、角色、清单版本和发送状态。

内部响应应回显“已验证/已构造的 manifest”和真实 attempt 回执。它能证明 Python 未把参考图静默删掉并且 transport 观察到相应内容，不等价于供应商逐图确认。验收文字采用“供应商成功返回；本次请求携带 3 张图”，避免“AI 已阅读全部图”。

## 9. 幂等、重试、计量和清理

现有 Python 幂等是内存缓存。首期必须明确其边界，并在 Gateway/worker 持久化请求/attempt 占用事实：同一逻辑 run、冻结清单和幂等 ID 只能有一个活动 attempt。多实例不能仅靠进程字典避免重复模型调用；同 ID 不同清单返回冲突。

重试沿用现有受控策略，但补充不确定发送规则：`prepared` 后、尚未进入 transport 的失败可以原清单恢复；供应商明确可重试错误按政策新建 attempt；`unknown` 不自动当成未发送继续发下一次，先恢复已有回执/缓存或由负责人明确重试，仍计入预算风险。每次重试都绑定原清单，重新检查撤权、试改状态、租约和预算，不在重试中换图。模型结构修复请求也是新的外部 attempt，图和提示修复版本均有记录。

现有 `attempts/provider_request_count`、`usage_status` 必须继续准确。所有实际发起的 attempt 都记录，包括最终结果未通过 schema、迟到/撤权丢弃、超时和任务取消；token 不可得时记 unknown/unavailable，不记 0。逻辑请求汇总保留完整/部分/未知计量状态。沿现有 `model_usage_event` 的幂等键聚合时，不能先写一次缺失值后依赖 `ON CONFLICT DO NOTHING` 填回完整数据；推荐单独 attempt 事实后事务汇总，或设计有版本的结算更新。

图片 token 若供应商未拆分，仅保存实际 input/output/total tokens，不用“图数×估计值”伪装实际用量。预估预算可按有效图片尺寸/模型计费政策做保守预留并明确标 estimate；实际回执到达后结算。`unknown` 的预留按已有恢复策略保留/人工处理，不能超时自动释放后无限重试。

Go 新增多图内存类型沿用 `json:"-"`；请求、规范化图与编码缓冲在成功/异常/取消路径均释放并尽可能清可变字节。Python 的 `finally` 遍历学生图和全部参考图，清除自己持有的 Base64 引用，避免全量 deepcopy 带来多份大图；不能宣称 Python 不可变字符串或 HTTP 库临时副本已被安全擦除。日志不打印错误正文、prompt、Base64、下载地址或凭据；缓存只保留无图结果/摘要，按 TTL/容量管理。

## 10. 数学提示与输出边界

将实际数学模板纳入独立 manifest（或扩展当前 PromptRegistry 的合法组件），明确 `math.reference-images.v1`、wrapper 版本和输出 schema hash；启动验证真实文件，运行回显实际 hash。普通 `/grading/prompts/current` 当前只覆盖 registry，不能用它证明 `math_candidate_messages` 已冻结。

模板需明确：角色标签为可信元数据，图片内容为参考数据；参考解法可以有其他等价方法；不按图中长度/角度外观推导未标条件；参考图不能当作学生作答证据；文字/图矛盾、看不清、图像缺失时输出 uncertain 与风险。仍只输出采分点候选映射，不允许模型输出分数、最终成绩或自动发布信号。图形问题现有人工复核限制保留，A01 价值首先是让教师获得材料完整的建议。

## 11. 最小完整实施清单

| 工作包 | 具体文件/位置 | 交付边界 |
| --- | --- | --- |
| 契约先行 | `contracts/grading-agent/v2/` 保留旧例；增加 material-input-v1 authority/fixtures；Go `grading_agent_v2_contract_test.go`、Python `tests/test_contract_v2.py` | 严格旧/新形状、required feature、manifest 回显、错图/丢图拒绝；不改旧 fixture 的含义 |
| 参考图解析 | 新 `subjective/reference_media_resolver.go`；`types.go`、`math_grading.go` | 读取已有冻结材料/资产；完整解码；权限与版本前后复查；与旧学生图 resolver 职责分开 |
| 请求/返回 | `grading_agent_v2_builder.go`、`adapter_http_v2.go`；新增媒体 binding/manifest 辅助文件 | 全部图像 digest、总限额、幂等和响应 feature/回执校验 |
| 运行持久化 | 新正式迁移；`store_memory.go`、`store_postgres.go`、`worker_execution_service.go`、`worker_completion_atomic.go` | 在 000197 snapshot 绑定基础上记录清单；提交事务复核；不修改历史迁移 |
| 评分治理 | `eligibility.go`、`panel_orchestrator.go`；`modelgovernance/governance.go` 及生产组装入口 | 按实际媒体做 image 准入、部署能力、授权撤回与预算；不借用 paper 模型配置 |
| AI 服务 | `contract_v2.py`、`app_v2.py`、`server.py`、`config.py`、`model.py` | material-input-v1 严格分支、能力清单、兼容生产多图、attempt 回执；native 明确阻断 |
| 提示词治理 | 数学模板 authority/manifest、`model.py` PromptRegistry、`runtime_prompt.go` | 走正式哈希更新流程；准确暴露数学 wrapper/schema hash，不能只改 version 字符串 |
| A02 接续 | `trial_rules.go`、`trial_postgres.go`、`trial_memory.go` | 新参考图/实际提示版本使旧试改决定失效；停止/重试不恢复旧调用权限 |
| 计量审计 | attempt 事实新表/存储；`types.go` telemetry、`store_postgres.go`、既有 usage ledger | 逻辑请求和外部 attempt 分开；unknown 不记零；最终失败也保留调用事实 |
| 教师可核对性 | 对外 OpenAPI authority → SDK；既有冻结参考/试改 UI | 展示真正冻结/发送内容、角色、顺序和状态；详情必须受现有租户/学校/考试权限保护 |

不得把“只完成前端选图”或“只完成契约”作为整项完成。以上工作包应先确定字段/状态及迁移，再按明确文件所有权并行开发。

## 12. 有效验收情景

1. 兼容生产 transport 的两张参考图＋一张学生图请求：真实 wire 内容数、顺序、hash 与 frozen manifest 一致；不含身份、存储地址或原文件名。
2. 旧实例/旧 schema、缺 required feature、漏一张图、错清单 hash、替换角色/顺序：明确拒绝；不自动退回纯文本。
3. 同租户错题、跨租户资产、非当前 snapshot、撤权、缺图、损坏 PNG、伪造 IHDR、像素/体积/图数超限：发供应商请求计数为 0。
4. 读对象期间换图；prepare 后撤权；模型返回与提交之间换快照；停止试改后迟到结果：旧结果不可落为新建议，用量事实仍保留。
5. 网关重试、AI 服务重启、多实例同时接同 ID、响应丢失、进程在 dispatch 后退出：不将 unknown 伪装未发送；不产生静默换图或无限重试。
6. 现有纯文字/单图 v2 考试回归；数学候选仍只能引用学生证据 ID，图形不确定仍需人工，服务器拥有最终量规结算与成绩确认权。
7. token reported/partial/unavailable、结构修复两次调用、最终失败/取消后的用量：请求数和费用状态准确，逻辑聚合幂等。
8. synthetic 模型/transport 测试只证明组装与控制流程；上线前另行用已授权的合成图对目标部署验证真实多图能力。准确率、题型覆盖和费用收益必须有独立评测数据，不能从请求成功推断。

本文未执行上述测试；这些是后续开发验收清单。
