package subjective

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

func (s *PostgresStore) CreateGrade(ctx context.Context, tenantID string, actorID string, grade Grade) (Grade, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Grade{}, err
	}
	defer tx.Rollback()
	out, err := createGradeInTx(ctx, tx, tenantID, actorID, grade)
	if err != nil {
		return Grade{}, err
	}
	if err := tx.Commit(); err != nil {
		return Grade{}, err
	}
	return out, nil
}

func createGradeInTx(ctx context.Context, tx *sql.Tx, tenantID string, actorID string, grade Grade) (Grade, error) {
	// 试改身份由已保存的服务端样本判定，不能信任供应商回传的 risk_flags；重放也需先补同一标记再比较事实。
	// 样本只能来自试改对应题目，先走题目索引，再展开样本，避免每次回写扫描整个租户的历史试改。
	var trialSample bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM scoring_ai_trial trial CROSS JOIN LATERAL jsonb_array_elements(trial.payload->'samples') sample WHERE trial.tenant_id=$1::uuid AND trial.question_id=$3::uuid AND sample->>'segment_id'=$2)`, tenantID, grade.AnswerSegmentID, grade.QuestionID).Scan(&trialSample); err != nil {
		return Grade{}, err
	}
	if trialSample {
		grade = markTrialGrade(grade)
	}
	matched, _ := json.Marshal(grade.MatchedPoints)
	missing, _ := json.Marshal(grade.MissingPoints)
	evidence, _ := json.Marshal(grade.Evidence)
	risks, _ := json.Marshal(grade.RiskFlags)
	raw, _ := json.Marshal(cloneMap(grade.RawOutput))
	// 建议沿用来源运行冻结的快照；来源缺失或历史运行未记录快照时保持未知，不能用当前题目补造。
	row := tx.QueryRowContext(ctx, `
INSERT INTO ai_grade (
  tenant_id, answer_segment_id, question_id, question_no, question_type, answer_version,
  exam_question_snapshot_id,
  grader_type, rule_version, suggested_score, max_score, confidence,
  matched_points, missing_points, evidence, risk_flags, needs_human_review,
  auto_pass, mock, raw_output, created_by, status, failure_reason,
  model_version, prompt_version, student_feedback, teacher_note,
  rubric_version, delivery_mode, capability_profile, adapter_request_id,
  adapter_name, provider_key, deployment_key, deployment_region,
  adapter_attempts, adapter_latency_ms, adapter_repair_attempted, subjective_grading_run_id,
  math_artifact_id, math_artifact_version, math_correction_revision, math_scoring_version
)
VALUES ($1, $2, $3, $4, $5, $6,
  CASE WHEN NULLIF($36,'') IS NOT NULL THEN
    (SELECT exam_question_snapshot_id FROM subjective_grading_run WHERE tenant_id=$1::uuid AND id=NULLIF($36,'')::uuid AND question_id=$3::uuid AND answer_segment_id=$2::uuid)
  ELSE NULL END,
  $7, 'llm-adapter', $8, $9, $10,
  $11, $12, $13, $14, $15, false, $16, $17, $18, $19, NULLIF($20, ''),
  $21, $22, $23, $24, $25, $26, $27, $28, $29, $30, $31, $32, $33, $34, $35, NULLIF($36, '')::uuid,
  NULLIF($37, '')::uuid, $38, $39, $40)
ON CONFLICT (tenant_id, adapter_request_id) WHERE adapter_request_id <> '' AND deleted_at IS NULL DO NOTHING
RETURNING id::text, tenant_id::text, answer_segment_id::text, question_id::text, question_no, question_type,
  answer_version, grader_type, model_version, prompt_version, rubric_version, delivery_mode, capability_profile,
  adapter_request_id, adapter_name, provider_key, deployment_key, deployment_region,
  adapter_attempts, adapter_latency_ms, adapter_repair_attempted,
  suggested_score::float8, max_score::float8, confidence::float8,
  matched_points, missing_points, evidence, risk_flags, needs_human_review, student_feedback, teacher_note,
  mock, status, COALESCE(failure_reason, ''), raw_output, created_by::text, created_at,
  COALESCE(subjective_grading_run_id::text, ''), COALESCE(math_artifact_id::text, ''),
  math_artifact_version, math_correction_revision, math_scoring_version
