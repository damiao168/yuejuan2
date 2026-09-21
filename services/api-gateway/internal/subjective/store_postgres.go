package subjective

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"edugrade-enterprise/services/api-gateway/internal/paper"
)

type PostgresStore struct {
	db *sql.DB
}

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
	status := RunProcessing
	attemptCount := 1
	if input.BatchID != "" || input.PanelID != "" {
		status = RunQueued
		attemptCount = 0
	}
	row := s.db.QueryRowContext(ctx, `
INSERT INTO subjective_grading_run (
  tenant_id, batch_id, answer_segment_id, answer_version, question_id, rubric_version,
  model_version, prompt_version, min_confidence, request_id, panel_id, agent_role,
  math_artifact_id, math_artifact_version, math_correction_revision, math_scoring_version,
  status, attempt_count, started_at
)
VALUES ($1::uuid, NULLIF($2, '')::uuid, $3::uuid, $4, $5::uuid, $6, $7, $8, $9, $10, NULLIF($11, '')::uuid, $12,
  NULLIF($13, '')::uuid, $14, $15, $16, $17, $18, CASE WHEN $17 = 'processing' THEN now() ELSE NULL END)
ON CONFLICT (tenant_id, request_id) DO UPDATE SET updated_at = subjective_grading_run.updated_at
RETURNING id::text, tenant_id::text, COALESCE(batch_id::text, ''), answer_segment_id::text, answer_version,
  question_id::text, rubric_version, model_version, prompt_version, min_confidence::float8, request_id,
  COALESCE(panel_id::text, ''), agent_role,
  COALESCE(math_artifact_id::text, ''), math_artifact_version, math_correction_revision, math_scoring_version,
  status, attempt_count, COALESCE(grade_id::text, ''), COALESCE(error_code, ''),
  started_at, completed_at, created_at, updated_at
`, tenantID, input.BatchID, input.AnswerSegmentID, input.AnswerVersion, input.QuestionID, input.RubricVersion, input.ModelVersion, input.PromptVersion, input.MinConfidence, input.RequestID,
		input.PanelID, input.AgentRole, input.MathArtifactID, input.MathArtifactVersion, input.MathCorrectionRevision, input.MathScoringVersion, status, attemptCount)
	run, err := scanRun(row)
	if err != nil {
		if input.PanelID != "" && panelPersistenceConflict(err) {
			return GradingRun{}, ErrIdempotencyConflict
		}
		return GradingRun{}, err
	}
	if input.PanelID != "" && !runPanelIdentityMatches(run, input) {
		return GradingRun{}, ErrIdempotencyConflict
	}
	return run, nil
}

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
  started_at, completed_at, created_at, updated_at`, tenantID, runID)
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
	if input.Status != RunQueued && input.Status != RunProcessing && input.Status != RunSucceeded && input.Status != RunFailed && input.Status != RunConflict {
		return GradingRun{}, ErrInvalidInput
	}
	row := s.db.QueryRowContext(ctx, `
UPDATE subjective_grading_run
SET status=$3,
  grade_id=NULLIF($4, '')::uuid,
  error_code=$5,
  attempt_count=CASE WHEN $6 > 0 THEN $6 ELSE attempt_count END,
  started_at=CASE WHEN $3 = 'processing' THEN COALESCE(started_at, now()) ELSE started_at END,
  completed_at=CASE WHEN $3 IN ('succeeded','failed','conflict') THEN now() ELSE completed_at END,
  updated_at=now()
WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL
RETURNING id::text, tenant_id::text, COALESCE(batch_id::text, ''), answer_segment_id::text, answer_version,
  question_id::text, rubric_version, model_version, prompt_version, min_confidence::float8, request_id,
  COALESCE(panel_id::text, ''), agent_role,
  COALESCE(math_artifact_id::text, ''), math_artifact_version, math_correction_revision, math_scoring_version,
  status, attempt_count, COALESCE(grade_id::text, ''), COALESCE(error_code, ''),
  started_at, completed_at, created_at, updated_at
