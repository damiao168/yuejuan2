package gradingevaluation

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

type PostgresStore struct{ db *sql.DB }

func NewPostgresStore(db *sql.DB) *PostgresStore { return &PostgresStore{db: db} }

func (s *PostgresStore) CreateRun(ctx context.Context, tenantID, actorID string, input CreateRunInput) (Run, error) {
	if s.db == nil {
		return Run{}, ErrInvalidInput
	}
	row := s.db.QueryRowContext(ctx, `
INSERT INTO grading_evaluation_run(
 tenant_id,run_key,display_name,model_reference,prompt_version,rubric_version,dataset_reference,dataset_sha256,created_by
) VALUES($1::uuid,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,'')::uuid)
RETURNING id::text,tenant_id::text,run_key,display_name,model_reference,prompt_version,rubric_version,dataset_reference,dataset_sha256,
          status,created_by::text,created_at,completed_at,invalidated_at,invalidation_reason`,
		tenantID, input.Key, input.DisplayName, input.ModelReference, input.PromptVersion, input.RubricVersion, input.DatasetReference, input.DatasetSHA256, actorID)
	item, err := scanRun(row)
	return item, mapStoreError(err)
}

func (s *PostgresStore) GetRun(ctx context.Context, tenantID, runID string) (Run, error) {
	if s.db == nil {
		return Run{}, ErrInvalidInput
	}
	row := s.db.QueryRowContext(ctx, `
SELECT run.id::text,run.tenant_id::text,run.run_key,run.display_name,run.model_reference,run.prompt_version,run.rubric_version,
       run.dataset_reference,run.dataset_sha256,run.status,run.created_by::text,run.created_at,run.completed_at,run.invalidated_at,run.invalidation_reason,
       (SELECT count(*) FROM grading_evaluation_observation obs WHERE obs.tenant_id=run.tenant_id AND obs.run_id=run.id)
       + (SELECT count(*) FROM grading_panel_evaluation_observation panel_obs WHERE panel_obs.tenant_id=run.tenant_id AND panel_obs.run_id=run.id)
FROM grading_evaluation_run run WHERE run.tenant_id=$1::uuid AND run.id=$2::uuid`, tenantID, runID)
	item, err := scanRunWithCount(row)
	return item, mapStoreError(err)
}

