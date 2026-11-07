package subjective

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// 数据库用 tenant/request_id 唯一键实现运行幂等；冲突后仍需校验面板角色和版本，避免复用错误运行。
func (s *PostgresStore) GetOrCreateRun(ctx context.Context, tenantID string, _ string, input CreateRunInput) (GradingRun, error) {
	if tenantID == "" || input.AnswerSegmentID == "" || input.QuestionID == "" || input.RequestID == "" {
		return GradingRun{}, ErrInvalidInput
	}
	if input.AgentRole == "" {
		input.AgentRole = AgentRoleSingle
	}
	if !validRunPanelRole(input.PanelID, input.AgentRole) {
		return GradingRun{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return GradingRun{}, err
	}
	defer tx.Rollback()
	if input.ExamQuestionSnapshotID != "" {
		var matches bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM exam_question_snapshot WHERE tenant_id=$1::uuid AND id=$2::uuid AND question_id=$3::uuid AND rubric_snapshot_json->>'version'=$4)`, tenantID, input.ExamQuestionSnapshotID, input.QuestionID, input.RubricVersion).Scan(&matches); err != nil {
			return GradingRun{}, err
		}
		if !matches {
			return GradingRun{}, ErrIdempotencyConflict
		}
	}
	if err := validateTrialRunInTx(ctx, tx, tenantID, input); err != nil {
		return GradingRun{}, err
	}
	status := RunProcessing
	attemptCount := 1
	if input.BatchID != "" || input.PanelID != "" {
		status = RunQueued
		attemptCount = 0
	}
	row := tx.QueryRowContext(ctx, `
INSERT INTO subjective_grading_run (
  tenant_id, batch_id, answer_segment_id, answer_version, question_id, rubric_version,
  model_version, prompt_version, min_confidence, request_id, panel_id, agent_role,
  math_artifact_id, math_artifact_version, math_correction_revision, math_scoring_version,
  status, attempt_count, started_at, exam_question_snapshot_id
)
VALUES ($1::uuid, NULLIF($2, '')::uuid, $3::uuid, $4, $5::uuid, $6, $7, $8, $9, $10, NULLIF($11, '')::uuid, $12,
  NULLIF($13, '')::uuid, $14, $15, $16, $17, $18, CASE WHEN $17 = 'processing' THEN now() ELSE NULL END, NULLIF($19, '')::uuid)
ON CONFLICT (tenant_id, request_id) DO UPDATE SET updated_at = subjective_grading_run.updated_at
RETURNING id::text, tenant_id::text, COALESCE(batch_id::text, ''), answer_segment_id::text, answer_version,
  question_id::text, rubric_version, model_version, prompt_version, min_confidence::float8, request_id,
  COALESCE(panel_id::text, ''), agent_role,
  COALESCE(math_artifact_id::text, ''), math_artifact_version, math_correction_revision, math_scoring_version,
  status, attempt_count, COALESCE(grade_id::text, ''), COALESCE(error_code, ''),
  started_at, completed_at, created_at, updated_at, execution_diagnostics, COALESCE(exam_question_snapshot_id::text, '')
`, tenantID, input.BatchID, input.AnswerSegmentID, input.AnswerVersion, input.QuestionID, input.RubricVersion, input.ModelVersion, input.PromptVersion, input.MinConfidence, input.RequestID,
		input.PanelID, input.AgentRole, input.MathArtifactID, input.MathArtifactVersion, input.MathCorrectionRevision, input.MathScoringVersion, status, attemptCount, input.ExamQuestionSnapshotID)
	run, err := scanRun(row)
	if err != nil {
		if input.PanelID != "" && panelPersistenceConflict(err) {
			return GradingRun{}, ErrIdempotencyConflict
		}
		return GradingRun{}, err
	}
	if run.ExamQuestionSnapshotID != input.ExamQuestionSnapshotID || input.PanelID != "" && !runPanelIdentityMatches(run, input) {
		return GradingRun{}, ErrIdempotencyConflict
	}
	return run, tx.Commit()
}

// 通过带 status=queued 条件的更新做原子抢占，只有成功更新的 worker 才能调用模型。
func (s *PostgresStore) ClaimPanelRun(ctx context.Context, tenantID, runID string) (GradingRun, bool, error) {
	if tenantID == "" || runID == "" {
		return GradingRun{}, false, ErrInvalidInput
	}
	row := s.db.QueryRowContext(ctx, `
UPDATE subjective_grading_run
SET status='processing',attempt_count=attempt_count+1,started_at=COALESCE(started_at,now()),updated_at=now()
WHERE tenant_id=$1::uuid AND id=$2::uuid AND panel_id IS NOT NULL AND agent_role<>'single'
  AND status='queued' AND deleted_at IS NULL
RETURNING id::text, tenant_id::text, COALESCE(batch_id::text, ''), answer_segment_id::text, answer_version,
  question_id::text, rubric_version, model_version, prompt_version, min_confidence::float8, request_id,
  COALESCE(panel_id::text, ''), agent_role,
  COALESCE(math_artifact_id::text, ''), math_artifact_version, math_correction_revision, math_scoring_version,
  status, attempt_count, COALESCE(grade_id::text, ''), COALESCE(error_code, ''),
  started_at, completed_at, created_at, updated_at, execution_diagnostics, COALESCE(exam_question_snapshot_id::text, '')`, tenantID, runID)
	run, err := scanRun(row)
	if err == nil {
		return run, true, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return GradingRun{}, false, err
	}
	run, err = s.GetRun(ctx, tenantID, runID)
	if err != nil {
		return GradingRun{}, false, err
	}
	if run.PanelID == "" || run.AgentRole == AgentRoleSingle {
		return GradingRun{}, false, ErrInvalidInput
	}
	return run, false, nil
}

func (s *PostgresStore) UpdateRun(ctx context.Context, tenantID string, runID string, input UpdateRunInput) (GradingRun, error) {
	return updateRunInDB(ctx, s.db, tenantID, runID, input)
}

func updateRunInDB(ctx context.Context, query rowQueryer, tenantID string, runID string, input UpdateRunInput) (GradingRun, error) {
	if input.Status != RunQueued && input.Status != RunProcessing && input.Status != RunSucceeded && input.Status != RunFailed && input.Status != RunConflict {
		return GradingRun{}, ErrInvalidInput
	}
	if err := validateExecutionDiagnostics(input.ExecutionDiagnostics); err != nil {
		return GradingRun{}, err
	}
	var diagnostics any
	if input.ExecutionDiagnostics != nil {
		raw, err := json.Marshal(input.ExecutionDiagnostics)
		if err != nil {
			return GradingRun{}, err
		}
		diagnostics = string(raw)
	}
	// 旧协议成功响应没有诊断时必须清除上次失败；排队和处理中仍保留最近已知事实。
	row := query.QueryRowContext(ctx, `
UPDATE subjective_grading_run
SET status=$3,
  grade_id=NULLIF($4, '')::uuid,
  error_code=$5,
  attempt_count=CASE WHEN $6 > 0 THEN $6 ELSE attempt_count END,
  started_at=CASE WHEN $3 = 'processing' THEN COALESCE(started_at, now()) ELSE started_at END,
  completed_at=CASE WHEN $3 IN ('succeeded','failed','conflict') THEN now() ELSE completed_at END,
  execution_diagnostics=CASE WHEN $3='succeeded' THEN $7::jsonb ELSE COALESCE($7::jsonb, execution_diagnostics) END,
  updated_at=now()
WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL
RETURNING id::text, tenant_id::text, COALESCE(batch_id::text, ''), answer_segment_id::text, answer_version,
  question_id::text, rubric_version, model_version, prompt_version, min_confidence::float8, request_id,
  COALESCE(panel_id::text, ''), agent_role,
  COALESCE(math_artifact_id::text, ''), math_artifact_version, math_correction_revision, math_scoring_version,
  status, attempt_count, COALESCE(grade_id::text, ''), COALESCE(error_code, ''),
  started_at, completed_at, created_at, updated_at, execution_diagnostics, COALESCE(exam_question_snapshot_id::text, '')
`, tenantID, runID, input.Status, input.GradeID, input.ErrorCode, input.AttemptCount, diagnostics)
	return scanRun(row)
}

func (s *PostgresStore) GetRun(ctx context.Context, tenantID string, runID string) (GradingRun, error) {
	return getRunInDB(ctx, s.db, tenantID, runID, false)
}

func getRunInDB(ctx context.Context, query rowQueryer, tenantID string, runID string, lock bool) (GradingRun, error) {
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	row := query.QueryRowContext(ctx, `
SELECT id::text, tenant_id::text, COALESCE(batch_id::text, ''), answer_segment_id::text, answer_version,
  question_id::text, rubric_version, model_version, prompt_version, min_confidence::float8, request_id,
  COALESCE(panel_id::text, ''), agent_role,
  COALESCE(math_artifact_id::text, ''), math_artifact_version, math_correction_revision, math_scoring_version,
  status, attempt_count, COALESCE(grade_id::text, ''), COALESCE(error_code, ''),
  started_at, completed_at, created_at, updated_at, execution_diagnostics, COALESCE(exam_question_snapshot_id::text, '')
FROM subjective_grading_run
WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL
`+suffix, tenantID, runID)
	return scanRun(row)
}

type runScanner interface {
	Scan(dest ...any) error
}

func scanRun(row runScanner) (GradingRun, error) {
	var out GradingRun
	var diagnostics []byte
	var startedAt, completedAt sql.NullTime
	if err := row.Scan(&out.ID, &out.TenantID, &out.BatchID, &out.AnswerSegmentID, &out.AnswerVersion, &out.QuestionID, &out.RubricVersion, &out.ModelVersion, &out.PromptVersion, &out.MinConfidence, &out.RequestID,
		&out.PanelID, &out.AgentRole,
		&out.MathArtifactID, &out.MathArtifactVersion, &out.MathCorrectionRevision, &out.MathScoringVersion,
		&out.Status, &out.AttemptCount, &out.GradeID, &out.ErrorCode, &startedAt, &completedAt, &out.CreatedAt, &out.UpdatedAt, &diagnostics, &out.ExamQuestionSnapshotID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return GradingRun{}, ErrNotFound
		}
		return GradingRun{}, err
	}
	if len(diagnostics) > 0 {
		if err := json.Unmarshal(diagnostics, &out.ExecutionDiagnostics); err != nil {
			return GradingRun{}, err
		}
	}
	if startedAt.Valid {
		value := startedAt.Time.UTC()
		out.StartedAt = &value
	}
	if completedAt.Valid {
		value := completedAt.Time.UTC()
		out.CompletedAt = &value
	}
	out.CreatedAt = out.CreatedAt.UTC()
	out.UpdatedAt = out.UpdatedAt.UTC()
	return out, nil
}