`, tenantID, grade.AnswerSegmentID, grade.QuestionID, grade.QuestionNo, grade.QuestionType,
		grade.AnswerVersion, grade.GraderType, grade.SuggestedScore, grade.MaxScore, grade.Confidence,
		matched, missing, evidence, risks, grade.NeedsHumanReview, grade.Mock, raw, actorID,
		grade.Status, grade.FailureReason, grade.ModelVersion, grade.PromptVersion, grade.StudentFeedback, grade.TeacherNote,
		grade.RubricVersion, grade.DeliveryMode, grade.CapabilityProfile, grade.AdapterRequestID,
		grade.AdapterName, grade.ProviderKey, grade.DeploymentKey, grade.DeploymentRegion,
		grade.AdapterAttempts, grade.AdapterLatencyMS, grade.AdapterRepairAttempted, grade.RunID,
		grade.MathArtifactID, grade.MathArtifactVersion, grade.MathCorrectionRevision, grade.MathScoringVersion)
	var out Grade
	if err := scanGrade(row, &out); err != nil {
		if errors.Is(err, ErrNotFound) && grade.AdapterRequestID != "" {
			existing, lookupErr := getGradeByRequestInDB(ctx, tx, tenantID, grade.AdapterRequestID)
			if lookupErr == nil {
				if sameGradeIdentity(existing, grade) {
					return existing, nil
				}
				return Grade{}, ErrIdempotencyConflict
			}
		}
		return Grade{}, err
	}
	if !out.Mock && grade.RawOutput["panel_execution"] != nil {
		if err := recordImagePanelCallsInTx(ctx, tx, tenantID, grade); err != nil {
			return Grade{}, err
		}
		return out, nil
	}
	if !out.Mock && gradeUsageReported(grade) &&
		out.AdapterRequestID != "" &&
		out.AdapterName != "" &&
		out.ProviderKey != "" &&
		(out.DeploymentKey != "" || grade.ModelConfigID != "") &&
		out.DeploymentRegion != "" {
		// 在成绩事务内同时记录供应商的完整用量。model_call_fact 触发器为旧调用方提供回退；
		// 先插入让其 ON CONFLICT 保留缓存和推理 token。
		if _, err := tx.ExecContext(ctx, `
INSERT INTO model_usage_event (
  tenant_id, school_id, request_id, feature, agent_role, provider_key, model_name,
  request_count, input_tokens, output_tokens, cached_input_tokens, reasoning_tokens, total_tokens,
  status, metadata
)
SELECT $1::uuid, exam.school_id, $2, 'subjective_grading',
  (SELECT NULLIF(run.agent_role, 'single') FROM subjective_grading_run run
   WHERE run.tenant_id=$1::uuid AND run.id=NULLIF($3, '')::uuid), $4, $5,
  CASE WHEN $15::bigint > 0 THEN $15::bigint ELSE 1 END,
  $6::bigint, $7::bigint, $8::bigint, $9::bigint,
  CASE WHEN $10::bigint > 0 THEN $10::bigint ELSE $6::bigint+$7::bigint END, $11,
  jsonb_build_object('deployment_key', $12::text, 'question_id', $13::uuid, 'answer_segment_id', $14::uuid)
FROM answer_segment segment
JOIN submission submission ON submission.tenant_id=segment.tenant_id AND submission.id=segment.submission_id
JOIN exam ON exam.tenant_id=submission.tenant_id AND exam.id=submission.exam_id
WHERE segment.tenant_id=$1::uuid AND segment.id=$14::uuid
ON CONFLICT (tenant_id, request_id, feature) DO NOTHING
`, tenantID, grade.AdapterRequestID, grade.RunID, grade.ProviderKey, grade.ModelVersion,
			grade.InputTokens, grade.OutputTokens, grade.CachedInputTokens, grade.ReasoningTokens,
			grade.TotalTokens, grade.Status, grade.DeploymentKey, grade.QuestionID, grade.AnswerSegmentID,
			grade.AdapterAttempts); err != nil {
			return Grade{}, err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO model_call_fact (
  tenant_id, request_id, answer_segment_id, question_id,
  provider_key, deployment_key, adapter_type, model_version,
  managed_model_api_config_id, model_name,
  prompt_version, rubric_version, capability_profile, deployment_region,
  route_mode, route_reason, status, attempts, latency_ms, input_units, output_units, error_code
)
VALUES (
  $1, $2, $3, $4,
  $5, $6, $7, $8,
  NULLIF($19, '')::uuid, $20,
  $9, $10, $11, $12,
  'local_only', 'configured governed grading-agent model', $13, $14, $15, $16, $17, $18
)
`, tenantID, out.AdapterRequestID, out.AnswerSegmentID, out.QuestionID,
			out.ProviderKey, out.DeploymentKey, out.AdapterName, out.ModelVersion,
			out.PromptVersion, out.RubricVersion, out.CapabilityProfile, out.DeploymentRegion,
			out.Status, out.AdapterAttempts, out.AdapterLatencyMS, grade.InputTokens, grade.OutputTokens, out.FailureReason,
			grade.ModelConfigID, grade.ModelName); err != nil {
			return Grade{}, err
		}
	}
	return out, nil
}