func (s *PostgresStore) ListRuns(ctx context.Context, tenantID string, filter RunFilter) ([]Run, error) {
	if s.db == nil {
		return nil, ErrInvalidInput
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT run.id::text,run.tenant_id::text,run.run_key,run.display_name,run.model_reference,run.prompt_version,run.rubric_version,
       run.dataset_reference,run.dataset_sha256,run.status,run.created_by::text,run.created_at,run.completed_at,run.invalidated_at,run.invalidation_reason,
       (SELECT count(*) FROM grading_evaluation_observation obs WHERE obs.tenant_id=run.tenant_id AND obs.run_id=run.id)
       + (SELECT count(*) FROM grading_panel_evaluation_observation panel_obs WHERE panel_obs.tenant_id=run.tenant_id AND panel_obs.run_id=run.id)
FROM grading_evaluation_run run WHERE run.tenant_id=$1::uuid ORDER BY run.created_at DESC,run.id DESC LIMIT $2`, tenantID, filter.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Run{}
	for rows.Next() {
		item, scanErr := scanRunWithCount(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) AddObservation(ctx context.Context, tenantID, runID string, input AddObservationInput) (Observation, error) {
	if s.db == nil {
		return Observation{}, ErrInvalidInput
	}
	row := s.db.QueryRowContext(ctx, `
INSERT INTO grading_evaluation_observation(
 tenant_id,run_id,response_key,response_fingerprint,reference_kind,subject,archetype,ocr_quality,answer_length,rubric_complexity,
 reference_score,model_score,max_score,reference_score_band,page_match_correct,crop_iou,transcription_cer,formula_exact,
 rubric_criterion_agreement,error_source,needs_human_review,reference_reviewer_count,reference_adjudicated
) VALUES($1::uuid,$2::uuid,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23)
RETURNING id::text,run_id::text,response_key,response_fingerprint,reference_kind,subject,archetype,ocr_quality,answer_length,rubric_complexity,
          reference_score,model_score,max_score,reference_score_band,page_match_correct,crop_iou,transcription_cer,formula_exact,
          rubric_criterion_agreement,error_source,needs_human_review,reference_reviewer_count,reference_adjudicated,observed_at`,
		tenantID, runID, input.ResponseKey, input.ResponseFingerprint, input.ReferenceKind, input.Subject, input.Archetype,
		input.OCRQuality, input.AnswerLength, input.RubricComplexity, input.ReferenceScore, input.ModelScore, input.MaxScore, scoreBand(input.ReferenceScore, input.MaxScore),
		input.PageMatchCorrect, input.CropIoU, input.TranscriptionCER, input.FormulaExact, input.RubricAgreement, input.ErrorSource,
		input.NeedsHumanReview, input.ReferenceReviewers, input.ReferenceAdjudicated)
	item, err := scanObservation(row)
	return item, mapStoreError(err)
}

func (s *PostgresStore) AddPanelObservation(ctx context.Context, tenantID, runID string, input PanelObservation) (PanelObservation, error) {
	if s.db == nil || !validPersistedPanelObservation(input) {
		return PanelObservation{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PanelObservation{}, err
	}
	defer tx.Rollback()
	var status RunStatus
	if err = tx.QueryRowContext(ctx, `SELECT status FROM grading_evaluation_run WHERE tenant_id=$1::uuid AND id=$2::uuid FOR UPDATE`, tenantID, runID).Scan(&status); err != nil {
		return PanelObservation{}, mapStoreError(err)
	}
	if status != RunDraft {
		return PanelObservation{}, ErrStateConflict
	}
	item, err := scanPanelObservation(tx.QueryRowContext(ctx, `
INSERT INTO grading_panel_evaluation_observation(
 tenant_id,run_id,response_key,response_fingerprint,education_stage,subject_code,archetype_code,
 reference_score,max_score,score_a,score_b,score_c,resolved_score,arbitration_triggered,
 resolution_source,human_escalated,reference_kind,reference_reviewer_count,reference_adjudicated,
 primary_a_cost_micros,primary_b_cost_micros,arbiter_cost_micros
) VALUES($1::uuid,$2::uuid,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22)
RETURNING id::text,run_id::text,response_key,response_fingerprint,education_stage,subject_code,archetype_code,
 reference_kind,reference_score,max_score,score_a,score_b,score_c,resolved_score,arbitration_triggered,
 resolution_source,human_escalated,reference_reviewer_count,reference_adjudicated,
 primary_a_cost_micros,primary_b_cost_micros,arbiter_cost_micros,observed_at`,
		tenantID, runID, input.ResponseKey, input.ResponseFingerprint, input.EducationStage, input.Subject, input.Archetype,
		input.ReferenceScore, input.MaxScore, input.ScoreA, input.ScoreB, input.ScoreC, input.ResolvedScore,
		input.ArbitrationTriggered, input.ResolutionSource, input.HumanEscalated, input.ReferenceKind,
		input.ReferenceReviewers, input.ReferenceAdjudicated, input.PrimaryACostMicros, input.PrimaryBCostMicros,
		input.ArbiterCostMicros))
	if err != nil {
		return PanelObservation{}, mapStoreError(err)
	}
	if err = tx.Commit(); err != nil {
		return PanelObservation{}, mapStoreError(err)
	}
	return item, nil
}

func (s *PostgresStore) ListPanelObservations(ctx context.Context, tenantID, runID string) ([]PanelObservation, error) {
	if s.db == nil {
		return nil, ErrInvalidInput
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id::text,run_id::text,response_key,response_fingerprint,education_stage,subject_code,archetype_code,
 reference_kind,reference_score,max_score,score_a,score_b,score_c,resolved_score,arbitration_triggered,
 resolution_source,human_escalated,reference_reviewer_count,reference_adjudicated,
 primary_a_cost_micros,primary_b_cost_micros,arbiter_cost_micros,observed_at
FROM grading_panel_evaluation_observation
WHERE tenant_id=$1::uuid AND run_id=$2::uuid ORDER BY observed_at,id`, tenantID, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []PanelObservation{}
	for rows.Next() {
		item, scanErr := scanPanelObservation(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(items) == 0 {
		if _, getErr := s.GetRun(ctx, tenantID, runID); getErr != nil {
			return nil, getErr
		}
	}
	return items, nil
}

func (s *PostgresStore) ListObservations(ctx context.Context, tenantID, runID string) ([]Observation, error) {
	if s.db == nil {
		return nil, ErrInvalidInput
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id::text,run_id::text,response_key,response_fingerprint,reference_kind,subject,archetype,ocr_quality,answer_length,rubric_complexity,
       reference_score,model_score,max_score,reference_score_band,page_match_correct,crop_iou,transcription_cer,formula_exact,
       rubric_criterion_agreement,error_source,needs_human_review,reference_reviewer_count,reference_adjudicated,observed_at
FROM grading_evaluation_observation WHERE tenant_id=$1::uuid AND run_id=$2::uuid ORDER BY observed_at,id`, tenantID, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Observation{}
	for rows.Next() {
		item, scanErr := scanObservation(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(items) == 0 {
		if _, getErr := s.GetRun(ctx, tenantID, runID); getErr != nil {
			return nil, getErr
		}
	}
	return items, nil
}

// 完成事务锁住运行并重新统计原始观测，再替换派生指标；并发新增样本会使本次提交失败而非污染结果。
func (s *PostgresStore) ReplaceComputed(ctx context.Context, tenantID, runID string, expectedCount int, slices []SliceMetric, difficulty []ResponseDifficulty, at time.Time) (Run, error) {
	if s.db == nil {
		return Run{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Run{}, err
	}
	defer tx.Rollback()
	var status string
	if err = tx.QueryRowContext(ctx, `SELECT status FROM grading_evaluation_run WHERE tenant_id=$1::uuid AND id=$2::uuid FOR UPDATE`, tenantID, runID).Scan(&status); err != nil {
		return Run{}, mapStoreError(err)
	}
	if status != string(RunDraft) {
		return Run{}, ErrStateConflict
	}
	var actual int
	if err = tx.QueryRowContext(ctx, `SELECT
  (SELECT count(*) FROM grading_evaluation_observation WHERE tenant_id=$1::uuid AND run_id=$2::uuid)
  + (SELECT count(*) FROM grading_panel_evaluation_observation WHERE tenant_id=$1::uuid AND run_id=$2::uuid)`, tenantID, runID).Scan(&actual); err != nil {
		return Run{}, err
	}
	if actual != expectedCount {
		return Run{}, ErrConflict
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM grading_evaluation_slice_metric WHERE tenant_id=$1::uuid AND run_id=$2::uuid`, tenantID, runID); err != nil {
		return Run{}, mapStoreError(err)
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM grading_evaluation_response_difficulty WHERE tenant_id=$1::uuid AND run_id=$2::uuid`, tenantID, runID); err != nil {
		return Run{}, mapStoreError(err)
	}
	for _, metric := range slices {
		_, err = tx.ExecContext(ctx, `
INSERT INTO grading_evaluation_slice_metric(
 tenant_id,run_id,dimension,slice_value,sample_count,mae,exact_rate,within_one_rate,severe_error_rate,false_zero_rate,false_full_rate,qwk,qwk_unavailable_reason,computed_at
) VALUES($1::uuid,$2::uuid,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
			tenantID, runID, metric.Dimension, metric.Value, metric.Metrics.SampleCount, metric.Metrics.MAE, metric.Metrics.ExactRate,
			metric.Metrics.WithinOneRate, metric.Metrics.SevereErrorRate, metric.Metrics.FalseZeroRate, metric.Metrics.FalseFullRate,
			metric.Metrics.QWK, metric.Metrics.QWKUnavailableReason, at.UTC())
		if err != nil {
			return Run{}, mapStoreError(err)
		}
	}
	for _, item := range difficulty {
		_, err = tx.ExecContext(ctx, `
INSERT INTO grading_evaluation_response_difficulty(
 tenant_id,run_id,response_key,response_fingerprint,difficulty_score,difficulty_band,normalized_error,severe_error,ocr_quality,answer_length,rubric_complexity,evidence_note,computed_at
) VALUES($1::uuid,$2::uuid,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
			tenantID, runID, item.ResponseKey, item.ResponseFingerprint, item.DifficultyScore, item.DifficultyBand, item.NormalizedError,
			item.SevereError, item.OCRQuality, item.AnswerLength, item.RubricComplexity, item.EvidenceNote, at.UTC())
		if err != nil {
			return Run{}, mapStoreError(err)
		}
	}
	row := tx.QueryRowContext(ctx, `
UPDATE grading_evaluation_run SET status='completed',completed_at=$3 WHERE tenant_id=$1::uuid AND id=$2::uuid AND status='draft'
RETURNING id::text,tenant_id::text,run_key,display_name,model_reference,prompt_version,rubric_version,dataset_reference,dataset_sha256,
          status,created_by::text,created_at,completed_at,invalidated_at,invalidation_reason`, tenantID, runID, at.UTC())
	run, err := scanRun(row)
	if err != nil {
		return Run{}, mapStoreError(err)
	}
	run.ObservationCount = actual
	if err = tx.Commit(); err != nil {
		return Run{}, mapStoreError(err)
	}
	return run, nil
}

func (s *PostgresStore) ListSliceMetrics(ctx context.Context, tenantID, runID string) ([]SliceMetric, error) {
	if s.db == nil {
		return nil, ErrInvalidInput
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id::text,run_id::text,dimension,slice_value,sample_count,mae,exact_rate,within_one_rate,severe_error_rate,false_zero_rate,false_full_rate,qwk,qwk_unavailable_reason,computed_at
FROM grading_evaluation_slice_metric WHERE tenant_id=$1::uuid AND run_id=$2::uuid ORDER BY dimension,slice_value`, tenantID, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []SliceMetric{}
	for rows.Next() {
		item, scanErr := scanSliceMetric(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(items) == 0 {
		if _, getErr := s.GetRun(ctx, tenantID, runID); getErr != nil {
			return nil, getErr
		}
	}
	return items, nil
}

func (s *PostgresStore) ListResponseDifficulty(ctx context.Context, tenantID, runID string) ([]ResponseDifficulty, error) {
	if s.db == nil {
		return nil, ErrInvalidInput
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id::text,run_id::text,response_key,response_fingerprint,difficulty_score,difficulty_band,normalized_error,severe_error,ocr_quality,answer_length,rubric_complexity,evidence_note,computed_at
FROM grading_evaluation_response_difficulty WHERE tenant_id=$1::uuid AND run_id=$2::uuid ORDER BY difficulty_score DESC,response_key`, tenantID, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ResponseDifficulty{}
	for rows.Next() {
		item, scanErr := scanDifficulty(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(items) == 0 {
		if _, getErr := s.GetRun(ctx, tenantID, runID); getErr != nil {
			return nil, getErr
		}
	}
	return items, nil
}

func (s *PostgresStore) InvalidateRun(ctx context.Context, tenantID, runID, reason string, at time.Time) (Run, error) {
	if s.db == nil {
		return Run{}, ErrInvalidInput
	}
	row := s.db.QueryRowContext(ctx, `
UPDATE grading_evaluation_run SET status='invalidated',invalidated_at=$3,invalidation_reason=$4
WHERE tenant_id=$1::uuid AND id=$2::uuid AND status <> 'invalidated'
RETURNING id::text,tenant_id::text,run_key,display_name,model_reference,prompt_version,rubric_version,dataset_reference,dataset_sha256,
          status,created_by::text,created_at,completed_at,invalidated_at,invalidation_reason`, tenantID, runID, at.UTC(), reason)
	run, err := scanRun(row)
	if err != nil {
		return Run{}, mapStoreError(err)
	}
	var count int
	if err = s.db.QueryRowContext(ctx, `SELECT
  (SELECT count(*) FROM grading_evaluation_observation WHERE tenant_id=$1::uuid AND run_id=$2::uuid)
  + (SELECT count(*) FROM grading_panel_evaluation_observation WHERE tenant_id=$1::uuid AND run_id=$2::uuid)`, tenantID, runID).Scan(&count); err != nil {
		return Run{}, err
	}
	run.ObservationCount = count
	return run, nil
}

type scanner interface{ Scan(...any) error }

func scanRun(row scanner) (Run, error) {
	var item Run
	var createdBy sql.NullString
	var completedAt, invalidatedAt sql.NullTime
	err := row.Scan(&item.ID, &item.TenantID, &item.Key, &item.DisplayName, &item.ModelReference, &item.PromptVersion, &item.RubricVersion, &item.DatasetReference, &item.DatasetSHA256, &item.Status, &createdBy, &item.CreatedAt, &completedAt, &invalidatedAt, &item.InvalidationReason)
	if err != nil {
		return Run{}, err
	}
	item.CreatedBy = createdBy.String
	if completedAt.Valid {
		item.CompletedAt = &completedAt.Time
	}
	if invalidatedAt.Valid {
		item.InvalidatedAt = &invalidatedAt.Time
	}
	return item, nil
}
func scanRunWithCount(row scanner) (Run, error) {
	var item Run
	var createdBy sql.NullString
	var completedAt, invalidatedAt sql.NullTime
	err := row.Scan(
		&item.ID, &item.TenantID, &item.Key, &item.DisplayName, &item.ModelReference, &item.PromptVersion, &item.RubricVersion,
		&item.DatasetReference, &item.DatasetSHA256, &item.Status, &createdBy, &item.CreatedAt, &completedAt, &invalidatedAt,
		&item.InvalidationReason, &item.ObservationCount,
	)
	if err != nil {
		return Run{}, err
	}
	item.CreatedBy = createdBy.String
	if completedAt.Valid {
		item.CompletedAt = &completedAt.Time
	}
	if invalidatedAt.Valid {
		item.InvalidatedAt = &invalidatedAt.Time
	}
	return item, nil
}

func scanObservation(row scanner) (Observation, error) {
	var item Observation
	err := row.Scan(
		&item.ID, &item.RunID, &item.ResponseKey, &item.ResponseFingerprint, &item.ReferenceKind, &item.Subject, &item.Archetype,
		&item.OCRQuality, &item.AnswerLength, &item.RubricComplexity, &item.ReferenceScore, &item.ModelScore, &item.MaxScore,
		&item.ReferenceScoreBand, &item.PageMatchCorrect, &item.CropIoU, &item.TranscriptionCER, &item.FormulaExact,
		&item.RubricAgreement, &item.ErrorSource, &item.NeedsHumanReview, &item.ReferenceReviewers, &item.ReferenceAdjudicated,
		&item.ObservedAt,
	)
	return item, err
}

func scanPanelObservation(row scanner) (PanelObservation, error) {
	var item PanelObservation
	var scoreC, resolved sql.NullFloat64
	err := row.Scan(
		&item.ID, &item.RunID, &item.ResponseKey, &item.ResponseFingerprint, &item.EducationStage, &item.Subject, &item.Archetype,
		&item.ReferenceKind, &item.ReferenceScore, &item.MaxScore, &item.ScoreA, &item.ScoreB, &scoreC, &resolved,
		&item.ArbitrationTriggered, &item.ResolutionSource, &item.HumanEscalated, &item.ReferenceReviewers,
		&item.ReferenceAdjudicated, &item.PrimaryACostMicros, &item.PrimaryBCostMicros, &item.ArbiterCostMicros,
		&item.ObservedAt,
	)
	if err != nil {
		return PanelObservation{}, err
	}
	if scoreC.Valid {
		value := scoreC.Float64
		item.ScoreC = &value
	}
	if resolved.Valid {
		value := resolved.Float64
		item.ResolvedScore = &value
	}
	item.ObservedAt = item.ObservedAt.UTC()
	return item, nil
}

func scanSliceMetric(row scanner) (SliceMetric, error) {
	var item SliceMetric
	var qwk sql.NullFloat64
	err := row.Scan(
		&item.ID, &item.RunID, &item.Dimension, &item.Value, &item.Metrics.SampleCount, &item.Metrics.MAE,
		&item.Metrics.ExactRate, &item.Metrics.WithinOneRate, &item.Metrics.SevereErrorRate, &item.Metrics.FalseZeroRate,
		&item.Metrics.FalseFullRate, &qwk, &item.Metrics.QWKUnavailableReason, &item.ComputedAt,
	)
	if err != nil {
		return SliceMetric{}, err
	}
	if qwk.Valid {
		value := qwk.Float64
		item.Metrics.QWK, item.Metrics.QWKAvailable = &value, true
	}
	return item, nil
}

func scanDifficulty(row scanner) (ResponseDifficulty, error) {
	var item ResponseDifficulty
	err := row.Scan(
		&item.ID, &item.RunID, &item.ResponseKey, &item.ResponseFingerprint, &item.DifficultyScore, &item.DifficultyBand,
		&item.NormalizedError, &item.SevereError, &item.OCRQuality, &item.AnswerLength, &item.RubricComplexity,
		&item.EvidenceNote, &item.ComputedAt,
	)
	return item, err
}

func mapStoreError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return ErrConflict
		case "23514", "23503", "22P02":
			return ErrInvalidInput
		}
	}
	return err
}
