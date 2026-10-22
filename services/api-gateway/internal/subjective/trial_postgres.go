package subjective

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"edugrade-enterprise/services/api-gateway/internal/grading"
	"github.com/google/uuid"
)

type postgresTrialRepository struct {
	ctx      context.Context
	tx       *sql.Tx
	tenantID string
}

// 先锁评分运行，再锁试改资源；同一运行的创建、扩批、恢复和取消使用同一持久化边界。
func (r postgresTrialRepository) lockRun(runID string, mutable bool) error {
	var status string
	err := r.tx.QueryRowContext(r.ctx, `SELECT status FROM scoring_run WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, r.tenantID, runID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if mutable && status != "queued" && status != "processing" && status != "needs_review" && status != "failed" {
		return ErrTrialConflict
	}
	return nil
}

func (r postgresTrialRepository) Scope(runID, questionID string) (trialScope, error) {
	scope := trialScope{Segments: map[string]trialScopeSegment{}, Covered: map[string]bool{}}
	rows, err := r.tx.QueryContext(r.ctx, `
SELECT sr.exam_id::text,seg.id::text,
  COALESCE(NULLIF(eqs.rubric_snapshot_json->>'max_score','')::float8,q.score::float8),
  jsonb_build_object('snapshot',to_jsonb(eqs),'rubric',COALESCE(NULLIF(eqs.rubric_snapshot_json,'{}'::jsonb),to_jsonb(qr)),
    'reference',ref.question_snapshot,'reference_hash',ref.import_snapshot_hash,
    'question',jsonb_build_object('id',q.id,'stem',q.stem,'score',q.score,'question_type',q.question_type))
    || CASE WHEN material.id IS NULL THEN '{}'::jsonb ELSE jsonb_build_object('material_authorization',jsonb_build_object('manifest_hash',material.content_hash,'authorization_revision',material.authorization_revision,'use_state',material.use_state,'input_profile','material-input-v1')) END,
  jsonb_build_object('answer_id',ans.id,'answer_text',ans.answer_text,'page_id',seg.submission_page_id,
    'bbox',seg.bbox,'normalized_bbox',seg.normalized_bbox,'registration_run_id',seg.registration_run_id),
  EXISTS(SELECT 1 FROM subjective_grading_run ai_run WHERE ai_run.tenant_id=seg.tenant_id
    AND ai_run.answer_segment_id=seg.id AND ai_run.answer_version=ans.id::text AND ai_run.deleted_at IS NULL)
  OR EXISTS(SELECT 1 FROM ai_grade grade WHERE grade.tenant_id=seg.tenant_id AND grade.answer_segment_id=seg.id
    AND grade.answer_version IN ('',ans.id::text) AND grade.deleted_at IS NULL)
FROM scoring_run sr
JOIN submission sub ON sub.tenant_id=sr.tenant_id AND sub.exam_id=sr.exam_id AND sub.deleted_at IS NULL
JOIN answer_segment seg ON seg.tenant_id=sub.tenant_id AND seg.submission_id=sub.id AND seg.question_id=$3::uuid AND seg.deleted_at IS NULL
JOIN question q ON q.tenant_id=seg.tenant_id AND q.id=seg.question_id AND q.exam_id=sr.exam_id AND q.deleted_at IS NULL
JOIN LATERAL (SELECT * FROM exam_question_snapshot snapshot WHERE snapshot.tenant_id=q.tenant_id AND snapshot.exam_id=q.exam_id AND snapshot.question_id=q.id ORDER BY snapshot.snapshot_version DESC LIMIT 1) eqs ON true
LEFT JOIN LATERAL (SELECT * FROM question_rubric rubric WHERE rubric.tenant_id=q.tenant_id AND rubric.question_id=q.id AND rubric.deleted_at IS NULL ORDER BY rubric.created_at DESC LIMIT 1) qr ON true
JOIN LATERAL (SELECT id,answer_text FROM answer_segment_answer a WHERE a.tenant_id=seg.tenant_id AND a.answer_segment_id=seg.id AND a.deleted_at IS NULL ORDER BY a.created_at DESC LIMIT 1) ans ON ans.answer_text<>''
LEFT JOIN LATERAL (
 SELECT item.value question_snapshot,readiness.import_snapshot_hash
 FROM exam_readiness_snapshot readiness CROSS JOIN LATERAL jsonb_array_elements(readiness.import_snapshot_json->'questions') item(value)
 WHERE readiness.tenant_id=q.tenant_id AND readiness.exam_id=q.exam_id AND readiness.status='passed'
 AND item.value->>'id'=q.id::text AND item.value->>'assessment_snapshot_id'=eqs.id::text
 AND item.value->>'assessment_snapshot_hash'=eqs.content_hash ORDER BY readiness.confirmed_at DESC LIMIT 1
) ref ON true
LEFT JOIN question_reference_manifest material ON material.tenant_id=q.tenant_id AND material.id=NULLIF(ref.question_snapshot->'reference_materials'->>'manifest_id','')::uuid
WHERE sr.tenant_id=$1::uuid AND sr.id=$2::uuid AND sr.deleted_at IS NULL
 AND (ref.question_snapshot->'reference_materials' IS NULL OR (material.use_state='active' AND NOT EXISTS(SELECT 1 FROM question_reference_manifest_item i JOIN file_asset f ON f.tenant_id=i.tenant_id AND f.id IN(i.source_file_asset_id,i.page_file_asset_id,i.derived_file_asset_id) WHERE i.tenant_id=material.tenant_id AND i.manifest_id=material.id AND (f.lifecycle_status<>'active' OR f.deleted_at IS NOT NULL))))
 AND sr.status IN ('queued','processing','needs_review','failed')
 AND q.question_type IN ('short_answer','calculation','essay','discussion')
 AND eqs.scoring_policy_snapshot_json->>'mode' IN ('AI_ASSIST','AI_FAST_CONFIRM')
 AND EXISTS(SELECT 1 FROM review_task task WHERE task.tenant_id=seg.tenant_id AND task.scoring_run_id=sr.id
 AND task.answer_segment_id=seg.id AND task.question_id=seg.question_id AND task.deleted_at IS NULL
 AND task.status IN ('pending','assigned','in_progress','returned'))
ORDER BY seg.id`, r.tenantID, runID, questionID)
	if err != nil {
		return scope, err
	}
	for rows.Next() {
		var examID, segmentID string
		var maxScore float64
		var exposedToAI bool
		var versionRaw, answerRaw []byte
		if err := rows.Scan(&examID, &segmentID, &maxScore, &versionRaw, &answerRaw, &exposedToAI); err != nil {
			rows.Close()
			return scope, err
		}
		var version, answer any
		if err := json.Unmarshal(versionRaw, &version); err != nil {
			rows.Close()
			return scope, err
		}
		if err := json.Unmarshal(answerRaw, &answer); err != nil {
			rows.Close()
			return scope, err
		}
		scope.ExamID, scope.Version = examID, version
		scope.Segments[segmentID] = trialScopeSegment{maxScore, answer}
		// 停止只取消后续推理，不能让已经启动过 AI 的同版答卷重新冒充独立人工基准。
		if exposedToAI {
			scope.Covered[segmentID] = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return scope, err
	}
	if len(scope.Segments) == 0 {
		return scope, ErrTrialConflict
	}
	rows, err = r.tx.QueryContext(r.ctx, `SELECT requested.id FROM subjective_grading_batch batch CROSS JOIN LATERAL jsonb_array_elements_text(batch.segment_ids) requested(id) WHERE batch.tenant_id=$1::uuid AND batch.scoring_run_id=$2::uuid AND batch.deleted_at IS NULL AND batch.status<>'cancelled'`, r.tenantID, runID)
	if err != nil {
		return scope, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return scope, err
		}
		scope.Covered[id] = true
	}
	return scope, rows.Err()
}

func scanTrial(row interface{ Scan(...any) error }) (ScoringTrial, error) {
	var trial ScoringTrial
	var raw []byte
	var tenantID, requestHash string
	if err := row.Scan(&raw, &tenantID, &requestHash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return trial, ErrNotFound
		}
		return trial, err
	}
	if err := json.Unmarshal(raw, &trial); err != nil {
		return trial, err
	}
	trial.TenantID, trial.RequestHash = tenantID, requestHash
	return trial, nil
}
func (r postgresTrialRepository) List(runID string) ([]ScoringTrial, error) {
	rows, err := r.tx.QueryContext(r.ctx, `SELECT payload,tenant_id::text,request_hash FROM scoring_ai_trial WHERE tenant_id=$1::uuid AND scoring_run_id=$2::uuid ORDER BY created_at,id FOR UPDATE`, r.tenantID, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	trials := []ScoringTrial{}
	for rows.Next() {
		trial, err := scanTrial(rows)
		if err != nil {
			return nil, err
		}
		trials = append(trials, trial)
	}
	return trials, rows.Err()
}
func (r postgresTrialRepository) Get(id string) (ScoringTrial, error) {
	return scanTrial(r.tx.QueryRowContext(r.ctx, `SELECT payload,tenant_id::text,request_hash FROM scoring_ai_trial WHERE tenant_id=$1::uuid AND id=$2::uuid FOR UPDATE`, r.tenantID, id))
}
func (r postgresTrialRepository) Save(trial ScoringTrial, create bool) error {
	saved := cloneTrial(trial)
	for index := range saved.Samples {
		saved.Samples[index].Result = nil
	}
	raw, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	if create {
		_, err = r.tx.ExecContext(r.ctx, `INSERT INTO scoring_ai_trial(id,tenant_id,scoring_run_id,exam_id,question_id,command_id,created_by,status,revision,sample_batch_id,expansion_batch_id,request_hash,payload,created_at,updated_at) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,$7::uuid,$8,$9,$10::uuid,NULLIF($11,'')::uuid,$12,$13::jsonb,$14,$15)`, trial.ID, r.tenantID, trial.RunID, trial.ExamID, trial.QuestionID, trial.CommandID, trial.CreatedBy, trial.Status, trial.Revision, trial.SampleBatchID, trial.ExpansionBatchID, trial.RequestHash, raw, trial.CreatedAt, trial.UpdatedAt)
	} else {
		_, err = r.tx.ExecContext(r.ctx, `UPDATE scoring_ai_trial SET status=$3,revision=$4,expansion_batch_id=NULLIF($5,'')::uuid,payload=$6::jsonb,updated_at=$7 WHERE tenant_id=$1::uuid AND id=$2::uuid`, r.tenantID, trial.ID, trial.Status, trial.Revision, trial.ExpansionBatchID, raw, trial.UpdatedAt)
	}
	return err
}
func (r postgresTrialRepository) CreateBatch(actorID, runID, commandID string, segments []string) (string, error) {
	segments, err := normalizeBatchSegments(segments)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(segments)
	if err != nil {
		return "", err
	}
	var id string
	err = r.tx.QueryRowContext(r.ctx, `INSERT INTO subjective_grading_batch(tenant_id,scoring_run_id,idempotency_key,status,segment_ids,total_count,created_by,command_request_hash) VALUES($1::uuid,$2::uuid,$3,'planned',$4::jsonb,$5,$6::uuid,$7) RETURNING id::text`, r.tenantID, runID, commandID, raw, len(segments), actorID, batchRequestHash(segments)).Scan(&id)
	return id, err
}

func (r postgresTrialRepository) CancelBatch(batchID string) error {
	if _, err := r.tx.ExecContext(r.ctx, `UPDATE subjective_grading_batch SET status='cancelled',updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid AND status<>'cancelled'`, r.tenantID, batchID); err != nil {
		return err
	}
	// 先关闭来源运行，再停止 runtime；与 worker 结算使用同一锁序，迟到结果不能恢复成功态。
	if _, err := r.tx.ExecContext(r.ctx, `UPDATE subjective_grading_run SET status='failed',error_code='scoring_trial_stopped',completed_at=now(),updated_at=now() WHERE tenant_id=$1::uuid AND batch_id=$2::uuid AND status IN ('queued','processing') AND deleted_at IS NULL`, r.tenantID, batchID); err != nil {
		return err
	}
	if _, err := r.tx.ExecContext(r.ctx, `UPDATE agent_worker_task task SET status='cancelled',cancelled_at=now(),completed_at=now(),updated_at=now(),revision=revision+1 FROM subjective_grading_run run WHERE run.tenant_id=$1::uuid AND run.batch_id=$2::uuid AND task.tenant_id=run.tenant_id AND task.source_type='subjective_grading_run' AND task.source_id=run.id AND task.status IN ('queued','leased','running')`, r.tenantID, batchID); err != nil {
		return err
	}
	_, err := r.tx.ExecContext(r.ctx, `UPDATE agent_worker_task_attempt attempt SET status='cancelled',completed_at=now(),error_code='scoring_trial_stopped' FROM agent_worker_task task JOIN subjective_grading_run run ON run.tenant_id=task.tenant_id AND task.source_type='subjective_grading_run' AND task.source_id=run.id WHERE run.tenant_id=$1::uuid AND run.batch_id=$2::uuid AND task.status='cancelled' AND attempt.tenant_id=task.tenant_id AND attempt.task_id=task.id AND attempt.attempt_no=task.attempt_count AND attempt.completed_at IS NULL`, r.tenantID, batchID)
	return err
}

func (r postgresTrialRepository) Results(trial *ScoringTrial) error {
	for index := range trial.Samples {
		trial.Samples[index].Result = nil
	}
	// 响应未通过 schema 时仍可能产生用量；缺少 ai_grade 不应抹掉同一次调用的已报告 token。
	rows, err := r.tx.QueryContext(r.ctx, `SELECT run.answer_segment_id::text,run.status,COALESCE(run.error_code,''),COALESCE(grade.id::text,''),CASE WHEN grade.status='succeeded' AND run.status='succeeded' THEN grade.suggested_score::float8 END,COALESCE(grade.evidence,'[]'::jsonb),COALESCE(grade.risk_flags,'[]'::jsonb),NULLIF(grade.adapter_latency_ms,0),COALESCE(usage.total_tokens,(SELECT (p.usage_json->>'total_tokens')::bigint FROM subjective_request_material_receipt p WHERE p.tenant_id=run.tenant_id AND p.run_id=run.id AND p.stage='provider_response_received' AND p.usage_state IN('reported','partial') ORDER BY p.recorded_at DESC LIMIT 1),0),
 (SELECT jsonb_build_object('stage',p.stage,'manifest_hash',p.manifest_hash,'image_count',jsonb_array_length(p.ordered_media_json),'permit_consumed',p.consumed_at IS NOT NULL,'recorded_at',p.recorded_at) FROM subjective_request_material_receipt p WHERE p.tenant_id=run.tenant_id AND p.run_id=run.id ORDER BY CASE p.stage WHEN 'provider_response_received' THEN 4 WHEN 'unknown_after_attempt' THEN 3 WHEN 'provider_request_attempted' THEN 2 ELSE 1 END DESC,p.recorded_at DESC LIMIT 1)
FROM subjective_grading_run run
LEFT JOIN ai_grade grade ON grade.tenant_id=run.tenant_id AND grade.id=run.grade_id AND grade.subjective_grading_run_id=run.id AND grade.adapter_request_id=run.request_id AND grade.deleted_at IS NULL
LEFT JOIN model_usage_event usage ON usage.tenant_id=run.tenant_id AND usage.request_id=run.request_id AND usage.feature='subjective_grading'
WHERE run.tenant_id=$1::uuid AND run.batch_id=$2::uuid AND run.deleted_at IS NULL ORDER BY run.created_at`, r.tenantID, trial.SampleBatchID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var segmentID string
		result := TrialSampleResult{Evidence: []grading.Evidence{}, RiskFlags: []string{}}
		var score sql.NullFloat64
		var latency sql.NullInt64
		var totalTokens int64
		var evidenceRaw, risksRaw []byte
		var materialRaw []byte
		if err := rows.Scan(&segmentID, &result.Status, &result.ErrorCode, &result.GradeID, &score, &evidenceRaw, &risksRaw, &latency, &totalTokens, &materialRaw); err != nil {
			return err
		}
		if len(materialRaw) > 0 {
			if err := json.Unmarshal(materialRaw, &result.MaterialReceipt); err != nil {
				return err
			}
		}
		if score.Valid {
			v := score.Float64
			result.SuggestedScore = &v
		}
		if latency.Valid {
			v := latency.Int64
			result.LatencyMS = &v
		}
		if totalTokens > 0 {
			result.TotalTokens = &totalTokens
		}
		if err := json.Unmarshal(evidenceRaw, &result.Evidence); err != nil {
			return err
		}
		if err := json.Unmarshal(risksRaw, &result.RiskFlags); err != nil {
			return err
		}
		if result.Evidence == nil {
			result.Evidence = []grading.Evidence{}
		}
		if result.RiskFlags == nil {
			result.RiskFlags = []string{}
		}
		if result.Status == RunSucceeded && result.GradeID == "" {
			result.Status = RunFailed
			result.ErrorCode = "trial_grade_missing"
		}
		for index := range trial.Samples {
			if trial.Samples[index].SegmentID == segmentID {
				copy := result
				trial.Samples[index].Result = &copy
			}
		}
	}
	return rows.Err()
}

func (s *PostgresStore) trialTransaction(ctx context.Context, tenantID, runID string, operation func(postgresTrialRepository) error) error {
	if _, err := uuid.Parse(tenantID); err != nil {
		return ErrInvalidInput
	}
	if _, err := uuid.Parse(runID); err != nil {
		return ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	repo := postgresTrialRepository{ctx, tx, tenantID}
	if err := repo.lockRun(runID, false); err != nil {
		return err
	}
	err = operation(repo)
	// 版本不符会先持久化 invalidated；即使本次命令被拒绝，也不能回滚这条作废事实。
	if err != nil && !errors.Is(err, ErrTrialConflict) {
		return err
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return commitErr
	}
	return err
}
func (s *PostgresStore) CreateScoringTrial(ctx context.Context, tenantID, actorID, runID string, input CreateTrialInput, policy ModelPolicy) (trial ScoringTrial, err error) {
	if _, parseErr := uuid.Parse(input.QuestionID); parseErr != nil {
		return trial, ErrInvalidInput
	}
	for _, sample := range input.Samples {
		if _, parseErr := uuid.Parse(sample.SegmentID); parseErr != nil {
			return trial, ErrInvalidInput
		}
	}
	err = s.trialTransaction(ctx, tenantID, runID, func(repo postgresTrialRepository) error {
		var e error
		trial, e = createTrial(repo, tenantID, actorID, runID, input, policy)
		return e
	})
	return
}
func (s *PostgresStore) ListScoringTrials(ctx context.Context, tenantID, runID string, policy ModelPolicy) (trials []ScoringTrial, err error) {
	err = s.trialTransaction(ctx, tenantID, runID, func(repo postgresTrialRepository) error {
		var e error
		trials, e = repo.List(runID)
		if e != nil {
			return e
		}
		for index := range trials {
			trials[index], e = refreshTrial(repo, trials[index], policy)
			if e != nil {
				return e
			}
		}
		return nil
	})
	return
}
func (s *PostgresStore) DecideScoringTrial(ctx context.Context, tenantID, actorID, runID, trialID string, input TrialDecisionInput, policy ModelPolicy) (trial ScoringTrial, err error) {
	if _, e := uuid.Parse(trialID); e != nil {
		return trial, ErrInvalidInput
	}
	err = s.trialTransaction(ctx, tenantID, runID, func(repo postgresTrialRepository) error {
		var e error
		trial, e = decideTrial(repo, actorID, runID, trialID, input, policy)
		return e
	})
	return
}
func (s *PostgresStore) PrepareScoringTrialEnqueue(ctx context.Context, tenantID, runID, trialID string, policy ModelPolicy) (trial ScoringTrial, err error) {
	if _, e := uuid.Parse(trialID); e != nil {
		return trial, ErrInvalidInput
	}
	err = s.trialTransaction(ctx, tenantID, runID, func(repo postgresTrialRepository) error {
		var e error
		trial, e = prepareTrialEnqueue(repo, runID, trialID, policy)
		return e
	})
	return
}
func (s *PostgresStore) AuthorizeTrialBatch(ctx context.Context, tenantID, batchID string, policy ModelPolicy) error {
	batch, err := s.GetBatch(ctx, tenantID, batchID)
	if err != nil {
		return err
	}
	if batch.ScoringRunID == "" {
		return nil
	}
	return s.trialTransaction(ctx, tenantID, batch.ScoringRunID, func(repo postgresTrialRepository) error {
		var required bool
		if err := repo.tx.QueryRowContext(ctx, `SELECT ai_trial_required FROM scoring_run WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, batch.ScoringRunID).Scan(&required); err != nil {
			return err
		}
		trials, err := repo.List(batch.ScoringRunID)
		if err != nil {
			return err
		}
		if required && len(trials) == 0 {
			return ErrTrialConflict
		}
		return authorizeTrialBatch(repo, batch.ScoringRunID, batchID, policy)
	})
}
