# 学校 AI 对话与兼容查询 API

## 统一 AI 对话

当前页面与考试内“问助手”入口使用 `POST /api/v1/school-assistant/conversations/stream`，要求有效登录、`school_admin` 和有效认证数据范围。用户直接提问，不选择预设任务，也不区分学校业务与一般问答。

```json
{
  "messages": [{ "role": "user", "content": "这场数学考试为什么还不能向学生发布？" }],
  "context": { "exam_id": "71bf7470-b371-4011-9c6a-5c190a18c2bd", "origin_module": "exam_workspace" }
}
```

上下文可省略；示例 ID 为合成数据。消息沿用 `ManagedChatMessage`，只接受 `user/assistant`，最多 50 条，并沿用既有长度、附件数量和正文限制。客户端不能提交系统指令、工具调用或工具结果。`context` 只接受考试 ID、来源模块和分区，考试名称由服务端获准读取。

模型可以直接回答、追问、选择工具，并在得到结果后继续选择其他工具。当前支持：

| 工具 | 权限 | 业务来源 |
| --- | --- | --- |
| `search_exams` | `exam:manage` | 当前 AccessScope 下考试列表；有限分页检索 |
| `get_exam_progress` | `dashboard:read` | 同一考试 workspace 服务 |
| `diagnose_publication` | `score:manage` | 完整门禁 Inspect 与学生端当前发布指针 |

工具名称对应领域能力，独立于菜单名称。工具列表按本次权限和依赖筛选；执行时再次检查身份、参数、资源边界及整场考试读取范围。不接受模型提供租户、操作者或授权信息。每次追问重新读取业务事实，历史回答不能代替本轮证据。

当前实现使用原生函数工具调用，适配已有 OpenAI-compatible 和 DashScope native 配置。原 JSON 能力检测不足以证明工具支持，因此先发送不含业务数据的工具探针；成功按模型配置短暂缓存 10 分钟，配置更新后重测。探针不执行任何业务工具；失败明确返回 `ai_chat_tools_unsupported`，没有固定任务或 JSON 计划的隐式降级。

每次请求最多 4 轮规划、6 次业务工具调用；最终回答另外流式生成。请求 context 限时 3 分钟，规划累计 token 达 60000 时停止，单个工具结果上限 64 KiB，超过时提示缩小范围。所有模型调用包含探针均独立记账，并关联同一对话请求；不将提问、回答或工具正文写入用量或审计。当前没有业务写入工具，也没有聊天幂等回执。

### 对话 SSE

每个 data JSON 带服务端生成的 `request_id` 和单调递增 `seq`。

| type | 字段 | 作用 |
| --- | --- | --- |
| `model` | `model` | 当前配置的模型信息 |
| `tool_started` | `call_id/tool_name/label` | 正在执行的工具 |
| `tool_finished` | 前述字段、`status/message` | 执行完成或失败；来源状态另行说明 |
| `sources` | `sources/actions`、可选 `scope` | 本轮累计真实来源和受控导航；数组即使为空也存在 |
| `delta` | `channel/delta` | 最终回答的 content 或 reasoning 增量 |
| `done` | `completion` | 完整回答与本次全部模型调用的累计用量 |
| `error` | `code/message` | 限流、超时、工具能力或模型错误 |

没有 `done` 的断流不算成功。停止、卸载和授权变化会取消请求并丢弃迟到事件；失败和中断的处理入口禁用。只有单一实际考试范围才可沿用到下一轮，多场结果不会默认选择最后一场。处理入口来自服务端结构化目标，仍经过现有导航权限和未保存修改拦截。

业务会话（含失败的工具尝试）与带考试上下文的会话仅保留在页面内存；业务正文、来源、范围及附件正文不写入浏览器历史。个人设备的一般问答历史按完整授权指纹隔离，只有完整回复后保存；公共电脑不保存。旧 v1 历史不自动迁入。

检索每次最多扫描 200 行、返回 10 个匹配候选，创建时间不是考试举行日期。`has_more/next_cursor` 表示仍需继续检索，不能把有限候选当成全校总数或唯一考试。成绩分析、成员、题库、申诉、审计等专用工具尚未接入；模型需说明能力范围，不能编造这些模块的实时数据。

## 兼容 M0 固定查询

以下两种模板保留给原调用方；当前统一聊天页面不使用这条固定查询链路。

