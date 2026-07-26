# Scores API

当前文档属于 `STORY-020 最终成绩与成绩发布`。

## 边界

本模块实现后端最终成绩与发布闭环：

- 从已有 `final_grade`、单评 `human_grade` 或可自动通过的规则型 `ai_grade` 汇总最终题目分。
- 为每个 `submission` 生成 `submission_grade` 总分。
- 支持成绩确认。
- 发布前执行质量检查。
- 发布后锁定核心分数。
- 学生可查询已发布成绩。
- 支持 CSV 导出并写入水印字段。

本模块不实现 Web 成绩管理页面、学生端 UI、申诉改分、Excel `.xlsx` 二进制导出、成绩排名、发布通知或报表分析。

## 权限

- `score:manage`：最终成绩汇总、查询、确认、发布、导出。
- `student:grade:read`：查询学生已发布成绩。

所有接口按当前登录用户的 `tenant_id` 隔离数据。

## POST /api/v1/exams/{examId}/finalize

生成最终题目分和答卷总分。

生成规则：

- 已有 `final_grade` 会被保留。
- 已提交的单评 `human_grade` 可生成 `source=single_review` 的题目分。
- `auto_pass=true`、`needs_human_review=false`、`mock=false` 的规则型 `ai_grade` 可生成 `source=rule_auto` 的题目分。
- 不会把 mock AI 或需要人工复核的评分变成最终分。

响应：`201 Created`

```json
{
  "status": "pending_confirmation",
  "created_finals": 2,
  "submission_grades": [],
  "quality": {
    "passed": true,
    "issues": []
  },
  "available_statuses": [
    "calculating",
    "pending_confirmation",
    "confirmed",
    "pending_publish",
    "published",
    "locked"
  ]
}
```

## GET /api/v1/exams/{examId}/grades

查询答卷总分和题目分。要求 `score:manage`。

响应：

```json
{
  "grades": [
    {
      "submission_id": "submission-id",
      "anonymous_code": "ANON-001",
      "total_score": 88,
      "max_score": 100,
      "status": "pending_confirmation",
      "items": []
    }
  ]
}
```

## GET /api/v1/exams/{examId}/grades/quality

只读质量检查。要求 `score:manage`。

支持查询参数：

- `stage=publish`：按发布前要求检查，包含是否已确认成绩。
- 未传 `stage=publish` 时按确认前要求检查。

响应：

```json
{
  "stage": "publish",
  "can_publish": false,
  "quality": {
    "passed": false,
    "issues": [
      {
        "code": "grades_not_confirmed",
        "message": "there are grades not ready for publish",
        "blocking": true,
        "count": 1
      }
    ]
  }
}
```

## POST /api/v1/exams/{examId}/confirm-grades

确认成绩。用于学科组长确认环节。

请求：

```json
{
  "reason": "checked by subject lead"
}
```

成功后状态进入 `confirmed`。发布接口会接受 `confirmed` 或 `pending_publish` 状态的成绩并执行发布锁定。

## POST /api/v1/exams/{examId}/publish

发布成绩。要求 `score:manage`。

发布前质量检查：

- 未完成阅卷任务。
- 未完成仲裁任务。
- OCR 失败未处理。
- 缺失最终题目分。
- 未确认的答卷总分。

任一阻断项存在时返回 `409 Conflict`。

成功发布后：

- `submission_grade.status=published`。
- `submission_grade.locked=true`。
- `final_grade.status=locked`。
- `final_grade.locked=true`。

## GET /api/v1/students/{studentId}/exams/{examId}/grade

学生查询已发布成绩。要求 `student:grade:read`。

未发布或未锁定的成绩不会返回。

## GET /api/v1/exams/{examId}/grades/export

导出 CSV。要求 `score:manage`。

CSV 字段：

```text
submission_id,student_id,anonymous_code,total_score,max_score,status,locked,exported_by,exported_at,watermark
```

`watermark` 字段包含租户、考试、导出人和导出时间。导出动作写审计。

## 审计

以下动作写入 `audit_log`：

- `score.finalized`
- `score.confirmed`
- `score.published`
- `score.exported`