`, tenantID, runID, input.Status, input.GradeID, input.ErrorCode, input.AttemptCount)
	return scanRun(row)
}

func (s *PostgresStore) GetRun(ctx context.Context, tenantID string, runID string) (GradingRun, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id::text, tenant_id::text, COALESCE(batch_id::text, ''), answer_segment_id::text, answer_version,
  question_id::text, rubric_version, model_version, prompt_version, min_confidence::float8, request_id,
  COALESCE(panel_id::text, ''), agent_role,
  COALESCE(math_artifact_id::text, ''), math_artifact_version, math_correction_revision, math_scoring_version,
  status, attempt_count, COALESCE(grade_id::text, ''), COALESCE(error_code, ''),
  started_at, completed_at, created_at, updated_at
FROM subjective_grading_run
WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL
`, tenantID, runID)
	return scanRun(row)
}

func (s *PostgresStore) CreateBatch(ctx context.Context, tenantID string, actorID string, input CreateBatchInput) (GradingBatch, error) {
	segments, err := normalizeBatchSegments(input.SegmentIDs)
	if err != nil || tenantID == "" || input.IdempotencyKey == "" || actorID == "" {
		return GradingBatch{}, ErrInvalidInput
	}
	raw, err := json.Marshal(segments)
	if err != nil {
		return GradingBatch{}, err
	}
	row := s.db.QueryRowContext(ctx, `
INSERT INTO subjective_grading_batch (tenant_id, idempotency_key, status, segment_ids, total_count, created_by,command_request_hash)
VALUES ($1::uuid, $2, 'planned', $3::jsonb, $4, $5::uuid,$6)
ON CONFLICT (tenant_id, idempotency_key) DO UPDATE SET updated_at = subjective_grading_batch.updated_at
RETURNING id::text, tenant_id::text, idempotency_key, status, segment_ids,
  total_count, queued_count, processing_count, succeeded_count, failed_count,
  created_by::text, created_at, updated_at
`, tenantID, input.IdempotencyKey, raw, len(segments), actorID, batchRequestHash(segments))
	batch, err := scanBatch(row)
	if err != nil {
		return GradingBatch{}, err
	}
	if batch.CreatedBy != actorID || !sameStringSlice(batch.SegmentIDs, segments) {
		return GradingBatch{}, ErrIdempotencyConflict
	}
	return batch, nil
}

