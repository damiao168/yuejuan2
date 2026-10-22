# Paper And Question Config API

当前文档属于 `STORY-009 试卷与题目配置模块`。

## 边界

本 Story 只登记试卷文件元数据、题目、标准答案和 Rubric 配置。真实文件二进制上传、MinIO/S3 写入和下载授权由 `docs/api/files.md` 中的文件 API 提供；当前试卷配置接口仍不直接接收 multipart 文件内容。

## 权限

所有接口都需要有效的登录会话或 Bearer token，并要求 `exam:manage` 权限。服务端按当前登录用户的 `tenant_id` 隔离数据。

## POST /api/v1/exams/{examId}/papers

登记试卷文件。推荐链路是先用文件 API 上传真实二进制，再把返回的 `file.id` 作为 `file_asset_id` 传给本接口；后端会从真实 `file_asset` 记录读取文件元数据和对象存储位置，并关联为 `exam_paper`。

推荐请求：

```json
{
  "file_asset_id": "file_001"
}
```

兼容请求：仍支持直接登记文件元数据。此兼容模式不接收 multipart 文件内容，只保存文件名、类型、大小、哈希和对象存储 key。

请求：

```json
{
  "file": {
    "original_name": "physics-final.pdf",
    "content_type": "application/pdf",
    "size_bytes": 2481024,
    "hash_sha256": "7b1f...",
    "storage_bucket": "papers",
    "storage_key": "tenant/demo/exams/exam_001/papers/physics-final.pdf"
  }
}
```

响应：`201 Created`

```json
{
  "paper": {
    "id": "paper_001",
    "exam_id": "exam_001",
    "version_no": 1,
    "status": "uploaded"
  },
  "note": "file metadata registered; binary upload is handled by file upload story"
}
```

## GET /api/v1/exams/{examId}/papers

列出考试下的试卷文件版本。

## POST /api/v1/exams/{examId}/questions

创建题目配置。

受控题型：

```text
single_choice
multiple_choice
true_false
fill_blank
numeric
formula
short_answer
calculation
essay
discussion
coding
```

请求：

```json
{
  "exam_paper_id": "paper_001",
  "question_no": "Q1",
  "question_type": "short_answer",
  "score": 10,
  "stem": "Explain Newton's second law.",
  "knowledge_points": ["force", "motion"],
  "answer_area": {"page": 1, "x": 120, "y": 340, "w": 680, "h": 180},
  "sort_order": 1,
  "answer_key": {
    "standard_answer": "F=ma",
    "equivalent_answers": ["force equals mass times acceleration"],
    "tolerance": {}
  }
}
```

如果传入 `exam_paper_id`，服务端会校验该试卷属于同一租户和同一考试。

## GET /api/v1/exams/{examId}/questions

列出考试题目配置。

## POST /api/v1/exams/{examId}/questions/{questionId}/rubric-draft

为正式数学解答题生成建议分值划分。请求体为 `{"expected_question": <刚读取的完整题目对象>}`；该快照只用于检查界面是否过期，模型输入从当前租户、考试范围内的已保存题目读取。需要 `exam:manage`，考试须处于 `draft` 或 `configured`，且该题评分细则尚未锁定。

参考解答优先使用非空 `solution.steps`，步骤证据 ID 按原数组位置为 `step-1`、`step-2`；跳过空内容时不重排 ID。没有步骤时，将 `solution.raw_text` 按非空逻辑行拆分，`$$…$$` 或 `\[…\]` 的多行公式保持为一段；只有字符串标准答案时按相同规则回退。没有上传来源的手工题或题库题保留空 `source_refs`，不会伪造文件来源。

教师调整了自动细分边界时，可额外提交 `solution_segments`。每段只包含原文位置，不接收改写文本。新增字段示例如下，`expected_question` 仍按上面的契约提交完整题目对象：

```json
{
  "solution_segments": [
    {"spans": [{"source_id": "step-1", "start": 0, "end": 12}]},
    {"spans": [{"source_id": "step-1", "start": 12, "end": 24}, {"source_id": "step-2", "start": 0, "end": 8}]}
  ]
}
```

`source_id` 指上面的粗来源步骤；`start` 包含、`end` 不包含，偏移单位是该来源 `content` 首尾去空白后的 UTF-16 单元，与浏览器 `String.slice` 一致。段数最多 500，全部跨度合计最多 1000。服务端要求所有非空来源步骤按原顺序完整覆盖，拒绝遗漏、重叠、乱序、越界、空单元或切开 emoji 代理对，失败返回 400，且不调用模型。

服务端从当前仓储重建各段内容，建议中的证据 ID 改为细分后数组位置 `step-1`、`step-2`。教师合并相邻原步骤时，同段可以包含多个来源跨度。完整上下文也从当前来源步骤重建后提供给模型，供理解被切开的多行公式环境；模型仍只能引用细分步骤 ID。未传 `solution_segments` 时保持原步骤行为，不改写已保存的解析或评分细则。