学校业务助手在当前认证范围内查询真实业务数据。M0 只提供 `exam_progress`（考试进度）和 `publish_diagnosis`（学生端成绩发布诊断）两个只读模板。服务端以固定模板生成事实、来源及导航；无需配置模型即可完成这两种查询。

兼容查询结果仅保留在前端当前页面内存。该旧接口没有模型编排、会话历史、附件读取、实体搜索或业务写入能力；原通用问答接口也保留以兼容已有调用方。

## 接口与授权

| 方法 | 路径 | 作用 |
| --- | --- | --- |
| GET | `/api/v1/school-assistant/capabilities` | 返回两种模板的可用性、所需权限及禁用原因 |
| POST | `/api/v1/school-assistant/queries/stream` | 确认范围，读取业务事实，以 SSE 返回来源与结果 |

两个入口都要求有效登录及 `school_admin` 角色。考试进度另要求 `dashboard:read`，发布诊断另要求 `score:manage`。能力返回只供界面展示，每次查询仍重新检查权限。

租户、操作者和数据范围从认证上下文取得。`context_hint.exam_id` 是待校验的资源提示，不是授权范围；服务端显式检查考试的资源边界，并使用同一认证范围读取考试。M0 读取整场考试数据，无法安全满足班级子集查询时拒绝，不把整场结果冒充班级结果。

`request_id` 只用于关联本次响应。查询不经过通用业务幂等回执，不持久化问题或回答；重试是一次新的读取，不能保证数字与上一次相同。

## 请求

```json
{
  "request_id": "d4318b13-2c70-4ea4-8c17-4cac4e4b1956",
  "question": "这场考试为什么还不能发给学生？",
  "preset_intent": "publish_diagnosis",
  "context_hint": {
    "exam_id": "71bf7470-b371-4011-9c6a-5c190a18c2bd",
    "origin_module": "exams",
    "origin_section": "scores",
    "release_channel": "student_portal"
  }
}
```

示例考试和请求 ID 为合成数据。

- `request_id` 为 UUID；`question` 长度为 1–2000 个字符。
- `preset_intent` 必须指定 `exam_progress` 或 `publish_diagnosis`。M0 按明确选中的任务执行，不从自由文本匹配任务或猜测考试范围；缺失或未知任务返回 400。
- `context_hint` 可带考试 ID、来源模块/步骤、revision、发布渠道。revision 仅为上下文提示，响应以本次读取的 revision 为准。
- M0 仅支持 `student_portal` 发布诊断。`legacy_exam` 返回不支持渠道错误，不能用学生端门禁替代旧版考试发布。
- 非空 `class_ids` 返回不支持筛选错误。M0 不会静默丢掉筛选条件。
- `previous_scope_hint` 是保留字段；M0 出现该字段时返回 `unsupported_previous_scope`。用户需通过当前范围明确选择考试，不自动继承历史范围。
- 发布诊断缺少 `release_channel` 时返回 `needs_clarification`，要求确认学生端发布。未指定考试时也返回范围澄清，不自动选择列表中的第一场考试。

## SSE 事件

成功响应媒体类型为 `text/event-stream`。每个事件的 JSON 数据都有 `type`、`request_id`、递增 `seq`，SSE `event` 字段与 `type` 一致。

| type | 事件数据 | 用途 |
| --- | --- | --- |
| `scope_resolved` | `scope` | 显示本次实际查询范围 |
| `source_started` | `source_id`、`label` | 显示已开始读取的来源 |
| `source_finished` | `source`、`duration_ms` | 显示该来源可用、空或不可用状态 |
| `result` | `result` | 返回完整结构化结果 |
| `done` | `status`、`duration_ms` | 标记本次查询完成 |
| `error` | `code`、`message`、`retryable` | 已开始串流后的错误；message 不含内部错误或凭据 |

前端停止、切账号或开始新问题时取消 fetch，并丢弃不属于当前请求和账号的事件。服务端读取沿用请求 context，取消后不继续输出结果。该协议没有恢复游标；断流重试需要重新查询，不能拼接两次读取的事实。

`duration_ms` 为零时可省略；`error.retryable` 缺失时按 false 处理。`cancelled` 作为取消终态保留在结果状态契约中；客户端取消通常已经关闭连接，不能依赖服务器继续发送取消结果。

现有 SDK 生成器只生成普通请求方法，不解析 SSE。前端 SSE 传输需使用独立 fetch/parser；请求、结果与事件类型从正式生成的 SDK 引用。