func (s *PostgresStore) GetBatch(ctx context.Context, tenantID string, batchID string) (GradingBatch, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id::text, tenant_id::text, idempotency_key, status, segment_ids,
  total_count, queued_count, processing_count, succeeded_count, failed_count,
  created_by::text, created_at, updated_at
FROM subjective_grading_batch
WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL
`, tenantID, batchID)
	return scanBatch(row)
}

func (s *PostgresStore) RefreshBatch(ctx context.Context, tenantID string, batchID string) (GradingBatch, error) {
	row := s.db.QueryRowContext(ctx, `
WITH counts AS (
  SELECT
    count(*) FILTER (WHERE status = 'queued')::int AS queued_count,
    count(*) FILTER (WHERE status = 'processing')::int AS processing_count,
    count(*) FILTER (WHERE status = 'succeeded')::int AS succeeded_count,
    count(*) FILTER (WHERE status IN ('failed', 'conflict'))::int AS failed_count
  FROM subjective_grading_run
  WHERE tenant_id=$1::uuid AND batch_id=$2::uuid AND deleted_at IS NULL
)
UPDATE subjective_grading_batch AS batch
SET queued_count=counts.queued_count,
  processing_count=counts.processing_count,
  succeeded_count=counts.succeeded_count,
  failed_count=counts.failed_count,
  status=CASE
    WHEN batch.status IN ('planned', 'cancelled') THEN batch.status
    WHEN counts.succeeded_count + counts.failed_count = batch.total_count AND counts.failed_count > 0 THEN 'failed'
    WHEN counts.succeeded_count = batch.total_count THEN 'completed'
    ELSE 'processing'
  END,
  updated_at=now()
FROM counts
WHERE batch.tenant_id=$1::uuid AND batch.id=$2::uuid AND batch.deleted_at IS NULL
RETURNING batch.id::text, batch.tenant_id::text, batch.idempotency_key, batch.status, batch.segment_ids,
  batch.total_count, batch.queued_count, batch.processing_count, batch.succeeded_count, batch.failed_count,
  batch.created_by::text, batch.created_at, batch.updated_at
`, tenantID, batchID)
	return scanBatch(row)
}

func (s *PostgresStore) UpdateBatch(ctx context.Context, tenantID string, batchID string, input UpdateBatchInput) (GradingBatch, error) {
	if input.Status != "planned" && input.Status != "processing" && input.Status != "completed" && input.Status != "failed" && input.Status != "cancelled" {
		return GradingBatch{}, ErrInvalidInput
	}
	row := s.db.QueryRowContext(ctx, `
UPDATE subjective_grading_batch
SET status=$3, queued_count=$4, processing_count=$5, succeeded_count=$6, failed_count=$7, updated_at=now()
WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL
RETURNING id::text, tenant_id::text, idempotency_key, status, segment_ids,
  total_count, queued_count, processing_count, succeeded_count, failed_count,
  created_by::text, created_at, updated_at
`, tenantID, batchID, input.Status, input.QueuedCount, input.ProcessingCount, input.SucceededCount, input.FailedCount)
	return scanBatch(row)
}

func scanBatch(row gradeScanner) (GradingBatch, error) {
	var out GradingBatch
	var segmentRaw []byte
	if err := row.Scan(&out.ID, &out.TenantID, &out.IdempotencyKey, &out.Status, &segmentRaw, &out.TotalCount, &out.QueuedCount, &out.ProcessingCount, &out.SucceededCount, &out.FailedCount, &out.CreatedBy, &out.CreatedAt, &out.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return GradingBatch{}, ErrNotFound
		}
		return GradingBatch{}, err
	}
	if err := json.Unmarshal(segmentRaw, &out.SegmentIDs); err != nil {
		return GradingBatch{}, err
	}
	out.CreatedAt = out.CreatedAt.UTC()
	out.UpdatedAt = out.UpdatedAt.UTC()
	return out, nil
}

type runScanner interface {
	Scan(dest ...any) error
}

func scanRun(row runScanner) (GradingRun, error) {
	var out GradingRun
	var startedAt, completedAt sql.NullTime
	if err := row.Scan(&out.ID, &out.TenantID, &out.BatchID, &out.AnswerSegmentID, &out.AnswerVersion, &out.QuestionID, &out.RubricVersion, &out.ModelVersion, &out.PromptVersion, &out.MinConfidence, &out.RequestID,
		&out.PanelID, &out.AgentRole,
		&out.MathArtifactID, &out.MathArtifactVersion, &out.MathCorrectionRevision, &out.MathScoringVersion,
		&out.Status, &out.AttemptCount, &out.GradeID, &out.ErrorCode, &startedAt, &completedAt, &out.CreatedAt, &out.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return GradingRun{}, ErrNotFound
		}
		return GradingRun{}, err
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

func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (s *PostgresStore) LoadContext(ctx context.Context, tenantID string, segmentID string) (Context, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT
  seg.id::text, to_jsonb(eqs), seg.submission_page_id::text, seg.bbox,
  e.subject, COALESCE(cohort.grade_level, ''),
  q.id::text, q.tenant_id::text, q.exam_id::text, COALESCE(q.exam_paper_id::text, ''),
  q.question_no, q.question_type, q.score::float8, COALESCE(q.stem, ''),
  q.knowledge_points, q.answer_area, q.sort_order, q.status,
  COALESCE(qr.id::text, ''), COALESCE(qr.question_id::text, ''), COALESCE(rv.version, ''),
  COALESCE(qr.status, ''), COALESCE(qr.max_score::float8, 0), COALESCE(qr.points, '[]'::jsonb),
  COALESCE(qr.deductions, '[]'::jsonb), COALESCE(qr.examples, '[]'::jsonb),
  COALESCE(ans.id::text, ''), COALESCE(ans.answer_text, ''), ans.confidence::float8, ans.created_at
FROM answer_segment seg
JOIN question q ON q.tenant_id = seg.tenant_id AND q.id = seg.question_id
JOIN exam_question_snapshot eqs ON eqs.tenant_id = q.tenant_id AND eqs.exam_id = q.exam_id AND eqs.question_id = q.id
JOIN exam e ON e.tenant_id = q.tenant_id AND e.id = q.exam_id AND e.deleted_at IS NULL
LEFT JOIN LATERAL (
  SELECT CASE
    WHEN COUNT(DISTINCT g.level_no) = 1 AND MIN(g.level_no) BETWEEN 7 AND 9 THEN 'junior'
    WHEN COUNT(DISTINCT g.level_no) = 1 AND MIN(g.level_no) BETWEEN 10 AND 12 THEN 'senior'
    ELSE ''
  END AS grade_level
  FROM exam_class ec
  JOIN school_class sc ON sc.tenant_id = ec.tenant_id AND sc.id = ec.class_id AND sc.deleted_at IS NULL
  JOIN grade g ON g.tenant_id = sc.tenant_id AND g.id = sc.grade_id AND g.deleted_at IS NULL
  WHERE ec.tenant_id = q.tenant_id AND ec.exam_id = q.exam_id AND ec.deleted_at IS NULL
) cohort ON true
LEFT JOIN LATERAL (
  SELECT qr.id, qr.question_id, qr.status, qr.max_score, qr.points, qr.deductions, qr.examples, qr.rubric_version_id
  FROM question_rubric qr
  WHERE qr.tenant_id = q.tenant_id AND qr.question_id = q.id AND qr.deleted_at IS NULL
  ORDER BY qr.created_at DESC
  LIMIT 1
) qr ON true
LEFT JOIN rubric_version rv ON rv.tenant_id = q.tenant_id AND rv.id = qr.rubric_version_id
LEFT JOIN LATERAL (
  SELECT id, answer_text, confidence, created_at
  FROM answer_segment_answer
  WHERE tenant_id = seg.tenant_id AND answer_segment_id = seg.id AND deleted_at IS NULL
  ORDER BY created_at DESC
  LIMIT 1
) ans ON true
WHERE seg.tenant_id = $1 AND seg.id::text = $2 AND seg.deleted_at IS NULL
`, tenantID, segmentID)
	var out Context
	var snapshotRaw []byte
	var submissionPageID string
	var bboxRaw, kpRaw, areaRaw []byte
	var question paper.Question
	var rubric paper.Rubric
	var pointsRaw, deductionsRaw, examplesRaw []byte
	var answerID string
	var ocrConfidence sql.NullFloat64
	var answerCreated sql.NullTime
	if err := row.Scan(
		&out.SegmentID,
		&snapshotRaw,
		&submissionPageID,
		&bboxRaw,
		&out.Subject,
		&out.GradeLevel,
		&question.ID,
		&question.TenantID,
		&question.ExamID,
		&question.ExamPaperID,
		&question.QuestionNo,
		&question.QuestionType,
		&question.Score,
		&question.Stem,
		&kpRaw,
		&areaRaw,
		&question.SortOrder,
		&question.Status,
		&rubric.ID,
		&rubric.QuestionID,
		&rubric.Version,
		&rubric.Status,
		&rubric.MaxScore,
		&pointsRaw,
		&deductionsRaw,
		&examplesRaw,
		&answerID,
		&out.AnswerText,
		&ocrConfidence,
		&answerCreated,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Context{}, ErrNotFound
		}
		return Context{}, err
	}
	if err := json.Unmarshal(snapshotRaw, &out.AssessmentSnapshot); err != nil {
		return Context{}, err
	}
	out.Subject = string(out.AssessmentSnapshot.SubjectCode)
	out.GradeLevel = string(out.AssessmentSnapshot.EducationStage)
	if !IsSupportedQuestionType(question.QuestionType) {
		return Context{}, ErrUnsupportedQuestionType
	}
	_ = json.Unmarshal(kpRaw, &question.KnowledgePoints)
	if len(areaRaw) > 0 {
		_ = json.Unmarshal(areaRaw, &question.AnswerArea)
	}
	if rubric.ID == "" {
		return Context{}, ErrRubricMissing
	}
	_ = json.Unmarshal(pointsRaw, &rubric.Points)
	_ = json.Unmarshal(deductionsRaw, &rubric.Deductions)
	_ = json.Unmarshal(examplesRaw, &rubric.Examples)
	if frozen, ok := rubricFromAssessmentSnapshot(out.AssessmentSnapshot.RubricSnapshot, question.ID); ok {
		rubric = frozen
	}
	if answerID == "" {
		return Context{}, ErrAnswerMissing
	}
	out.AnswerVersion = answerID
	var bbox []float64
	_ = json.Unmarshal(bboxRaw, &bbox)
	out.Question = question
	out.Rubric = rubric
	out.AnswerImageRef = map[string]any{"submission_page_id": submissionPageID, "bbox": bbox}
	if ocrConfidence.Valid {
		value := ocrConfidence.Float64
		out.OCRConfidence = &value
	}
	if answerCreated.Valid {
		out.AnswerCreatedAt = answerCreated.Time.UTC()
	}
	return out, nil
}