响应沿用 `MathRubricDraftResponse.suggested_rubric_candidates`，`candidate_id` 对应正式题目 ID，`status` 为 `review_required`。建议关联真实的解答步骤；缺分值或分值合计不符时由教师调整。模型使用学校当前解析模型及结构化能力准入规则，调用前后核对题目、标准答案、解答和评分细则版本，期间发生变化返回 `409 question_rubric_draft_conflict`；考试配置已冻结或评分细则已锁定也返回 `409`。

本接口不创建评分细则，不自动锁定。教师可以先按步骤人工配分，或核对 AI 建议后应用为草稿；最终仍通过下面的评分细则保存接口校验满分和各采分点总分。

新页面使用下述后台任务接口。同步入口保留用于兼容，装配任务服务后也经过同一持久化队列并等待结果，不能绕过并发限制；HTTP 断开只结束等待，不取消已提交任务。队列已满时同样返回 `429 rubric_draft_queue_full`。

## 数学分值建议后台任务

任务需要 `exam:manage` 和考试资源授权，只能查看、取消当前登录用户在当前租户和考试中创建的任务。后台任务与浏览器生命周期分开，换题、刷新或关闭页面不会取消已提交任务。结果保存在任务中，不自动写入、保存或锁定正式评分细则。

多个 API 副本共同限制最多 **5 个未结束的模型调用**；本地模型在此上限内另外限制为 1 个。超出并发时排队，每位用户跨考试最多保留 20 个 `queued` 任务。运行中取消的调用仍占用名额，直到响应或超时；上游可能已经计费。

### POST /api/v1/exams/{examId}/questions/{questionId}/rubric-draft-jobs

提交后立即返回 `202 Accepted`，正文为 `{"job": <QuestionRubricDraftJob>}`。创建、复用已有任务都返回 202；创建成功不代表模型生成成功。

请求在同步接口的完整题目快照与 `solution_segments` 规则上，增加必填 `request_id` 和可选 `regenerate`：

```javascript
{
  "request_id": "d4cf728c-368f-4c8c-8ff6-38473f246149",
  "regenerate": false,
  "expected_question": currentQuestion
}
```

上例中 `currentQuestion` 是刚读取的完整题目对象，序列化后作为 JSON 正文提交。`solution_segments` 可省略或为 `null`，显式空数组无效。

考试须为 `draft/configured` 数学考试，题目须为有正分值和参考解答的主观题，且评分细则未锁定。相同用户、题目及输入的活动任务会复用；相同输入最近一轮成功结果也会复用。只有明确重新生成时传 `regenerate: true` 并使用新的 `request_id`。同题仍有活动任务时，相同输入继续复用，输入不同返回 409，避免两轮结果同时竞争。

`request_id` 用于重复提交复用原任务；网络响应丢失后用原 ID 和原输入重试。相同 ID 对应不同题目、考试或输入返回 409。模型身份和提示词修订也计入输入指纹；换模型后应使用新 ID。失败、取消或过期后手动重试也使用新 ID，避免重新取得原终态任务。

创建和取消任务均可选传 `Idempotency-Key`，键长 1–128 字符，满足 `^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`。提供时进入通用 HTTP 命令收据机制；创建时推荐与 `request_id` 一致，取消使用独立键。这不是后台任务输入指纹的替代品。HTTP 重放响应中的状态可能已经变化，应随后读取任务详情。无效键返回 400；同键不同请求或同命令正在处理中返回 409。

错误包括：400（非法请求、快照或原文跨度）、401/403（未登录或权限不足）、404（范围内资源不存在）、409（题目过期、同题不同输入任务活动中、考试冻结或细则锁定）、429（20 个等待名额已满）、503（任务或模型服务暂不可用）。

### GET /api/v1/exams/{examId}/rubric-draft-jobs

返回 `{"jobs": [...], "concurrency": 5}`。`jobs` 是当前用户在本考试创建的任务，活动任务优先，各组按创建时间降序，合计最多 100 个，不包含结果正文 `result`。切题或刷新后重新读取列表即可恢复状态。列表中的成功状态不保证结果仍可采用，采用前必须读取详情重新核对来源。

### GET /api/v1/exams/{examId}/rubric-draft-jobs/{jobId}

返回 `{"job": <QuestionRubricDraftJob>}`。任务字段包括 `id`、`exam_id`、`question_id`、`question_no`、`request_id`、`status`、`input`、`model_name`、`attempt`、`created_at`、`updated_at`，以及可选的 `error`、`result`。`input` 沿用 `QuestionMathRubricDraftRequest`，成功结果沿用 `MathRubricDraftResponse`，不返回后台权限快照、用户身份字段、模型密钥或配置摘要。创建、列表、取消响应均省略 `result`；结果只通过详情接口核对后读取。

