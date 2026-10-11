# Double Mark And Arbitration API

当前文档属于 `STORY-019 双评与仲裁模块`。

## 边界

本模块实现后端双评与仲裁闭环：

- 配置考试级或题目级 double mark policy。
- 为同一 `answer_segment` 创建两个独立 `review_task`。
- 双盲期间普通 review task API 不返回另一名阅卷员评分。
- 两个评分完成后自动比较分差。
- 分差不超过阈值时按策略写入 `final_grade`。
- 分差超过阈值时创建 `arbitration_task`。
- 仲裁员提交最终分后写入 `final_grade`。

仲裁详情返回任务对应的题干、选项、参考答案、解析、冻结评分标准、满分以及已保存的作答、OCR 文本和 AI 摘要。历史仲裁记录缺少标准时，从同租户、同答题片段的冻结上下文补齐；不以最新可编辑标准替代。评分标准快照缺失时返回 `rubric.status=missing`，前端应阻止提交并提示联系本场考试负责人补齐材料。没有真实作答或 AI 数据时，对应字段为空。

## 权限

- double mark policy 和 session 接口要求 `review:manage`。
- 仲裁任务读取和提交按既有 `arbitration:manage` / `arbitration:work` 权限及任务分配范围校验；普通仲裁教师只能读取分配给自己的任务。
- `context.question` 和 `context.rubric` 随获授权的任务详情返回，不要求 `exam:manage`，也不授予教师管理整场考试或读取其他任务的权限。
- 所有接口按当前登录用户的 `tenant_id` 隔离数据。

## PUT /api/v1/exams/{examId}/double-mark-policy

配置考试级双评策略。

请求：

```json
{
  "enabled": true,
  "threshold": 2,
  "resolution_strategy": "average",
  "allow_same_arbitrator": false
}
```

`resolution_strategy` 支持：

- `average`
- `first`
- `second`
- `higher`
- `lower`

## PUT /api/v1/questions/{id}/double-mark-policy

配置题目级双评策略。题目级策略优先于考试级策略。

## GET /api/v1/double-mark-policies

查询策略。支持查询参数：

- `exam_id`
- `question_id`

## POST /api/v1/double-mark-sessions

创建双评会话，并生成两个独立 `review_task`。

请求：

```json
{
  "answer_segment_id": "segment-id",
  "first_reviewer_id": "reviewer-a",
  "second_reviewer_id": "reviewer-b",
  "priority": 3
}
```

规则：

- 两个阅卷员不能为空且不能相同。
- answer segment 必须存在。
- 必须存在已启用的题目级或考试级 double mark policy。

响应：

```json
{
  "double_mark_session": {
    "id": "session-id",
    "first_review_task_id": "task-a",
    "second_review_task_id": "task-b",
    "status": "pending",
    "threshold": 2,
    "resolution_strategy": "average"
  }
}
```

## 双评提交后的结果

双评任务仍通过 `POST /api/v1/review-tasks/{id}/submit` 提交。

第一次提交只更新会话状态。第二次提交会自动比较分差：

- `score_difference <= threshold`：响应包含 `final_grade`，会话状态为 `auto_finalized`。
- `score_difference > threshold`：响应包含 `arbitration_task`，会话状态为 `needs_arbitration`。

## GET /api/v1/double-mark-sessions

查询双评会话。支持查询参数：

- `status`
- `answer_segment_id`

## GET /api/v1/double-mark-sessions/{id}

查询双评会话详情。

## POST /api/v1/arbitration-tasks

手动创建仲裁任务。通常仲裁任务会在双评分差超过阈值时自动创建。

请求：

```json
{
  "double_mark_session_id": "session-id",
  "assigned_to": "arbitrator-id",
  "difference_reason": "score difference exceeds threshold"
}
```

## GET /api/v1/arbitration-tasks

查询仲裁任务。支持查询参数：

- `status`
- `assigned_to`

## GET /api/v1/arbitration-tasks/{id}

查询仲裁详情。响应包含匿名码、题号、两名阅卷员分数、分差、分差原因和现有上下文字段。

普通 review task API 不返回这些双评分数；只有仲裁接口会展示。

## POST /api/v1/arbitration-tasks/{id}/assign

分配仲裁员。

请求：

```json
{
  "assigned_to": "arbitrator-id"
}
```

默认情况下，仲裁员不能是前两名阅卷员之一。只有策略中 `allow_same_arbitrator=true` 时允许。

## POST /api/v1/arbitration-tasks/{id}/submit

提交仲裁最终分。

请求：

```json
{
  "final_score": 4,
  "reason": "rubric evidence supports this score",
  "student_feedback": "Final score after arbitration."
}
```

响应：

```json
{
  "arbitration_task": {
    "status": "submitted",
    "final_score": 4
  },
  "final_grade": {
    "score": 4,
    "source": "arbitration",
    "locked": false
  }
}
```

## 审计

以下动作写入 `audit_log`：

- `review.double_mark_policy_set`
- `review.double_mark_session_created`
- `review.human_grade_submitted`
- `review.double_mark_auto_finalized`
- `arbitration.task_created`
- `arbitration.task_assigned`
- `arbitration.submitted`
- `final_grade.created`

审计日志读取见 `docs/api/audit.md` 的 `GET /api/v1/audit-logs`。