func rubricFromAssessmentSnapshot(snapshot map[string]any, questionID string) (paper.Rubric, bool) {
	if len(snapshot) == 0 {
		return paper.Rubric{}, false
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return paper.Rubric{}, false
	}
	var rubric paper.Rubric
	if err := json.Unmarshal(raw, &rubric); err != nil {
		return paper.Rubric{}, false
	}
	rubric.QuestionID = questionID
	return rubric, rubric.ID != "" || rubric.Version != "" || len(rubric.Points) > 0
}

func (s *PostgresStore) CreateGrade(ctx context.Context, tenantID string, actorID string, grade Grade) (Grade, error) {
	matched, _ := json.Marshal(grade.MatchedPoints)
	missing, _ := json.Marshal(grade.MissingPoints)
	evidence, _ := json.Marshal(grade.Evidence)
	risks, _ := json.Marshal(grade.RiskFlags)
	raw, _ := json.Marshal(cloneMap(grade.RawOutput))
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Grade{}, err
	}
	defer tx.Rollback()
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
VALUES ($1, $2, $3, $4, $5, $6, (SELECT id FROM exam_question_snapshot WHERE tenant_id=$1::uuid AND question_id=$3::uuid), $7, 'llm-adapter', $8, $9, $10,
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
			existing, lookupErr := s.GetGradeByAdapterRequestID(ctx, tenantID, grade.AdapterRequestID)
			if lookupErr == nil {
				if sameGradeIdentity(existing, grade) {
					return existing, nil
				}
				return Grade{}, ErrIdempotencyConflict
			}
		}
		return Grade{}, err
	}
	if !out.Mock &&
		out.AdapterRequestID != "" &&
		out.AdapterName != "" &&
		out.ProviderKey != "" &&
		out.DeploymentKey != "" &&
		out.DeploymentRegion != "" {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO model_call_fact (
  tenant_id, request_id, answer_segment_id, question_id,
  provider_key, deployment_key, adapter_type, model_version,
  prompt_version, rubric_version, capability_profile, deployment_region,
  route_mode, route_reason, status, attempts, latency_ms, error_code
)
VALUES (
  $1, $2, $3, $4,
  $5, $6, $7, $8,
  $9, $10, $11, $12,
  'local_only', 'configured governed grading-agent deployment', $13, $14, $15, $16
)
`, tenantID, out.AdapterRequestID, out.AnswerSegmentID, out.QuestionID,
			out.ProviderKey, out.DeploymentKey, out.AdapterName, out.ModelVersion,
			out.PromptVersion, out.RubricVersion, out.CapabilityProfile, out.DeploymentRegion,
			out.Status, out.AdapterAttempts, out.AdapterLatencyMS, out.FailureReason); err != nil {
			return Grade{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Grade{}, err
	}
	return out, nil
}

func (s *PostgresStore) GetGradeByAdapterRequestID(ctx context.Context, tenantID string, requestID string) (Grade, error) {
	if requestID == "" {
		return Grade{}, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, `
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