| 状态 | 含义与恢复方式 |
| --- | --- |
| `queued` | 等待名额，或上游限流后的延迟重试 |
| `running` | 已领取执行，切题后继续 |
| `cancelling` | 已请求取消，等待当前调用结束后丢弃结果 |
| `cancelled` | 已取消，可用新请求 ID 手动重试 |
| `succeeded` | 已生成，采用前读取详情核对版本并由教师复核 |
| `failed` | 生成失败、超时或执行中断，可用新请求 ID 手动重试 |
| `stale` | 题目、答案、解答、细则、考试状态或模型配置发生变化，核对后重新生成 |

成功任务在读取详情时再次核对当前来源；不匹配则转为 `stale` 并省略 `result`。执行前后也核对题目快照和模型身份。教师采用前调用详情接口，仍须核对建议总分与证据，再通过原评分细则接口保存；读取完成之后若数据又被其他教师修改，正式保存仍须遵守原接口的状态和分值约束。

上游返回 429 时，第一次、第二次执行后分别延迟约 5 秒、10 秒重新排队，最多执行 3 次。其他失败、超时或租约中断不盲目重放模型调用；提示教师手动重试，上次调用可能已计费。

### POST /api/v1/exams/{examId}/rubric-draft-jobs/{jobId}/cancel

无需请求体，返回 `{"job": <QuestionRubricDraftJob>}`。等待任务立即转为 `cancelled`；运行任务转为 `cancelling`，当前调用结束后丢弃结果。重复取消或对已结束任务取消，返回当前任务状态。取消不会删除任务历史，也不撤销已保存的正式细则。

## PATCH /api/v1/questions/{id}

修改题号、题型、分值、题干、知识点、答题区域、排序或标准答案。题型仍必须属于受控枚举，分值必须大于 0。

## DELETE /api/v1/questions/{id}

软删除题目。

## POST /api/v1/questions/{id}/rubric

为题目创建新的 Rubric 版本。每次提交都会生成递增版本号，不覆盖历史版本。

请求：

```json
{
  "status": "draft",
  "max_score": 10,
  "points": [
    {"id": "p1", "description": "Key concept is correct", "score": 6, "required": true},
    {"id": "p2", "description": "Expression is clear", "score": 4}
  ],
  "deductions": [],
  "examples": []
}
```

规则：

- `points` 总分必须等于题目分值。
- `max_score` 必须等于题目分值。
- 最新 Rubric 为 `locked` 时不能再创建新版本，返回 `409 rubric_locked`。
- 支持状态：`draft`、`pending_review`、`approved`、`locked`。

## POST /api/v1/exams/{examId}/validate-paper-config

检查试卷配置完整性，返回问题列表。

响应示例：

```json
{
  "result": {
    "valid": false,
    "issues": [
      {
        "code": "total_score_mismatch",
        "message": "question total 90.00 does not equal exam total 100.00"
      }
    ]
  }
}
```

## POST /api/v1/paper-imports/{id}/fill-content

请求：`{"expected_generation": 7}`。按题号补齐已有正式题目的空白题干、选项、答案和解析，返回 `filled_questions`、`missing_answers` 和 `skipped`（题号及原因）。权限为 `exam:manage`，仅作用于当前租户，考试须处于 draft/configured。

它保留题型、分值及已有内容，不确认完整导入、不锁定评分细则、不发布规则。代次过期或考试冻结返回 409；重复请求跳过已填内容。具体匹配、恢复及 AI 参考数据边界见 [识别答案自动填入](../engineering/recognized-answer-content.md)。

## 审计动作

以下动作会写入 `audit_log`：

- `paper.created`
- `paper.question_created`
- `paper.question_updated`
- `paper.question_deleted`
- `paper.rubric_created`
- `paper.config_validated`
- `paper.import_content_filled`

## 资料结构确认

考试默认按资料创建，不提前生成题目。`creation_mode=materials/quick` 会忽略旧页面携带的模板分区；只有 `template` 模式采用预设分区。

- `GET /api/v1/paper-imports/{id}/structure-preview`：预览题目范围、题型与分值差异。
- `POST /api/v1/paper-imports/{id}/adopt-structure`：提交识别代次、预览哈希和完整试卷确认。需要 `exam:manage`，仅适用可编辑考试。

采用只移除空白占位，保留人工内容；资料缺分值时显示待确认，不根据预期满分补题或平均分配。采用后旧候选整批导入返回冲突，继续在正式题目中核对。当前代次的结构确认、全部小题正分值与评分配置都需要通过准备就绪检查。详细状态、事务和恢复规则见 [按考试资料建立题目结构](../engineering/material-paper-structure.md)。