## 事实、来源与导航

`result` 包含 `scope`、`facts`、`blocks`、`sources`、`actions` 和 `limitations`。状态区分 `completed`、`partial`、`needs_clarification`、`failed`、`cancelled`。

- 指标携带 `available`、`value`、单位、定义 ID 和来源 ID。来源缺失时数值为 null，不把初始化零值表示成真实统计。
- 来源状态为 `available`、`partial`、`empty`、`unavailable` 或 `forbidden`。`partial` 表示工作区存在来源警告，只能展示核实过的事实；`empty` 可表示尚无学生端生效发布，这与来源读取失败不同。
- 来源记录读取时间、可用的考试 revision 或发布版本。`consistency: source_local` 表示来源各自读取，不承诺多来源原子快照。
- 导航只返回结构化 `exam_section` 目标。前端使用现有工作区导航，不使用模型文本或来源字符串拼任意 URL。
- 阻塞类计数可能重叠，不相加为异常学生总数。来源有警告时，必须结合可用状态判断指标能否展示。

## 发布诊断的规则边界

诊断调用完整 `releasegate.Service.Inspect`，覆盖基础门禁、重评阻塞、当前有效策略以及警告确认/豁免。`Inspect` 不追加发布证据；正式发布仍调用 `RecheckForPublish` 重新求值并写入证据。

完整门禁读取失败时显示无法核实，不能从考试进度或基础 gate 推断“已通过”。当前学生端成绩版本通过 `CurrentPublished` 读取当前指针，不按最大版本号选择。

该诊断说明当前状态，不授权或执行发布、改分、豁免、仲裁、分配任务等动作。处理入口进入原业务页面；原页面继续按其权限与状态规则提交。

## 业务变化时的维护边界

业务模块仍是规则的唯一来源。助手通过 `WorkspaceReader`、`GateReader`、`CurrentReleaseReader` 等窄接口复用原服务；不调用原 HTTP handler，不复制门禁、计数、版本选择或授权规则。工作区与完整门禁规则更新时，原页面与助手使用同一服务实例读取新的事实。

| 变化 | 需要修改的位置 | 助手对话协议 |
| --- | --- | --- |
| 菜单标题、页面布局调整 | 原界面 | 不变 |
| 路由或工作区步骤变更 | 前端中央导航适配器及其测试 | 保持 `exam_section`，未知步骤隐藏入口 |
| 门禁、工作区计数规则变更 | 原领域服务与测试；必要时更新薄适配器对来源可用性的解释 | 通常不变 |
| 来源增加问题码 | 来源模块；已知码有专用入口时补中央动作映射 | `issue.code` 为开放字符串，未知问题仍展示说明 |
| 添加可选来源字段 | 权威 OpenAPI、正式生成 SDK、相关适配器 | 可保持现有调用 |
| 权限或授权范围变化 | 原授权服务、助手能力/资源检查及回归测试 | 重新检查实际权限，不能靠旧能力响应放行 |
| 新统计口径或新任务 | 新窄查询接口、任务编排、契约与验收 | 明确增量扩展；不通过提示词暗中改变旧任务含义 |

客户端从正式生成 SDK 引用 DTO，避免在界面和传输层复制字段定义。结果状态、块类型和来源类型采用严格枚举；业务问题码与业务步骤为开放字符串。开放字段只允许展示未知说明，不允许未知值绕过导航映射或授权。

本层的测试应检查调用同一业务来源、隔离权限、正确传递当前版本和安全处理未知问题码。业务阈值及计数公式由领域测试验证，避免在助手里保留另一套公式。

## 错误与失败恢复

开始 SSE 前的输入、身份、权限和资源问题使用标准 JSON 错误信封。400 表示输入或未支持的条件，401 表示未登录，403 表示权限/范围不足，404 表示考试不存在或不可见，503 表示必要资源查询不可用。

开始 SSE 后，单个业务来源失败可返回 `partial` 结果并标明失败来源。发布门禁失败必须保留“无法核实发布条件”的限制。需要重新读取时使用新请求 ID；本接口无持久化回执或旧结果重放。

权威字段定义位于 [OpenAPI](../../services/api-gateway/openapi/edugrade-api.openapi.json)，实现位于 [schoolassistant](../../services/api-gateway/internal/schoolassistant)。总体及后续范围见 [实施规格](../design-reviews/2026-10-02-school-admin-ai-assistant-implementation.md)。