func (s *PostgresStore) GetGradeByAdapterRequestID(ctx context.Context, tenantID string, requestID string) (Grade, error) {
	return getGradeByRequestInDB(ctx, s.db, tenantID, requestID)
}

func getGradeByRequestInDB(ctx context.Context, query rowQueryer, tenantID string, requestID string) (Grade, error) {
	if requestID == "" {
		return Grade{}, ErrNotFound
	}
	row := query.QueryRowContext(ctx, `
SELECT id::text, tenant_id::text, answer_segment_id::text, question_id::text, question_no, question_type,
  answer_version, grader_type, model_version, prompt_version, rubric_version, delivery_mode, capability_profile,
  adapter_request_id, adapter_name, provider_key, deployment_key, deployment_region,
  adapter_attempts, adapter_latency_ms, adapter_repair_attempted,
  suggested_score::float8, max_score::float8, confidence::float8,
  matched_points, missing_points, evidence, risk_flags, needs_human_review, student_feedback, teacher_note,
  mock, status, COALESCE(failure_reason, ''), raw_output, created_by::text, created_at,
  COALESCE(subjective_grading_run_id::text, ''), COALESCE(math_artifact_id::text, ''),
  math_artifact_version, math_correction_revision, math_scoring_version
FROM ai_grade
WHERE tenant_id = $1 AND adapter_request_id = $2 AND deleted_at IS NULL
ORDER BY created_at DESC
LIMIT 1
`, tenantID, requestID)
	var out Grade
	if err := scanGrade(row, &out); err != nil {
		return Grade{}, err
	}
	return out, nil
}

type gradeScanner interface {
	Scan(dest ...any) error
}

func scanGrade(row gradeScanner, out *Grade) error {
	var matchedRaw, missingRaw, evidenceRaw, risksRaw, rawOutput []byte
	if err := row.Scan(
		&out.ID,
		&out.TenantID,
		&out.AnswerSegmentID,
		&out.QuestionID,
		&out.QuestionNo,
		&out.QuestionType,
		&out.AnswerVersion,
		&out.GraderType,
		&out.ModelVersion,
		&out.PromptVersion,
		&out.RubricVersion,
		&out.DeliveryMode,
		&out.CapabilityProfile,
		&out.AdapterRequestID,
		&out.AdapterName,
		&out.ProviderKey,
		&out.DeploymentKey,
		&out.DeploymentRegion,
		&out.AdapterAttempts,
		&out.AdapterLatencyMS,
		&out.AdapterRepairAttempted,
		&out.SuggestedScore,
		&out.MaxScore,
		&out.Confidence,
		&matchedRaw,
		&missingRaw,
		&evidenceRaw,
		&risksRaw,
		&out.NeedsHumanReview,
		&out.StudentFeedback,
		&out.TeacherNote,
		&out.Mock,
		&out.Status,
		&out.FailureReason,
		&rawOutput,
		&out.CreatedBy,
		&out.CreatedAt,
		&out.RunID,
		&out.MathArtifactID,
		&out.MathArtifactVersion,
		&out.MathCorrectionRevision,
		&out.MathScoringVersion,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	_ = json.Unmarshal(matchedRaw, &out.MatchedPoints)
	_ = json.Unmarshal(missingRaw, &out.MissingPoints)
	_ = json.Unmarshal(evidenceRaw, &out.Evidence)
	_ = json.Unmarshal(risksRaw, &out.RiskFlags)
	out.RawOutput = map[string]any{}
	_ = json.Unmarshal(rawOutput, &out.RawOutput)
	out.CreatedAt = out.CreatedAt.UTC()
	return nil
}
