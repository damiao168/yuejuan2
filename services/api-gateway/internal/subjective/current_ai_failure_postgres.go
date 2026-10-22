package subjective

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// CurrentAIFailure 只读取当前任务、最新答案、冻结量规和评分批次对应的最新运行。
// 迁移前未记录快照的运行不作投影，不能用创建时间或当前题目推测其历史依据。
func (s *PostgresStore) CurrentAIFailure(ctx context.Context, tenantID, taskID, segmentID, questionID, snapshotID, rubricVersion string) (string, error) {
	if tenantID == "" || taskID == "" || segmentID == "" || questionID == "" || snapshotID == "" || rubricVersion == "" {
		return "", nil
	}
	var status string
	var raw []byte
	var mathBindingCurrent bool
	err := s.db.QueryRowContext(ctx, `
WITH task_binding AS (
  SELECT t.scoring_run_id, t.exam_id, snapshot.id AS snapshot_id,
         seg.crop_sha256, answer.id::text AS answer_version
  FROM review_task t
  JOIN answer_segment seg ON seg.tenant_id=t.tenant_id AND seg.id=t.answer_segment_id
    AND seg.question_id=t.question_id AND seg.submission_id=t.submission_id AND seg.deleted_at IS NULL
  JOIN submission sub ON sub.tenant_id=t.tenant_id AND sub.id=t.submission_id
    AND sub.exam_id=t.exam_id AND sub.deleted_at IS NULL
  JOIN question q ON q.tenant_id=t.tenant_id AND q.id=t.question_id AND q.exam_id=t.exam_id AND q.deleted_at IS NULL
  JOIN LATERAL (
    SELECT id, created_at, rubric_snapshot_json
    FROM exam_question_snapshot
    WHERE tenant_id=t.tenant_id AND exam_id=t.exam_id AND question_id=t.question_id
    ORDER BY snapshot_version DESC, id DESC LIMIT 1
  ) snapshot ON snapshot.id=$6::uuid AND snapshot.rubric_snapshot_json->>'version'=$5
  JOIN LATERAL (
    SELECT id FROM answer_segment_answer
    WHERE tenant_id=t.tenant_id AND answer_segment_id=seg.id AND deleted_at IS NULL
    ORDER BY created_at DESC, id DESC LIMIT 1
  ) answer ON true
  WHERE t.tenant_id=$1::uuid AND t.id=$2::uuid AND t.answer_segment_id=$3::uuid
    AND t.question_id=$4::uuid AND t.deleted_at IS NULL
)
SELECT r.status, r.execution_diagnostics,
  (r.math_artifact_id IS NULL OR EXISTS (
    SELECT 1 FROM math_understanding_artifact m
    WHERE m.tenant_id=r.tenant_id AND m.id=r.math_artifact_id
      AND m.answer_segment_id=r.answer_segment_id AND m.exam_question_snapshot_id=t.snapshot_id
      AND m.version=r.math_artifact_version AND m.is_current AND m.stage='verified'
      AND r.math_scoring_version=$7
      AND m.correction_revision + COALESCE((
        SELECT max(c.revision) FROM math_understanding_correction c
        WHERE c.tenant_id=m.tenant_id AND c.artifact_id=m.id
      ), 0)=r.math_correction_revision
      AND lower(regexp_replace(btrim(m.input_hash), '^sha256:', '', 'i')) ~ '^[0-9a-f]{64}$'
      AND lower(regexp_replace(btrim(m.input_hash), '^sha256:', '', 'i'))=lower(btrim(t.crop_sha256))
  )) AS math_binding_current
FROM subjective_grading_run r
JOIN task_binding t ON t.answer_version=r.answer_version AND r.exam_question_snapshot_id=t.snapshot_id
  AND ((t.scoring_run_id IS NULL AND r.batch_id IS NULL) OR
   (t.scoring_run_id IS NOT NULL AND EXISTS (
    SELECT 1 FROM subjective_grading_batch b
    JOIN scoring_run sr ON sr.tenant_id=b.tenant_id AND sr.id=b.scoring_run_id
      AND sr.exam_id=t.exam_id AND sr.deleted_at IS NULL
    WHERE b.tenant_id=r.tenant_id AND b.id=r.batch_id AND b.scoring_run_id=t.scoring_run_id AND b.deleted_at IS NULL
   )))
WHERE r.tenant_id=$1::uuid AND r.answer_segment_id=$3::uuid AND r.question_id=$4::uuid
  AND r.rubric_version=$5 AND r.deleted_at IS NULL
ORDER BY r.created_at DESC, r.id DESC
LIMIT 1`, tenantID, taskID, segmentID, questionID, rubricVersion, snapshotID, MathScoringVersionV1).Scan(&status, &raw, &mathBindingCurrent)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	// 先选最新运行，再验证数学绑定。新版成功、处理中或证据已变化时，不能回退显示更老的失败。
	if !mathBindingCurrent || status != RunFailed && status != RunConflict {
		return "", nil
	}
	var diagnostics *ExecutionDiagnostics
	_ = json.Unmarshal(raw, &diagnostics)
	return teacherVisibleFailureKind(GradingRun{Status: status, ExecutionDiagnostics: diagnostics}), nil
}
