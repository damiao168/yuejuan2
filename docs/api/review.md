# Human Review API

当前文档属于 `STORY-018 人工复核与阅卷工作流`。

## 边界

本模块实现后端人工复核任务和人工评分记录：

- 创建 review_task。
- 查询 review_task 列表和详情。
- 分配阅卷员。
- 批量分配阅卷员。
- 阅卷员提交 human_grade。
- 退回重评。

本模块不实现 Web 阅卷工作台、成绩确认或成绩发布。双评分差比较、仲裁任务和 `final_grade` 写入属于 `STORY-019`，见 `docs/api/arbitration.md`。

## 权限

接口接受有效会话 Cookie 或 Bearer token。创建、分派等管理操作要求 `review:manage`；本人任务读取、领取、草稿和提交等工作操作要求 `review:work`，并检查当前对象作用域。服务端按当前登录用户的 `tenant_id` 隔离数据。Cookie 写操作还须满足现有 CSRF 校验；以实际路由 guard 为准，管理权限不隐含工作权限。

阅卷员只能提交分配给自己的任务。查询结果包含 `anonymous_code`，默认不返回学生姓名。

## 个人稍后复查

个人书签与任务状态、租约、评分草稿和成绩分别保存，绑定具体任务而非题块。同一题块的不同轮次不会合并。普通任务与暗样本走相同公开流程，响应不包含内部来源类型或参考分。

| 方法与路径 | 行为 |
| --- | --- |
| GET `/api/v1/me/review-bookmarks` | 本人当前有权读取的清单；考试/题目/学科/state/q 筛选与游标分页 |
| GET/PUT `/api/v1/me/review-bookmarks/by-task/{taskId}` | 读取状态或新增/编辑说明；状态包含取消后的墓碑版本 |
| GET `/api/v1/me/review-bookmarks/{bookmarkId}/context` | 独立只读材料与本人本次提交分，不含后续终分 |
| GET/HEAD `/api/v1/me/review-bookmarks/{bookmarkId}/image?version=...` | 重新鉴权并核对版本的私密题块图 |
| DELETE `/api/v1/me/review-bookmarks/{bookmarkId}` | 取消本人标记并清空说明，保留递增版本 |
| GET `/api/v1/me/review-bookmarks/cleanup` | 仅列本人不可用记录 ID 与版本以便清理 |

内容读取和新增/编辑要求当前 `review:work`、同租户本人归属、当前负责人及真实同校关系；本人已提交任务还须存在同一任务的本人提交事实。仅历史任务不必加入普通活动任务作用域。取消和清理只核对活跃、已解锁账号与本人元数据，撤销阅卷角色或考试归档后仍可取消；归档后禁止新增、编辑和恢复标记。

PUT 请求仅含 `reason` 和 `expected_revision`，DELETE 仅含 `expected_revision`；写请求须携带 `Idempotency-Key`。原因最多 200 个 Unicode 码点，与评分备注分开。成功写入只返回 `bookmark_id/revision/marked` 回执，客户端重新读取当前状态，不把历史回执当最新事实。同键异载荷或旧版本返回 409。原因不进入通用命令恢复、HTTP 响应缓存或跨会话自动补写队列。

取消后 context/image 不再可读。回看使用当前仍获授权的裁剪图，不能视为提交时图像快照；版本变化时整体重读材料。详细请求/响应以 OpenAPI 权威文件为准，边界与验收见[设计文档](../architecture/personal-review-bookmarks-design-2026-10-18.md)。

首版普通任务与暗样本统一不提供识别文字：`answer.text` 为空，`text_binding` 为 `unavailable`。现有数据不能为两种来源都提供可公开且绑定裁剪版本的文字。题干和选项沿用考试进入 ready 后的数据库冻结规则，评分细则取对应考试题目快照；草稿或配置中的考试不把可变题目当成历史材料，context 返回不可用。账号安全页提供独立清理入口，撤销阅卷权限后仍能移除本人不可用标记。

## POST /api/v1/review-tasks

创建人工复核任务。

请求：

```json
{
  "answer_segment_id": "segment-id",
  "source": "evidence_verification_failed",
  "priority": 3,
  "assigned_to": "reviewer-user-id"
}
```

支持的 `source`：

- `ai_low_confidence`
- `ocr_low_confidence`
- `subjective_default_review`
- `evidence_verification_failed`
- `double_mark_required`
- `score_anomaly`
- `manual_sample`

响应：`201 Created`

```json
{
  "task": {
    "id": "review-task-id",
    "answer_segment_id": "segment-id",
    "anonymous_code": "ANON-001",
    "source": "evidence_verification_failed",
    "status": "pending",
    "priority": 3
  }
}
```

## GET /api/v1/review-tasks

查询复核任务列表。

支持查询参数：

- `status`
- `assigned_to`

响应：`200 OK`

```json
{
  "tasks": []
}
```

## GET /api/v1/review-tasks/{id}

查询复核任务详情。

响应：`200 OK`

```json
{
  "task": {
    "id": "review-task-id",
    "anonymous_code": "ANON-001",
    "status": "assigned"
  }
}
```

## POST /api/v1/review-tasks/{id}/assign

分配单个复核任务。

请求：

```json
{
  "assigned_to": "reviewer-user-id"
}
```

响应：`200 OK`

## POST /api/v1/review-tasks/batch-assign

批量分配复核任务。服务端按事务方式处理；任一任务不存在或状态不允许分配时整体失败。

请求：

```json
{
  "task_ids": ["task-a", "task-b"],
  "assigned_to": "reviewer-user-id"
}
```

响应：`200 OK`

```json
{
  "tasks": []
}
```

## POST /api/v1/review-tasks/{id}/submit

提交人工评分。只有分配给当前登录用户的任务可以提交。

请求：

```json
{
  "score": 4,
  "rubric_selections": [
    {
      "point_id": "p1",
      "score": 4
    }
  ],
  "comments": "clear answer",
  "private_note": "teacher-only note",
  "student_feedback": "Good work.",
  "reason": "manual review completed"
}
```

校验：

- `score` 必须在 0 到题目满分之间。
- `rubric_selections[].point_id` 必须属于题目最新 Rubric points。
- 任务必须处于 `assigned`、`in_progress` 或 `returned`。

响应：`201 Created`

```json
{
  "task": {
    "status": "submitted"
  },
  "human_grade": {
    "score": 4,
    "max_score": 5,
    "grade_round": "single"
  }
}
```

## POST /api/v1/review-tasks/{id}/return

退回重评。

请求：

```json
{
  "reason": "needs second look"
}
```

响应：`200 OK`

## 审计

以下动作写入 `audit_log`：

- `review.task_created`
- `review.task_assigned`
- `review.tasks_batch_assigned`
- `review.human_grade_submitted`
- `review.task_returned`
